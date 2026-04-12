// Package anthropic provides a provider implementation for Anthropic's Claude API.
//
// Anthropic uses a different API format than OpenAI, so this package implements
// custom parsing, enrichment, and extraction logic.
//
// Key differences from OpenAI:
//   - Endpoint: /v1/messages
//   - Auth: x-api-key header (not Bearer token)
//   - Required header: anthropic-version
//   - System prompt is a separate field (not a message with role "system")
//   - Response uses content array instead of choices
//   - Token usage fields: input_tokens/output_tokens
//
// Basic usage:
//
//	provider, _ := anthropic.New("sk-ant-your-key")
//	proxy := llmproxy.NewProxy(provider)
package anthropic

import (
	"encoding/json"
	"io"

	"github.com/agentuity/llmproxy"
)

// Parser implements llmproxy.BodyParser for Anthropic's request format.
type Parser struct{}

// Parse reads an Anthropic request body and extracts metadata.
// It handles Anthropic-specific fields like the system prompt.
func (p *Parser) Parse(body io.ReadCloser) (llmproxy.BodyMetadata, []byte, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return llmproxy.BodyMetadata{}, nil, err
	}
	body.Close()

	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		return llmproxy.BodyMetadata{}, nil, err
	}

	meta := llmproxy.BodyMetadata{
		Model:     req.Model,
		Messages:  make([]llmproxy.Message, len(req.Messages)),
		MaxTokens: req.MaxTokens,
		Custom:    make(map[string]any),
	}

	for i, m := range req.Messages {
		meta.Messages[i] = llmproxy.Message{
			Role:    m.Role,
			Content: contentToString(m.Content),
		}
	}

	if req.System != "" {
		meta.Custom["system"] = req.System
	}

	for k, v := range req.Custom {
		meta.Custom[k] = v
	}

	return meta, data, nil
}

func contentToString(c Content) string {
	if c.Text != "" {
		return c.Text
	}
	if len(c.Parts) > 0 {
		var result string
		for _, part := range c.Parts {
			if part.Type == "text" {
				result += part.Text
			}
		}
		return result
	}
	return ""
}

// Request represents an Anthropic messages API request.
type Request struct {
	Model     string                 `json:"model"`
	Messages  []Message              `json:"messages"`
	MaxTokens int                    `json:"max_tokens,omitempty"`
	System    string                 `json:"system,omitempty"`
	Custom    map[string]interface{} `json:"-"`
}

// Message represents a message in an Anthropic request.
type Message struct {
	Role    string  `json:"role"`
	Content Content `json:"content"`
}

// Content can be either a string or an array of content blocks.
type Content struct {
	Text  string        `json:"-"`
	Parts []ContentPart `json:"-"`
}

// ContentPart represents a single content block.
type ContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// UnmarshalJSON handles Anthropic's flexible content format (string or array).
func (c *Content) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		c.Text = s
		return nil
	}

	var parts []ContentPart
	if err := json.Unmarshal(data, &parts); err != nil {
		return err
	}
	c.Parts = parts
	return nil
}

// UnmarshalJSON captures unknown fields into Custom.
func (r *Request) UnmarshalJSON(data []byte) error {
	type Alias Request
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(r),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	r.Custom = make(map[string]interface{})
	known := map[string]bool{
		"model": true, "messages": true, "max_tokens": true,
		"system": true, "stream": true, "temperature": true,
		"top_p": true, "top_k": true, "stop_sequences": true,
	}
	for k, v := range raw {
		if !known[k] {
			r.Custom[k] = v
		}
	}

	return nil
}
