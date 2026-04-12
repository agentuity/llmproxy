package interceptors

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/agentuity/llmproxy"
)

// Metrics holds aggregated statistics about proxied requests.
// All fields are safe for concurrent access via atomic operations.
type Metrics struct {
	// TotalRequests is the total number of requests processed.
	TotalRequests int64
	// TotalTokens is the sum of all tokens consumed.
	TotalTokens int64
	// TotalPromptTokens is the sum of all prompt tokens consumed.
	TotalPromptTokens int64
	// TotalCompletionTokens is the sum of all completion tokens generated.
	TotalCompletionTokens int64
	// TotalLatency is the cumulative latency in nanoseconds.
	TotalLatency int64
	// Errors is the count of failed requests.
	Errors int64
}

// MetricsInterceptor collects metrics about proxied requests.
// It tracks request counts, token usage, latency, and errors.
type MetricsInterceptor struct {
	// Metrics is the destination for collected metrics.
	Metrics *Metrics
}

// Intercept increments metrics counters and measures latency.
// It records:
//   - TotalRequests (always)
//   - TotalLatency (always)
//   - Errors (on failure)
//   - Token counts (on success)
func (i *MetricsInterceptor) Intercept(req *http.Request, meta llmproxy.BodyMetadata, rawBody []byte, next llmproxy.RoundTripFunc) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
	start := time.Now()
	atomic.AddInt64(&i.Metrics.TotalRequests, 1)

	resp, respMeta, rawRespBody, err := next(req)

	atomic.AddInt64(&i.Metrics.TotalLatency, int64(time.Since(start)))
	if err != nil {
		atomic.AddInt64(&i.Metrics.Errors, 1)
		return resp, respMeta, rawRespBody, err
	}

	atomic.AddInt64(&i.Metrics.TotalTokens, int64(respMeta.Usage.TotalTokens))
	atomic.AddInt64(&i.Metrics.TotalPromptTokens, int64(respMeta.Usage.PromptTokens))
	atomic.AddInt64(&i.Metrics.TotalCompletionTokens, int64(respMeta.Usage.CompletionTokens))

	return resp, respMeta, rawRespBody, nil
}

// NewMetrics creates a new metrics interceptor that records to the given Metrics struct.
// The Metrics struct should be created once and shared across all requests.
//
// Example:
//
//	m := &interceptors.Metrics{}
//	proxy := llmproxy.NewProxy(provider,
//	    llmproxy.WithInterceptor(interceptors.NewMetrics(m)),
//	)
//	// Later, read m.TotalRequests, etc.
func NewMetrics(m *Metrics) *MetricsInterceptor {
	return &MetricsInterceptor{Metrics: m}
}
