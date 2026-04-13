package anthropic

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/agentuity/llmproxy"
)

func TestParser(t *testing.T) {
	t.Run("parses basic request", func(t *testing.T) {
		body := `{"model":"claude-3-opus-20240229","max_tokens":1024,"messages":[{"role":"user","content":"hello"}]}`
		parser := &Parser{}

		meta, raw, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if meta.Model != "claude-3-opus-20240229" {
			t.Errorf("expected model claude-3-opus-20240229, got %s", meta.Model)
		}
		if meta.MaxTokens != 1024 {
			t.Errorf("expected max_tokens 1024, got %d", meta.MaxTokens)
		}
		if len(meta.Messages) != 1 {
			t.Errorf("expected 1 message, got %d", len(meta.Messages))
		}
		if string(raw) != body {
			t.Error("raw body mismatch")
		}
	})

	t.Run("parses request with system prompt", func(t *testing.T) {
		body := `{"model":"claude-3-opus-20240229","max_tokens":1024,"system":"You are helpful.","messages":[{"role":"user","content":"hello"}]}`
		parser := &Parser{}

		meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if meta.Custom["system"] != "You are helpful." {
			t.Errorf("expected system prompt, got %v", meta.Custom["system"])
		}
	})

	t.Run("parses content as array", func(t *testing.T) {
		body := `{"model":"claude-3-opus-20240229","max_tokens":1024,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`
		parser := &Parser{}

		meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(meta.Messages) != 1 {
			t.Errorf("expected 1 message, got %d", len(meta.Messages))
		}
		if meta.Messages[0].Content != "hello" {
			t.Errorf("expected content 'hello', got %s", meta.Messages[0].Content)
		}
	})

	t.Run("returns error for invalid JSON", func(t *testing.T) {
		parser := &Parser{}
		_, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte("invalid"))))
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestEnricher(t *testing.T) {
	t.Run("sets required headers", func(t *testing.T) {
		enricher := NewEnricher("test-key")
		req, _ := http.NewRequest("POST", "http://example.com", nil)
		meta := llmproxy.BodyMetadata{}

		err := enricher.Enrich(req, meta, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Header.Get("x-api-key") != "test-key" {
			t.Errorf("expected x-api-key header, got %s", req.Header.Get("x-api-key"))
		}
		if req.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("expected anthropic-version header, got %s", req.Header.Get("anthropic-version"))
		}
	})

	t.Run("sets custom version", func(t *testing.T) {
		enricher := NewEnricherWithVersion("test-key", "2024-01-01")
		req, _ := http.NewRequest("POST", "http://example.com", nil)
		meta := llmproxy.BodyMetadata{}

		err := enricher.Enrich(req, meta, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Header.Get("anthropic-version") != "2024-01-01" {
			t.Errorf("expected anthropic-version 2024-01-01, got %s", req.Header.Get("anthropic-version"))
		}
	})
}

func TestResolver(t *testing.T) {
	t.Run("resolves to messages endpoint", func(t *testing.T) {
		resolver, err := NewResolver("https://api.anthropic.com")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		meta := llmproxy.BodyMetadata{Model: "claude-3-opus-20240229"}
		u, err := resolver.Resolve(meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := "https://api.anthropic.com/v1/messages"
		if u.String() != expected {
			t.Errorf("expected %s, got %s", expected, u.String())
		}
	})
}

func TestExtractor(t *testing.T) {
	t.Run("extracts response metadata", func(t *testing.T) {
		extractor := &Extractor{}
		respBody := `{"id":"msg_123","type":"message","role":"assistant","model":"claude-3-opus-20240229","content":[{"type":"text","text":"Hello!"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`

		resp := &http.Response{
			Body: io.NopCloser(bytes.NewReader([]byte(respBody))),
		}

		meta, raw, err := extractor.Extract(resp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if meta.ID != "msg_123" {
			t.Errorf("expected ID msg_123, got %s", meta.ID)
		}
		if meta.Usage.PromptTokens != 10 {
			t.Errorf("expected 10 prompt tokens, got %d", meta.Usage.PromptTokens)
		}
		if meta.Usage.CompletionTokens != 5 {
			t.Errorf("expected 5 completion tokens, got %d", meta.Usage.CompletionTokens)
		}
		if len(meta.Choices) != 1 {
			t.Errorf("expected 1 choice, got %d", len(meta.Choices))
		}
		if meta.Choices[0].Message.Content != "Hello!" {
			t.Errorf("expected content 'Hello!', got %s", meta.Choices[0].Message.Content)
		}
		if string(raw) != respBody {
			t.Error("raw body mismatch")
		}
	})

	t.Run("extracts cache usage", func(t *testing.T) {
		extractor := &Extractor{}
		respBody := `{"id":"msg_123","type":"message","role":"assistant","model":"claude-3-opus-20240229","content":[{"type":"text","text":"Hello!"}],"stop_reason":"end_turn","usage":{"input_tokens":50,"output_tokens":5,"cache_creation_input_tokens":500,"cache_read_input_tokens":1000},"cache_creation":{"ephemeral_5m_input_tokens":500,"ephemeral_1h_input_tokens":0}}`

		resp := &http.Response{
			Body: io.NopCloser(bytes.NewReader([]byte(respBody))),
		}

		meta, _, err := extractor.Extract(resp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		cacheUsage, ok := meta.Custom["cache_usage"].(llmproxy.CacheUsage)
		if !ok {
			t.Fatal("expected cache_usage in Custom map")
		}
		if cacheUsage.CacheCreationInputTokens != 500 {
			t.Errorf("expected 500 cache creation tokens, got %d", cacheUsage.CacheCreationInputTokens)
		}
		if cacheUsage.CacheReadInputTokens != 1000 {
			t.Errorf("expected 1000 cache read tokens, got %d", cacheUsage.CacheReadInputTokens)
		}
		if cacheUsage.Ephemeral5mInputTokens != 500 {
			t.Errorf("expected 500 5m tokens, got %d", cacheUsage.Ephemeral5mInputTokens)
		}
	})

	t.Run("no cache usage when not present", func(t *testing.T) {
		extractor := &Extractor{}
		respBody := `{"id":"msg_123","type":"message","role":"assistant","model":"claude-3-opus-20240229","content":[{"type":"text","text":"Hello!"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`

		resp := &http.Response{
			Body: io.NopCloser(bytes.NewReader([]byte(respBody))),
		}

		meta, _, err := extractor.Extract(resp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if _, ok := meta.Custom["cache_usage"]; ok {
			t.Error("expected no cache_usage in Custom map when not present in response")
		}
	})
}
