package openai_compatible

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/agentuity/llmproxy"
)

// Extractor implements llmproxy.ResponseExtractor for OpenAI-compatible responses.
// It parses the response JSON and extracts token usage, choices, and other metadata.
type Extractor struct{}

// Extract reads the response body and parses it as an OpenAI-compatible response.
// It extracts the ID, model, usage statistics, and completion choices.
//
// Returns:
//   - metadata: Parsed response metadata
//   - rawBody: The original response body bytes (preserved for forwarding)
//   - error: Any parsing error
//
// The raw body is returned so it can be re-attached to the response for the caller,
// preserving any custom/unsupported fields in the original JSON.
func (e *Extractor) Extract(resp *http.Response) (llmproxy.ResponseMetadata, []byte, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return llmproxy.ResponseMetadata{}, nil, err
	}

	var openaiResp OpenAIResponse
	if err := json.Unmarshal(body, &openaiResp); err != nil {
		return llmproxy.ResponseMetadata{}, nil, err
	}

	meta := llmproxy.ResponseMetadata{
		ID:     openaiResp.ID,
		Object: openaiResp.Object,
		Model:  openaiResp.Model,
		Usage: llmproxy.Usage{
			PromptTokens:     openaiResp.Usage.PromptTokens,
			CompletionTokens: openaiResp.Usage.CompletionTokens,
			TotalTokens:      openaiResp.Usage.TotalTokens,
		},
		Choices: make([]llmproxy.Choice, len(openaiResp.Choices)),
		Custom:  make(map[string]any),
	}

	for i, c := range openaiResp.Choices {
		meta.Choices[i] = llmproxy.Choice{
			Index:        c.Index,
			FinishReason: c.FinishReason,
		}
		if c.Message != nil {
			meta.Choices[i].Message = &llmproxy.Message{
				Role:    c.Message.Role,
				Content: c.Message.Content,
			}
		}
		if c.Delta != nil {
			meta.Choices[i].Delta = &llmproxy.Message{
				Role:    c.Delta.Role,
				Content: c.Delta.Content,
			}
		}
	}

	return meta, body, nil
}

// OpenAIResponse represents an OpenAI-compatible chat completion response.
type OpenAIResponse struct {
	// ID is the unique response identifier.
	ID string `json:"id"`
	// Object is the object type (e.g., "chat.completion").
	Object string `json:"object"`
	// Created is the Unix timestamp of creation.
	Created int64 `json:"created"`
	// Model is the model used for completion.
	Model string `json:"model"`
	// Usage contains token consumption statistics.
	Usage UsageInfo `json:"usage"`
	// Choices contains the completion choices.
	Choices []ResponseChoice `json:"choices"`
}

// UsageInfo tracks token usage in an OpenAI-compatible response.
type UsageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ResponseChoice represents a single completion choice.
type ResponseChoice struct {
	// Index is the choice position.
	Index int `json:"index"`
	// Message contains the completed message (non-streaming).
	Message *ResponseMessage `json:"message,omitempty"`
	// Delta contains the partial message (streaming).
	Delta *ResponseMessage `json:"delta,omitempty"`
	// FinishReason indicates why completion stopped.
	FinishReason string `json:"finish_reason"`
}

// ResponseMessage represents a message in a completion choice.
type ResponseMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// NewExtractor creates a new OpenAI-compatible response extractor.
func NewExtractor() *Extractor {
	return &Extractor{}
}
