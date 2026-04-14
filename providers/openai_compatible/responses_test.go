package openai_compatible

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/agentuity/llmproxy"
)

func TestResponsesParser(t *testing.T) {
	body := `{
		"model": "gpt-4o",
		"input": "Hello, world!",
		"instructions": "Be helpful",
		"max_output_tokens": 100,
		"temperature": 0.7,
		"tools": [{"type": "web_search_preview"}]
	}`

	parser := &ResponsesParser{}
	meta, data, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if meta.Model != "gpt-4o" {
		t.Errorf("Model = %q, want %q", meta.Model, "gpt-4o")
	}

	if meta.MaxTokens != 100 {
		t.Errorf("MaxTokens = %d, want 100", meta.MaxTokens)
	}

	if len(meta.Messages) != 1 {
		t.Errorf("Messages length = %d, want 1", len(meta.Messages))
	} else {
		if meta.Messages[0].Role != "user" {
			t.Errorf("Message role = %q, want user", meta.Messages[0].Role)
		}
		if meta.Messages[0].Content != "Hello, world!" {
			t.Errorf("Message content = %q, want 'Hello, world!'", meta.Messages[0].Content)
		}
	}

	if meta.Custom["instructions"] != "Be helpful" {
		t.Errorf("instructions = %v, want 'Be helpful'", meta.Custom["instructions"])
	}

	if meta.Custom["api_type"] != llmproxy.APITypeResponses {
		t.Errorf("api_type = %v, want responses", meta.Custom["api_type"])
	}

	if len(data) == 0 {
		t.Error("data is empty")
	}
}

func TestResponsesParser_InputArray(t *testing.T) {
	body := `{
		"model": "gpt-4o",
		"input": [
			{"role": "system", "content": "You are helpful"},
			{"role": "user", "content": "Hello"}
		]
	}`

	parser := &ResponsesParser{}
	meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if len(meta.Messages) != 2 {
		t.Fatalf("Messages length = %d, want 2", len(meta.Messages))
	}

	if meta.Messages[0].Role != "system" {
		t.Errorf("First message role = %q, want system", meta.Messages[0].Role)
	}
	if meta.Messages[1].Role != "user" {
		t.Errorf("Second message role = %q, want user", meta.Messages[1].Role)
	}
}

func TestResponsesExtractor(t *testing.T) {
	respBody := `{
		"id": "resp_abc123",
		"object": "response",
		"created": 1234567890,
		"model": "gpt-4o",
		"status": "completed",
		"output": [
			{
				"id": "msg_123",
				"type": "message",
				"status": "completed",
				"role": "assistant",
				"content": [
					{
						"type": "output_text",
						"text": "Hello! How can I help you?"
					}
				]
			}
		],
		"usage": {
			"input_tokens": 10,
			"output_tokens": 20,
			"total_tokens": 30,
			"input_tokens_details": {
				"cached_tokens": 5
			}
		}
	}`

	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(respBody))),
	}

	extractor := &ResponsesExtractor{}
	meta, rawBody, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	if meta.ID != "resp_abc123" {
		t.Errorf("ID = %q, want resp_abc123", meta.ID)
	}

	if meta.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", meta.Model)
	}

	if meta.Usage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10", meta.Usage.PromptTokens)
	}
	if meta.Usage.CompletionTokens != 20 {
		t.Errorf("CompletionTokens = %d, want 20", meta.Usage.CompletionTokens)
	}
	if meta.Usage.TotalTokens != 30 {
		t.Errorf("TotalTokens = %d, want 30", meta.Usage.TotalTokens)
	}

	if len(meta.Choices) != 1 {
		t.Fatalf("Choices length = %d, want 1", len(meta.Choices))
	}

	if meta.Choices[0].Message == nil {
		t.Fatal("Message is nil")
	}
	if meta.Choices[0].Message.Content != "Hello! How can I help you?" {
		t.Errorf("Message content = %q, want 'Hello! How can I help you?'", meta.Choices[0].Message.Content)
	}

	if meta.Custom["status"] != "completed" {
		t.Errorf("status = %v, want completed", meta.Custom["status"])
	}

	cacheUsage, ok := meta.Custom["cache_usage"].(llmproxy.CacheUsage)
	if !ok {
		t.Fatal("cache_usage not found or wrong type")
	}
	if cacheUsage.CachedTokens != 5 {
		t.Errorf("CachedTokens = %d, want 5", cacheUsage.CachedTokens)
	}

	if len(rawBody) == 0 {
		t.Error("rawBody is empty")
	}
}

func TestMultiAPIParser_ChatCompletions(t *testing.T) {
	body := `{
		"model": "gpt-4",
		"messages": [{"role": "user", "content": "Hello"}],
		"max_tokens": 100
	}`

	parser := NewMultiAPIParser()
	meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if meta.Model != "gpt-4" {
		t.Errorf("Model = %q, want gpt-4", meta.Model)
	}

	if len(meta.Messages) != 1 {
		t.Errorf("Messages length = %d, want 1", len(meta.Messages))
	}
}

func TestMultiAPIParser_Responses(t *testing.T) {
	body := `{
		"model": "gpt-4o",
		"input": "Hello",
		"instructions": "Be helpful"
	}`

	parser := NewMultiAPIParser()
	meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if meta.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", meta.Model)
	}

	if meta.Custom["api_type"] != llmproxy.APITypeResponses {
		t.Errorf("api_type = %v, want responses", meta.Custom["api_type"])
	}
}

func TestResolver_ResponsesAPI(t *testing.T) {
	resolver, err := NewResolver("https://api.openai.com")
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}

	meta := llmproxy.BodyMetadata{
		Model:  "gpt-4o",
		Custom: map[string]any{"api_type": llmproxy.APITypeResponses},
	}

	url, err := resolver.Resolve(meta)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	expected := "https://api.openai.com/v1/responses"
	if url.String() != expected {
		t.Errorf("URL = %q, want %q", url.String(), expected)
	}
}

func TestResolver_ChatCompletionsAPI(t *testing.T) {
	resolver, err := NewResolver("https://api.openai.com")
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}

	meta := llmproxy.BodyMetadata{
		Model:  "gpt-4",
		Custom: map[string]any{"api_type": llmproxy.APITypeChatCompletions},
	}

	url, err := resolver.Resolve(meta)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	expected := "https://api.openai.com/v1/chat/completions"
	if url.String() != expected {
		t.Errorf("URL = %q, want %q", url.String(), expected)
	}
}

func TestNewMultiAPI(t *testing.T) {
	provider, err := NewMultiAPI("test", "api-key", "https://api.example.com")
	if err != nil {
		t.Fatalf("NewMultiAPI() error = %v", err)
	}

	if provider.Name() != "test" {
		t.Errorf("Name() = %q, want test", provider.Name())
	}

	if provider.BodyParser() == nil {
		t.Error("BodyParser() is nil")
	}

	if provider.ResponseExtractor() == nil {
		t.Error("ResponseExtractor() is nil")
	}

	if provider.RequestEnricher() == nil {
		t.Error("RequestEnricher() is nil")
	}

	if provider.URLResolver() == nil {
		t.Error("URLResolver() is nil")
	}
}
