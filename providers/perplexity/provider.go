package perplexity

import (
	"github.com/agentuity/llmproxy"
	"github.com/agentuity/llmproxy/providers/openai_compatible"
)

type Provider struct {
	*llmproxy.BaseProvider
}

func New(apiKey string) (*Provider, error) {
	resolver, err := NewResolver("https://api.perplexity.ai")
	if err != nil {
		return nil, err
	}

	return &Provider{
		BaseProvider: llmproxy.NewBaseProvider("perplexity",
			llmproxy.WithBodyParser(&openai_compatible.Parser{}),
			llmproxy.WithRequestEnricher(openai_compatible.NewEnricher(apiKey)),
			llmproxy.WithResponseExtractor(openai_compatible.NewExtractor()),
			llmproxy.WithURLResolver(resolver),
		),
	}, nil
}
