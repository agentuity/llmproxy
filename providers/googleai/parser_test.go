package googleai

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/agentuity/llmproxy"
)

func TestParser(t *testing.T) {
	t.Run("parses basic request", func(t *testing.T) {
		body := `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`
		parser := &Parser{}

		meta, raw, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(meta.Messages) != 1 {
			t.Errorf("expected 1 message, got %d", len(meta.Messages))
		}
		if meta.Messages[0].Role != "user" {
			t.Errorf("expected role user, got %s", meta.Messages[0].Role)
		}
		if meta.Messages[0].Content != "hello" {
			t.Errorf("expected content 'hello', got %v", meta.Messages[0].Content)
		}
		if string(raw) != body {
			t.Error("raw body mismatch")
		}
	})

	t.Run("parses request with generation config", func(t *testing.T) {
		body := `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":100}}`
		parser := &Parser{}

		meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if meta.MaxTokens != 100 {
			t.Errorf("expected max_tokens 100, got %d", meta.MaxTokens)
		}
	})

	t.Run("parses request with system instruction", func(t *testing.T) {
		body := `{"systemInstruction":{"parts":[{"text":"You are helpful."}]},"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`
		parser := &Parser{}

		meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if meta.Custom["system_instruction"] != "You are helpful." {
			t.Errorf("expected system instruction, got %v", meta.Custom["system_instruction"])
		}
	})

	t.Run("maps model role to assistant", func(t *testing.T) {
		body := `{"contents":[{"role":"model","parts":[{"text":"hi there"}]}]}`
		parser := &Parser{}

		meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if meta.Messages[0].Role != "assistant" {
			t.Errorf("expected role assistant, got %s", meta.Messages[0].Role)
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
		if req.Header.Get("x-goog-api-key") != "test-key" {
			t.Errorf("expected x-goog-api-key header, got %s", req.Header.Get("x-goog-api-key"))
		}
	})
}

func TestResolver(t *testing.T) {
	t.Run("resolves to generateContent endpoint", func(t *testing.T) {
		resolver, err := NewResolver("https://generativelanguage.googleapis.com")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		meta := llmproxy.BodyMetadata{Model: "gemini-pro"}
		u, err := resolver.Resolve(meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := "https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent"
		if u.String() != expected {
			t.Errorf("expected %s, got %s", expected, u.String())
		}
	})

	t.Run("defaults to gemini-pro when model is empty", func(t *testing.T) {
		resolver, err := NewResolver("https://generativelanguage.googleapis.com")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		meta := llmproxy.BodyMetadata{}
		u, err := resolver.Resolve(meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := "https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent"
		if u.String() != expected {
			t.Errorf("expected %s, got %s", expected, u.String())
		}
	})
}

func TestExtractor(t *testing.T) {
	t.Run("extracts response metadata", func(t *testing.T) {
		extractor := &Extractor{}
		respBody := `{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello!"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}`

		resp := &http.Response{
			Body: io.NopCloser(bytes.NewReader([]byte(respBody))),
		}

		meta, raw, err := extractor.Extract(resp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
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
			t.Errorf("expected content 'Hello!', got %v", meta.Choices[0].Message.Content)
		}
		if meta.Choices[0].FinishReason != "stop" {
			t.Errorf("expected finish_reason 'stop', got %s", meta.Choices[0].FinishReason)
		}
		if string(raw) != respBody {
			t.Error("raw body mismatch")
		}
	})

	t.Run("maps finish reasons", func(t *testing.T) {
		tests := []struct {
			input    string
			expected string
		}{
			{"STOP", "stop"},
			{"MAX_TOKENS", "length"},
			{"SAFETY", "content_filter"},
			{"RECITATION", "content_filter"},
			{"UNKNOWN", "UNKNOWN"},
		}

		for _, tc := range tests {
			result := mapFinishReason(tc.input)
			if result != tc.expected {
				t.Errorf("mapFinishReason(%s) = %s, expected %s", tc.input, result, tc.expected)
			}
		}
	})
}

func TestParser_MessageWithMultipleParts(t *testing.T) {
	body := `{"contents":[{"role":"user","parts":[{"text":"hello"},{"text":"world"}]}]}`
	parser := &Parser{}

	meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Messages[0].Content != "helloworld" {
		t.Errorf("expected combined content 'helloworld', got %v", meta.Messages[0].Content)
	}
}

func TestParser_MessageWithInlineData(t *testing.T) {
	body := `{"contents":[{"role":"user","parts":[{"text":"describe this"},{"inlineData":{"mimeType":"image/png","data":"abc123"}}]}]}`
	parser := &Parser{}

	meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Messages[0].Content != "describe this" {
		t.Errorf("expected text content, got %v", meta.Messages[0].Content)
	}
}
