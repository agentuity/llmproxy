package googleai

import (
	"fmt"
	"net/url"

	"github.com/agentuity/llmproxy"
)

// Resolver implements llmproxy.URLResolver for Google AI's API.
// It constructs the generateContent endpoint URL with the model in the path.
type Resolver struct {
	// BaseURL is the Google AI API base URL.
	BaseURL *url.URL
}

// Resolve returns the full URL for the generateContent or streamGenerateContent endpoint.
// When meta.Stream is true, the URL uses streamGenerateContent with alt=sse for SSE format.
// Otherwise, the URL uses generateContent.
//
// If meta.Model is empty, defaults to "gemini-pro".
func (r *Resolver) Resolve(meta llmproxy.BodyMetadata) (*url.URL, error) {
	model := meta.Model
	if model == "" {
		model = "gemini-pro"
	}

	var endpoint *url.URL
	if meta.Stream {
		endpoint = r.BaseURL.JoinPath("v1beta", "models", fmt.Sprintf("%s:streamGenerateContent", model))
		q := endpoint.Query()
		q.Set("alt", "sse")
		endpoint.RawQuery = q.Encode()
	} else {
		endpoint = r.BaseURL.JoinPath("v1beta", "models", fmt.Sprintf("%s:generateContent", model))
	}
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
