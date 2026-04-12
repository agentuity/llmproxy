package openai_compatible

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/agentuity/llmproxy"
)

func TestParser(t *testing.T) {
	t.Run("parses basic request", func(t *testing.T) {
		body := `{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`
		parser := &Parser{}

		meta, raw, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if meta.Model != "gpt-4" {
			t.Errorf("expected model gpt-4, got %s", meta.Model)
		}
		if len(meta.Messages) != 1 {
			t.Errorf("expected 1 message, got %d", len(meta.Messages))
		}
		if string(raw) != body {
			t.Error("raw body mismatch")
		}
	})

	t.Run("parses custom fields", func(t *testing.T) {
		body := `{"model":"gpt-4","custom_field":"value"}`
		parser := &Parser{}

		meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if meta.Custom["custom_field"] != "value" {
			t.Errorf("expected custom_field value, got %v", meta.Custom["custom_field"])
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
	t.Run("sets authorization header", func(t *testing.T) {
		enricher := NewEnricher("test-key")
		req, _ := http.NewRequest("POST", "http://example.com", nil)
		meta := llmproxy.BodyMetadata{}

		err := enricher.Enrich(req, meta, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Bearer token, got %s", req.Header.Get("Authorization"))
		}
	})
}

func TestResolver(t *testing.T) {
	t.Run("resolves to correct endpoint", func(t *testing.T) {
		resolver, err := NewResolver("https://api.example.com")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		meta := llmproxy.BodyMetadata{Model: "gpt-4"}
		u, err := resolver.Resolve(meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := "https://api.example.com/v1/chat/completions"
		if u.String() != expected {
			t.Errorf("expected %s, got %s", expected, u.String())
		}
	})
}
