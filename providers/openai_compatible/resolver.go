package openai_compatible

import (
	"net/url"
	"strings"

	"github.com/agentuity/llmproxy"
)

type Resolver struct {
	BaseURL *url.URL
	APIType llmproxy.APIType
}

func (r *Resolver) Resolve(meta llmproxy.BodyMetadata) (*url.URL, error) {
	apiType := r.APIType
	if apiType == "" {
		if v, ok := meta.Custom["api_type"].(llmproxy.APIType); ok {
			apiType = v
		} else {
			apiType = llmproxy.APITypeChatCompletions
		}
	}

	switch apiType {
	case llmproxy.APITypeResponses:
		return r.BaseURL.JoinPath("v1", "responses"), nil
	case llmproxy.APITypeCompletions:
		return r.BaseURL.JoinPath("v1", "completions"), nil
	default:
		return r.BaseURL.JoinPath("v1", "chat", "completions"), nil
	}
}

func NewResolver(baseURL string) (*Resolver, error) {
	u, err := url.Parse(normalizeBaseURL(baseURL))
	if err != nil {
		return nil, err
	}
	return &Resolver{BaseURL: u}, nil
}

func NewResolverWithAPIType(baseURL string, apiType llmproxy.APIType) (*Resolver, error) {
	u, err := url.Parse(normalizeBaseURL(baseURL))
	if err != nil {
		return nil, err
	}
	return &Resolver{BaseURL: u, APIType: apiType}, nil
}

// normalizeBaseURL strips a trailing "/v1" or "/v1/" suffix from the base URL
// so that Resolve and WebSocketURL can unconditionally prepend "/v1/..." without
// producing double segments like "/v1/v1/responses".
func normalizeBaseURL(raw string) string {
	raw = strings.TrimRight(raw, "/")
	if strings.HasSuffix(raw, "/v1") {
		raw = raw[:len(raw)-3]
	}
	return raw
}
