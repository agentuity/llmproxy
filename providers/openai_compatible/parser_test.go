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

func TestParser_BasicRequest(t *testing.T) {
	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`
	parser := &Parser{}

	meta, raw, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Model != "gpt-4" {
		t.Errorf("model = %q, want %q", meta.Model, "gpt-4")
	}
	if len(meta.Messages) != 1 {
		t.Fatalf("messages count = %d, want 1", len(meta.Messages))
	}
	if meta.Messages[0].Role != "user" {
		t.Errorf("message role = %q, want %q", meta.Messages[0].Role, "user")
	}
	if meta.Messages[0].Content != "hello" {
		t.Errorf("message content = %q, want %q", meta.Messages[0].Content, "hello")
	}
	if string(raw) != body {
		t.Errorf("raw body mismatch")
	}
}

func TestParser_AllFields(t *testing.T) {
	body := `{"model":"gpt-4","messages":[{"role":"system","content":"You are helpful"},{"role":"user","content":"hi"},{"role":"assistant","content":"Hello!"}],"max_tokens":1000,"stream":true}`
	parser := &Parser{}

	meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if meta.Model != "gpt-4" {
		t.Errorf("model = %q, want %q", meta.Model, "gpt-4")
	}
	if len(meta.Messages) != 3 {
		t.Errorf("messages count = %d, want 3", len(meta.Messages))
	}
	if meta.MaxTokens != 1000 {
		t.Errorf("max_tokens = %d, want 1000", meta.MaxTokens)
	}
	if !meta.Stream {
		t.Errorf("stream = %v, want true", meta.Stream)
	}
}

func TestParser_CustomFields(t *testing.T) {
	body := `{"model":"gpt-4","custom_field":"custom_value","another_custom":123,"provider_specific":{"nested":"data"}}`
	parser := &Parser{}

	meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if meta.Custom["custom_field"] != "custom_value" {
		t.Errorf("custom_field = %v, want custom_value", meta.Custom["custom_field"])
	}
	if meta.Custom["another_custom"] != 123.0 {
		t.Errorf("another_custom = %v, want 123", meta.Custom["another_custom"])
	}
	if meta.Custom["provider_specific"] == nil {
		t.Error("provider_specific should be in Custom")
	}
}

func TestParser_KnownFieldsNotInCustom(t *testing.T) {
	body := `{"model":"gpt-4","temperature":0.7,"top_p":0.9,"frequency_penalty":0.5,"presence_penalty":0.3,"stop":["stop1","stop2"]}`
	parser := &Parser{}

	meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	knownFields := []string{"model", "messages", "max_tokens", "stream", "temperature", "top_p", "n", "stop", "presence_penalty", "frequency_penalty", "logit_bias", "user"}
	for _, field := range knownFields {
		if _, ok := meta.Custom[field]; ok {
			t.Errorf("known field %q should not be in Custom map", field)
		}
	}
}

func TestParser_EmptyRequest(t *testing.T) {
	body := `{}`
	parser := &Parser{}

	meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if meta.Model != "" {
		t.Errorf("model should be empty, got %q", meta.Model)
	}
	if len(meta.Messages) != 0 {
		t.Errorf("messages should be empty, got %d", len(meta.Messages))
	}
}

func TestParser_InvalidJSON(t *testing.T) {
	parser := &Parser{}

	_, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte("invalid json"))))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParser_MultilineContent(t *testing.T) {
	body := `{"model":"gpt-4","messages":[{"role":"user","content":"line1\nline2\nline3"}]}`
	parser := &Parser{}

	meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if meta.Messages[0].Content != "line1\nline2\nline3" {
		t.Errorf("multiline content not preserved: %q", meta.Messages[0].Content)
	}
}

func TestParser_UnicodeContent(t *testing.T) {
	body := `{"model":"gpt-4","messages":[{"role":"user","content":"Hello 世界 🌍"}]}`
	parser := &Parser{}

	meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if meta.Messages[0].Content != "Hello 世界 🌍" {
		t.Errorf("unicode content not preserved: %q", meta.Messages[0].Content)
	}
}

func TestEnricher_SetsHeaders(t *testing.T) {
	enricher := NewEnricher("test-api-key")
	req := httptest.NewRequest("POST", "https://api.example.com/v1/chat/completions", nil)

	err := enricher.Enrich(req, llmproxy.BodyMetadata{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if auth := req.Header.Get("Authorization"); auth != "Bearer test-api-key" {
		t.Errorf("Authorization = %q, want %q", auth, "Bearer test-api-key")
	}
	if ct := req.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

func TestEnricher_EmptyKey(t *testing.T) {
	enricher := NewEnricher("")
	req := httptest.NewRequest("POST", "https://example.com", nil)
	req.Header.Set("Authorization", "Bearer incoming-token")

	err := enricher.Enrich(req, llmproxy.BodyMetadata{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	auth := req.Header.Get("Authorization")
	if auth != "" {
		t.Errorf("Authorization = %q, want empty (header should be deleted for empty key)", auth)
	}
	if ct := req.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

func TestExtractor_BasicResponse(t *testing.T) {
	body := `{"id":"chatcmpl-123","object":"chat.completion","created":1700000000,"model":"gpt-4","usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150},"choices":[{"index":0,"message":{"role":"assistant","content":"Hello!"},"finish_reason":"stop"}]}`
	extractor := NewExtractor()

	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	meta, rawBody, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if meta.ID != "chatcmpl-123" {
		t.Errorf("ID = %q, want %q", meta.ID, "chatcmpl-123")
	}
	if meta.Object != "chat.completion" {
		t.Errorf("Object = %q, want %q", meta.Object, "chat.completion")
	}
	if meta.Model != "gpt-4" {
		t.Errorf("Model = %q, want %q", meta.Model, "gpt-4")
	}
	if meta.Usage.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", meta.Usage.PromptTokens)
	}
	if meta.Usage.CompletionTokens != 50 {
		t.Errorf("CompletionTokens = %d, want 50", meta.Usage.CompletionTokens)
	}
	if meta.Usage.TotalTokens != 150 {
		t.Errorf("TotalTokens = %d, want 150", meta.Usage.TotalTokens)
	}
	if len(meta.Choices) != 1 {
		t.Fatalf("Choices count = %d, want 1", len(meta.Choices))
	}
	if meta.Choices[0].Index != 0 {
		t.Errorf("Choice index = %d, want 0", meta.Choices[0].Index)
	}
	if meta.Choices[0].Message == nil {
		t.Fatal("Choice message is nil")
	}
	if meta.Choices[0].Message.Role != "assistant" {
		t.Errorf("Choice message role = %q, want assistant", meta.Choices[0].Message.Role)
	}
	if meta.Choices[0].Message.Content != "Hello!" {
		t.Errorf("Choice message content = %q, want Hello!", meta.Choices[0].Message.Content)
	}
	if meta.Choices[0].FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop", meta.Choices[0].FinishReason)
	}
	if string(rawBody) != body {
		t.Error("raw body not preserved")
	}
}

func TestExtractor_MultipleChoices(t *testing.T) {
	body := `{"id":"chatcmpl-123","model":"gpt-4","usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30},"choices":[{"index":0,"message":{"role":"assistant","content":"Option A"},"finish_reason":"stop"},{"index":1,"message":{"role":"assistant","content":"Option B"},"finish_reason":"stop"}]}`
	extractor := NewExtractor()

	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(meta.Choices) != 2 {
		t.Fatalf("Choices count = %d, want 2", len(meta.Choices))
	}
	if meta.Choices[0].Message.Content != "Option A" {
		t.Errorf("Choice 0 content = %q, want Option A", meta.Choices[0].Message.Content)
	}
	if meta.Choices[1].Message.Content != "Option B" {
		t.Errorf("Choice 1 content = %q, want Option B", meta.Choices[1].Message.Content)
	}
}

func TestExtractor_DeltaForStreaming(t *testing.T) {
	body := `{"id":"chatcmpl-123","model":"gpt-4","usage":{"prompt_tokens":10,"completion_tokens":0,"total_tokens":10},"choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":""}]}`
	extractor := NewExtractor()

	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if meta.Choices[0].Delta == nil {
		t.Fatal("Delta should not be nil")
	}
	if meta.Choices[0].Delta.Role != "assistant" {
		t.Errorf("Delta role = %q, want assistant", meta.Choices[0].Delta.Role)
	}
	if meta.Choices[0].Delta.Content != "Hello" {
		t.Errorf("Delta content = %q, want Hello", meta.Choices[0].Delta.Content)
	}
}

func TestExtractor_EmptyChoices(t *testing.T) {
	body := `{"id":"chatcmpl-123","model":"gpt-4","usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0},"choices":[]}`
	extractor := NewExtractor()

	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(meta.Choices) != 0 {
		t.Errorf("Choices count = %d, want 0", len(meta.Choices))
	}
}

func TestExtractor_ZeroUsage(t *testing.T) {
	body := `{"id":"chatcmpl-123","model":"gpt-4","usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0},"choices":[]}`
	extractor := NewExtractor()

	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if meta.Usage.PromptTokens != 0 {
		t.Errorf("PromptTokens = %d, want 0", meta.Usage.PromptTokens)
	}
	if meta.Usage.CompletionTokens != 0 {
		t.Errorf("CompletionTokens = %d, want 0", meta.Usage.CompletionTokens)
	}
	if meta.Usage.TotalTokens != 0 {
		t.Errorf("TotalTokens = %d, want 0", meta.Usage.TotalTokens)
	}
}

func TestExtractor_InvalidJSON(t *testing.T) {
	extractor := NewExtractor()

	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("invalid json")),
	}

	_, _, err := extractor.Extract(resp)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestResolver_BasicURL(t *testing.T) {
	resolver, err := NewResolver("https://api.openai.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	meta := llmproxy.BodyMetadata{Model: "gpt-4"}
	u, err := resolver.Resolve(meta)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "https://api.openai.com/v1/chat/completions"
	if u.String() != expected {
		t.Errorf("URL = %q, want %q", u.String(), expected)
	}
}

func TestResolver_CustomBaseURL(t *testing.T) {
	resolver, err := NewResolver("https://api.groq.com/openai")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	meta := llmproxy.BodyMetadata{Model: "llama-3-70b"}
	u, err := resolver.Resolve(meta)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "https://api.groq.com/openai/v1/chat/completions"
	if u.String() != expected {
		t.Errorf("URL = %q, want %q", u.String(), expected)
	}
}

func TestResolver_InvalidURL(t *testing.T) {
	_, err := NewResolver("://invalid-url")
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestResolver_TrailingSlash(t *testing.T) {
	resolver, err := NewResolver("https://api.openai.com/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	meta := llmproxy.BodyMetadata{}
	u, err := resolver.Resolve(meta)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "https://api.openai.com/v1/chat/completions"
	if u.String() != expected {
		t.Errorf("URL = %q, want %q", u.String(), expected)
	}
}

func TestProvider_New(t *testing.T) {
	provider, err := New("test-provider", "test-key", "https://api.test.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if provider.Name() != "test-provider" {
		t.Errorf("Name = %q, want %q", provider.Name(), "test-provider")
	}
	if provider.BodyParser() == nil {
		t.Error("BodyParser should not be nil")
	}
	if provider.RequestEnricher() == nil {
		t.Error("RequestEnricher should not be nil")
	}
	if provider.ResponseExtractor() == nil {
		t.Error("ResponseExtractor should not be nil")
	}
	if provider.URLResolver() == nil {
		t.Error("URLResolver should not be nil")
	}
}

func TestProvider_NewInvalidURL(t *testing.T) {
	_, err := New("test", "key", "://invalid")
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestProvider_NewWithProvider(t *testing.T) {
	parser := &Parser{}
	base := llmproxy.NewBaseProvider("custom",
		llmproxy.WithBodyParser(parser),
	)

	provider := NewWithProvider("custom", base)
	if provider.Name() != "custom" {
		t.Errorf("Name = %q, want %q", provider.Name(), "custom")
	}
	if provider.BodyParser() != parser {
		t.Errorf("BodyParser not preserved: got %v, want %v", provider.BodyParser(), parser)
	}
}

func TestParseOpenAIRequestBody(t *testing.T) {
	data := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"test"}]}`)

	meta, err := ParseOpenAIRequestBody(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if meta.Model != "gpt-4" {
		t.Errorf("Model = %q, want gpt-4", meta.Model)
	}
	if len(meta.Messages) != 1 {
		t.Errorf("Messages count = %d, want 1", len(meta.Messages))
	}
}

func TestNewExtractor(t *testing.T) {
	extractor := NewExtractor()
	if extractor == nil {
		t.Error("NewExtractor returned nil")
	}
}

func TestNewEnricher(t *testing.T) {
	enricher := NewEnricher("test-key")
	if enricher == nil {
		t.Error("NewEnricher returned nil")
	}
	if enricher.APIKey != "test-key" {
		t.Errorf("APIKey = %q, want test-key", enricher.APIKey)
	}
}

func TestExtractor_CacheUsage(t *testing.T) {
	body := `{"id":"chatcmpl-123","model":"gpt-4","usage":{"prompt_tokens":2006,"completion_tokens":300,"total_tokens":2306,"prompt_tokens_details":{"cached_tokens":1920}},"choices":[{"index":0,"message":{"role":"assistant","content":"Hello!"},"finish_reason":"stop"}]}`
	extractor := NewExtractor()

	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cacheUsage, ok := meta.Custom["cache_usage"].(llmproxy.CacheUsage)
	if !ok {
		t.Fatal("expected cache_usage in Custom map")
	}
	if cacheUsage.CachedTokens != 1920 {
		t.Errorf("CachedTokens = %d, want 1920", cacheUsage.CachedTokens)
	}
}

func TestExtractor_NoCacheUsage(t *testing.T) {
	body := `{"id":"chatcmpl-123","model":"gpt-4","usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150},"choices":[{"index":0,"message":{"role":"assistant","content":"Hello!"},"finish_reason":"stop"}]}`
	extractor := NewExtractor()

	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := meta.Custom["cache_usage"]; ok {
		t.Error("expected no cache_usage in Custom map when not present in response")
	}
}

func TestExtractor_ZeroCachedTokens(t *testing.T) {
	body := `{"id":"chatcmpl-123","model":"gpt-4","usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":0}},"choices":[]}`
	extractor := NewExtractor()

	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := meta.Custom["cache_usage"]; ok {
		t.Error("expected no cache_usage when cached_tokens is 0")
	}
}
