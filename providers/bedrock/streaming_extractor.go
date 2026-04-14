package bedrock

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

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
	ct := resp.Header.Get("Content-Type")
	return llmproxy.IsSSEStream(ct) || isEventStream(ct)
}

func isEventStream(contentType string) bool {
	return strings.Contains(contentType, "vnd.amazon.eventstream")
}

func (e *StreamingExtractor) ExtractStreamingWithController(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (llmproxy.ResponseMetadata, error) {
	ct := resp.Header.Get("Content-Type")
	if isEventStream(ct) {
		return e.extractEventStreamWithController(resp, w, rc)
	}
	return e.extractNonStreamingWithController(resp, w, rc)
}

// eventStreamEvent represents a parsed AWS event stream event.
type eventStreamEvent struct {
	EventType string
	Payload   []byte
}

func (e *StreamingExtractor) extractEventStreamWithController(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (llmproxy.ResponseMetadata, error) {
	meta := llmproxy.ResponseMetadata{
		Choices: make([]llmproxy.Choice, 0, 1),
		Custom:  make(map[string]any),
	}

	for {
		event, rawBytes, err := readEventStreamMessage(resp.Body)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				// Write any partial bytes we managed to read
				if len(rawBytes) > 0 {
					_, _ = w.Write(rawBytes)
					_ = rc.Flush()
				}
				break
			}
			if errors.Is(err, context.Canceled) {
				break
			}
			return meta, err
		}

		// Forward raw bytes immediately
		if _, writeErr := w.Write(rawBytes); writeErr != nil {
			return meta, writeErr
		}
		_ = rc.Flush()

		// Process event for metadata extraction
		if event != nil {
			processBedrockStreamEvent(event, &meta)
		}
	}

	return meta, nil
}

// readEventStreamMessage reads a single AWS event stream message from the reader.
// AWS event stream format:
//
//	[4 bytes: total length][4 bytes: headers length][4 bytes: prelude CRC]
//	[headers...][payload...][4 bytes: message CRC]
func readEventStreamMessage(r io.Reader) (*eventStreamEvent, []byte, error) {
	// Read the 12-byte prelude
	prelude := make([]byte, 12)
	if _, err := io.ReadFull(r, prelude); err != nil {
		return nil, nil, err
	}

	totalLen := binary.BigEndian.Uint32(prelude[0:4])
	headersLen := binary.BigEndian.Uint32(prelude[4:8])

	// Sanity check: minimum message is 16 bytes (12 prelude + 4 message CRC),
	// maximum is 16MB to prevent memory issues
	if totalLen < 16 || totalLen > 16*1024*1024 {
		return nil, prelude, fmt.Errorf("invalid event stream message length: %d", totalLen)
	}

	// Read remaining bytes (total - 12 bytes of prelude already read)
	remaining := make([]byte, totalLen-12)
	if _, err := io.ReadFull(r, remaining); err != nil {
		// Return prelude bytes so they can still be forwarded
		return nil, prelude, err
	}

	// Reconstruct full raw message for forwarding
	rawBytes := make([]byte, totalLen)
	copy(rawBytes, prelude)
	copy(rawBytes[12:], remaining)

	// Parse headers
	headers := parseEventStreamHeaders(remaining[:headersLen])

	// Extract payload (between headers and message CRC)
	payloadLen := totalLen - 12 - headersLen - 4
	var payload []byte
	if payloadLen > 0 {
		payload = remaining[headersLen : headersLen+payloadLen]
	}

	return &eventStreamEvent{
		EventType: headers[":event-type"],
		Payload:   payload,
	}, rawBytes, nil
}

// parseEventStreamHeaders parses AWS event stream binary headers.
// Header format: [1 byte: name length][name][1 byte: type][value...]
// Type 7 (string): [2 bytes: value length][value]
func parseEventStreamHeaders(data []byte) map[string]string {
	headers := make(map[string]string)
	offset := uint32(0)
	dataLen := uint32(len(data))

	for offset < dataLen {
		// Read header name length
		nameLen := uint32(data[offset])
		offset++
		if offset+nameLen > dataLen {
			break
		}

		// Read header name
		name := string(data[offset : offset+nameLen])
		offset += nameLen
		if offset >= dataLen {
			break
		}

		// Read header type
		headerType := data[offset]
		offset++

		// Skip value based on type
		switch headerType {
		case 0, 1: // bool_true, bool_false - no value bytes
		case 2: // byte
			offset++
		case 3: // short
			offset += 2
		case 4: // int
			offset += 4
		case 5, 8: // long, timestamp
			offset += 8
		case 6, 7: // bytes, string - 2-byte length prefix + value
			if offset+2 > dataLen {
				return headers
			}
			valueLen := uint32(binary.BigEndian.Uint16(data[offset : offset+2]))
			offset += 2
			if offset+valueLen > dataLen {
				return headers
			}
			if headerType == 7 {
				headers[name] = string(data[offset : offset+valueLen])
			}
			offset += valueLen
		case 9: // uuid
			offset += 16
		default:
			return headers // unknown type, bail
		}
	}

	return headers
}

// Bedrock stream event payload types

type bedrockStreamStart struct {
	Role string `json:"role"`
}

type bedrockStreamDelta struct {
	ContentBlockIndex int `json:"contentBlockIndex"`
	Delta             struct {
		Text string `json:"text,omitempty"`
	} `json:"delta"`
}

type bedrockStreamStop struct {
	StopReason string `json:"stopReason"`
}

type bedrockStreamMetadata struct {
	Usage   ResponseUsage    `json:"usage"`
	Metrics *ResponseMetrics `json:"metrics,omitempty"`
}

func processBedrockStreamEvent(event *eventStreamEvent, meta *llmproxy.ResponseMetadata) {
	if len(event.Payload) == 0 {
		return
	}

	switch event.EventType {
	case "messageStart":
		var start bedrockStreamStart
		if json.Unmarshal(event.Payload, &start) == nil {
			role := start.Role
			if role == "" {
				role = "assistant"
			}
			if len(meta.Choices) == 0 {
				meta.Choices = append(meta.Choices, llmproxy.Choice{
					Index: 0,
					Message: &llmproxy.Message{
						Role: role,
					},
				})
			}
		}

	case "contentBlockDelta":
		var delta bedrockStreamDelta
		if json.Unmarshal(event.Payload, &delta) == nil {
			if len(meta.Choices) == 0 {
				meta.Choices = append(meta.Choices, llmproxy.Choice{
					Index: 0,
					Message: &llmproxy.Message{
						Role: "assistant",
					},
				})
			}
			if meta.Choices[0].Message != nil {
				meta.Choices[0].Message.Content += delta.Delta.Text
			}
		}

	case "messageStop":
		var stop bedrockStreamStop
		if json.Unmarshal(event.Payload, &stop) == nil {
			if len(meta.Choices) > 0 {
				meta.Choices[0].FinishReason = stop.StopReason
			}
		}

	case "metadata":
		var metadata bedrockStreamMetadata
		if json.Unmarshal(event.Payload, &metadata) == nil {
			meta.Usage = llmproxy.Usage{
				PromptTokens:     metadata.Usage.InputTokens,
				CompletionTokens: metadata.Usage.OutputTokens,
				TotalTokens:      metadata.Usage.TotalTokens,
			}
			if metadata.Metrics != nil {
				meta.Custom["latency_ms"] = metadata.Metrics.LatencyMs
			}
			if metadata.Usage.CacheReadInputTokens > 0 || metadata.Usage.CacheWriteInputTokens > 0 {
				cacheDetails := extractCacheDetails(metadata.Usage.CacheDetails)
				meta.Custom["cache_usage"] = llmproxy.CacheUsage{
					CachedTokens:     metadata.Usage.CacheReadInputTokens,
					CacheWriteTokens: metadata.Usage.CacheWriteInputTokens,
					CacheDetails:     cacheDetails,
				}
			}
		}
	}
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
