package anthropic

import (
	"github.com/agentuity/llmproxy"
)

type Provider struct {
	*llmproxy.BaseProvider
}

func New(apiKey string) (*Provider, error) {
	resolver, err := NewResolver("https://api.anthropic.com")
	if err != nil {
		return nil, err
	}

	return &Provider{
		BaseProvider: llmproxy.NewBaseProvider("anthropic",
			llmproxy.WithBodyParser(&Parser{}),
			llmproxy.WithRequestEnricher(NewEnricher(apiKey)),
			llmproxy.WithResponseExtractor(NewStreamingExtractor()),
			llmproxy.WithURLResolver(resolver),
		),
	}, nil
}

func NewWithVersion(apiKey, version string) (*Provider, error) {
	resolver, err := NewResolver("https://api.anthropic.com")
	if err != nil {
		return nil, err
	}

	return &Provider{
		BaseProvider: llmproxy.NewBaseProvider("anthropic",
			llmproxy.WithBodyParser(&Parser{}),
			llmproxy.WithRequestEnricher(NewEnricherWithVersion(apiKey, version)),
			llmproxy.WithResponseExtractor(NewStreamingExtractor()),
			llmproxy.WithURLResolver(resolver),
		),
	}, nil
}
