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

// Message represents a single message in a chat completion request.
type Message struct {
	// Role is the role of the message author (e.g., "user", "assistant", "system").
	Role string `json:"role"`
	// Content is the text content of the message.
	Content string `json:"content"`
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
