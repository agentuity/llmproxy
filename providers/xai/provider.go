// Package xai provides a provider implementation for x.AI's Grok API.
//
// x.AI uses an OpenAI-compatible API, so this is a thin wrapper around
// openai_compatible with the correct base URL.
//
// Basic usage:
//
//	provider, _ := xai.New("xai-your-key")
//	proxy := llmproxy.NewProxy(provider)
package xai

import (
	"github.com/agentuity/llmproxy/providers/openai_compatible"
)

// New creates a new x.AI provider with the given API key.
// The provider is configured to use x.AI's API endpoint (https://api.x.ai).
//
// Example:
//
//	provider, _ := xai.New("xai-your-api-key")
func New(apiKey string) (*openai_compatible.Provider, error) {
	return openai_compatible.New("xai", apiKey, "https://api.x.ai")
}
