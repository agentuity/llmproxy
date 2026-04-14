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
		return p.responsesParser.Parse(io.NopCloser(NewBytesReader(data)))
	default:
		return p.chatCompletionsParser.Parse(io.NopCloser(NewBytesReader(data)))
	}
}

type byteReader struct {
	data []byte
	pos  int
}

func NewBytesReader(data []byte) *byteReader {
	return &byteReader{data: data}
}

func (r *byteReader) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

func (r *byteReader) Close() error {
	return nil
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

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err == nil {
		if _, hasOutput := raw["output"]; hasOutput {
			if _, hasChoices := raw["choices"]; !hasChoices {
				resp.Body = io.NopCloser(bytes.NewReader(body))
				return e.responsesExtractor.Extract(resp)
			}
		}
	}

	resp.Body = io.NopCloser(bytes.NewReader(body))
	return e.chatCompletionsExtractor.Extract(resp)
}
