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

// Resolve returns the full URL for the generateContent endpoint.
// The URL format is: {base}/v1beta/models/{model}:generateContent
//
// If meta.Model is empty, defaults to "gemini-pro".
func (r *Resolver) Resolve(meta llmproxy.BodyMetadata) (*url.URL, error) {
	model := meta.Model
	if model == "" {
		model = "gemini-pro"
	}

	endpoint := r.BaseURL.JoinPath("v1beta", "models", fmt.Sprintf("%s:generateContent", model))
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
