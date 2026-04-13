package openai_compatible

import (
	"net/http"

	"github.com/agentuity/llmproxy"
)

// Enricher implements llmproxy.RequestEnricher for OpenAI-compatible APIs.
// It sets the required Authorization header with a Bearer token.
type Enricher struct {
	// APIKey is the API key for authentication.
	APIKey string
}

// Enrich adds the Authorization and Content-Type headers to the request.
// It sets:
//   - Authorization: Bearer <APIKey>
//   - Content-Type: application/json
func (e *Enricher) Enrich(req *http.Request, meta llmproxy.BodyMetadata, rawBody []byte) error {
	req.Header.Set("Content-Type", "application/json")
	if e.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.APIKey)
	} else {
		req.Header.Del("Authorization")
	}
	return nil
}

// NewEnricher creates a new enricher with the given API key.
func NewEnricher(apiKey string) *Enricher {
	return &Enricher{APIKey: apiKey}
}
