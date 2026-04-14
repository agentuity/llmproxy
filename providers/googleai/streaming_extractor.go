package googleai

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

	for scanner.Scan() {
		line := scanner.Bytes()

		if len(line) == 0 {
			continue
		}

		if bytes.HasPrefix(line, []byte("data: ")) {
			data := bytes.TrimPrefix(line, []byte("data: "))
			data = bytes.TrimSpace(data)

			// Forward the raw line immediately
			if _, err := w.Write(line); err != nil {
				return meta, err
			}
			if _, err := w.Write([]byte("\n\n")); err != nil {
				return meta, err
			}
			_ = rc.Flush()

			if len(data) == 0 {
				continue
			}

			// Parse for metadata extraction
			var chunk Response
			if err := json.Unmarshal(data, &chunk); err != nil {
				continue
			}

			// Extract model
			if chunk.ModelName != "" {
				meta.Model = chunk.ModelName
			}

			// Extract usage (typically in each chunk, final values in last chunk)
			if chunk.UsageMetadata.TotalTokenCount > 0 {
				meta.Usage = llmproxy.Usage{
					PromptTokens:     chunk.UsageMetadata.PromptTokenCount,
					CompletionTokens: chunk.UsageMetadata.CandidatesTokenCount,
					TotalTokens:      chunk.UsageMetadata.TotalTokenCount,
				}
			}

			// Extract text from candidates
			for i, candidate := range chunk.Candidates {
				if len(meta.Choices) <= i {
					meta.Choices = append(meta.Choices, llmproxy.Choice{
						Index: i,
						Message: &llmproxy.Message{
							Role: "assistant",
						},
					})
				}
				if candidate.Content != nil {
					text := extractTextFromParts(candidate.Content.Parts)
					meta.Choices[i].Message.Content += text
				}
				if candidate.FinishReason != "" {
					meta.Choices[i].FinishReason = mapFinishReason(candidate.FinishReason)
				}
			}

			// Extract prompt feedback
			if chunk.PromptFeedback != nil {
				meta.Custom["prompt_feedback"] = chunk.PromptFeedback
			}
		} else {
			// Forward non-data lines (e.g., comments, event types)
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

	return meta, nil
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
