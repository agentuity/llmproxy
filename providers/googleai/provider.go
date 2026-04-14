package googleai

import (
	"github.com/agentuity/llmproxy"
)

// Provider is a Google AI provider implementation.
type Provider struct {
	*llmproxy.BaseProvider
}

// New creates a new Google AI provider with the given API key.
// The provider is configured to use Google AI's API endpoint (https://generativelanguage.googleapis.com).
//
// Example:
//
//	provider, _ := googleai.New("your-google-ai-api-key")
func New(apiKey string) (*Provider, error) {
	resolver, err := NewResolver("https://generativelanguage.googleapis.com")
	if err != nil {
		return nil, err
	}

	return &Provider{
		BaseProvider: llmproxy.NewBaseProvider("googleai",
			llmproxy.WithBodyParser(&Parser{}),
			llmproxy.WithRequestEnricher(NewEnricher(apiKey)),
			llmproxy.WithResponseExtractor(NewStreamingExtractor()),
			llmproxy.WithURLResolver(resolver),
		),
	}, nil
}
