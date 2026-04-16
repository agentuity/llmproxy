package openai_compatible

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/agentuity/llmproxy"
)

type ResponsesStreamingExtractor struct {
	*ResponsesExtractor
}

func NewResponsesStreamingExtractor() *ResponsesStreamingExtractor {
	return &ResponsesStreamingExtractor{
		ResponsesExtractor: NewResponsesExtractor(),
	}
}

func (e *ResponsesStreamingExtractor) IsStreamingResponse(resp *http.Response) bool {
	return llmproxy.IsSSEStream(resp.Header.Get("Content-Type"))
}

func (e *ResponsesStreamingExtractor) ExtractStreamingWithController(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (llmproxy.ResponseMetadata, error) {
	if !e.IsStreamingResponse(resp) {
		return e.extractNonStreamingWithController(resp, w, rc)
	}

	return e.extractResponsesStreamingWithController(resp, w, rc)
}

func (e *ResponsesStreamingExtractor) extractNonStreamingWithController(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (llmproxy.ResponseMetadata, error) {
	var buf bytes.Buffer
	tee := io.TeeReader(resp.Body, &buf)

	meta, _, err := e.ResponsesExtractor.Extract(&http.Response{
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

func (e *ResponsesStreamingExtractor) extractResponsesStreamingWithController(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (llmproxy.ResponseMetadata, error) {
	meta := llmproxy.ResponseMetadata{
		Choices: make([]llmproxy.Choice, 0),
		Custom:  map[string]any{"api_type": llmproxy.APITypeResponses},
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var accumulatedUsage *llmproxy.StreamingUsage

	for scanner.Scan() {
		line := scanner.Bytes()

		if _, err := w.Write(line); err != nil {
			return meta, err
		}
		if _, err := w.Write([]byte("\n")); err != nil {
			return meta, err
		}
		_ = rc.Flush()

		if len(line) == 0 {
			continue
		}

		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}

		data := bytes.TrimPrefix(line, []byte("data:"))
		data = bytes.TrimSpace(data)

		if bytes.Equal(data, []byte("[DONE]")) {
			continue
		}

		event, err := llmproxy.ParseResponsesSSEEvent(data)
		if err != nil {
			if errors.Is(err, llmproxy.ErrStreamComplete) {
				continue
			}
			continue
		}
		if event == nil {
			continue
		}

		if len(event.Response) > 0 {
			var response llmproxy.ResponsesStreamResponse
			if err := json.Unmarshal(event.Response, &response); err == nil {
				if response.ID != "" {
					meta.ID = response.ID
				}
				if response.Model != "" {
					meta.Model = response.Model
				}
				if response.Object != "" {
					meta.Object = response.Object
				}
				if response.Status != "" {
					meta.Custom["status"] = response.Status
				}
				if response.Usage != nil && response.Usage.OutputTokensDetails != nil && response.Usage.OutputTokensDetails.ReasoningTokens > 0 {
					meta.Custom["reasoning_tokens"] = response.Usage.OutputTokensDetails.ReasoningTokens
				}
			}
		}

		usage := llmproxy.ExtractUsageFromResponsesEvent(event)
		if usage != nil {
			accumulatedUsage = usage
		}
	}

	if err := scanner.Err(); err != nil {
		return meta, err
	}

	if accumulatedUsage != nil {
		meta.Usage = llmproxy.Usage{
			PromptTokens:     accumulatedUsage.PromptTokens,
			CompletionTokens: accumulatedUsage.CompletionTokens,
			TotalTokens:      accumulatedUsage.TotalTokens,
		}
		if accumulatedUsage.CacheUsage != nil {
			meta.Custom["cache_usage"] = *accumulatedUsage.CacheUsage
		}
		if accumulatedUsage.ReasoningTokens > 0 {
			meta.Custom["reasoning_tokens"] = accumulatedUsage.ReasoningTokens
		}
	}

	return meta, nil
}
