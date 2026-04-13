package anthropic

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/agentuity/llmproxy"
)

// Extractor implements llmproxy.ResponseExtractor for Anthropic responses.
type Extractor struct{}

// Extract parses an Anthropic response and returns unified metadata.
//
// Anthropic responses use:
//   - content array instead of choices
//   - input_tokens/output_tokens instead of prompt_tokens/completion_tokens
//
// Returns metadata, raw body bytes, and any error.
func (e *Extractor) Extract(resp *http.Response) (llmproxy.ResponseMetadata, []byte, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return llmproxy.ResponseMetadata{}, nil, err
	}

	var anthropicResp Response
	if err := json.Unmarshal(body, &anthropicResp); err != nil {
		return llmproxy.ResponseMetadata{}, nil, err
	}

	meta := llmproxy.ResponseMetadata{
		ID:     anthropicResp.ID,
		Object: anthropicResp.Type,
		Model:  anthropicResp.Model,
		Usage: llmproxy.Usage{
			PromptTokens:     anthropicResp.Usage.InputTokens,
			CompletionTokens: anthropicResp.Usage.OutputTokens,
			TotalTokens:      anthropicResp.Usage.InputTokens + anthropicResp.Usage.OutputTokens,
		},
		Choices: make([]llmproxy.Choice, 0, 1),
		Custom:  make(map[string]any),
	}

	cacheUsage := llmproxy.CacheUsage{
		CacheCreationInputTokens: anthropicResp.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     anthropicResp.Usage.CacheReadInputTokens,
	}
	if anthropicResp.CacheCreation != nil {
		cacheUsage.Ephemeral5mInputTokens = anthropicResp.CacheCreation.Ephemeral5mInputTokens
		cacheUsage.Ephemeral1hInputTokens = anthropicResp.CacheCreation.Ephemeral1hInputTokens
	}
	if cacheUsage.CacheCreationInputTokens > 0 || cacheUsage.CacheReadInputTokens > 0 {
		meta.Custom["cache_usage"] = cacheUsage
	}

	if len(anthropicResp.Content) > 0 {
		var content string
		var role string
		for _, block := range anthropicResp.Content {
			if block.Type == "text" {
				content += block.Text
			}
		}
		if anthropicResp.Role != "" {
			role = anthropicResp.Role
		} else {
			role = "assistant"
		}
		meta.Choices = append(meta.Choices, llmproxy.Choice{
			Index: 0,
			Message: &llmproxy.Message{
				Role:    role,
				Content: content,
			},
			FinishReason: anthropicResp.StopReason,
		})
	}

	return meta, body, nil
}

// Response represents an Anthropic messages API response.
type Response struct {
	ID            string             `json:"id"`
	Type          string             `json:"type"`
	Role          string             `json:"role"`
	Model         string             `json:"model"`
	Content       []ContentBlock     `json:"content"`
	StopReason    string             `json:"stop_reason"`
	StopSequence  string             `json:"stop_sequence,omitempty"`
	Usage         UsageInfo          `json:"usage"`
	CacheCreation *CacheCreationInfo `json:"cache_creation,omitempty"`
}

// ContentBlock represents a content block in an Anthropic response.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// UsageInfo tracks token usage in an Anthropic response.
type UsageInfo struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// CacheCreationInfo tracks cache creation token breakdown.
type CacheCreationInfo struct {
	Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens,omitempty"`
	Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens,omitempty"`
}

// NewExtractor creates a new Anthropic response extractor.
func NewExtractor() *Extractor {
	return &Extractor{}
}
