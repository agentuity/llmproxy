package llmproxy

import "net/http"

// ResponseExtractor parses an upstream provider response and extracts metadata.
//
// Implementations handle provider-specific response formats and map them
// to the common ResponseMetadata structure. This allows the proxy to track
// token usage, costs, and other metrics in a provider-agnostic way.
//
// The extractor must return the raw response body bytes so the proxy can
// re-attach them to the response for the caller. This preserves any
// custom/unsupported fields in the original JSON.
type ResponseExtractor interface {
	// Extract parses the HTTP response and returns unified metadata.
	//
	// The method reads and consumes the response body, parses it for metadata,
	// and returns both the metadata and the raw body bytes. The proxy will
	// re-attach the raw bytes to the response so the caller can read them.
	//
	// Parameters:
	//   - resp: The HTTP response from the upstream provider
	//
	// Returns:
	//   - metadata: Parsed response metadata (tokens, model, etc.)
	//   - rawBody: The original response body bytes (must be returned for forwarding)
	//   - error: Any parsing error
	Extract(resp *http.Response) (metadata ResponseMetadata, rawBody []byte, err error)
}
