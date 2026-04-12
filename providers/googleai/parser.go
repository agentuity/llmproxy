// Package googleai provides a provider implementation for Google AI's Gemini API.
//
// Google AI uses a different API format than OpenAI, so this package implements
// custom parsing, enrichment, and extraction logic.
//
// Key differences from OpenAI:
//   - Endpoint: /v1beta/models/{model}:generateContent (model in path)
//   - Auth: x-goog-api-key header (not Bearer token)
//   - Request uses contents array with parts instead of messages
//   - System prompt is in systemInstruction field
//   - Response uses candidates instead of choices
//   - Token fields: promptTokenCount/candidatesTokenCount
//
// Basic usage:
//
//	provider, _ := googleai.New("your-api-key")
//	proxy := llmproxy.NewProxy(provider)
package googleai

import (
	"encoding/json"
	"io"

	"github.com/agentuity/llmproxy"
)

// Parser implements llmproxy.BodyParser for Google AI's request format.
type Parser struct{}

// Parse reads a Google AI request body and extracts metadata.
// It handles Google AI-specific fields like contents with parts.
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
		Messages:  make([]llmproxy.Message, 0, len(req.Contents)),
		MaxTokens: req.GenerationConfig.MaxOutputTokens,
		Custom:    make(map[string]any),
	}

	for _, content := range req.Contents {
		text := extractTextFromParts(content.Parts)
		if text != "" || content.Role != "" {
			role := content.Role
			if role == "model" {
				role = "assistant"
			}
			meta.Messages = append(meta.Messages, llmproxy.Message{
				Role:    role,
				Content: text,
			})
		}
	}

	if req.SystemInstruction != nil {
		text := extractTextFromParts(req.SystemInstruction.Parts)
		if text != "" {
			meta.Custom["system_instruction"] = text
		}
	}

	for k, v := range req.Custom {
		meta.Custom[k] = v
	}

	return meta, data, nil
}

func extractTextFromParts(parts []Part) string {
	var text string
	for _, part := range parts {
		if part.Text != "" {
			text += part.Text
		}
	}
	return text
}

// Request represents a Google AI generateContent request.
type Request struct {
	Model             string                 `json:"-"` // Extracted from path
	Contents          []Content              `json:"contents,omitempty"`
	SystemInstruction *Content               `json:"systemInstruction,omitempty"`
	GenerationConfig  GenerationConfig       `json:"generationConfig,omitempty"`
	SafetySettings    []SafetySetting        `json:"safetySettings,omitempty"`
	Custom            map[string]interface{} `json:"-"`
}

// Content represents a content block with role and parts.
type Content struct {
	Role  string `json:"role,omitempty"`
	Parts []Part `json:"parts"`
}

// Part represents a single part of content (text, inline_data, etc.).
type Part struct {
	Text       string                 `json:"text,omitempty"`
	InlineData *InlineData            `json:"inlineData,omitempty"`
	Custom     map[string]interface{} `json:"-"`
}

// InlineData represents binary data in a part.
type InlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// GenerationConfig contains generation parameters.
type GenerationConfig struct {
	Temperature     float64  `json:"temperature,omitempty"`
	TopP            float64  `json:"topP,omitempty"`
	TopK            int      `json:"topK,omitempty"`
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

// SafetySetting represents a safety configuration.
type SafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
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
		"contents": true, "systemInstruction": true, "generationConfig": true,
		"safetySettings": true, "tools": true, "toolConfig": true,
	}
	for k, v := range raw {
		if !known[k] {
			r.Custom[k] = v
		}
	}

	return nil
}

// UnmarshalJSON handles flexible part content.
func (p *Part) UnmarshalJSON(data []byte) error {
	type Alias Part
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(p),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	p.Custom = make(map[string]interface{})
	known := map[string]bool{
		"text": true, "inlineData": true, "functionCall": true,
		"functionResponse": true, "fileData": true,
	}
	for k, v := range raw {
		if !known[k] {
			p.Custom[k] = v
		}
	}

	return nil
}
