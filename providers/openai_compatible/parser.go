// Package openai_compatible provides a reusable implementation for LLM providers
// that use OpenAI-compatible APIs.
//
// This includes providers like OpenAI, Groq, Together AI, Fireworks AI, and others
// that implement the OpenAI chat completions API format.
//
// Basic usage:
//
//	provider, _ := openai_compatible.New("groq", "gsk_xxx", "https://api.groq.com")
//	proxy := llmproxy.NewProxy(provider)
package openai_compatible

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/agentuity/llmproxy"
)

// Parser implements llmproxy.BodyParser for OpenAI-compatible request formats.
// It extracts model, messages, and other fields into a unified BodyMetadata structure.
type Parser struct{}

// Parse reads an OpenAI-compatible request body and extracts metadata.
// It returns both the parsed metadata and the raw body bytes for later use.
//
// The parser handles standard OpenAI fields and captures unknown fields in
// the Custom map for provider-specific extensions.
func (p *Parser) Parse(body io.ReadCloser) (llmproxy.BodyMetadata, []byte, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return llmproxy.BodyMetadata{}, nil, err
	}
	body.Close()

	var req OpenAIRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return llmproxy.BodyMetadata{}, nil, err
	}

	meta := llmproxy.BodyMetadata{
		Model:     req.Model,
		Messages:  req.Messages,
		MaxTokens: req.MaxTokens,
		Stream:    req.Stream,
		Custom:    make(map[string]any),
	}

	for k, v := range req.Custom {
		meta.Custom[k] = v
	}

	return meta, data, nil
}

// OpenAIRequest represents an OpenAI-compatible chat completion request.
// It includes standard fields and captures custom fields for provider extensions.
type OpenAIRequest struct {
	// Model is the model identifier (e.g., "gpt-4", "llama-2-70b").
	Model string `json:"model"`
	// Messages is the conversation history.
	Messages []llmproxy.Message `json:"messages"`
	// MaxTokens limits the generation length.
	MaxTokens int `json:"max_tokens,omitempty"`
	// Stream enables streaming responses.
	Stream bool `json:"stream"`
	// Custom holds provider-specific parameters not in the standard schema.
	Custom map[string]interface{} `json:"-"`
}

// UnmarshalJSON implements custom JSON unmarshaling to capture unknown fields.
func (r *OpenAIRequest) UnmarshalJSON(data []byte) error {
	type Alias OpenAIRequest
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
		"stream": true, "temperature": true, "top_p": true,
		"n": true, "stop": true, "presence_penalty": true,
		"frequency_penalty": true, "logit_bias": true, "user": true,
	}
	for k, v := range raw {
		if !known[k] {
			r.Custom[k] = v
		}
	}

	return nil
}

// ParseOpenAIRequest is a convenience function that parses an OpenAI-compatible
// request body and returns the metadata and raw bytes.
func ParseOpenAIRequest(body io.ReadCloser) (llmproxy.BodyMetadata, []byte, error) {
	return (&Parser{}).Parse(body)
}

// ParseOpenAIRequestBody parses raw JSON bytes as an OpenAI-compatible request.
// It returns only the metadata, not the raw bytes.
func ParseOpenAIRequestBody(data []byte) (llmproxy.BodyMetadata, error) {
	meta, _, err := (&Parser{}).Parse(io.NopCloser(bytes.NewReader(data)))
	return meta, err
}
