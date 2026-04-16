package openai_compatible

import (
	"fmt"
	"net/url"

	"github.com/agentuity/llmproxy"
)

// WebSocketURL converts the provider HTTP base URL to a WebSocket URL.
//
//	https://api.openai.com -> wss://api.openai.com/v1/responses
//	http://localhost:8080 -> ws://localhost:8080/v1/responses
func (r *Resolver) WebSocketURL(meta llmproxy.BodyMetadata) (*url.URL, error) {
	if r == nil || r.BaseURL == nil {
		return nil, fmt.Errorf("resolver base URL is nil")
	}

	u := *r.BaseURL
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}

	return u.JoinPath("v1", "responses"), nil
}
