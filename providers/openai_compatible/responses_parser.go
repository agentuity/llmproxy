package openai_compatible

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/agentuity/llmproxy"
)

type ResponsesParser struct{}

func (p *ResponsesParser) Parse(body io.ReadCloser) (llmproxy.BodyMetadata, []byte, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return llmproxy.BodyMetadata{}, nil, err
	}
	body.Close()

	var req ResponsesRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return llmproxy.BodyMetadata{}, nil, err
	}

	meta := llmproxy.BodyMetadata{
		Model:     req.Model,
		MaxTokens: req.MaxOutputTokens,
		Stream:    req.Stream,
		Custom:    make(map[string]any),
	}

	if req.Input != nil {
		switch v := req.Input.(type) {
		case string:
			meta.Messages = []llmproxy.Message{{Role: "user", Content: v}}
		case []interface{}:
			msgs := make([]llmproxy.Message, 0, len(v))
			for _, item := range v {
				if m, ok := item.(map[string]interface{}); ok {
					role, hasRole := m["role"].(string)
					content, hasContent := m["content"].(string)
					// Only append if both role and content are present and non-empty
					if hasRole && hasContent && role != "" && content != "" {
						msgs = append(msgs, llmproxy.Message{Role: role, Content: content})
					}
				}
			}
			meta.Messages = msgs
		}
	}

	for k, v := range req.Custom {
		meta.Custom[k] = v
	}

	meta.Custom["api_type"] = llmproxy.APITypeResponses
	if len(req.Instructions) > 0 {
		meta.Custom["instructions"] = req.Instructions
	}
	if len(req.Tools) > 0 {
		meta.Custom["tools"] = req.Tools
	}

	return meta, data, nil
}

type ResponsesRequest struct {
	Model           string                 `json:"model"`
	Input           interface{}            `json:"input,omitempty"`
	Instructions    string                 `json:"instructions,omitempty"`
	MaxOutputTokens int                    `json:"max_output_tokens,omitempty"`
	Temperature     *float64               `json:"temperature,omitempty"`
	TopP            *float64               `json:"top_p,omitempty"`
	Stream          bool                   `json:"stream"`
	Tools           []interface{}          `json:"tools,omitempty"`
	Truncation      string                 `json:"truncation,omitempty"`
	Custom          map[string]interface{} `json:"-"`
}

func (r *ResponsesRequest) UnmarshalJSON(data []byte) error {
	type Alias ResponsesRequest
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
		"model": true, "input": true, "instructions": true,
		"max_output_tokens": true, "temperature": true, "top_p": true,
		"stream": true, "tools": true, "truncation": true,
		"user": true, "metadata": true, "parallel_tool_calls": true,
		"previous_response_id": true, "response_format": true,
		"seed": true, "service_tier": true, "store": true,
	}
	for k, v := range raw {
		if !known[k] {
			r.Custom[k] = v
		}
	}

	return nil
}

func ParseResponsesRequest(body io.ReadCloser) (llmproxy.BodyMetadata, []byte, error) {
	return (&ResponsesParser{}).Parse(body)
}

func ParseResponsesRequestBody(data []byte) (llmproxy.BodyMetadata, error) {
	meta, _, err := (&ResponsesParser{}).Parse(io.NopCloser(bytes.NewReader(data)))
	return meta, err
}
