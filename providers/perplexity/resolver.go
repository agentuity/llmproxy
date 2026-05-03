package perplexity

import (
	"net/url"
	"strings"

	"github.com/agentuity/llmproxy"
)

type Resolver struct {
	BaseURL *url.URL
}

func (r *Resolver) Resolve(meta llmproxy.BodyMetadata) (*url.URL, error) {
	return r.BaseURL.JoinPath("v1", "sonar"), nil
}

func NewResolver(baseURL string) (*Resolver, error) {
	u, err := url.Parse(normalizeBaseURL(baseURL))
	if err != nil {
		return nil, err
	}
	return &Resolver{BaseURL: u}, nil
}

func normalizeBaseURL(raw string) string {
	raw = strings.TrimRight(raw, "/")
	if strings.HasSuffix(raw, "/v1") {
		raw = raw[:len(raw)-3]
	}
	return raw
}
