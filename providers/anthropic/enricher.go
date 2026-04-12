package anthropic

import (
	"net/http"

	"github.com/agentuity/llmproxy"
)

// Enricher implements llmproxy.RequestEnricher for Anthropic's API.
// It sets the required x-api-key and anthropic-version headers.
type Enricher struct {
	// APIKey is the Anthropic API key.
	APIKey string
	// Version is the Anthropic API version (defaults to 2023-06-01).
	Version string
}

// Enrich adds Anthropic-specific headers to the request.
// Sets:
//   - x-api-key: <APIKey>
//   - anthropic-version: <Version> (defaults to 2023-06-01)
//   - Content-Type: application/json
func (e *Enricher) Enrich(req *http.Request, meta llmproxy.BodyMetadata, rawBody []byte) error {
	req.Header.Set("x-api-key", e.APIKey)
	req.Header.Set("Content-Type", "application/json")

	version := e.Version
	if version == "" {
		version = "2023-06-01"
	}
	req.Header.Set("anthropic-version", version)

	return nil
}

// NewEnricher creates a new Anthropic enricher with the given API key.
// The API version defaults to 2023-06-01 if not specified.
func NewEnricher(apiKey string) *Enricher {
	return &Enricher{APIKey: apiKey}
}

// NewEnricherWithVersion creates a new Anthropic enricher with a specific API version.
func NewEnricherWithVersion(apiKey, version string) *Enricher {
	return &Enricher{APIKey: apiKey, Version: version}
}
