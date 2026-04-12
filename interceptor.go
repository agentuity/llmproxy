package llmproxy

import (
	"context"
	"net/http"
)

// Interceptor wraps the request/response cycle for cross-cutting concerns.
//
// Interceptors form a chain around the actual request execution, allowing
// behavior to be added before and after the upstream call. Common uses include:
//   - Logging request/response details
//   - Collecting metrics (latency, token usage)
//   - Retrying failed requests
//   - Rate limiting
//   - Caching responses
//
// Interceptors must call next(req) to continue the chain. Not calling next
// will short-circuit the request (useful for caching or mocking).
type Interceptor interface {
	// Intercept processes a request through the interceptor chain.
	//
	// Parameters:
	//   - req: The HTTP request to send upstream
	//   - meta: Parsed metadata from the request body
	//   - rawBody: The original request body bytes
	//   - next: The next handler in the chain (call this to continue)
	//
	// Returns:
	//   - resp: The HTTP response (body will be re-attached from rawRespBody)
	//   - respMeta: Parsed response metadata
	//   - rawRespBody: The raw response body bytes
	//   - error: Any error that occurred
	Intercept(req *http.Request, meta BodyMetadata, rawBody []byte, next RoundTripFunc) (resp *http.Response, respMeta ResponseMetadata, rawRespBody []byte, err error)
}

// RoundTripFunc is the signature for executing a request through the chain.
// It returns the response, metadata, raw response body, and error.
type RoundTripFunc func(*http.Request) (*http.Response, ResponseMetadata, []byte, error)

// InterceptorChain is an ordered list of interceptors that are applied
// in sequence. Interceptors are applied in reverse order during wrapping
// so that they execute in forward order during request processing.
type InterceptorChain []Interceptor

// Wrap chains all interceptors around the final RoundTripFunc.
// Interceptors are wrapped in reverse order so they execute in forward order.
//
// Example: Given interceptors [A, B, C] and final function F:
//   - Wrapping produces: A(B(C(F)))
//   - Execution order: A -> B -> C -> F -> C -> B -> A
func (c InterceptorChain) Wrap(final RoundTripFunc) RoundTripFunc {
	for i := len(c) - 1; i >= 0; i-- {
		final = wrapInterceptor(c[i], final)
	}
	return final
}

func wrapInterceptor(interceptor Interceptor, next RoundTripFunc) RoundTripFunc {
	return func(req *http.Request) (*http.Response, ResponseMetadata, []byte, error) {
		metaRaw := GetMetaFromContext(req.Context())
		return interceptor.Intercept(req, metaRaw.Meta, metaRaw.RawBody, next)
	}
}

// MetaContextKey is the context key for storing request metadata.
type MetaContextKey struct{}

// MetaContextValue holds the metadata stored in request context.
type MetaContextValue struct {
	Meta    BodyMetadata
	RawBody []byte
}

// GetMetaFromContext retrieves the metadata stored in a context.
// Returns an empty MetaContextValue if the context is nil or doesn't contain metadata.
func GetMetaFromContext(ctx context.Context) MetaContextValue {
	if ctx == nil {
		return MetaContextValue{}
	}
	if v := ctx.Value(MetaContextKey{}); v != nil {
		if meta, ok := v.(MetaContextValue); ok {
			return meta
		}
	}
	return MetaContextValue{}
}
