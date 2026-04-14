package openai_compatible

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/agentuity/llmproxy"
)

type MultiAPIParser struct {
	chatCompletionsParser *Parser
	responsesParser       *ResponsesParser
}

func NewMultiAPIParser() *MultiAPIParser {
	return &MultiAPIParser{
		chatCompletionsParser: &Parser{},
		responsesParser:       &ResponsesParser{},
	}
}

func (p *MultiAPIParser) Parse(body io.ReadCloser) (llmproxy.BodyMetadata, []byte, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return llmproxy.BodyMetadata{}, nil, err
	}
	body.Close()

	apiType := llmproxy.DetectAPIType(data)
	switch apiType {
	case llmproxy.APITypeResponses:
		return p.responsesParser.Parse(io.NopCloser(bytes.NewReader(data)))
	default:
		return p.chatCompletionsParser.Parse(io.NopCloser(bytes.NewReader(data)))
	}
}

type MultiAPIExtractor struct {
	chatCompletionsExtractor *Extractor
	responsesExtractor       *ResponsesExtractor
}

func NewMultiAPIExtractor() *MultiAPIExtractor {
	return &MultiAPIExtractor{
		chatCompletionsExtractor: &Extractor{},
		responsesExtractor:       &ResponsesExtractor{},
	}
}

func (e *MultiAPIExtractor) Extract(resp *http.Response) (llmproxy.ResponseMetadata, []byte, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return llmproxy.ResponseMetadata{}, nil, err
	}
	resp.Body.Close()

	// Detect response type by inspecting response-specific fields
	// Responses API has "output" and "status", Chat Completions has "choices"
	var raw map[string]any
	isResponsesAPI := false
	if err := json.Unmarshal(body, &raw); err == nil {
		if _, hasOutput := raw["output"]; hasOutput {
			if _, hasChoices := raw["choices"]; !hasChoices {
				isResponsesAPI = true
			}
		}
	}

	// Restore body for downstream extractors
	resp.Body = io.NopCloser(bytes.NewReader(body))

	if isResponsesAPI {
		return e.responsesExtractor.Extract(resp)
	}
	return e.chatCompletionsExtractor.Extract(resp)
}
