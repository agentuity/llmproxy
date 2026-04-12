package googleai

import (
	"net/http"

	"github.com/agentuity/llmproxy"
)

// Enricher implements llmproxy.RequestEnricher for Google AI's API.
// It sets the required x-goog-api-key header.
type Enricher struct {
	// APIKey is the Google AI API key.
	APIKey string
}

// Enrich adds Google AI-specific headers to the request.
// Sets:
//   - x-goog-api-key: <APIKey>
//   - Content-Type: application/json
func (e *Enricher) Enrich(req *http.Request, meta llmproxy.BodyMetadata, rawBody []byte) error {
	req.Header.Set("x-goog-api-key", e.APIKey)
	req.Header.Set("Content-Type", "application/json")
	return nil
}

// NewEnricher creates a new Google AI enricher with the given API key.
func NewEnricher(apiKey string) *Enricher {
	return &Enricher{APIKey: apiKey}
}
