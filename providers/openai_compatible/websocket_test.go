package openai_compatible

import (
	"testing"

	"github.com/agentuity/llmproxy"
)

func TestWebSocketURL_HTTPS(t *testing.T) {
	r, err := NewResolver("https://api.openai.com")
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	u, err := r.WebSocketURL(llmproxy.BodyMetadata{})
	if err != nil {
		t.Fatalf("WebSocketURL() error = %v", err)
	}
	if got, want := u.String(), "wss://api.openai.com/v1/responses"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
}

func TestWebSocketURL_HTTP(t *testing.T) {
	r, err := NewResolver("http://localhost:8080")
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	u, err := r.WebSocketURL(llmproxy.BodyMetadata{})
	if err != nil {
		t.Fatalf("WebSocketURL() error = %v", err)
	}
	if got, want := u.String(), "ws://localhost:8080/v1/responses"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
}

func TestWebSocketURL_WithTrailingSlash(t *testing.T) {
	r, err := NewResolver("https://api.openai.com/")
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	u, err := r.WebSocketURL(llmproxy.BodyMetadata{})
	if err != nil {
		t.Fatalf("WebSocketURL() error = %v", err)
	}
	if got, want := u.String(), "wss://api.openai.com/v1/responses"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
}

func TestWebSocketURL_WithExistingPath(t *testing.T) {
	r, err := NewResolver("https://api.openai.com/v1")
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	u, err := r.WebSocketURL(llmproxy.BodyMetadata{})
	if err != nil {
		t.Fatalf("WebSocketURL() error = %v", err)
	}
	if got, want := u.String(), "wss://api.openai.com/v1/v1/responses"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
}
