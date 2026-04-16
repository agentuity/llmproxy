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

func runResponsesStreamExtraction(t *testing.T, contentType, body string) (llmproxy.ResponseMetadata, string, error) {
	t.Helper()

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	recorder := httptest.NewRecorder()
	rc := http.NewResponseController(recorder)

	extractor := NewResponsesStreamingExtractor()
	meta, err := extractor.ExtractStreamingWithController(resp, recorder, rc)
	return meta, recorder.Body.String(), err
}

func TestResponsesStreamingExtractor_FullLifecycle(t *testing.T) {
	stream := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_123\",\"object\":\"response\",\"model\":\"gpt-4o\",\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.in_progress\",\"response\":{\"id\":\"resp_123\",\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\",\"id\":\"msg_1\"},\"output_index\":0}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello\",\"content_index\":0,\"output_index\":0,\"item_id\":\"msg_1\"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\" world\",\"content_index\":0,\"output_index\":0,\"item_id\":\"msg_1\"}\n\n" +
		"data: {\"type\":\"response.output_text.done\",\"text\":\"Hello world\",\"content_index\":0,\"output_index\":0}\n\n" +
		"data: {\"type\":\"response.content_part.done\",\"part\":{\"type\":\"output_text\",\"text\":\"Hello world\"},\"content_index\":0,\"output_index\":0}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_123\",\"object\":\"response\",\"model\":\"gpt-4o\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":5,\"total_tokens\":15}}}\n\n" +
		"data: [DONE]\n\n"

	meta, output, err := runResponsesStreamExtraction(t, "text/event-stream", stream)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if output != stream {
		t.Fatalf("stream passthrough mismatch\nwant:\n%s\ngot:\n%s", stream, output)
	}
	if meta.ID != "resp_123" {
		t.Errorf("ID = %q, want resp_123", meta.ID)
	}
	if meta.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", meta.Model)
	}
	if meta.Object != "response" {
		t.Errorf("Object = %q, want response", meta.Object)
	}
	if meta.Usage.TotalTokens != 15 {
		t.Errorf("TotalTokens = %d, want 15", meta.Usage.TotalTokens)
	}
	if meta.Custom["api_type"] != llmproxy.APITypeResponses {
		t.Errorf("api_type = %v, want responses", meta.Custom["api_type"])
	}
}

func TestResponsesStreamingExtractor_UsageExtraction(t *testing.T) {
	stream := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-4o\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":101,\"output_tokens\":44,\"total_tokens\":145}}}\n\n" +
		"data: [DONE]\n\n"

	meta, _, err := runResponsesStreamExtraction(t, "text/event-stream", stream)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Usage.PromptTokens != 101 {
		t.Errorf("PromptTokens = %d, want 101", meta.Usage.PromptTokens)
	}
	if meta.Usage.CompletionTokens != 44 {
		t.Errorf("CompletionTokens = %d, want 44", meta.Usage.CompletionTokens)
	}
	if meta.Usage.TotalTokens != 145 {
		t.Errorf("TotalTokens = %d, want 145", meta.Usage.TotalTokens)
	}
}

func TestResponsesStreamingExtractor_CacheUsageExtraction(t *testing.T) {
	stream := "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":20,\"total_tokens\":120,\"input_tokens_details\":{\"cached_tokens\":80}}}}\n\n" +
		"data: [DONE]\n\n"

	meta, _, err := runResponsesStreamExtraction(t, "text/event-stream", stream)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cacheUsage, ok := meta.Custom["cache_usage"].(llmproxy.CacheUsage)
	if !ok {
		t.Fatalf("expected cache_usage in custom metadata")
	}
	if cacheUsage.CachedTokens != 80 {
		t.Errorf("CachedTokens = %d, want 80", cacheUsage.CachedTokens)
	}
}

func TestResponsesStreamingExtractor_ReasoningTokens(t *testing.T) {
	stream := "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":10,\"total_tokens\":20,\"output_tokens_details\":{\"reasoning_tokens\":7}}}}\n\n" +
		"data: [DONE]\n\n"

	meta, _, err := runResponsesStreamExtraction(t, "text/event-stream", stream)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	value, ok := meta.Custom["reasoning_tokens"].(int)
	if !ok {
		t.Fatalf("expected reasoning_tokens custom field")
	}
	if value != 7 {
		t.Errorf("reasoning_tokens = %d, want 7", value)
	}
}

func TestResponsesStreamingExtractor_FunctionCallStream(t *testing.T) {
	stream := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_fc\",\"model\":\"gpt-4o\"}}\n\n" +
		"data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{\\\"city\\\":\"}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"name\":\"get_weather\",\"arguments\":\"{\\\"city\\\":\\\"SF\\\"}\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":15,\"output_tokens\":9,\"total_tokens\":24}}}\n\n" +
		"data: [DONE]\n\n"

	meta, output, err := runResponsesStreamExtraction(t, "text/event-stream", stream)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "function_call_arguments.delta") {
		t.Fatalf("expected function call delta in passthrough")
	}
	if meta.Usage.TotalTokens != 24 {
		t.Errorf("TotalTokens = %d, want 24", meta.Usage.TotalTokens)
	}
}

func TestResponsesStreamingExtractor_ErrorEvent(t *testing.T) {
	stream := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_err\",\"model\":\"gpt-4o\"}}\n\n" +
		"data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_err\",\"status\":\"failed\"},\"error\":{\"message\":\"upstream failed\"}}\n\n" +
		"data: [DONE]\n\n"

	meta, output, err := runResponsesStreamExtraction(t, "text/event-stream", stream)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "response.failed") {
		t.Fatalf("expected failed event in output")
	}
	if meta.ID != "resp_err" {
		t.Errorf("ID = %q, want resp_err", meta.ID)
	}
}

func TestResponsesStreamingExtractor_NoCompletedEvent(t *testing.T) {
	stream := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-4o\"}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"

	meta, _, err := runResponsesStreamExtraction(t, "text/event-stream", stream)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Usage.TotalTokens != 0 {
		t.Errorf("expected no usage extraction, got %+v", meta.Usage)
	}
}

func TestResponsesStreamingExtractor_EmptyStream(t *testing.T) {
	stream := "data: [DONE]\n\n"

	meta, output, err := runResponsesStreamExtraction(t, "text/event-stream", stream)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output != stream {
		t.Fatalf("expected passthrough output to match input")
	}
	if meta.Usage.TotalTokens != 0 {
		t.Errorf("expected empty usage, got %+v", meta.Usage)
	}
}

func TestResponsesStreamingExtractor_MalformedEvents(t *testing.T) {
	stream := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-4o\"}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":4,\"total_tokens\":7}}}\n\n" +
		"data: [DONE]\n\n"

	meta, output, err := runResponsesStreamExtraction(t, "text/event-stream", stream)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "response.output_text.delta") {
		t.Fatalf("expected malformed line to still be forwarded")
	}
	if meta.Usage.TotalTokens != 7 {
		t.Errorf("TotalTokens = %d, want 7", meta.Usage.TotalTokens)
	}
}

func TestResponsesStreamingExtractor_NonStreamingFallback(t *testing.T) {
	body := `{"id":"resp_123","object":"response","model":"gpt-4o","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`

	meta, output, err := runResponsesStreamExtraction(t, "application/json", body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Custom["api_type"] != llmproxy.APITypeResponses {
		t.Errorf("api_type = %v, want responses", meta.Custom["api_type"])
	}
	if output != body {
		t.Fatalf("non-streaming passthrough mismatch")
	}
}

func TestResponsesStreamingExtractor_IsStreamingResponse(t *testing.T) {
	extractor := NewResponsesStreamingExtractor()

	tests := []struct {
		name        string
		contentType string
		expected    bool
	}{
		{name: "sse", contentType: "text/event-stream", expected: true},
		{name: "sse with charset", contentType: "text/event-stream; charset=utf-8", expected: true},
		{name: "json", contentType: "application/json", expected: false},
		{name: "plain", contentType: "text/plain", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{"Content-Type": []string{tt.contentType}}}
			if got := extractor.IsStreamingResponse(resp); got != tt.expected {
				t.Errorf("IsStreamingResponse() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestResponsesStreamingExtractor_WithEventPrefixes(t *testing.T) {
	stream := "event: response.created\n" +
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-4o\"}}\n\n" +
		"event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"usage\":{\"input_tokens\":1,\"output_tokens\":2,\"total_tokens\":3}}}\n\n" +
		"data: [DONE]\n\n"

	meta, output, err := runResponsesStreamExtraction(t, "text/event-stream", stream)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "event: response.created") {
		t.Fatalf("expected event line passthrough")
	}
	if meta.Usage.TotalTokens != 3 {
		t.Errorf("TotalTokens = %d, want 3", meta.Usage.TotalTokens)
	}
}

func TestResponsesStreamingExtractor_DataPassthrough(t *testing.T) {
	stream := "event: response.created\n" +
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_passthrough\",\"model\":\"gpt-4o\"}}\n\n" +
		": ping\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
		"data: [DONE]\n\n"

	_, output, err := runResponsesStreamExtraction(t, "text/event-stream", stream)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal([]byte(output), []byte(stream)) {
		t.Fatalf("expected byte-accurate passthrough")
	}
}
