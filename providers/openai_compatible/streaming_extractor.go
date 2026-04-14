package openai_compatible

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/agentuity/llmproxy"
)

type StreamingExtractor struct {
	*Extractor
}

func NewStreamingExtractor() *StreamingExtractor {
	return &StreamingExtractor{
		Extractor: NewExtractor(),
	}
}

func (e *StreamingExtractor) IsStreamingResponse(resp *http.Response) bool {
	return llmproxy.IsSSEStream(resp.Header.Get("Content-Type"))
}

func (e *StreamingExtractor) ExtractStreamingWithController(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (llmproxy.ResponseMetadata, error) {
	if !e.IsStreamingResponse(resp) {
		return e.extractNonStreamingWithController(resp, w, rc)
	}

	return e.extractStreamingWithController(resp, w, rc)
}

func (e *StreamingExtractor) extractNonStreamingWithController(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (llmproxy.ResponseMetadata, error) {
	var buf bytes.Buffer
	tee := io.TeeReader(resp.Body, &buf)

	meta, _, err := e.Extractor.Extract(&http.Response{
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

func (e *StreamingExtractor) extractStreamingWithController(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (llmproxy.ResponseMetadata, error) {
	meta := llmproxy.ResponseMetadata{
		Choices: make([]llmproxy.Choice, 0),
		Custom:  make(map[string]any),
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var lastChunk *llmproxy.OpenAIStreamChunk
	var accumulatedUsage *llmproxy.StreamingUsage

	for scanner.Scan() {
		line := scanner.Bytes()

		if len(line) == 0 {
			continue
		}

		if bytes.HasPrefix(line, []byte("data: ")) {
			data := bytes.TrimPrefix(line, []byte("data: "))
			data = bytes.TrimSpace(data)

			if bytes.Equal(data, []byte("[DONE]")) {
				if _, err := w.Write([]byte("data: [DONE]\n\n")); err != nil {
					return meta, err
				}
				_ = rc.Flush()
				break
			}

			// Forward the raw data regardless of parsing success
			if _, err := w.Write(line); err != nil {
				return meta, err
			}
			if _, err := w.Write([]byte("\n\n")); err != nil {
				return meta, err
			}
			_ = rc.Flush()

			chunk, err := llmproxy.ParseOpenAISSEEvent(data)
			if err != nil {
				continue
			}

			if chunk == nil {
				continue
			}

			lastChunk = chunk

			if chunk.ID != "" {
				meta.ID = chunk.ID
			}
			if chunk.Model != "" {
				meta.Model = chunk.Model
			}
			if chunk.Object != "" {
				meta.Object = chunk.Object
			}

			if chunk.Usage != nil {
				usage := llmproxy.ExtractUsageFromOpenAIChunk(chunk)
				if usage != nil {
					accumulatedUsage = usage
				}
			}
		} else {
			if _, err := w.Write(line); err != nil {
				return meta, err
			}
			if _, err := w.Write([]byte("\n")); err != nil {
				return meta, err
			}
			_ = rc.Flush()
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
	} else if lastChunk != nil {
		for _, choice := range lastChunk.Choices {
			c := llmproxy.Choice{
				Index:        choice.Index,
				FinishReason: choice.FinishReason,
			}
			if choice.Delta != nil {
				c.Delta = &llmproxy.Message{
					Role:    choice.Delta.Role,
					Content: choice.Delta.Content,
				}
			}
			meta.Choices = append(meta.Choices, c)
		}
	}

	return meta, nil
}
