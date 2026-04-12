package llmproxy

import "io"

// BodyParser extracts metadata from a request body.
//
// Since io.ReadCloser can only be read once, Parse returns both the extracted
// metadata and the raw body bytes. The caller is responsible for reconstructing
// the body for the upstream request.
//
// Implementations should handle provider-specific JSON formats and map them
// to the common BodyMetadata structure.
type BodyParser interface {
	// Parse reads the request body and extracts metadata.
	// It returns the parsed metadata, the raw body bytes (for later use),
	// and any error encountered during parsing.
	//
	// The caller is responsible for closing the body ReadCloser.
	Parse(body io.ReadCloser) (BodyMetadata, []byte, error)
}
