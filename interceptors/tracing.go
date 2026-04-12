package interceptors

import (
	"context"
	"encoding/hex"
	"net/http"

	"github.com/agentuity/llmproxy"
)

// TraceInfo holds OpenTelemetry trace context information.
type TraceInfo struct {
	// TraceID is the 16-byte trace identifier (32 hex chars).
	TraceID [16]byte
	// SpanID is the 8-byte span identifier (16 hex chars).
	SpanID [8]byte
	// Sampled indicates whether the trace is sampled.
	Sampled bool
}

// TraceExtractor extracts trace information from a request context.
// Return empty TraceInfo if no trace context is available.
type TraceExtractor func(ctx context.Context) TraceInfo

// TracingInterceptor adds OpenTelemetry trace headers to upstream requests
// and propagates the trace ID back as a response header for correlation.
type TracingInterceptor struct {
	// Extract extracts trace info from the incoming request context.
	// If nil, no trace headers are added.
	Extract TraceExtractor
	// ResponseHeader is the header name for the trace ID in the response.
	// Defaults to "X-Request-ID" if empty.
	ResponseHeader string
}

// Intercept adds trace headers to the upstream request and sets the response header.
//
// Upstream headers set:
//   - X-Request-ID: the trace ID (32 hex chars)
//   - traceparent: W3C Trace Context format (version-traceid-spanid-flags)
//
// Response header set:
//   - X-Request-ID (or custom ResponseHeader): the trace ID for correlation
func (i *TracingInterceptor) Intercept(req *http.Request, meta llmproxy.BodyMetadata, rawBody []byte, next llmproxy.RoundTripFunc) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
	if i.Extract != nil {
		traceInfo := i.Extract(req.Context())
		if traceInfo.TraceID != [16]byte{} {
			traceIDHex := hex.EncodeToString(traceInfo.TraceID[:])
			spanIDHex := hex.EncodeToString(traceInfo.SpanID[:])

			req.Header.Set("X-Request-ID", traceIDHex)

			flags := "00"
			if traceInfo.Sampled {
				flags = "01"
			}
			traceparent := "00-" + traceIDHex + "-" + spanIDHex + "-" + flags
			req.Header.Set("traceparent", traceparent)
		}
	}

	resp, respMeta, rawRespBody, err := next(req)

	if i.Extract != nil && resp != nil {
		traceInfo := i.Extract(req.Context())
		if traceInfo.TraceID != [16]byte{} {
			headerName := i.ResponseHeader
			if headerName == "" {
				headerName = "X-Request-ID"
			}
			traceIDHex := hex.EncodeToString(traceInfo.TraceID[:])
			resp.Header.Set(headerName, traceIDHex)
		}
	}

	return resp, respMeta, rawRespBody, err
}

// NewTracing creates a tracing interceptor with the given trace extractor.
//
// The extractor function should pull trace context from the incoming request
// and return TraceInfo. For OpenTelemetry, you can use:
//
//	func otelExtractor(ctx context.Context) interceptors.TraceInfo {
//		span := trace.SpanFromContext(ctx)
//		if !span.SpanContext().IsValid() {
//			return interceptors.TraceInfo{}
//		}
//		return interceptors.TraceInfo{
//			TraceID: span.SpanContext().TraceID(),
//			SpanID:  span.SpanContext().SpanID(),
//			Sampled: span.SpanContext().IsSampled(),
//		}
//	}
//
// Example:
//
//	tracing := interceptors.NewTracing(otelExtractor)
//	proxy := llmproxy.NewProxy(provider, llmproxy.WithInterceptor(tracing))
func NewTracing(extractor TraceExtractor) *TracingInterceptor {
	return &TracingInterceptor{
		Extract:        extractor,
		ResponseHeader: "X-Request-ID",
	}
}

// NewTracingWithHeader creates a tracing interceptor with a custom response header name.
func NewTracingWithHeader(extractor TraceExtractor, responseHeader string) *TracingInterceptor {
	return &TracingInterceptor{
		Extract:        extractor,
		ResponseHeader: responseHeader,
	}
}
