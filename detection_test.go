package llmproxy

import (
	"net/http"
	"testing"
)

func TestDetectAPIType(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected APIType
	}{
		{
			name:     "chat completions with messages",
			body:     `{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`,
			expected: APITypeChatCompletions,
		},
		{
			name:     "responses API with input",
			body:     `{"model":"gpt-4o","input":"hello world"}`,
			expected: APITypeResponses,
		},
		{
			name:     "responses API with instructions",
			body:     `{"model":"gpt-4o","input":"hello","instructions":"be helpful"}`,
			expected: APITypeResponses,
		},
		{
			name:     "legacy completions with prompt",
			body:     `{"model":"gpt-3.5-turbo-instruct","prompt":"hello"}`,
			expected: APITypeCompletions,
		},
		{
			name:     "invalid JSON defaults to chat completions",
			body:     `invalid`,
			expected: APITypeChatCompletions,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetectAPIType([]byte(tt.body))
			if result != tt.expected {
				t.Errorf("DetectAPIType() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestDetectAPITypeFromPath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected APIType
	}{
		{
			name:     "chat completions path",
			path:     "/v1/chat/completions",
			expected: APITypeChatCompletions,
		},
		{
			name:     "responses path",
			path:     "/v1/responses",
			expected: APITypeResponses,
		},
		{
			name:     "legacy completions path",
			path:     "/v1/completions",
			expected: APITypeCompletions,
		},
		{
			name:     "anthropic messages path",
			path:     "/v1/messages",
			expected: APITypeMessages,
		},
		{
			name:     "google generate content path",
			path:     "/v1/models/gemini-pro:generateContent",
			expected: APITypeGenerateContent,
		},
		{
			name:     "bedrock converse path",
			path:     "/model/model-id/converse",
			expected: APITypeConverse,
		},
		{
			name:     "unknown path returns empty",
			path:     "/unknown",
			expected: "",
		},
		{
			name:     "root path returns empty",
			path:     "/",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetectAPITypeFromPath(tt.path)
			if result != tt.expected {
				t.Errorf("DetectAPITypeFromPath() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestDetectProviderFromModel(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		expected string
	}{
		{"gpt-4", "gpt-4", "openai"},
		{"gpt-3.5-turbo", "gpt-3.5-turbo", "openai"},
		{"o1-preview", "o1-preview", "openai"},
		{"o3-mini", "o3-mini", "openai"},
		{"chatgpt-4o-latest", "chatgpt-4o-latest", "openai"},
		{"claude-3-opus", "claude-3-opus", "anthropic"},
		{"claude-3-5-sonnet", "claude-3-5-sonnet", "anthropic"},
		{"gemini-pro", "gemini-pro", "googleai"},
		{"gemma-2b", "gemma-2b", "googleai"},
		{"grok-1", "grok-1", "xai"},
		{"fireworks-llama", "accounts/fireworks/models/llama-v3", "fireworks"},
		{"sonar-small", "sonar-small-online", "perplexity"},
		{"bedrock claude", "anthropic.claude-3-sonnet", "bedrock"},
		{"bedrock amazon", "amazon.titan-text-express", "bedrock"},
		{"unknown model", "unknown-model", ""},
		{"empty model", "", ""},
		// Provider prefix tests
		{"openai/gpt-4 prefix", "openai/gpt-4", "openai"},
		{"anthropic/claude-3-opus prefix", "anthropic/claude-3-opus", "anthropic"},
		{"googleai/gemini-pro prefix", "googleai/gemini-pro", "googleai"},
		{"groq/llama-3 prefix", "groq/llama-3-70b", "groq"},
		{"fireworks/llama prefix", "fireworks/llama-v3", "fireworks"},
		{"xai/grok prefix", "xai/grok-1", "xai"},
		{"perplexity/sonar prefix", "perplexity/sonar-small", "perplexity"},
		{"bedrock/claude prefix", "bedrock/anthropic.claude-3", "bedrock"},
		{"azure/gpt-4 prefix", "azure/gpt-4", "azure"},
		{"unknown/ prefix returns unknown", "unknown/model", ""},
		{"single slash only", "/", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetectProviderFromModel(tt.model)
			if result != tt.expected {
				t.Errorf("DetectProviderFromModel(%q) = %q, want %q", tt.model, result, tt.expected)
			}
		})
	}
}

func TestDefaultProviderDetector(t *testing.T) {
	tests := []struct {
		name     string
		hint     ProviderHint
		expected string
	}{
		{
			name:     "detect from model",
			hint:     ProviderHint{Model: "gpt-4"},
			expected: "openai",
		},
		{
			name:     "detect from anthropic model",
			hint:     ProviderHint{Model: "claude-3-opus"},
			expected: "anthropic",
		},
		{
			name: "detect from X-Provider header",
			hint: ProviderHint{
				Model:   "unknown",
				Headers: http.Header{"X-Provider": []string{"custom-provider"}},
			},
			expected: "custom-provider",
		},
		{
			name:     "empty hint returns empty",
			hint:     ProviderHint{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DefaultProviderDetector.Detect(tt.hint)
			if result != tt.expected {
				t.Errorf("Detect() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestDetectAPITypeFromBodyAndProvider(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		provider string
		expected APIType
	}{
		{
			name:     "openai with messages -> chat completions",
			body:     `{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`,
			provider: "openai",
			expected: APITypeChatCompletions,
		},
		{
			name:     "anthropic with messages -> messages API",
			body:     `{"model":"claude-3-opus","messages":[{"role":"user","content":"hello"}]}`,
			provider: "anthropic",
			expected: APITypeMessages,
		},
		{
			name:     "anthropic with system and messages -> messages API",
			body:     `{"model":"claude-3-opus","system":"You are helpful","messages":[{"role":"user","content":"hello"}]}`,
			provider: "anthropic",
			expected: APITypeMessages,
		},
		{
			name:     "responses API with input",
			body:     `{"model":"gpt-4o","input":"hello world"}`,
			provider: "openai",
			expected: APITypeResponses,
		},
		{
			name:     "legacy completions with prompt",
			body:     `{"model":"gpt-3.5-turbo-instruct","prompt":"hello"}`,
			provider: "openai",
			expected: APITypeCompletions,
		},
		{
			name:     "googleai with contents -> generateContent",
			body:     `{"model":"gemini-pro","contents":[{"parts":[{"text":"hello"}]}]}`,
			provider: "googleai",
			expected: APITypeGenerateContent,
		},
		{
			name:     "groq with messages -> chat completions",
			body:     `{"model":"llama-3-70b","messages":[{"role":"user","content":"hello"}]}`,
			provider: "groq",
			expected: APITypeChatCompletions,
		},
		{
			name:     "bedrock with messages -> converse",
			body:     `{"model":"anthropic.claude-3","messages":[{"role":"user","content":"hello"}]}`,
			provider: "bedrock",
			expected: APITypeConverse,
		},
		{
			name:     "unknown provider with messages -> chat completions",
			body:     `{"model":"unknown-model","messages":[{"role":"user","content":"hello"}]}`,
			provider: "",
			expected: APITypeChatCompletions,
		},
		{
			name:     "system without messages -> chat completions",
			body:     `{"model":"model","system":"be helpful"}`,
			provider: "openai",
			expected: APITypeChatCompletions,
		},
		{
			name:     "invalid JSON -> chat completions",
			body:     `invalid`,
			provider: "openai",
			expected: APITypeChatCompletions,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetectAPITypeFromBodyAndProvider([]byte(tt.body), tt.provider)
			if result != tt.expected {
				t.Errorf("DetectAPITypeFromBodyAndProvider() = %v, want %v", result, tt.expected)
			}
		})
	}
}
