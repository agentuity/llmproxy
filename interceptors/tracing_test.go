package interceptors

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentuity/llmproxy"
)

func TestTracingInterceptor_WithTrace(t *testing.T) {
	traceID := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	spanID := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}

	extractor := func(ctx context.Context) TraceInfo {
		return TraceInfo{
			TraceID: traceID,
			SpanID:  spanID,
			Sampled: true,
		}
	}

	tracing := NewTracing(extractor)

	var capturedReq *http.Request
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"chatcmpl-123","object":"chat.completion","model":"gpt-4","usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15},"choices":[{"index":0,"message":{"role":"assistant","content":"Hello!"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	req, _ := http.NewRequestWithContext(context.Background(), "POST", upstream.URL, nil)

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	resp, _, _, err := tracing.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}

	traceIDHex := hex.EncodeToString(traceID[:])
	spanIDHex := hex.EncodeToString(spanID[:])

	if got := capturedReq.Header.Get("X-Request-ID"); got != traceIDHex {
		t.Errorf("X-Request-ID header = %q, want %q", got, traceIDHex)
	}

	expectedTraceparent := "00-" + traceIDHex + "-" + spanIDHex + "-01"
	if got := capturedReq.Header.Get("traceparent"); got != expectedTraceparent {
		t.Errorf("traceparent header = %q, want %q", got, expectedTraceparent)
	}

	if got := resp.Header.Get("X-Request-ID"); got != traceIDHex {
		t.Errorf("Response X-Request-ID header = %q, want %q", got, traceIDHex)
	}
}

func TestTracingInterceptor_NoTrace(t *testing.T) {
	extractor := func(ctx context.Context) TraceInfo {
		return TraceInfo{}
	}

	tracing := NewTracing(extractor)

	var capturedReq *http.Request
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"chatcmpl-123"}`))
	}))
	defer upstream.Close()

	req, _ := http.NewRequestWithContext(context.Background(), "POST", upstream.URL, nil)

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	resp, _, _, err := tracing.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}

	if got := capturedReq.Header.Get("X-Request-ID"); got != "" {
		t.Errorf("X-Request-ID header should be empty, got %q", got)
	}

	if got := capturedReq.Header.Get("traceparent"); got != "" {
		t.Errorf("traceparent header should be empty, got %q", got)
	}

	if got := resp.Header.Get("X-Request-ID"); got != "" {
		t.Errorf("Response X-Request-ID header should be empty, got %q", got)
	}
}

func TestTracingInterceptor_NilExtractor(t *testing.T) {
	tracing := &TracingInterceptor{}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	req, _ := http.NewRequestWithContext(context.Background(), "POST", upstream.URL, nil)

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	_, _, _, err := tracing.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestTracingInterceptor_CustomResponseHeader(t *testing.T) {
	traceID := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}

	extractor := func(ctx context.Context) TraceInfo {
		return TraceInfo{TraceID: traceID}
	}

	tracing := NewTracingWithHeader(extractor, "X-Correlation-ID")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	req, _ := http.NewRequestWithContext(context.Background(), "POST", upstream.URL, nil)

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	resp, _, _, err := tracing.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}

	traceIDHex := hex.EncodeToString(traceID[:])

	if got := resp.Header.Get("X-Correlation-ID"); got != traceIDHex {
		t.Errorf("Response X-Correlation-ID header = %q, want %q", got, traceIDHex)
	}

	if got := resp.Header.Get("X-Request-ID"); got != "" {
		t.Errorf("Response X-Request-ID header should be empty, got %q", got)
	}
}
