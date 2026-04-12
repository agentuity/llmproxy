package llmproxy

import "net/http"

// RequestEnricher modifies an outgoing request before it's sent to the upstream provider.
//
// Typical uses include:
//   - Setting authentication headers (Authorization, X-API-Key, etc.)
//   - Adding provider-specific headers
//   - Modifying the request body or URL
//
// The rawBody is provided for cases where the enricher needs to modify the body content.
type RequestEnricher interface {
	// Enrich modifies the request with provider-specific enhancements.
	// The meta parameter contains parsed body metadata for decision-making.
	// The rawBody contains the original request body bytes.
	//
	// Implementations should modify req in place (headers, URL, etc.)
	// and return nil on success, or an error to abort the request.
	Enrich(req *http.Request, meta BodyMetadata, rawBody []byte) error
}
