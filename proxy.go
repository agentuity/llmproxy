package llmproxy

import (
	"bytes"
	"context"
	"io"
	"net/http"
)

// Proxy forwards requests to an upstream LLM provider.
//
// Proxy handles the complete request lifecycle:
//  1. Reads and parses the request body
//  2. Resolves the upstream URL
//  3. Creates and enriches the upstream request
//  4. Executes the request through the interceptor chain
//  5. Extracts metadata from the response
//  6. Re-attaches the raw response body
//
// Use NewProxy with functional options to configure:
//
//	proxy := NewProxy(provider,
//	    WithInterceptor(loggingInterceptor),
//	    WithHTTPClient(customClient),
//	)
type Proxy struct {
	provider           Provider
	globalInterceptors InterceptorChain
	client             *http.Client
}

// ProxyOption configures a Proxy during construction.
type ProxyOption func(*Proxy)

// WithInterceptor adds an interceptor to the global chain.
// Interceptors are applied in the order they are added.
func WithInterceptor(i Interceptor) ProxyOption {
	return func(p *Proxy) { p.globalInterceptors = append(p.globalInterceptors, i) }
}

// WithHTTPClient sets a custom HTTP client for upstream requests.
// If not set, http.DefaultClient is used.
func WithHTTPClient(c *http.Client) ProxyOption {
	return func(p *Proxy) { p.client = c }
}

// NewProxy creates a new proxy for the given provider.
// Options can be used to add interceptors or customize the HTTP client.
func NewProxy(provider Provider, opts ...ProxyOption) *Proxy {
	p := &Proxy{
		provider: provider,
		client:   http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Forward sends a request to the upstream provider and returns the response.
//
// The method:
//  1. Reads and parses the request body to extract metadata
//  2. Resolves the upstream URL based on the metadata
//  3. Creates a new request for the upstream, copying headers
//  4. Enriches the request with provider-specific headers
//  5. Executes the request through the interceptor chain
//  6. Extracts metadata from the response
//  7. Re-attaches the raw response body so the caller can read it
//
// The returned response body contains the original raw bytes from the upstream
// and can be read by the caller. Any custom/unsupported fields in the JSON
// are preserved.
func (p *Proxy) Forward(ctx context.Context, req *http.Request) (*http.Response, ResponseMetadata, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, ResponseMetadata{}, err
	}
	req.Body.Close()

	meta, _, err := p.provider.BodyParser().Parse(io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		return nil, ResponseMetadata{}, err
	}

	upstreamURL, err := p.provider.URLResolver().Resolve(meta)
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

	if err := p.provider.RequestEnricher().Enrich(upstreamReq, meta, body); err != nil {
		return nil, ResponseMetadata{}, err
	}

	ctxValue := MetaContextValue{Meta: meta, RawBody: body}
	upstreamReq = upstreamReq.WithContext(context.WithValue(upstreamReq.Context(), MetaContextKey{}, ctxValue))

	chain := p.globalInterceptors
	roundTrip := p.roundTrip

	if len(chain) > 0 {
		roundTrip = chain.Wrap(roundTrip)
	}

	resp, respMeta, rawRespBody, err := roundTrip(upstreamReq)
	if err != nil {
		return nil, respMeta, err
	}

	// Re-attach the raw response body so the caller can read it
	resp.Body = io.NopCloser(bytes.NewReader(rawRespBody))
	return resp, respMeta, nil
}

func (p *Proxy) roundTrip(req *http.Request) (*http.Response, ResponseMetadata, []byte, error) {
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, ResponseMetadata{}, nil, err
	}

	respMeta, rawBody, err := p.provider.ResponseExtractor().Extract(resp)
	if err != nil {
		return nil, ResponseMetadata{}, nil, err
	}

	return resp, respMeta, rawBody, nil
}
