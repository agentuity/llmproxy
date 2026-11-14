package llmproxy

import (
	"net/http"
	"strings"
)

// upstreamRequestHeaders copies only supported request options, never caller
// authentication. Enrichers and interceptors add trusted provider credentials
// after this boundary. Unknown headers are deliberately not forwarded.
func upstreamRequestHeaders(in http.Header, provider string) http.Header {
	blocked := map[string]bool{
		"connection": true, "proxy-connection": true, "keep-alive": true,
		"proxy-authenticate": true, "proxy-authorization": true,
		"te": true, "trailer": true, "transfer-encoding": true, "upgrade": true,
	}
	// Connection can name additional hop-by-hop fields. Iterate directly so
	// noncanonical header names supplied by library callers are handled too.
	for name, values := range in {
		if strings.EqualFold(name, "Connection") {
			for _, value := range values {
				for _, token := range strings.Split(value, ",") {
					blocked[strings.ToLower(strings.TrimSpace(token))] = true
				}
			}
		}
	}

	out := make(http.Header)
	for name, values := range in {
		key := strings.ToLower(name)
		if blocked[key] || !allowedUpstreamHeader(key, strings.ToLower(provider)) {
			continue
		}
		// Do not alias the caller's header value slices.
		for _, value := range values {
			out.Add(name, value)
		}
	}
	return out
}

func allowedUpstreamHeader(name, provider string) bool {
	switch name {
	case "accept", "content-type", "content-encoding", "cache-control",
		"user-agent", "idempotency-key", "x-request-id", "x-client-request-id",
		"traceparent", "tracestate",
		// These are consumed by the existing prompt caching interceptors.
		"x-cache-key", "x-org-id":
		return true
	}
	switch provider {
	case "openai", "azure":
		return name == "openai-beta" || name == "openai-organization" || name == "openai-project"
	case "anthropic":
		return name == "anthropic-version" || name == "anthropic-beta"
	case "xai":
		return name == "x-grok-conv-id"
	case "fireworks":
		return name == "x-session-affinity" || name == "x-prompt-cache-isolation-key"
	default:
		return false
	}
}
