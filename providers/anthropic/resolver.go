package anthropic

import (
	"net/url"

	"github.com/agentuity/llmproxy"
)

// Resolver implements llmproxy.URLResolver for Anthropic's API.
// It constructs the messages endpoint URL.
type Resolver struct {
	// BaseURL is the Anthropic API base URL.
	BaseURL *url.URL
}

// Resolve returns the full URL for the Anthropic messages endpoint.
// Appends "/v1/messages" to the base URL.
func (r *Resolver) Resolve(meta llmproxy.BodyMetadata) (*url.URL, error) {
	endpoint := r.BaseURL.JoinPath("v1", "messages")
	return endpoint, nil
}

// NewResolver creates a new resolver with the given base URL.
func NewResolver(baseURL string) (*Resolver, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	return &Resolver{BaseURL: u}, nil
}
