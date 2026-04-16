package openai_compatible

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	var raw map[string]any
	isResponsesAPI := false
	if err := json.Unmarshal(body, &raw); err == nil {
		if _, hasOutput := raw["output"]; hasOutput {
			if _, hasChoices := raw["choices"]; !hasChoices {
				isResponsesAPI = true
			}
		}
	}

	resp.Body = io.NopCloser(bytes.NewReader(body))

	if isResponsesAPI {
		return e.responsesExtractor.Extract(resp)
	}
	return e.chatCompletionsExtractor.Extract(resp)
}

type StreamingMultiAPIExtractor struct {
	*MultiAPIExtractor
	chatCompletionsStreaming *StreamingExtractor
	responsesStreaming       *ResponsesStreamingExtractor
}

func NewStreamingMultiAPIExtractor() *StreamingMultiAPIExtractor {
	return &StreamingMultiAPIExtractor{
		MultiAPIExtractor:        NewMultiAPIExtractor(),
		chatCompletionsStreaming: NewStreamingExtractor(),
		responsesStreaming:       NewResponsesStreamingExtractor(),
	}
}

func (e *StreamingMultiAPIExtractor) IsStreamingResponse(resp *http.Response) bool {
	return llmproxy.IsSSEStream(resp.Header.Get("Content-Type"))
}

func (e *StreamingMultiAPIExtractor) ExtractStreamingWithController(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (llmproxy.ResponseMetadata, error) {
	if !e.IsStreamingResponse(resp) {
		return e.extractNonStreamingWithController(resp, w, rc)
	}

	if resp.Request != nil {
		metaCtx := llmproxy.GetMetaFromContext(resp.Request.Context())
		if apiType, ok := metaCtx.Meta.Custom["api_type"].(llmproxy.APIType); ok && apiType == llmproxy.APITypeResponses {
			return e.responsesStreaming.ExtractStreamingWithController(resp, w, rc)
		}
	}

	return e.chatCompletionsStreaming.ExtractStreamingWithController(resp, w, rc)
}

func (e *StreamingMultiAPIExtractor) extractNonStreamingWithController(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (llmproxy.ResponseMetadata, error) {
	var buf bytes.Buffer
	tee := io.TeeReader(resp.Body, &buf)

	meta, _, err := e.MultiAPIExtractor.Extract(&http.Response{
		StatusCode: resp.StatusCode,
		Header:     resp.Header,
		Body:       io.NopCloser(tee),
	})
	if err != nil {
		return meta, err
	}

	readBuf := make([]byte, 1024*512)
	for {
		n, err := buf.Read(readBuf)
		if err != nil {
			if err == io.EOF {
				if n > 0 {
					if _, writeErr := w.Write(readBuf[:n]); writeErr != nil {
						return meta, writeErr
					}
				}
				break
			}
			if errors.Is(err, context.Canceled) {
				break
			}
			return meta, err
		}
		if n == 0 {
			break
		}
		if _, writeErr := w.Write(readBuf[:n]); writeErr != nil {
			return meta, writeErr
		}
		if flushErr := rc.Flush(); flushErr != nil {
			return meta, flushErr
		}
	}

	return meta, nil
}
