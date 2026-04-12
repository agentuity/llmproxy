package openai_compatible

import (
	"net/url"

	"github.com/agentuity/llmproxy"
)

// Resolver implements llmproxy.URLResolver for OpenAI-compatible APIs.
// It constructs the chat completions endpoint URL from a base URL.
type Resolver struct {
	// BaseURL is the provider's API base URL (e.g., "https://api.openai.com").
	BaseURL *url.URL
}

// Resolve returns the full URL for the chat completions endpoint.
// It appends "/v1/chat/completions" to the base URL.
func (r *Resolver) Resolve(meta llmproxy.BodyMetadata) (*url.URL, error) {
	endpoint := r.BaseURL.JoinPath("v1", "chat", "completions")
	return endpoint, nil
}

// NewResolver creates a new resolver with the given base URL.
// The baseURL should be the provider's API domain (e.g., "https://api.openai.com").
func NewResolver(baseURL string) (*Resolver, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	return &Resolver{BaseURL: u}, nil
}
