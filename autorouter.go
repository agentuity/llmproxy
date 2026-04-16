package llmproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type AutoRouter struct {
	registry            Registry
	detector            ProviderDetector
	modelProviderLookup ModelProviderLookup
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

	var raw map[string]any
	var model string
	if err := json.Unmarshal(body, &raw); err == nil {
		if m, ok := raw["model"].(string); ok {
			model = m
		}
	}

	hint := ProviderHint{
		Model:   model,
		Headers: req.Header,
	}
	providerName := a.detector.Detect(hint)

	if providerName == "" && a.modelProviderLookup != nil && model != "" {
		providerName = a.modelProviderLookup(model)
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
			var err error
			body, err = json.Marshal(raw)
			if err != nil {
				return nil, ResponseMetadata{}, fmt.Errorf("failed to marshal request body: %w", err)
			}
		}
	}

	apiType := DetectAPITypeFromPath(req.URL.Path)
	if apiType == "" {
		apiType = DetectAPITypeFromBodyAndProvider(body, providerName)
	}

	meta, _, err := provider.BodyParser().Parse(io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		return nil, ResponseMetadata{}, err
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

	var raw map[string]any
	var model string
	if err := json.Unmarshal(body, &raw); err == nil {
		if m, ok := raw["model"].(string); ok {
			model = m
		}
	}

	hint := ProviderHint{
		Model:   model,
		Headers: req.Header,
	}
	providerName := a.detector.Detect(hint)

	if providerName == "" && a.modelProviderLookup != nil && model != "" {
		providerName = a.modelProviderLookup(model)
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

	if raw != nil {
		if strippedModel, hasPrefix := stripProviderPrefix(model); hasPrefix {
			raw["model"] = strippedModel
			model = strippedModel
		}
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
	}

	meta, _, err := provider.BodyParser().Parse(io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		return ResponseMetadata{}, err
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
		w.Header().Set("Trailer", "X-Gateway-Cost,X-Gateway-Prompt-Tokens,X-Gateway-Completion-Tokens")
	}

	for k, v := range upstreamResp.Header {
		if k != "Content-Length" {
			w.Header()[k] = v
		}
	}

	w.WriteHeader(upstreamResp.StatusCode)

	rc := http.NewResponseController(w)

	extractor := provider.ResponseExtractor()
	streamExtractor, isStreaming := extractor.(StreamingResponseExtractor)

	var respMeta ResponseMetadata

	if isStreaming && streamExtractor.IsStreamingResponse(upstreamResp) {
		respMeta, err = streamExtractor.ExtractStreamingWithController(upstreamResp, w, rc)
		if err != nil {
			return respMeta, err
		}
	} else {
		respMeta, err = a.streamResponseWithFlush(upstreamResp.Body, w, rc, extractor)
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
		}
	}

	return respMeta, nil
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
				http.Error(w, err.Error(), http.StatusInternalServerError)
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
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		// Billing headers are sent as HTTP trailers in ForwardStreaming
		return
	}

	resp, meta, err := a.Forward(r.Context(), r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	for k, v := range resp.Header {
		w.Header()[k] = v
	}

	if billing, ok := meta.Custom["billing_result"].(BillingResult); ok {
		w.Header().Set("X-Gateway-Cost", fmt.Sprintf("%.6f", billing.TotalCost))
		w.Header().Set("X-Gateway-Prompt-Tokens", fmt.Sprintf("%d", billing.PromptTokens))
		w.Header().Set("X-Gateway-Completion-Tokens", fmt.Sprintf("%d", billing.CompletionTokens))
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
}

func stripProviderPrefix(model string) (stripped string, hasPrefix bool) {
	idx := strings.Index(model, "/")
	if idx < 0 {
		return model, false
	}
	prefix := model[:idx]
	if knownProviderPrefixes[prefix] {
		return model[idx+1:], true
	}
	return model, false
}
