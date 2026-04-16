package openai_compatible

import (
	"errors"
	"net/url"

	"github.com/agentuity/llmproxy"
)

// Provider is an OpenAI-compatible provider implementation.
// It embeds llmproxy.BaseProvider and can be further customized.
type Provider struct {
	*llmproxy.BaseProvider
}

var _ llmproxy.WebSocketCapableProvider = (*Provider)(nil)

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
			llmproxy.WithResponseExtractor(NewStreamingExtractor()),
			llmproxy.WithURLResolver(resolver),
		),
	}, nil
}

func NewMultiAPI(name, apiKey, baseURL string) (*Provider, error) {
	resolver, err := NewResolver(baseURL)
	if err != nil {
		return nil, err
	}

	return &Provider{
		BaseProvider: llmproxy.NewBaseProvider(name,
			llmproxy.WithBodyParser(NewMultiAPIParser()),
			llmproxy.WithRequestEnricher(NewEnricher(apiKey)),
			llmproxy.WithResponseExtractor(NewStreamingMultiAPIExtractor()),
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

func (p *Provider) WebSocketURL(meta llmproxy.BodyMetadata) (*url.URL, error) {
	resolver := p.URLResolver()
	if resolver == nil {
		return nil, errors.New("provider has no URL resolver")
	}

	if wsResolver, ok := resolver.(interface {
		WebSocketURL(llmproxy.BodyMetadata) (*url.URL, error)
	}); ok {
		return wsResolver.WebSocketURL(meta)
	}

	return nil, errors.New("provider URL resolver does not support websocket URL")
}
