package perplexity

import (
	"net/url"

	"github.com/agentuity/llmproxy"
)

type Resolver struct {
	BaseURL *url.URL
}

func (r *Resolver) Resolve(meta llmproxy.BodyMetadata) (*url.URL, error) {
	return r.BaseURL.JoinPath("v1", "sonar"), nil
}

func NewResolver(baseURL string) (*Resolver, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	return &Resolver{BaseURL: u}, nil
}
