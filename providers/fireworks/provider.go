// Package fireworks provides a provider implementation for Fireworks AI's API.
//
// Fireworks uses an OpenAI-compatible API, so this is a thin wrapper around
// openai_compatible with the correct base URL.
//
// Basic usage:
//
//	provider, _ := fireworks.New("fw_your-key")
//	proxy := llmproxy.NewProxy(provider)
package fireworks

import (
	"github.com/agentuity/llmproxy/providers/openai_compatible"
)

// New creates a new Fireworks provider with the given API key.
// The provider is configured to use Fireworks' API endpoint (https://api.fireworks.ai/inference).
//
// Example:
//
//	provider, _ := fireworks.New("fw_your-fireworks-api-key")
func New(apiKey string) (*openai_compatible.Provider, error) {
	return openai_compatible.New("fireworks", apiKey, "https://api.fireworks.ai/inference")
}
