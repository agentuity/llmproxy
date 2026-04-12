package llmproxy

// Provider composes all the components needed to handle requests for an LLM provider.
//
// A provider brings together:
//   - BodyParser: To extract request metadata
//   - RequestEnricher: To modify outgoing requests
//   - ResponseExtractor: To parse responses
//   - URLResolver: To determine upstream URLs
//
// Implementations can use BaseProvider for a configurable default, or implement
// Provider directly for complete control.
type Provider interface {
	// Name returns the provider's unique identifier (e.g., "openai", "anthropic").
	Name() string

	// BodyParser returns the parser for extracting request metadata.
	BodyParser() BodyParser

	// RequestEnricher returns the enricher for modifying outgoing requests.
	RequestEnricher() RequestEnricher

	// ResponseExtractor returns the extractor for parsing responses.
	ResponseExtractor() ResponseExtractor

	// URLResolver returns the resolver for determining upstream URLs.
	URLResolver() URLResolver
}

// BaseProvider provides a configurable implementation of Provider.
// It allows setting individual components via functional options,
// making it easy to mix and match behaviors.
//
// Use NewBaseProvider with With* options to create a custom provider:
//
//	provider := NewBaseProvider("my-provider",
//	    WithBodyParser(myParser),
//	    WithRequestEnricher(myEnricher),
//	)
type BaseProvider struct {
	name              string
	bodyParser        BodyParser
	requestEnricher   RequestEnricher
	responseExtractor ResponseExtractor
	urlResolver       URLResolver
}

// ProviderOption configures a BaseProvider during construction.
type ProviderOption func(*BaseProvider)

// WithBodyParser sets the body parser for the provider.
func WithBodyParser(bp BodyParser) ProviderOption {
	return func(p *BaseProvider) { p.bodyParser = bp }
}

// WithRequestEnricher sets the request enricher for the provider.
func WithRequestEnricher(re RequestEnricher) ProviderOption {
	return func(p *BaseProvider) { p.requestEnricher = re }
}

// WithResponseExtractor sets the response extractor for the provider.
func WithResponseExtractor(re ResponseExtractor) ProviderOption {
	return func(p *BaseProvider) { p.responseExtractor = re }
}

// WithURLResolver sets the URL resolver for the provider.
func WithURLResolver(ur URLResolver) ProviderOption {
	return func(p *BaseProvider) { p.urlResolver = ur }
}

// NewBaseProvider creates a new provider with the given name and options.
// Unset components will return nil from their accessor methods.
func NewBaseProvider(name string, opts ...ProviderOption) *BaseProvider {
	p := &BaseProvider{name: name}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name returns the provider's name.
func (p *BaseProvider) Name() string { return p.name }

// BodyParser returns the configured body parser, or nil if not set.
func (p *BaseProvider) BodyParser() BodyParser { return p.bodyParser }

// RequestEnricher returns the configured request enricher, or nil if not set.
func (p *BaseProvider) RequestEnricher() RequestEnricher { return p.requestEnricher }

// ResponseExtractor returns the configured response extractor, or nil if not set.
func (p *BaseProvider) ResponseExtractor() ResponseExtractor { return p.responseExtractor }

// URLResolver returns the configured URL resolver, or nil if not set.
func (p *BaseProvider) URLResolver() URLResolver { return p.urlResolver }
