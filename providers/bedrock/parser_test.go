package bedrock

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/agentuity/llmproxy"
)

func TestParser(t *testing.T) {
	t.Run("parses converse request", func(t *testing.T) {
		body := `{"modelId":"anthropic.claude-3-sonnet-20240229-v1:0","messages":[{"role":"user","content":[{"text":"hello"}]}],"inferenceConfig":{"maxTokens":100}}`
		parser := &Parser{}

		meta, raw, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if meta.Model != "anthropic.claude-3-sonnet-20240229-v1:0" {
			t.Errorf("expected model anthropic.claude-3-sonnet-20240229-v1:0, got %s", meta.Model)
		}
		if meta.MaxTokens != 100 {
			t.Errorf("expected maxTokens 100, got %d", meta.MaxTokens)
		}
		if len(meta.Messages) != 1 {
			t.Errorf("expected 1 message, got %d", len(meta.Messages))
		}
		if meta.Messages[0].Role != "user" {
			t.Errorf("expected role user, got %s", meta.Messages[0].Role)
		}
		if meta.Messages[0].Content != "hello" {
			t.Errorf("expected content 'hello', got %s", meta.Messages[0].Content)
		}
		if string(raw) != body {
			t.Error("raw body mismatch")
		}
	})

	t.Run("parses request with system prompt", func(t *testing.T) {
		body := `{"modelId":"anthropic.claude-3-sonnet-20240229-v1:0","system":[{"text":"You are helpful."}],"messages":[{"role":"user","content":[{"text":"hello"}]}]}`
		parser := &Parser{}

		meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if meta.Custom["system"] != "You are helpful. " {
			t.Errorf("expected system prompt, got %v", meta.Custom["system"])
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
	t.Run("sets AWS headers", func(t *testing.T) {
		enricher := NewEnricher("us-east-1", "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "")
		req, _ := http.NewRequest("POST", "https://bedrock-runtime.us-east-1.amazonaws.com/model/test/converse", bytes.NewReader([]byte(`{}`)))
		meta := llmproxy.BodyMetadata{}

		err := enricher.Enrich(req, meta, []byte(`{}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type header")
		}
		if req.Header.Get("X-Amz-Date") == "" {
			t.Errorf("expected X-Amz-Date header")
		}
		if req.Header.Get("Authorization") == "" {
			t.Errorf("expected Authorization header")
		}
		auth := req.Header.Get("Authorization")
		if !bytes.Contains([]byte(auth), []byte("AWS4-HMAC-SHA256")) {
			t.Errorf("expected AWS4-HMAC-SHA256 in Authorization")
		}
	})

	t.Run("includes session token", func(t *testing.T) {
		enricher := NewEnricher("us-east-1", "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "test-session-token")
		req, _ := http.NewRequest("POST", "https://bedrock-runtime.us-east-1.amazonaws.com/model/test/converse", bytes.NewReader([]byte(`{}`)))
		meta := llmproxy.BodyMetadata{}

		err := enricher.Enrich(req, meta, []byte(`{}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Header.Get("X-Amz-Security-Token") != "test-session-token" {
			t.Errorf("expected X-Amz-Security-Token header")
		}
	})
}

func TestResolver(t *testing.T) {
	t.Run("resolves to converse endpoint", func(t *testing.T) {
		resolver := NewResolver("us-east-1")
		meta := llmproxy.BodyMetadata{Model: "anthropic.claude-3-sonnet-20240229-v1:0"}

		u, err := resolver.Resolve(meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := "https://bedrock-runtime.us-east-1.amazonaws.com/model/anthropic.claude-3-sonnet-20240229-v1:0/converse"
		if u.String() != expected {
			t.Errorf("expected %s, got %s", expected, u.String())
		}
	})

	t.Run("resolves to invoke endpoint", func(t *testing.T) {
		resolver := NewInvokeResolver("us-west-2")
		meta := llmproxy.BodyMetadata{Model: "amazon.titan-text-express-v1"}

		u, err := resolver.Resolve(meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := "https://bedrock-runtime.us-west-2.amazonaws.com/model/amazon.titan-text-express-v1/invoke"
		if u.String() != expected {
			t.Errorf("expected %s, got %s", expected, u.String())
		}
	})

	t.Run("url encodes model id", func(t *testing.T) {
		resolver := NewResolver("us-east-1")
		meta := llmproxy.BodyMetadata{Model: "anthropic.claude-3-sonnet-20240229-v1:0"}

		u, err := resolver.Resolve(meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// The model ID should be URL encoded (dots and colons)
		if !bytes.Contains([]byte(u.String()), []byte(url.PathEscape("anthropic.claude-3-sonnet-20240229-v1:0"))) {
			t.Errorf("model ID should be URL encoded")
		}
	})

	t.Run("returns error for empty model", func(t *testing.T) {
		resolver := NewResolver("us-east-1")
		meta := llmproxy.BodyMetadata{}

		_, err := resolver.Resolve(meta)
		if err == nil {
			t.Fatal("expected error for empty model")
		}
	})
}

func TestExtractor(t *testing.T) {
	t.Run("extracts response metadata", func(t *testing.T) {
		extractor := &Extractor{}
		respBody := `{"requestId":"req-123","modelId":"anthropic.claude-3-sonnet-20240229-v1:0","output":{"message":{"role":"assistant","content":[{"text":"Hello!"}]}},"usage":{"inputTokens":10,"outputTokens":5,"totalTokens":15},"stopReason":"end_turn"}`

		resp := &http.Response{
			Body: io.NopCloser(bytes.NewReader([]byte(respBody))),
		}

		meta, raw, err := extractor.Extract(resp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if meta.ID != "req-123" {
			t.Errorf("expected ID req-123, got %s", meta.ID)
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
		if meta.Choices[0].FinishReason != "end_turn" {
			t.Errorf("expected finish_reason end_turn, got %s", meta.Choices[0].FinishReason)
		}
		if string(raw) != respBody {
			t.Error("raw body mismatch")
		}
	})
}
