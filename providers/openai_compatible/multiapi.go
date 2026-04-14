package openai_compatible

import (
	"bytes"
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

	// Use same detection logic as parser for consistency
	apiType := llmproxy.DetectAPIType(body)
	switch apiType {
	case llmproxy.APITypeResponses:
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return e.responsesExtractor.Extract(resp)
	default:
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return e.chatCompletionsExtractor.Extract(resp)
	}
}
