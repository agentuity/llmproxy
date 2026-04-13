// Package bedrock provides a provider implementation for AWS Bedrock.
//
// AWS Bedrock provides access to foundation models from various providers
// (Anthropic, AI21, Cohere, Amazon, Meta, etc.) through a unified API.
//
// Key differences from OpenAI:
//   - Endpoint: https://bedrock-runtime.{region}.amazonaws.com/model/{modelId}/invoke
//   - Auth: AWS Signature V4 (requires AWS credentials)
//   - Uses Converse API for unified request format across models
//   - Model IDs include provider prefix (e.g., "anthropic.claude-3-sonnet-20240229-v1:0")
//
// Basic usage:
//
//	provider, _ := bedrock.New("us-east-1", "AKIA...", "secret...", "")
//	proxy := llmproxy.NewProxy(provider)
package bedrock

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/agentuity/llmproxy"
)

// Parser implements llmproxy.BodyParser for Bedrock's Converse API format.
type Parser struct{}

// Parse reads a Bedrock Converse API request and extracts metadata.
// Bedrock uses a unified format that works across different model providers.
func (p *Parser) Parse(body io.ReadCloser) (llmproxy.BodyMetadata, []byte, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return llmproxy.BodyMetadata{}, nil, err
	}
	body.Close()

	var req ConverseRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return llmproxy.BodyMetadata{}, nil, err
	}

	meta := llmproxy.BodyMetadata{
		Model:     req.ModelID,
		Messages:  make([]llmproxy.Message, 0, len(req.Messages)),
		MaxTokens: req.InferenceConfig.MaxTokens,
		Custom:    make(map[string]any),
	}

	for _, msg := range req.Messages {
		text := extractContentText(msg.Content)
		meta.Messages = append(meta.Messages, llmproxy.Message{
			Role:    msg.Role,
			Content: text,
		})
	}

	if req.System != nil {
		systemText := extractSystemText(req.System)
		meta.Custom["system"] = systemText
	}

	return meta, data, nil
}

func extractContentText(content []ContentBlock) string {
	var text string
	for _, block := range content {
		if block.Text != "" {
			text += block.Text
		}
	}
	return text
}

func extractSystemText(system []SystemBlock) string {
	var text string
	for _, block := range system {
		if block.Text != "" {
			text += block.Text + " "
		}
	}
	return text
}

// ConverseRequest represents a Bedrock Converse API request.
type ConverseRequest struct {
	ModelID         string          `json:"modelId,omitempty"`
	Messages        []Message       `json:"messages"`
	System          []SystemBlock   `json:"system,omitempty"`
	InferenceConfig InferenceConfig `json:"inferenceConfig,omitempty"`
	ToolConfig      *ToolConfig     `json:"toolConfig,omitempty"`
	Custom          map[string]any  `json:"-"`
}

// Message represents a message in a Converse request.
type Message struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

// ContentBlock represents a content block (text, image, tool use, etc.).
type ContentBlock struct {
	Text       string       `json:"text,omitempty"`
	Image      *ImageSource `json:"image,omitempty"`
	ToolUse    *ToolUse     `json:"toolUse,omitempty"`
	ToolResult *ToolResult  `json:"toolResult,omitempty"`
	CachePoint *CachePoint  `json:"cachePoint,omitempty"`
}

// CachePoint represents a cache checkpoint for prompt caching.
type CachePoint struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

// ImageSource represents an image in a content block.
type ImageSource struct {
	Format string          `json:"format"`
	Source ImageSourceData `json:"source"`
}

// ImageSourceData contains the image data.
type ImageSourceData struct {
	Bytes      []byte      `json:"bytes,omitempty"`
	S3Location *S3Location `json:"s3Location,omitempty"`
}

// S3Location represents an S3 location for image data.
type S3Location struct {
	URI string `json:"uri"`
}

// ToolUse represents a tool use request.
type ToolUse struct {
	ToolUseID string `json:"toolUseId"`
	Name      string `json:"name"`
	Input     any    `json:"input"`
}

// ToolResult represents a tool execution result.
type ToolResult struct {
	ToolUseID string         `json:"toolUseId"`
	Content   []ContentBlock `json:"content"`
	Status    string         `json:"status,omitempty"`
}

// SystemBlock represents a system message block.
type SystemBlock struct {
	Text       string      `json:"text"`
	CachePoint *CachePoint `json:"cachePoint,omitempty"`
}

// InferenceConfig contains inference parameters.
type InferenceConfig struct {
	MaxTokens     int      `json:"maxTokens,omitempty"`
	Temperature   float64  `json:"temperature,omitempty"`
	TopP          float64  `json:"topP,omitempty"`
	StopSequences []string `json:"stopSequences,omitempty"`
}

// ToolConfig contains tool configuration.
type ToolConfig struct {
	Tools      []Tool `json:"tools"`
	ToolChoice any    `json:"toolChoice,omitempty"`
}

// Tool represents a tool definition.
type Tool struct {
	ToolSpec   *ToolSpec   `json:"toolSpec,omitempty"`
	CachePoint *CachePoint `json:"cachePoint,omitempty"`
}

// ToolSpec contains tool specification.
type ToolSpec struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"inputSchema"`
}

// UnmarshalJSON captures unknown fields into Custom.
func (r *ConverseRequest) UnmarshalJSON(data []byte) error {
	type Alias ConverseRequest
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(r),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	r.Custom = make(map[string]any)
	known := map[string]bool{
		"modelId": true, "messages": true, "system": true,
		"inferenceConfig": true, "toolConfig": true,
	}
	for k, v := range raw {
		if !known[k] {
			r.Custom[k] = v
		}
	}

	return nil
}

// ParseConverseRequest parses raw bytes as a Converse request.
func ParseConverseRequest(data []byte) (llmproxy.BodyMetadata, error) {
	meta, _, err := (&Parser{}).Parse(io.NopCloser(bytes.NewReader(data)))
	return meta, err
}
