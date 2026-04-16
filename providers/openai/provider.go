// Package openai provides a provider implementation for OpenAI's API.
//
// This is a convenience wrapper around openai_compatible that configures
// the correct base URL for OpenAI.
//
// Basic usage:
//
//	provider, _ := openai.New("sk-your-key")
//	proxy := llmproxy.NewProxy(provider)
package openai

import (
	"github.com/agentuity/llmproxy"
	"github.com/agentuity/llmproxy/providers/openai_compatible"
)

var _ llmproxy.WebSocketCapableProvider = (*openai_compatible.Provider)(nil)

// New creates a new OpenAI provider with the given API key.
// The provider is configured to use OpenAI's API endpoint (https://api.openai.com).
//
// Example:
//
//	provider, _ := openai.New("sk-your-openai-api-key")
func New(apiKey string) (*openai_compatible.Provider, error) {
	return openai_compatible.NewMultiAPI("openai", apiKey, "https://api.openai.com")
}
