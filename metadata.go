// Package llmproxy provides a pluggable, composable library for proxying requests
// to upstream LLM providers.
//
// The library uses small, focused interfaces that can be mixed and matched to create
// custom provider implementations. It supports OpenAI-compatible APIs out of the box
// and can be extended for provider-specific behaviors.
//
// Core concepts:
//   - BodyParser: Extracts metadata from request bodies
//   - RequestEnricher: Modifies outgoing requests (headers, etc.)
//   - ResponseExtractor: Extracts metadata from responses
//   - URLResolver: Determines the upstream provider URL
//   - Provider: Composes the above components
//   - Interceptor: Wraps the request/response flow for cross-cutting concerns
//
// Basic usage:
//
//	provider, _ := openai.New("sk-your-key")
//	proxy := llmproxy.NewProxy(provider,
//	    llmproxy.WithInterceptor(interceptors.NewLogging(nil)),
//	)
//	resp, meta, _ := proxy.Forward(ctx, req)
package llmproxy

import "encoding/json"

// Message represents a single message in a chat completion request.
type Message struct {
	// Role is the role of the message author (e.g., "user", "assistant", "system").
	Role string `json:"role"`
	// Content is the content of the message (can be string or array for multimodal).
	Content any `json:"content"`
	// Custom holds provider-specific message fields that don't map to standard fields.
	Custom map[string]any `json:"-"`
}

// UnmarshalJSON implements custom JSON unmarshaling to capture unknown fields.
func (m *Message) UnmarshalJSON(data []byte) error {
	type Alias Message
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(m),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	m.Custom = make(map[string]any)
	for k, v := range raw {
		if k != "role" && k != "content" {
			m.Custom[k] = v
		}
	}

	return nil
}

// MarshalJSON implements custom JSON marshaling to include Custom fields.
func (m Message) MarshalJSON() ([]byte, error) {
	type Alias Message
	aux := &struct {
		Alias
	}{
		Alias: (Alias)(m),
	}

	data, err := json.Marshal(aux)
	if err != nil {
		return nil, err
	}

	if len(m.Custom) == 0 {
		return data, nil
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	for k, v := range m.Custom {
		if k == "role" || k == "content" {
			continue
		}
		result[k] = v
	}

	return json.Marshal(result)
}

// BodyMetadata contains extracted metadata from a parsed request body.
// It provides a common structure that works across different LLM providers
// while allowing provider-specific fields via the Custom map.
type BodyMetadata struct {
	// Model is the requested model identifier (e.g., "gpt-4", "claude-3-opus").
	Model string `json:"model"`
	// Messages contains the conversation history for chat completions.
	Messages []Message `json:"messages,omitempty"`
	// MaxTokens is the maximum number of tokens to generate.
	MaxTokens int `json:"max_tokens,omitempty"`
	// Stream indicates whether streaming is requested.
	Stream bool `json:"stream"`
	// Custom holds provider-specific fields that don't map to standard fields.
	Custom map[string]any `json:"-"`
}

// Usage tracks token consumption for a completion request.
type Usage struct {
	// PromptTokens is the number of tokens in the prompt.
	PromptTokens int `json:"prompt_tokens"`
	// CompletionTokens is the number of tokens generated in the completion.
	CompletionTokens int `json:"completion_tokens"`
	// TotalTokens is the sum of prompt and completion tokens.
	TotalTokens int `json:"total_tokens"`
}

// CacheUsage tracks prompt caching token consumption.
type CacheUsage struct {
	// CachedTokens is the number of tokens served from cache (OpenAI).
	CachedTokens int `json:"cached_tokens,omitempty"`
	// CacheCreationInputTokens is the number of tokens written to cache (Anthropic).
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	// CacheReadInputTokens is the number of tokens read from cache (Anthropic).
	CacheReadInputTokens int `json:"cache_read_input_tokens,omitempty"`
	// Ephemeral5mInputTokens is the number of 5-minute cache write tokens (Anthropic).
	Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens,omitempty"`
	// Ephemeral1hInputTokens is the number of 1-hour cache write tokens (Anthropic).
	Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens,omitempty"`
	// CacheWriteTokens is the number of tokens written to cache (Bedrock).
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
	// CacheDetails contains TTL-based cache write breakdown (Bedrock).
	CacheDetails []CacheDetail `json:"cache_details,omitempty"`
}

// CacheDetail contains cache details for a checkpoint (Bedrock).
type CacheDetail struct {
	// TTL is the time-to-live for the cache entry (e.g., "5m", "1h").
	TTL string `json:"ttl,omitempty"`
	// CacheWriteTokens is the number of tokens written to cache at this TTL.
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

// Choice represents a single completion choice in the response.
type Choice struct {
	// Index is the position of this choice in the choices array.
	Index int `json:"index"`
	// Message contains the completed message (for non-streaming responses).
	Message *Message `json:"message,omitempty"`
	// Delta contains the partial message (for streaming responses).
	Delta *Message `json:"delta,omitempty"`
	// FinishReason indicates why the completion stopped (e.g., "stop", "length").
	FinishReason string `json:"finish_reason"`
}

// ResponseMetadata contains extracted metadata from a provider response.
// It provides a unified view of response data across different providers.
type ResponseMetadata struct {
	// ID is the unique identifier for the response (provider-specific).
	ID string `json:"id,omitempty"`
	// Object is the object type (e.g., "chat.completion").
	Object string `json:"object,omitempty"`
	// Model is the model used for the completion.
	Model string `json:"model,omitempty"`
	// Usage contains token consumption statistics.
	Usage Usage `json:"usage"`
	// Choices contains the completion choices.
	Choices []Choice `json:"choices,omitempty"`
	// Custom holds provider-specific response fields.
	Custom map[string]any `json:"-"`
}
