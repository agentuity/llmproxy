package anthropic

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

	var accumulatedUsage *llmproxy.StreamingUsage
	var messageStart *llmproxy.AnthropicStreamMessage

	for scanner.Scan() {
		line := scanner.Bytes()

		if len(line) == 0 {
			continue
		}

		if bytes.HasPrefix(line, []byte("data: ")) {
			data := bytes.TrimPrefix(line, []byte("data: "))
			data = bytes.TrimSpace(data)

			event, err := llmproxy.ParseAnthropicSSEEvent(data)
			if err != nil {
				continue
			}

			if event == nil {
				continue
			}

			switch event.Type {
			case "message_start":
				if event.Message != nil {
					messageStart = event.Message
					meta.ID = event.Message.ID
					meta.Model = event.Message.Model
					if event.Message.Usage != nil {
						usage := &llmproxy.StreamingUsage{
							PromptTokens: event.Message.Usage.InputTokens,
						}
						if event.Message.Usage.CacheCreationInputTokens > 0 || event.Message.Usage.CacheReadInputTokens > 0 {
							usage.CacheUsage = &llmproxy.CacheUsage{
								CacheCreationInputTokens: event.Message.Usage.CacheCreationInputTokens,
								CacheReadInputTokens:     event.Message.Usage.CacheReadInputTokens,
							}
						}
						accumulatedUsage = usage
					}
				}
			case "content_block_start":
				if event.ContentBlock != nil && event.Index == 0 {
					meta.Choices = append(meta.Choices, llmproxy.Choice{
						Index: 0,
						Message: &llmproxy.Message{
							Role: "assistant",
						},
					})
				}
			case "content_block_delta":
				if event.Delta != nil && event.Delta.Type == "text_delta" {
					if len(meta.Choices) > 0 {
						if meta.Choices[0].Message == nil {
							meta.Choices[0].Message = &llmproxy.Message{Role: "assistant"}
						}
					}
				}
			case "message_delta":
				if event.Usage != nil {
					if accumulatedUsage == nil {
						accumulatedUsage = &llmproxy.StreamingUsage{}
					}
					accumulatedUsage.CompletionTokens = event.Usage.OutputTokens
					if event.Usage.CacheReadInputTokens > 0 || event.Usage.CacheCreationInputTokens > 0 {
						if accumulatedUsage.CacheUsage == nil {
							accumulatedUsage.CacheUsage = &llmproxy.CacheUsage{}
						}
						accumulatedUsage.CacheUsage.CacheReadInputTokens = event.Usage.CacheReadInputTokens
						accumulatedUsage.CacheUsage.CacheCreationInputTokens = event.Usage.CacheCreationInputTokens
					}
				}
				if event.Delta != nil && event.Delta.StopReason != "" {
					if len(meta.Choices) > 0 {
						meta.Choices[0].FinishReason = event.Delta.StopReason
					}
				}
			case "message_stop":
			}

			if _, err := w.Write(line); err != nil {
				return meta, err
			}
			if _, err := w.Write([]byte("\n\n")); err != nil {
				return meta, err
			}
			_ = rc.Flush()
		} else if bytes.HasPrefix(line, []byte("event: ")) {
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
			TotalTokens:      accumulatedUsage.PromptTokens + accumulatedUsage.CompletionTokens,
		}
		if accumulatedUsage.CacheUsage != nil {
			meta.Custom["cache_usage"] = *accumulatedUsage.CacheUsage
		}
	}

	if messageStart != nil {
		if meta.ID == "" {
			meta.ID = messageStart.ID
		}
		if meta.Model == "" {
			meta.Model = messageStart.Model
		}
	}

	return meta, nil
}
