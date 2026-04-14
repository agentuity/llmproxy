package llmproxy

import (
	"net/http"
	"strings"
)

// ProviderHint contains information that can be used to detect the provider.
type ProviderHint struct {
	Model   string
	Headers http.Header
}

// ProviderDetector determines the upstream provider based on request characteristics.
type ProviderDetector interface {
	Detect(hint ProviderHint) string
}

// ProviderDetectorFunc is a function that implements ProviderDetector.
type ProviderDetectorFunc func(hint ProviderHint) string

func (f ProviderDetectorFunc) Detect(hint ProviderHint) string {
	return f(hint)
}

// DefaultProviderDetector detects the provider from model name patterns and headers.
var DefaultProviderDetector = ProviderDetectorFunc(func(hint ProviderHint) string {
	if hint.Headers != nil {
		if provider := detectProviderFromHeaders(hint.Headers); provider != "" {
			return provider
		}
	}

	if hint.Model != "" {
		return DetectProviderFromModel(hint.Model)
	}

	return ""
})

func detectProviderFromHeaders(headers http.Header) string {
	if headers.Get("X-Provider") != "" {
		return headers.Get("X-Provider")
	}

	if headers.Get("anthropic-version") != "" || strings.HasPrefix(headers.Get("X-API-Key"), "sk-ant-") {
		return "anthropic"
	}

	if strings.HasPrefix(headers.Get("Authorization"), "Bearer sk-") {
		if strings.Contains(headers.Get("Authorization"), "sk-proj-") {
			return "openai"
		}
	}

	if strings.HasPrefix(headers.Get("api-key"), "") && headers.Get("api-key") != "" {
		return "azure"
	}

	if strings.HasPrefix(headers.Get("Authorization"), "Bearer gsk_") {
		return "groq"
	}

	return ""
}

// DetectProviderFromModel returns the provider name based on model naming patterns.
func DetectProviderFromModel(model string) string {
	if model == "" {
		return ""
	}

	// Check for explicit provider prefix (e.g., "openai/gpt-4", "anthropic/claude-3-opus")
	if idx := strings.Index(model, "/"); idx >= 0 {
		prefix := model[:idx]
		switch prefix {
		case "openai", "anthropic", "googleai", "groq", "fireworks", "xai", "perplexity", "bedrock", "azure":
			return prefix
		}
	}

	switch {
	case strings.HasPrefix(model, "gpt-"),
		strings.HasPrefix(model, "o1-"),
		strings.HasPrefix(model, "o3-"),
		strings.HasPrefix(model, "o4-"),
		strings.HasPrefix(model, "chatgpt-"),
		strings.HasPrefix(model, "text-"),
		strings.HasPrefix(model, "davinci-"),
		strings.HasPrefix(model, "curie-"),
		strings.HasPrefix(model, "babbage-"),
		strings.HasPrefix(model, "ada-"):
		return "openai"

	case strings.HasPrefix(model, "claude-"),
		strings.HasPrefix(model, "claude"):
		return "anthropic"

	case strings.HasPrefix(model, "gemini-"),
		strings.HasPrefix(model, "gemma-"),
		strings.HasPrefix(model, "palm-"):
		return "googleai"

	case strings.HasPrefix(model, "grok-"):
		return "xai"

	case strings.HasPrefix(model, "llama-"),
		strings.HasPrefix(model, "mixtral-"),
		strings.HasPrefix(model, "mistral-"):
		if strings.Contains(model, "groq") {
			return "groq"
		}
		return "openai_compatible"

	case strings.HasPrefix(model, "accounts/fireworks/"),
		strings.HasPrefix(model, "fireworks"):
		return "fireworks"

	case strings.Contains(model, "sonar"):
		return "perplexity"

	case strings.HasPrefix(model, "amazon."),
		strings.HasPrefix(model, "anthropic.claude-"),
		strings.HasPrefix(model, "meta."),
		strings.HasPrefix(model, "cohere."):
		return "bedrock"

	default:
		return ""
	}
}
