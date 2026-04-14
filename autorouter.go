package llmproxy

import (
	"bytes"
	"context"
	"encoding/json"
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

	// If no provider detected and we have a model provider lookup, try that
	if providerName == "" && a.modelProviderLookup != nil && model != "" {
		providerName = a.modelProviderLookup(model)
	}

	var provider Provider
	if providerName != "" {
		provider, _ = a.registry.Get(providerName)
		if provider == nil {
			// Explicit provider name was provided but not found in registry
			return nil, ResponseMetadata{}, ErrNoProvider
		}
	} else {
		// No provider detected, use fallback
		provider = a.fallbackProvider
		if provider == nil {
			return nil, ResponseMetadata{}, ErrNoProvider
		}
	}

	// Strip provider prefix from model name (e.g., "openai/gpt-4" -> "gpt-4")
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

	// Detect API type: path takes precedence, then body+provider detection
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

func (a *AutoRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	resp, meta, err := a.Forward(r.Context(), r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for k, v := range resp.Header {
		w.Header()[k] = v
	}

	if billing, ok := meta.Custom["billing_result"].(BillingResult); ok {
		w.Header().Set("X-Gateway-Cost", fmt.Sprintf("%.6f", billing.TotalCost))
		w.Header().Set("X-Gateway-Prompt-Tokens", fmt.Sprintf("%d", billing.PromptTokens))
		w.Header().Set("X-Gateway-Completion-Tokens", fmt.Sprintf("%d", billing.CompletionTokens))
	}

	w.WriteHeader(resp.StatusCode)
	if _, err := w.Write(body); err != nil {
		// Headers already sent, can't report error to client
	}
}

var ErrNoProvider = &ProviderError{Message: "no provider available for request"}

type ProviderError struct {
	Message string
}

func (e *ProviderError) Error() string {
	return e.Message
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
