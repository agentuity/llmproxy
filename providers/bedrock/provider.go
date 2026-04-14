package bedrock

import (
	"github.com/agentuity/llmproxy"
)

type Provider struct {
	*llmproxy.BaseProvider
}

func New(region, accessKeyID, secretAccessKey, sessionToken string) (*Provider, error) {
	return &Provider{
		BaseProvider: llmproxy.NewBaseProvider("bedrock",
			llmproxy.WithBodyParser(&Parser{}),
			llmproxy.WithRequestEnricher(NewEnricher(region, accessKeyID, secretAccessKey, sessionToken)),
			llmproxy.WithResponseExtractor(NewStreamingExtractor()),
			llmproxy.WithURLResolver(NewResolver(region)),
		),
	}, nil
}

func NewWithConfig(region, accessKeyID, secretAccessKey, sessionToken string, useConverseAPI bool) (*Provider, error) {
	var resolver llmproxy.URLResolver
	if useConverseAPI {
		resolver = NewResolver(region)
	} else {
		resolver = NewInvokeResolver(region)
	}

	return &Provider{
		BaseProvider: llmproxy.NewBaseProvider("bedrock",
			llmproxy.WithBodyParser(&Parser{}),
			llmproxy.WithRequestEnricher(NewEnricher(region, accessKeyID, secretAccessKey, sessionToken)),
			llmproxy.WithResponseExtractor(NewStreamingExtractor()),
			llmproxy.WithURLResolver(resolver),
		),
	}, nil
}
