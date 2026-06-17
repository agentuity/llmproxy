package llmproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/agentuity/go-common/slice"
)

var skipHeaders = []string{"Content-Encoding", "Content-Length"}

func disableUpstreamResponseCompression(req *http.Request) {
	// Response extractors parse provider bodies directly, so upstream
	// responses must stay uncompressed.
	req.Header.Set("Accept-Encoding", "identity")
}

func copyResponseHeaders(w http.ResponseWriter, headers http.Header) {
	header := w.Header()

	for k, v := range headers {
		if !slice.Contains(skipHeaders, k, slice.WithCaseInsensitive()) {
			for _, val := range v {
				header.Add(k, val)
			}
		}
	}
}

type AutoRouter struct {
	registry            Registry
	detector            ProviderDetector
	modelProviderLookup ModelProviderLookup
	modelMetadataLookup ModelMetadataLookup
	interceptors        InterceptorChain
	client              *http.Client
	fallbackProvider    Provider
	billingCalculator   *BillingCalculator
	wsUpgrader          WSUpgrader
	wsDialer            WSDialer
	wsBillingCallback   WSBillingCallback
}

type AutoRouterOption func(*AutoRouter)

func WithAutoRouterRegistry(r Registry) AutoRouterOption {
	return func(a *AutoRouter) { a.registry = r }
}

func WithAutoRouterDetector(d ProviderDetector) AutoRouterOption {
	return func(a *AutoRouter) { a.detector = d }
}

func WithAutoRouterInterceptor(i Interceptor) AutoRouterOption {
	return func(a *AutoRouter) { a.interceptors = append(a.interceptors, i) }
}

func WithAutoRouterHTTPClient(c *http.Client) AutoRouterOption {
	return func(a *AutoRouter) { a.client = c }
}

func WithAutoRouterFallbackProvider(p Provider) AutoRouterOption {
	return func(a *AutoRouter) { a.fallbackProvider = p }
}

func WithAutoRouterModelProviderLookup(lookup ModelProviderLookup) AutoRouterOption {
	return func(a *AutoRouter) { a.modelProviderLookup = lookup }
}

func WithAutoRouterModelMetadataLookup(lookup ModelMetadataLookup) AutoRouterOption {
	return func(a *AutoRouter) { a.modelMetadataLookup = lookup }
}

func WithAutoRouterBillingCalculator(calculator *BillingCalculator) AutoRouterOption {
	return func(a *AutoRouter) { a.billingCalculator = calculator }
}

func WithAutoRouterWebSocket(upgrader WSUpgrader, dialer WSDialer) AutoRouterOption {
	return func(a *AutoRouter) {
		a.wsUpgrader = upgrader
		a.wsDialer = dialer
	}
}

func WithAutoRouterWSBillingCallback(cb WSBillingCallback) AutoRouterOption {
	return func(a *AutoRouter) { a.wsBillingCallback = cb }
}

func NewAutoRouter(opts ...AutoRouterOption) *AutoRouter {
	a := &AutoRouter{
		registry: NewRegistry(),
		detector: DefaultProviderDetector,
		client:   http.DefaultClient,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

func (a *AutoRouter) BillingCalculator() *BillingCalculator {
	return a.billingCalculator
}

func (a *AutoRouter) RegisterProvider(p Provider) {
	a.registry.Register(p)
}

func (a *AutoRouter) GetProvider(name string) Provider {
	p, _ := a.registry.Get(name)
	return p
}

func (a *AutoRouter) Forward(ctx context.Context, req *http.Request) (*http.Response, ResponseMetadata, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, ResponseMetadata{}, err
	}
	req.Body.Close()

	model, raw := extractRequestModel(req.Header, body)
	if model == "" {
		model = extractModelFromPath(req.URL.Path)
	}

	hint := ProviderHint{
		Model:   model,
		Headers: req.Header,
	}
	providerName := a.detector.Detect(hint)

	if providerName == "" && a.modelProviderLookup != nil && model != "" {
		providerName = a.modelProviderLookup(model)
	}
	if providerName == "" {
		providerName = providerFromAPIType(DetectAPITypeFromPath(req.URL.Path))
	}

	var provider Provider
	if providerName != "" {
		provider, _ = a.registry.Get(providerName)
		if provider == nil {
			return nil, ResponseMetadata{}, ErrNoProvider
		}
	} else {
		provider = a.fallbackProvider
		if provider == nil {
			return nil, ResponseMetadata{}, ErrNoProvider
		}
	}

	if raw != nil {
		if strippedModel, hasPrefix := stripProviderPrefix(model); hasPrefix {
			raw["model"] = strippedModel
			model = strippedModel
		}
		normalizeProviderRequest(raw, providerName)
		var err error
		body, err = json.Marshal(raw)
		if err != nil {
			return nil, ResponseMetadata{}, fmt.Errorf("failed to marshal request body: %w", err)
		}
	} else if strippedModel, hasPrefix := stripProviderPrefix(model); hasPrefix {
		if rewrittenBody, contentType, ok := rewriteRequestModel(req.Header.Get("Content-Type"), body, strippedModel); ok {
			body = rewrittenBody
			req.Header.Set("Content-Type", contentType)
		}
		model = strippedModel
	}

	apiType := DetectAPITypeFromPath(req.URL.Path)
	if apiType == "" {
		apiType = DetectAPITypeFromBodyAndProvider(body, providerName)
	}
	if err := a.validateModelSurface(providerName, model, apiType); err != nil {
		return nil, ResponseMetadata{}, err
	}

	meta, _, err := provider.BodyParser().Parse(io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		if !canBuildPassthroughMetadata(apiType, model) {
			return nil, ResponseMetadata{}, err
		}
		meta = BodyMetadata{Model: model, Custom: make(map[string]any)}
	}
	if meta.Model == "" && model != "" {
		meta.Model = model
	}

	if meta.Custom == nil {
		meta.Custom = make(map[string]any)
	}
	meta.Custom["api_type"] = apiType
	meta.Custom["provider"] = providerName

	upstreamURL, err := provider.URLResolver().Resolve(meta)
	if err != nil {
		return nil, ResponseMetadata{}, err
	}

	upstreamReq, err := http.NewRequestWithContext(ctx, req.Method, upstreamURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, ResponseMetadata{}, err
	}

	for k, v := range req.Header {
		upstreamReq.Header[k] = v
	}

	if err := provider.RequestEnricher().Enrich(upstreamReq, meta, body); err != nil {
		return nil, ResponseMetadata{}, err
	}
	disableUpstreamResponseCompression(upstreamReq)

	ctxValue := MetaContextValue{Meta: meta, RawBody: body}
	upstreamReq = upstreamReq.WithContext(context.WithValue(upstreamReq.Context(), MetaContextKey{}, ctxValue))

	chain := a.interceptors
	roundTrip := func(req *http.Request) (*http.Response, ResponseMetadata, []byte, error) {
		return a.roundTrip(provider, req)
	}

	if len(chain) > 0 {
		roundTrip = chain.Wrap(roundTrip)
	}

	resp, respMeta, rawRespBody, err := roundTrip(upstreamReq)
	if err != nil {
		return nil, respMeta, err
	}

	resp.Body = io.NopCloser(bytes.NewReader(rawRespBody))
	return resp, respMeta, nil
}

func (a *AutoRouter) roundTrip(provider Provider, req *http.Request) (*http.Response, ResponseMetadata, []byte, error) {
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, ResponseMetadata{}, nil, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		if resp.Body != nil {
			defer resp.Body.Close()
		}
		rawBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, ResponseMetadata{}, nil, readErr
		}
		return resp, ResponseMetadata{}, rawBody, nil
	}

	respMeta, rawBody, err := provider.ResponseExtractor().Extract(resp)
	if err != nil {
		return nil, ResponseMetadata{}, nil, err
	}

	return resp, respMeta, rawBody, nil
}

func (a *AutoRouter) ForwardStreaming(ctx context.Context, req *http.Request, w http.ResponseWriter) (ResponseMetadata, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return ResponseMetadata{}, err
	}
	req.Body.Close()

	model, raw := extractRequestModel(req.Header, body)
	if model == "" {
		model = extractModelFromPath(req.URL.Path)
	}

	hint := ProviderHint{
		Model:   model,
		Headers: req.Header,
	}
	providerName := a.detector.Detect(hint)

	if providerName == "" && a.modelProviderLookup != nil && model != "" {
		providerName = a.modelProviderLookup(model)
	}
	if providerName == "" {
		providerName = providerFromAPIType(DetectAPITypeFromPath(req.URL.Path))
	}

	var provider Provider
	if providerName != "" {
		provider, _ = a.registry.Get(providerName)
		if provider == nil {
			return ResponseMetadata{}, ErrNoProvider
		}
	} else {
		provider = a.fallbackProvider
		if provider == nil {
			return ResponseMetadata{}, ErrNoProvider
		}
	}

	apiType := DetectAPITypeFromPath(req.URL.Path)
	if apiType == "" {
		apiType = DetectAPITypeFromBodyAndProvider(body, providerName)
	}
	if err := a.validateModelSurface(providerName, model, apiType); err != nil {
		return ResponseMetadata{}, err
	}

	if raw != nil {
		if strippedModel, hasPrefix := stripProviderPrefix(model); hasPrefix {
			raw["model"] = strippedModel
			model = strippedModel
		}
		normalizeProviderRequest(raw, providerName)
		if a.billingCalculator != nil {
			if stream, ok := raw["stream"].(bool); ok && stream {
				if !nativeStreamUsageProviders[providerName] && apiType != APITypeResponses {
					// Merge include_usage into existing stream_options if present
					streamOpts, ok := raw["stream_options"].(map[string]any)
					if !ok {
						streamOpts = make(map[string]any)
						raw["stream_options"] = streamOpts
					}
					streamOpts["include_usage"] = true
				}
			}
		}
		var err error
		body, err = json.Marshal(raw)
		if err != nil {
			return ResponseMetadata{}, fmt.Errorf("failed to marshal request body: %w", err)
		}
	} else if strippedModel, hasPrefix := stripProviderPrefix(model); hasPrefix {
		if rewrittenBody, contentType, ok := rewriteRequestModel(req.Header.Get("Content-Type"), body, strippedModel); ok {
			body = rewrittenBody
			req.Header.Set("Content-Type", contentType)
		}
		model = strippedModel
	}

	meta, _, err := provider.BodyParser().Parse(io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		if !canBuildPassthroughMetadata(apiType, model) {
			return ResponseMetadata{}, err
		}
		meta = BodyMetadata{Model: model, Custom: make(map[string]any)}
	}
	if meta.Model == "" && model != "" {
		meta.Model = model
	}

	if meta.Custom == nil {
		meta.Custom = make(map[string]any)
	}
	meta.Custom["api_type"] = apiType
	meta.Custom["provider"] = providerName
	meta.Stream = true

	upstreamURL, err := provider.URLResolver().Resolve(meta)
	if err != nil {
		return ResponseMetadata{}, err
	}

	upstreamReq, err := http.NewRequestWithContext(ctx, req.Method, upstreamURL.String(), bytes.NewReader(body))
	if err != nil {
		return ResponseMetadata{}, err
	}

	for k, v := range req.Header {
		upstreamReq.Header[k] = v
	}

	if err := provider.RequestEnricher().Enrich(upstreamReq, meta, body); err != nil {
		return ResponseMetadata{}, err
	}
	disableUpstreamResponseCompression(upstreamReq)

	ctxValue := MetaContextValue{Meta: meta, RawBody: body}
	upstreamReq = upstreamReq.WithContext(context.WithValue(upstreamReq.Context(), MetaContextKey{}, ctxValue))

	// Wrap with interceptor chain (mirrors Forward method pattern)
	chain := a.interceptors
	doRequest := func(req *http.Request) (*http.Response, ResponseMetadata, []byte, error) {
		resp, err := a.client.Do(req)
		if err != nil {
			return nil, ResponseMetadata{}, nil, err
		}
		// For streaming: return response with body still open.
		// ResponseMetadata will be extracted during streaming.
		return resp, ResponseMetadata{}, nil, nil
	}

	if len(chain) > 0 {
		doRequest = chain.Wrap(doRequest)
	}

	upstreamResp, _, _, err := doRequest(upstreamReq)
	if err != nil {
		return ResponseMetadata{}, err
	}
	if upstreamResp == nil {
		return ResponseMetadata{}, errors.New("no response from upstream")
	}
	defer upstreamResp.Body.Close()

	// Declare HTTP trailers for billing headers (must be before WriteHeader)
	if a.billingCalculator != nil {
		w.Header().Set("Trailer", gatewayBillingTrailerHeader())
	}

	copyResponseHeaders(w, upstreamResp.Header)

	w.WriteHeader(upstreamResp.StatusCode)

	var sseWriter *sseTerminalHoldingWriter
	streamWriter := w
	if a.billingCalculator != nil && IsSSEStream(upstreamResp.Header.Get("Content-Type")) {
		sseWriter = newSSETerminalHoldingWriter(w)
		streamWriter = sseWriter
	}

	rc := http.NewResponseController(streamWriter)

	extractor := provider.ResponseExtractor()
	streamExtractor, isStreaming := extractor.(StreamingResponseExtractor)

	var respMeta ResponseMetadata

	if isStreaming && streamExtractor.IsStreamingResponse(upstreamResp) {
		respMeta, err = streamExtractor.ExtractStreamingWithController(upstreamResp, streamWriter, rc)
		if err != nil {
			return respMeta, err
		}
	} else {
		respMeta, err = a.streamResponseWithFlush(upstreamResp.Body, streamWriter, rc, extractor)
		if err != nil {
			return respMeta, err
		}
	}

	if a.billingCalculator != nil {
		a.billingCalculator.Calculate(meta, &respMeta)
		// Set billing headers as HTTP trailers (sent after body completes)
		if billing, ok := respMeta.Custom["billing_result"].(BillingResult); ok {
			w.Header().Set("X-Gateway-Cost", fmt.Sprintf("%.6f", billing.TotalCost))
			w.Header().Set("X-Gateway-Prompt-Tokens", fmt.Sprintf("%d", billing.PromptTokens))
			w.Header().Set("X-Gateway-Completion-Tokens", fmt.Sprintf("%d", billing.CompletionTokens))
			setGatewayMeteredBillingHeaders(w.Header(), billing)
			if sseWriter != nil {
				if err := writeGatewayMetadataEvent(w, rc, billing); err != nil {
					return respMeta, err
				}
			}
		}
	}
	if sseWriter != nil {
		if err := sseWriter.FlushTerminal(); err != nil {
			return respMeta, err
		}
		if err := rc.Flush(); err != nil {
			return respMeta, err
		}
	}

	return respMeta, nil
}

type sseTerminalHoldingWriter struct {
	http.ResponseWriter
	terminal []byte
}

func newSSETerminalHoldingWriter(w http.ResponseWriter) *sseTerminalHoldingWriter {
	return &sseTerminalHoldingWriter{ResponseWriter: w}
}

func (w *sseTerminalHoldingWriter) Write(data []byte) (int, error) {
	idx := bytes.Index(data, []byte("data: [DONE]"))
	if idx < 0 {
		n, err := w.ResponseWriter.Write(data)
		if err != nil {
			return n, err
		}
		return len(data), nil
	}

	if idx > 0 {
		if _, err := w.ResponseWriter.Write(data[:idx]); err != nil {
			return 0, err
		}
	}
	w.terminal = append(w.terminal, data[idx:]...)
	return len(data), nil
}

func (w *sseTerminalHoldingWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *sseTerminalHoldingWriter) HasTerminal() bool {
	return len(w.terminal) > 0
}

func (w *sseTerminalHoldingWriter) FlushTerminal() error {
	if len(w.terminal) == 0 {
		return nil
	}
	_, err := w.ResponseWriter.Write(w.terminal)
	w.terminal = nil
	return err
}

func writeGatewayMetadataEvent(w http.ResponseWriter, rc *http.ResponseController, billing BillingResult) error {
	payload := map[string]any{
		"type": "gateway.metadata",
		"data": map[string]any{
			"cost": map[string]any{
				"total":            billing.TotalCost,
				"promptTokens":     billing.PromptTokens,
				"completionTokens": billing.CompletionTokens,
				"unit":             billing.Unit,
				"inputQuantity":    billing.InputQuantity,
				"outputQuantity":   billing.OutputQuantity,
			},
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	if _, err := w.Write([]byte("event: gateway.metadata\n")); err != nil {
		return err
	}
	if _, err := w.Write([]byte("data: ")); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if _, err := w.Write([]byte("\n\n")); err != nil {
		return err
	}

	return rc.Flush()
}

func gatewayBillingTrailerHeader() string {
	return strings.Join([]string{
		"X-Gateway-Cost",
		"X-Gateway-Prompt-Tokens",
		"X-Gateway-Completion-Tokens",
		"X-Gateway-Billing-Unit",
		"X-Gateway-Input-Quantity",
		"X-Gateway-Output-Quantity",
	}, ",")
}

func setGatewayMeteredBillingHeaders(header http.Header, billing BillingResult) {
	if billing.Unit != "" {
		header.Set("X-Gateway-Billing-Unit", billing.Unit)
	}
	if billing.InputQuantity != 0 {
		header.Set("X-Gateway-Input-Quantity", fmt.Sprintf("%.6f", billing.InputQuantity))
	}
	if billing.OutputQuantity != 0 {
		header.Set("X-Gateway-Output-Quantity", fmt.Sprintf("%.6f", billing.OutputQuantity))
	}
}

func (a *AutoRouter) streamResponseWithFlush(r io.Reader, w http.ResponseWriter, rc *http.ResponseController, extractor ResponseExtractor) (ResponseMetadata, error) {
	var buf bytes.Buffer
	tee := io.TeeReader(r, &buf)

	respMeta, _, err := extractor.Extract(&http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(tee),
	})
	if err != nil {
		return respMeta, err
	}

	readBuf := make([]byte, 1024*512)
	for {
		n, err := buf.Read(readBuf)
		if err != nil {
			if err == io.EOF {
				if n > 0 {
					if _, writeErr := w.Write(readBuf[:n]); writeErr != nil {
						return respMeta, fmt.Errorf("write chunk: %w", writeErr)
					}
				}
				break
			}
			if errors.Is(err, context.Canceled) {
				break
			}
			return respMeta, fmt.Errorf("copy chunk: %w", err)
		}
		if n == 0 {
			break
		}
		if _, writeErr := w.Write(readBuf[:n]); writeErr != nil {
			return respMeta, fmt.Errorf("write chunk: %w", writeErr)
		}
		if flushErr := rc.Flush(); flushErr != nil {
			return respMeta, fmt.Errorf("flush: %w", flushErr)
		}
	}

	return respMeta, nil
}

func (a *AutoRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if isWebSocketUpgrade(r) && a.wsUpgrader != nil && a.wsDialer != nil {
		if err := a.ForwardWebSocket(r.Context(), w, r); err != nil {
			if !headerSent(w) {
				http.Error(w, err.Error(), statusCodeForForwardError(err))
			}
		}
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	r.Body.Close()

	var raw map[string]any
	var isStreamingRequest bool
	if DetectAPITypeFromPath(r.URL.Path) == APITypeStreamGenerateContent ||
		strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		isStreamingRequest = true
	}
	if err := json.Unmarshal(body, &raw); err == nil {
		if stream, ok := raw["stream"].(bool); ok && stream {
			isStreamingRequest = true
		}
	}

	r.Body = io.NopCloser(bytes.NewReader(body))

	if isStreamingRequest {
		_, err := a.ForwardStreaming(r.Context(), r, w)
		if err != nil {
			if !headerSent(w) {
				http.Error(w, err.Error(), statusCodeForForwardError(err))
			}
			return
		}
		// Billing headers are sent as HTTP trailers in ForwardStreaming
		return
	}

	resp, meta, err := a.Forward(r.Context(), r)
	if err != nil {
		http.Error(w, err.Error(), statusCodeForForwardError(err))
		return
	}
	defer resp.Body.Close()

	copyResponseHeaders(w, resp.Header)

	if billing, ok := meta.Custom["billing_result"].(BillingResult); ok {
		w.Header().Set("X-Gateway-Cost", fmt.Sprintf("%.6f", billing.TotalCost))
		w.Header().Set("X-Gateway-Prompt-Tokens", fmt.Sprintf("%d", billing.PromptTokens))
		w.Header().Set("X-Gateway-Completion-Tokens", fmt.Sprintf("%d", billing.CompletionTokens))
		setGatewayMeteredBillingHeaders(w.Header(), billing)
	}

	w.WriteHeader(resp.StatusCode)

	rc := http.NewResponseController(w)
	readBuf := make([]byte, 1024*512)
	for {
		n, err := resp.Body.Read(readBuf)
		if err != nil {
			if err == io.EOF {
				if n > 0 {
					if _, writeErr := w.Write(readBuf[:n]); writeErr != nil {
						return
					}
				}
				break
			}
			if errors.Is(err, context.Canceled) {
				break
			}
			return
		}
		if n == 0 {
			break
		}
		if _, writeErr := w.Write(readBuf[:n]); writeErr != nil {
			return
		}
		_ = rc.Flush()
	}
}

func statusCodeForForwardError(err error) int {
	if isForwardTimeoutError(err) {
		return http.StatusGatewayTimeout
	}
	return http.StatusInternalServerError
}

func isForwardTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "timeout awaiting response headers")
}

func isWebSocketUpgrade(r *http.Request) bool {
	connection := strings.ToLower(r.Header.Get("Connection"))
	upgrade := strings.ToLower(r.Header.Get("Upgrade"))
	return strings.Contains(connection, "upgrade") && strings.Contains(upgrade, "websocket")
}

func headerSent(w http.ResponseWriter) bool {
	type headerChecker interface {
		WroteHeader() bool
	}
	if hc, ok := w.(headerChecker); ok {
		return hc.WroteHeader()
	}
	return false
}

var ErrNoProvider = &ProviderError{Message: "no provider available for request"}

type ProviderError struct {
	Message string
}

func (e *ProviderError) Error() string {
	return e.Message
}

// nativeStreamUsageProviders are providers that include usage data
// natively in their streaming events without needing stream_options.
var nativeStreamUsageProviders = map[string]bool{
	"anthropic": true,
	"bedrock":   true,
	"googleai":  true,
}

var knownProviderPrefixes = map[string]bool{
	"openai":     true,
	"anthropic":  true,
	"googleai":   true,
	"groq":       true,
	"fireworks":  true,
	"xai":        true,
	"perplexity": true,
	"bedrock":    true,
	"azure":      true,
	"mistral":    true,
	"deepseek":   true,
	"cohere":     true,
}

func (a *AutoRouter) validateModelSurface(providerName string, model string, apiType APIType) error {
	if a == nil || a.modelMetadataLookup == nil || model == "" {
		return nil
	}
	lookupModel := model
	if stripped, ok := stripProviderPrefix(lookupModel); ok {
		lookupModel = stripped
	}
	metadata, ok := a.modelMetadataLookup(providerName, lookupModel)
	if !ok {
		return nil
	}
	return validateAPITypeAgainstModel(apiType, providerName, lookupModel, metadata)
}

func providerFromAPIType(apiType APIType) string {
	switch apiType {
	case APITypeGenerateContent, APITypeStreamGenerateContent, APITypePredictLongRunning:
		return "googleai"
	default:
		return ""
	}
}

func extractRequestModel(headers http.Header, body []byte) (string, map[string]any) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err == nil {
		if model, ok := raw["model"].(string); ok {
			return model, raw
		}
		return "", raw
	}

	contentType := headers.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", nil
	}

	switch mediaType {
	case "multipart/form-data":
		boundary := params["boundary"]
		if boundary == "" {
			return "", nil
		}
		return extractMultipartModel(body, boundary), nil
	case "application/x-www-form-urlencoded":
		values, err := url.ParseQuery(string(body))
		if err != nil {
			return "", nil
		}
		return values.Get("model"), nil
	default:
		return "", nil
	}
}

func extractMultipartModel(body []byte, boundary string) string {
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := reader.NextPart()
		if err != nil {
			return ""
		}
		if part.FormName() != "model" {
			_ = part.Close()
			continue
		}
		data, err := io.ReadAll(part)
		_ = part.Close()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(data))
	}
}

func rewriteRequestModel(contentType string, body []byte, model string) ([]byte, string, bool) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, "", false
	}

	switch mediaType {
	case "multipart/form-data":
		boundary := params["boundary"]
		if boundary == "" {
			return nil, "", false
		}
		rewrittenBody, rewrittenContentType, err := rewriteMultipartModel(body, boundary, model)
		if err != nil {
			return nil, "", false
		}
		return rewrittenBody, rewrittenContentType, true
	case "application/x-www-form-urlencoded":
		values, err := url.ParseQuery(string(body))
		if err != nil {
			return nil, "", false
		}
		values.Set("model", model)
		return []byte(values.Encode()), contentType, true
	default:
		return nil, "", false
	}
}

func rewriteMultipartModel(body []byte, boundary string, model string) ([]byte, string, error) {
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	var output bytes.Buffer
	writer := multipart.NewWriter(&output)
	wroteModel := false

	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, "", err
		}

		if part.FormName() == "model" {
			if err := writer.WriteField("model", model); err != nil {
				_ = part.Close()
				return nil, "", err
			}
			wroteModel = true
			_ = part.Close()
			continue
		}

		dst, err := writer.CreatePart(part.Header)
		if err != nil {
			_ = part.Close()
			return nil, "", err
		}
		if _, err := io.Copy(dst, part); err != nil {
			_ = part.Close()
			return nil, "", err
		}
		_ = part.Close()
	}

	if !wroteModel {
		if err := writer.WriteField("model", model); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return output.Bytes(), writer.FormDataContentType(), nil
}

func canBuildPassthroughMetadata(apiType APIType, model string) bool {
	if model == "" {
		return false
	}
	switch apiType {
	case APITypeAudioTranscriptions:
		return true
	default:
		return false
	}
}

func extractModelFromPath(path string) string {
	const marker = "/models/"
	idx := strings.Index(path, marker)
	if idx < 0 {
		return ""
	}
	value := path[idx+len(marker):]
	if slash := strings.Index(value, "/"); slash >= 0 {
		value = value[:slash]
	}
	if colon := strings.Index(value, ":"); colon >= 0 {
		value = value[:colon]
	}
	return strings.TrimSpace(value)
}

func stripProviderPrefix(model string) (stripped string, hasPrefix bool) {
	idx := strings.Index(model, "/")
	if idx < 0 {
		return model, false
	}
	prefix := model[:idx]
	if knownProviderPrefixes[prefix] {
		stripped = model[idx+1:]
		if strings.HasPrefix(stripped, prefix+"/") {
			stripped = strings.TrimPrefix(stripped, prefix+"/")
		}
		return stripped, true
	}
	return model, false
}

func normalizeProviderRequest(raw map[string]any, providerName string) {
	if providerName == "googleai" {
		normalizeGoogleAIRequest(raw)
		return
	}

	if providerName == "anthropic" {
		normalizeAnthropicRequest(raw)
		return
	}

	if providerName == "openai" {
		normalizeOpenAIRequest(raw)
		return
	}

	if providerName != "deepseek" {
		return
	}

	reasoning, hasReasoning := raw["reasoning"]
	if !hasReasoning {
		return
	}

	switch value := reasoning.(type) {
	case string:
		switch strings.ToLower(value) {
		case "", "off", "false", "none", "disabled":
			delete(raw, "reasoning")
			delete(raw, "reasoning_effort")
			raw["thinking"] = map[string]any{"type": "disabled"}
		case "low", "medium", "high", "max", "xhigh":
			delete(raw, "reasoning")
			raw["thinking"] = map[string]any{"type": "enabled"}
			raw["reasoning_effort"] = value
		}
	case bool:
		delete(raw, "reasoning")
		if value {
			raw["thinking"] = map[string]any{"type": "enabled"}
		} else {
			delete(raw, "reasoning_effort")
			raw["thinking"] = map[string]any{"type": "disabled"}
		}
	}
}

const defaultAnthropicMaxTokens = 1024

func normalizeAnthropicRequest(raw map[string]any) {
	normalizeAnthropicSystemMessages(raw)

	if hasPositiveNumber(raw["max_tokens"]) {
		return
	}
	raw["max_tokens"] = defaultAnthropicMaxTokens
}

func normalizeAnthropicSystemMessages(raw map[string]any) {
	messages, ok := raw["messages"].([]any)
	if !ok || len(messages) == 0 {
		return
	}

	filtered := make([]any, 0, len(messages))
	systemParts := make([]any, 0, 1)
	for _, item := range messages {
		message, ok := item.(map[string]any)
		if !ok {
			filtered = append(filtered, item)
			continue
		}
		role, _ := message["role"].(string)
		if role != "system" {
			filtered = append(filtered, item)
			continue
		}
		if content, exists := message["content"]; exists {
			systemParts = append(systemParts, content)
		}
	}

	raw["messages"] = filtered
	if len(systemParts) > 0 {
		raw["system"] = mergeAnthropicSystem(raw["system"], systemParts)
	}
}

func mergeAnthropicSystem(existing any, systemParts []any) any {
	systemText := joinTextParts(systemParts)
	if existing == nil {
		if systemText != "" {
			return systemText
		}
		return systemParts[0]
	}

	existingText := joinTextParts([]any{existing})
	if existingText != "" && systemText != "" {
		return existingText + "\n\n" + systemText
	}
	if systemText != "" {
		return systemText
	}
	return existing
}

func joinTextParts(parts []any) string {
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		switch v := part.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				values = append(values, v)
			}
		case []any:
			for _, item := range v {
				block, ok := item.(map[string]any)
				if !ok {
					continue
				}
				blockType, _ := block["type"].(string)
				text, _ := block["text"].(string)
				if blockType == "text" && strings.TrimSpace(text) != "" {
					values = append(values, text)
				}
			}
		}
	}
	return strings.Join(values, "\n\n")
}

func hasPositiveNumber(value any) bool {
	switch v := value.(type) {
	case int:
		return v > 0
	case int8:
		return v > 0
	case int16:
		return v > 0
	case int32:
		return v > 0
	case int64:
		return v > 0
	case uint:
		return v > 0
	case uint8:
		return v > 0
	case uint16:
		return v > 0
	case uint32:
		return v > 0
	case uint64:
		return v > 0
	case float32:
		return v > 0
	case float64:
		return v > 0
	case json.Number:
		f, err := v.Float64()
		return err == nil && f > 0
	default:
		return false
	}
}

func normalizeOpenAIRequest(raw map[string]any) {
	if !openAIModelUsesMaxCompletionTokens(raw["model"]) {
		return
	}

	maxTokens, ok := raw["max_tokens"]
	if !ok {
		return
	}
	if _, exists := raw["max_completion_tokens"]; !exists {
		raw["max_completion_tokens"] = maxTokens
	}
	delete(raw, "max_tokens")
}

func openAIModelUsesMaxCompletionTokens(model any) bool {
	modelName, ok := model.(string)
	if !ok {
		return false
	}
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	if stripped, hasPrefix := stripProviderPrefix(modelName); hasPrefix {
		modelName = stripped
	}
	return strings.HasPrefix(modelName, "gpt-5") ||
		strings.HasPrefix(modelName, "o1") ||
		strings.HasPrefix(modelName, "o3") ||
		strings.HasPrefix(modelName, "o4")
}

func normalizeGoogleAIRequest(raw map[string]any) {
	delete(raw, "stream")

	if maxTokens, ok := raw["max_tokens"]; ok {
		generationConfig, _ := raw["generationConfig"].(map[string]any)
		if generationConfig == nil {
			generationConfig = make(map[string]any)
			raw["generationConfig"] = generationConfig
		}
		if _, exists := generationConfig["maxOutputTokens"]; !exists {
			generationConfig["maxOutputTokens"] = maxTokens
		}
		delete(raw, "max_tokens")
	}

	if systemInstruction, ok := raw["system_instruction"].(string); ok {
		if _, exists := raw["systemInstruction"]; !exists && systemInstruction != "" {
			raw["systemInstruction"] = map[string]any{
				"parts": []any{map[string]any{"text": systemInstruction}},
			}
		}
		delete(raw, "system_instruction")
	}

	if messages, ok := raw["messages"].([]any); ok {
		if _, exists := raw["contents"]; !exists {
			contents := make([]any, 0, len(messages))
			for _, item := range messages {
				message, ok := item.(map[string]any)
				if !ok {
					continue
				}
				content, ok := message["content"].(string)
				if !ok || content == "" {
					continue
				}
				role, _ := message["role"].(string)
				if role == "assistant" {
					role = "model"
				} else {
					role = "user"
				}
				contents = append(contents, map[string]any{
					"role":  role,
					"parts": []any{map[string]any{"text": content}},
				})
			}
			raw["contents"] = contents
		}
		delete(raw, "messages")
	}
}
