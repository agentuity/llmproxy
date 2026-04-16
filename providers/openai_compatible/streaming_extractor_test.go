package openai_compatible

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentuity/llmproxy"
)

func TestStreamingExtractor_ExtractStreaming(t *testing.T) {
	streamData := `data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}

data: [DONE]

`

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(streamData)),
	}

	recorder := httptest.NewRecorder()
	rc := http.NewResponseController(recorder)

	extractor := NewStreamingExtractor()

	meta, err := extractor.ExtractStreamingWithController(resp, recorder, rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if meta.ID != "chatcmpl-123" {
		t.Errorf("expected ID 'chatcmpl-123', got %q", meta.ID)
	}
	if meta.Model != "gpt-4" {
		t.Errorf("expected model 'gpt-4', got %q", meta.Model)
	}
	if meta.Usage.PromptTokens != 10 {
		t.Errorf("expected prompt tokens 10, got %d", meta.Usage.PromptTokens)
	}
	if meta.Usage.CompletionTokens != 5 {
		t.Errorf("expected completion tokens 5, got %d", meta.Usage.CompletionTokens)
	}

	output := recorder.Body.String()
	if !strings.Contains(output, "data: ") {
		t.Error("expected SSE data format in output")
	}
	if !strings.Contains(output, "[DONE]") {
		t.Error("expected [DONE] in output")
	}
}

func TestStreamingExtractor_IsStreamingResponse(t *testing.T) {
	extractor := NewStreamingExtractor()

	tests := []struct {
		contentType string
		expected    bool
	}{
		{"text/event-stream", true},
		{"text/event-stream; charset=utf-8", true},
		{"application/json", false},
		{"text/plain", false},
	}

	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			resp := &http.Response{
				Header: http.Header{"Content-Type": []string{tt.contentType}},
			}
			result := extractor.IsStreamingResponse(resp)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestStreamingExtractor_NonStreamingFallback(t *testing.T) {
	extractor := NewStreamingExtractor()

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"id":"test","model":"gpt-4","usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}}`))),
	}

	recorder := httptest.NewRecorder()
	rc := http.NewResponseController(recorder)

	meta, err := extractor.ExtractStreamingWithController(resp, recorder, rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if meta.Usage.PromptTokens != 100 {
		t.Errorf("expected prompt tokens 100, got %d", meta.Usage.PromptTokens)
	}
}

func TestStreamingExtractor_ExtractStreamingWithCache(t *testing.T) {
	streamData := `data: {"id":"chatcmpl-123","model":"gpt-4","choices":[{"index":0,"delta":{"content":"test"}}]}

data: {"id":"chatcmpl-123","model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":80}}}

data: [DONE]

`

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(streamData)),
	}

	recorder := httptest.NewRecorder()
	rc := http.NewResponseController(recorder)

	extractor := NewStreamingExtractor()

	meta, err := extractor.ExtractStreamingWithController(resp, recorder, rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if meta.Usage.PromptTokens != 100 {
		t.Errorf("expected prompt tokens 100, got %d", meta.Usage.PromptTokens)
	}

	cacheUsage, ok := meta.Custom["cache_usage"].(llmproxy.CacheUsage)
	if !ok {
		t.Fatal("expected cache_usage in custom map")
	}
	if cacheUsage.CachedTokens != 80 {
		t.Errorf("expected cached tokens 80, got %d", cacheUsage.CachedTokens)
	}
}

func TestStreamingExtractor_ExtractStreamingWithReasoning(t *testing.T) {
	streamData := `data: {"id":"chatcmpl-456","model":"o1","choices":[{"index":0,"delta":{"content":"test"}}]}

data: {"id":"chatcmpl-456","model":"o1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":75,"completion_tokens":1186,"total_tokens":1261,"completion_tokens_details":{"reasoning_tokens":1024}}}

data: [DONE]

`

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(streamData)),
	}

	recorder := httptest.NewRecorder()
	rc := http.NewResponseController(recorder)

	extractor := NewStreamingExtractor()

	meta, err := extractor.ExtractStreamingWithController(resp, recorder, rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if meta.Usage.PromptTokens != 75 {
		t.Errorf("expected prompt tokens 75, got %d", meta.Usage.PromptTokens)
	}
	if meta.Usage.CompletionTokens != 1186 {
		t.Errorf("expected completion tokens 1186, got %d", meta.Usage.CompletionTokens)
	}

	rt, ok := meta.Custom["reasoning_tokens"].(int)
	if !ok {
		t.Fatal("expected reasoning_tokens in custom map")
	}
	if rt != 1024 {
		t.Errorf("expected reasoning tokens 1024, got %d", rt)
	}
}

func TestStreamingExtractor_ExtractStreamingWithCacheAndReasoning(t *testing.T) {
	streamData := `data: {"id":"chatcmpl-789","model":"o1","choices":[{"index":0,"delta":{"content":"test"}}]}

data: {"id":"chatcmpl-789","model":"o1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":500,"total_tokens":600,"prompt_tokens_details":{"cached_tokens":80},"completion_tokens_details":{"reasoning_tokens":256}}}

data: [DONE]

`

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(streamData)),
	}

	recorder := httptest.NewRecorder()
	rc := http.NewResponseController(recorder)

	extractor := NewStreamingExtractor()

	meta, err := extractor.ExtractStreamingWithController(resp, recorder, rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cacheUsage, ok := meta.Custom["cache_usage"].(llmproxy.CacheUsage)
	if !ok {
		t.Fatal("expected cache_usage in custom map")
	}
	if cacheUsage.CachedTokens != 80 {
		t.Errorf("expected cached tokens 80, got %d", cacheUsage.CachedTokens)
	}

	rt, ok := meta.Custom["reasoning_tokens"].(int)
	if !ok {
		t.Fatal("expected reasoning_tokens in custom map")
	}
	if rt != 256 {
		t.Errorf("expected reasoning tokens 256, got %d", rt)
	}
}
