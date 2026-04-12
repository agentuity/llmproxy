package openai_compatible

import (
	"github.com/agentuity/llmproxy"
)

// Provider is an OpenAI-compatible provider implementation.
// It embeds llmproxy.BaseProvider and can be further customized.
type Provider struct {
	*llmproxy.BaseProvider
}

// New creates a new OpenAI-compatible provider with the given configuration.
//
// Parameters:
//   - name: A unique identifier for the provider (e.g., "openai", "groq")
//   - apiKey: The API key for authentication
//   - baseURL: The provider's API base URL (e.g., "https://api.openai.com")
//
// Example:
//
//	provider, _ := openai_compatible.New("groq", "gsk_xxx", "https://api.groq.com")
func New(name, apiKey, baseURL string) (*Provider, error) {
	resolver, err := NewResolver(baseURL)
	if err != nil {
		return nil, err
	}

	return &Provider{
		BaseProvider: llmproxy.NewBaseProvider(name,
			llmproxy.WithBodyParser(&Parser{}),
			llmproxy.WithRequestEnricher(NewEnricher(apiKey)),
			llmproxy.WithResponseExtractor(NewExtractor()),
			llmproxy.WithURLResolver(resolver),
		),
	}, nil
}

// NewWithProvider creates a Provider that wraps an existing BaseProvider.
// Use this when you need to customize individual components before creating the provider.
//
// Example:
//
//	base := llmproxy.NewBaseProvider("custom",
//	    llmproxy.WithBodyParser(&Parser{}),
//	    llmproxy.WithRequestEnricher(customEnricher),
//	)
//	provider := openai_compatible.NewWithProvider("custom", base)
func NewWithProvider(name string, p *llmproxy.BaseProvider) *Provider {
	return &Provider{BaseProvider: p}
}
