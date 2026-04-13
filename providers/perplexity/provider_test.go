package perplexity

import (
	"net/http/httptest"
	"testing"

	"github.com/agentuity/llmproxy"
)

func TestNew(t *testing.T) {
	provider, err := New("test-api-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if provider.Name() != "perplexity" {
		t.Errorf("Name = %q, want %q", provider.Name(), "perplexity")
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

func TestResolver_PerplexityURL(t *testing.T) {
	provider, err := New("test-api-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resolver := provider.URLResolver()
	meta := llmproxy.BodyMetadata{Model: "sonar"}
	u, err := resolver.Resolve(meta)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "https://api.perplexity.ai/v1/chat/completions"
	if u.String() != expected {
		t.Errorf("URL = %q, want %q", u.String(), expected)
	}
}

func TestEnricher_SetsAuthorization(t *testing.T) {
	provider, err := New("pplx-test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	enricher := provider.RequestEnricher()
	req := httptest.NewRequest("POST", "https://api.perplexity.ai/v1/chat/completions", nil)

	err = enricher.Enrich(req, llmproxy.BodyMetadata{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if auth := req.Header.Get("Authorization"); auth != "Bearer pplx-test-key" {
		t.Errorf("Authorization = %q, want %q", auth, "Bearer pplx-test-key")
	}
	if ct := req.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

func TestNew_EmptyKey(t *testing.T) {
	provider, err := New("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if provider.Name() != "perplexity" {
		t.Errorf("Name = %q, want %q", provider.Name(), "perplexity")
	}
}
