package azure

import (
	"context"
	"net/http"
	"net/url"

	"github.com/agentuity/llmproxy"
	"github.com/agentuity/llmproxy/providers/openai_compatible"
)

type Provider struct {
	*llmproxy.BaseProvider
	resourceName string
	deploymentID string
	apiVersion   string
}

type AuthMethod int

const (
	AuthMethodAPIKey AuthMethod = iota
	AuthMethodAzureAD
)

type Option func(*config)

type config struct {
	authMethod     AuthMethod
	apiKey         string
	azureADToken   string
	tokenRefresher func(ctx context.Context) (string, error)
}

func WithAPIKey(apiKey string) Option {
	return func(c *config) {
		c.authMethod = AuthMethodAPIKey
		c.apiKey = apiKey
	}
}

func WithAzureADToken(token string) Option {
	return func(c *config) {
		c.authMethod = AuthMethodAzureAD
		c.azureADToken = token
	}
}

func WithAzureADTokenRefresher(refresher func(ctx context.Context) (string, error)) Option {
	return func(c *config) {
		c.authMethod = AuthMethodAzureAD
		c.tokenRefresher = refresher
	}
}

func New(resourceName, deploymentID, apiVersion string, opts ...Option) (*Provider, error) {
	cfg := &config{}
	for _, opt := range opts {
		opt(cfg)
	}

	enricher := NewEnricher(cfg)
	resolver := NewResolver(resourceName, deploymentID, apiVersion)

	return &Provider{
		BaseProvider: llmproxy.NewBaseProvider("azure",
			llmproxy.WithBodyParser(&openai_compatible.Parser{}),
			llmproxy.WithRequestEnricher(enricher),
			llmproxy.WithResponseExtractor(&openai_compatible.Extractor{}),
			llmproxy.WithURLResolver(resolver),
		),
		resourceName: resourceName,
		deploymentID: deploymentID,
		apiVersion:   apiVersion,
	}, nil
}

func NewWithDynamicDeployment(resourceName, apiVersion string, opts ...Option) (*Provider, error) {
	cfg := &config{}
	for _, opt := range opts {
		opt(cfg)
	}

	enricher := NewEnricher(cfg)
	resolver := &Resolver{
		resourceName: resourceName,
		apiVersion:   apiVersion,
		baseURL:      &url.URL{Scheme: "https", Host: resourceName + ".openai.azure.com"},
	}

	return &Provider{
		BaseProvider: llmproxy.NewBaseProvider("azure",
			llmproxy.WithBodyParser(&openai_compatible.Parser{}),
			llmproxy.WithRequestEnricher(enricher),
			llmproxy.WithResponseExtractor(&openai_compatible.Extractor{}),
			llmproxy.WithURLResolver(resolver),
		),
		resourceName: resourceName,
		apiVersion:   apiVersion,
	}, nil
}

func DefaultAPIVersion() string {
	return "2024-02-15-preview"
}

type Enricher struct {
	config *config
}

func NewEnricher(cfg *config) *Enricher {
	return &Enricher{config: cfg}
}

func (e *Enricher) Enrich(req *http.Request, meta llmproxy.BodyMetadata, rawBody []byte) error {
	req.Header.Set("Content-Type", "application/json")

	switch e.config.authMethod {
	case AuthMethodAPIKey:
		req.Header.Set("api-key", e.config.apiKey)
	case AuthMethodAzureAD:
		token := e.config.azureADToken
		if e.config.tokenRefresher != nil {
			if refreshed, err := e.config.tokenRefresher(req.Context()); err == nil && refreshed != "" {
				token = refreshed
			}
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}

	return nil
}

type Resolver struct {
	resourceName string
	deploymentID string
	apiVersion   string
	baseURL      *url.URL
}

func NewResolver(resourceName, deploymentID, apiVersion string) *Resolver {
	return &Resolver{
		resourceName: resourceName,
		deploymentID: deploymentID,
		apiVersion:   apiVersion,
		baseURL:      &url.URL{Scheme: "https", Host: resourceName + ".openai.azure.com"},
	}
}

func (r *Resolver) Resolve(meta llmproxy.BodyMetadata) (*url.URL, error) {
	deploymentID := r.deploymentID
	if deploymentID == "" {
		deploymentID = meta.Model
	}

	path := "/openai/deployments/" + deploymentID + "/chat/completions"
	u, _ := url.Parse(path)
	u = r.baseURL.ResolveReference(u)
	q := u.Query()
	q.Set("api-version", r.apiVersion)
	u.RawQuery = q.Encode()

	return u, nil
}
