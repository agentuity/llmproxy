// Package groq provides a provider implementation for Groq's API.
//
// Groq uses an OpenAI-compatible API, so this is a thin wrapper around
// openai_compatible with the correct base URL.
//
// Basic usage:
//
//	provider, _ := groq.New("gsk_your-key")
//	proxy := llmproxy.NewProxy(provider)
package groq

import (
	"github.com/agentuity/llmproxy/providers/openai_compatible"
)

// New creates a new Groq provider with the given API key.
// The provider is configured to use Groq's API endpoint (https://api.groq.com/openai).
//
// Example:
//
//	provider, _ := groq.New("gsk_your-groq-api-key")
func New(apiKey string) (*openai_compatible.Provider, error) {
	return openai_compatible.New("groq", apiKey, "https://api.groq.com/openai")
}
