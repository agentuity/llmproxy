package anthropic

import (
	"github.com/agentuity/llmproxy"
)

// Provider is an Anthropic provider implementation.
type Provider struct {
	*llmproxy.BaseProvider
}

// New creates a new Anthropic provider with the given API key.
// The provider is configured to use Anthropic's API endpoint (https://api.anthropic.com).
//
// Example:
//
//	provider, _ := anthropic.New("sk-ant-your-api-key")
func New(apiKey string) (*Provider, error) {
	resolver, err := NewResolver("https://api.anthropic.com")
	if err != nil {
		return nil, err
	}

	return &Provider{
		BaseProvider: llmproxy.NewBaseProvider("anthropic",
			llmproxy.WithBodyParser(&Parser{}),
			llmproxy.WithRequestEnricher(NewEnricher(apiKey)),
			llmproxy.WithResponseExtractor(NewExtractor()),
			llmproxy.WithURLResolver(resolver),
		),
	}, nil
}

// NewWithVersion creates a new Anthropic provider with a specific API version.
//
// Example:
//
//	provider, _ := anthropic.NewWithVersion("sk-ant-your-api-key", "2024-01-01")
func NewWithVersion(apiKey, version string) (*Provider, error) {
	resolver, err := NewResolver("https://api.anthropic.com")
	if err != nil {
		return nil, err
	}

	return &Provider{
		BaseProvider: llmproxy.NewBaseProvider("anthropic",
			llmproxy.WithBodyParser(&Parser{}),
			llmproxy.WithRequestEnricher(NewEnricherWithVersion(apiKey, version)),
			llmproxy.WithResponseExtractor(NewExtractor()),
			llmproxy.WithURLResolver(resolver),
		),
	}, nil
}
