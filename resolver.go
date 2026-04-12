package llmproxy

import "net/url"

// URLResolver determines the upstream provider URL for a given request.
//
// This allows routing requests to different endpoints based on the request metadata,
// such as model name. Some providers may use different endpoints for different models
// or have region-specific URLs.
type URLResolver interface {
	// Resolve returns the upstream URL for the given request metadata.
	// The returned URL should be the full endpoint for the completion request.
	//
	// Implementations can use metadata fields (like Model) to make routing decisions.
	Resolve(meta BodyMetadata) (*url.URL, error)
}
