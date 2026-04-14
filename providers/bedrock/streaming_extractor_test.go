package bedrock

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentuity/llmproxy"
)

// threadSafeResponseWriter is an http.ResponseWriter that is safe for concurrent access.
// It signals via a channel when the first write occurs.
type threadSafeResponseWriter struct {
	mu         sync.Mutex
	buf        bytes.Buffer
	header     http.Header
	wroteHead  bool
	firstWrite chan struct{}
	closed     atomic.Bool
}

func newThreadSafeResponseWriter() *threadSafeResponseWriter {
	return &threadSafeResponseWriter{
		header:     make(http.Header),
		firstWrite: make(chan struct{}),
	}
}

func (w *threadSafeResponseWriter) Header() http.Header {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.header
}

func (w *threadSafeResponseWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	wrote := w.wroteHead
	if !wrote {
		w.wroteHead = true
	}
	n, err := w.buf.Write(data)
	w.mu.Unlock()

	if !wrote && !w.closed.Swap(true) {
		close(w.firstWrite)
	}
	return n, err
}

func (w *threadSafeResponseWriter) WriteHeader(code int) {
	w.mu.Lock()
	w.wroteHead = true
	w.mu.Unlock()
}

func (w *threadSafeResponseWriter) Flush() {
	// No-op for test - the actual flush would happen in real ResponseWriter
}

func (w *threadSafeResponseWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Bytes()
}

func (w *threadSafeResponseWriter) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Len()
}

// buildEventStreamMessage constructs a binary AWS event stream message
// with the given event type and JSON payload.
func buildEventStreamMessage(eventType string, payload []byte) []byte {
	var headers bytes.Buffer
	writeEventStreamHeader(&headers, ":event-type", eventType)
	writeEventStreamHeader(&headers, ":content-type", "application/json")
	writeEventStreamHeader(&headers, ":message-type", "event")

	headersBytes := headers.Bytes()
	headersLen := uint32(len(headersBytes))
	totalLen := uint32(12 + headersLen + uint32(len(payload)) + 4)

	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, totalLen)
	binary.Write(&buf, binary.BigEndian, headersLen)
	binary.Write(&buf, binary.BigEndian, uint32(0)) // prelude CRC (not validated in parser)
	buf.Write(headersBytes)
	buf.Write(payload)
	binary.Write(&buf, binary.BigEndian, uint32(0)) // message CRC (not validated in parser)

	return buf.Bytes()
}

func writeEventStreamHeader(buf *bytes.Buffer, name, value string) {
	buf.WriteByte(byte(len(name)))
	buf.WriteString(name)
	buf.WriteByte(7) // string type
	binary.Write(buf, binary.BigEndian, uint16(len(value)))
	buf.WriteString(value)
}

func TestStreamingExtractor_EventStream(t *testing.T) {
	// Build a complete Bedrock streaming response with binary events
	var stream bytes.Buffer

	// messageStart event
	startPayload, _ := json.Marshal(map[string]string{"role": "assistant"})
	stream.Write(buildEventStreamMessage("messageStart", startPayload))

	// contentBlockDelta events
	delta1, _ := json.Marshal(map[string]any{
		"contentBlockIndex": 0,
		"delta":             map[string]string{"text": "Hello"},
	})
	stream.Write(buildEventStreamMessage("contentBlockDelta", delta1))

	delta2, _ := json.Marshal(map[string]any{
		"contentBlockIndex": 0,
		"delta":             map[string]string{"text": " World"},
	})
	stream.Write(buildEventStreamMessage("contentBlockDelta", delta2))

	// messageStop event
	stopPayload, _ := json.Marshal(map[string]string{"stopReason": "end_turn"})
	stream.Write(buildEventStreamMessage("messageStop", stopPayload))

	// metadata event
	metadataPayload, _ := json.Marshal(map[string]any{
		"usage": map[string]int{
			"inputTokens":  10,
			"outputTokens": 5,
			"totalTokens":  15,
		},
		"metrics": map[string]int64{
			"latencyMs": 100,
		},
	})
	stream.Write(buildEventStreamMessage("metadata", metadataPayload))

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(&stream),
	}

	recorder := newThreadSafeResponseWriter()
	rc := http.NewResponseController(recorder)

	extractor := NewStreamingExtractor()
	meta, err := extractor.ExtractStreamingWithController(resp, recorder, rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify usage metadata
	if meta.Usage.PromptTokens != 10 {
		t.Errorf("expected prompt tokens 10, got %d", meta.Usage.PromptTokens)
	}
	if meta.Usage.CompletionTokens != 5 {
		t.Errorf("expected completion tokens 5, got %d", meta.Usage.CompletionTokens)
	}
	if meta.Usage.TotalTokens != 15 {
		t.Errorf("expected total tokens 15, got %d", meta.Usage.TotalTokens)
	}

	// Verify choices
	if len(meta.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(meta.Choices))
	}
	if meta.Choices[0].Message.Role != "assistant" {
		t.Errorf("expected role 'assistant', got %q", meta.Choices[0].Message.Role)
	}
	if meta.Choices[0].Message.Content != "Hello World" {
		t.Errorf("expected content 'Hello World', got %q", meta.Choices[0].Message.Content)
	}
	if meta.Choices[0].FinishReason != "end_turn" {
		t.Errorf("expected finish_reason 'end_turn', got %q", meta.Choices[0].FinishReason)
	}

	// Verify latency metric
	if latency, ok := meta.Custom["latency_ms"]; !ok || latency != int64(100) {
		t.Errorf("expected latency_ms 100, got %v", meta.Custom["latency_ms"])
	}

	// Verify data was forwarded to client
	if recorder.Len() == 0 {
		t.Error("no data written to client")
	}
}

func TestStreamingExtractor_EventStreamIncremental(t *testing.T) {
	// Use a pipe to simulate slow upstream
	pr, pw := io.Pipe()

	// Send events with delay to verify incrementality
	go func() {
		defer pw.Close()

		startPayload, _ := json.Marshal(map[string]string{"role": "assistant"})
		pw.Write(buildEventStreamMessage("messageStart", startPayload))

		delta1, _ := json.Marshal(map[string]any{
			"contentBlockIndex": 0,
			"delta":             map[string]string{"text": "Hello"},
		})
		pw.Write(buildEventStreamMessage("contentBlockDelta", delta1))

		time.Sleep(100 * time.Millisecond)

		delta2, _ := json.Marshal(map[string]any{
			"contentBlockIndex": 0,
			"delta":             map[string]string{"text": " World"},
		})
		pw.Write(buildEventStreamMessage("contentBlockDelta", delta2))

		metadataPayload, _ := json.Marshal(map[string]any{
			"usage": map[string]int{
				"inputTokens":  10,
				"outputTokens": 5,
				"totalTokens":  15,
			},
		})
		pw.Write(buildEventStreamMessage("metadata", metadataPayload))
	}()

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(pr),
	}

	recorder := newThreadSafeResponseWriter()
	rc := http.NewResponseController(recorder)

	extractor := NewStreamingExtractor()

	// Use a channel to safely receive the first write time
	firstByteTimeCh := make(chan time.Time, 1)
	go func() {
		<-recorder.firstWrite
		firstByteTimeCh <- time.Now()
	}()

	meta, err := extractor.ExtractStreamingWithController(resp, recorder, rc)
	streamDoneTime := time.Now()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify first bytes arrived before stream completed
	select {
	case firstByteTime := <-firstByteTimeCh:
		timeDiff := streamDoneTime.Sub(firstByteTime)
		if timeDiff < 50*time.Millisecond {
			t.Errorf("data did not arrive incrementally: first chunk and completion were only %v apart", timeDiff)
		}
	default:
		t.Fatal("no data was received")
	}

	// Verify metadata was still extracted
	if meta.Usage.TotalTokens != 15 {
		t.Errorf("expected total tokens 15, got %d", meta.Usage.TotalTokens)
	}
}

func TestStreamingExtractor_IsStreamingResponse(t *testing.T) {
	extractor := NewStreamingExtractor()

	tests := []struct {
		contentType string
		expected    bool
	}{
		{"text/event-stream", true},
		{"text/event-stream; charset=utf-8", true},
		{"application/vnd.amazon.eventstream", true},
		{"application/json", false},
		{"text/plain", false},
	}

	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			resp := &http.Response{
				Header: http.Header{"Content-Type": []string{tt.contentType}},
			}
			result := extractor.IsStreamingResponse(resp)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestStreamingExtractor_NonStreamingFallback(t *testing.T) {
	extractor := NewStreamingExtractor()

	respBody := `{"requestId":"req-123","modelId":"anthropic.claude-3-sonnet-20240229-v1:0","output":{"message":{"role":"assistant","content":[{"text":"Hello!"}]}},"usage":{"inputTokens":10,"outputTokens":5,"totalTokens":15},"stopReason":"end_turn"}`

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(respBody)),
	}

	recorder := newThreadSafeResponseWriter()
	rc := http.NewResponseController(recorder)

	meta, err := extractor.ExtractStreamingWithController(resp, recorder, rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if meta.Usage.PromptTokens != 10 {
		t.Errorf("expected prompt tokens 10, got %d", meta.Usage.PromptTokens)
	}
	if meta.Usage.CompletionTokens != 5 {
		t.Errorf("expected completion tokens 5, got %d", meta.Usage.CompletionTokens)
	}
	if len(meta.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(meta.Choices))
	}
	if meta.Choices[0].Message.Content != "Hello!" {
		t.Errorf("expected content 'Hello!', got %q", meta.Choices[0].Message.Content)
	}
}

func TestStreamingExtractor_EventStreamWithCache(t *testing.T) {
	var stream bytes.Buffer

	startPayload, _ := json.Marshal(map[string]string{"role": "assistant"})
	stream.Write(buildEventStreamMessage("messageStart", startPayload))

	deltaPayload, _ := json.Marshal(map[string]any{
		"contentBlockIndex": 0,
		"delta":             map[string]string{"text": "cached response"},
	})
	stream.Write(buildEventStreamMessage("contentBlockDelta", deltaPayload))

	stopPayload, _ := json.Marshal(map[string]string{"stopReason": "end_turn"})
	stream.Write(buildEventStreamMessage("messageStop", stopPayload))

	metadataPayload, _ := json.Marshal(map[string]any{
		"usage": map[string]any{
			"inputTokens":           100,
			"outputTokens":          50,
			"totalTokens":           150,
			"cacheReadInputTokens":  80,
			"cacheWriteInputTokens": 20,
		},
	})
	stream.Write(buildEventStreamMessage("metadata", metadataPayload))

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(&stream),
	}

	recorder := newThreadSafeResponseWriter()
	rc := http.NewResponseController(recorder)

	extractor := NewStreamingExtractor()
	meta, err := extractor.ExtractStreamingWithController(resp, recorder, rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if meta.Usage.PromptTokens != 100 {
		t.Errorf("expected prompt tokens 100, got %d", meta.Usage.PromptTokens)
	}

	cacheUsage, ok := meta.Custom["cache_usage"].(llmproxy.CacheUsage)
	if !ok {
		t.Fatal("expected cache_usage in custom map")
	}
	if cacheUsage.CachedTokens != 80 {
		t.Errorf("expected cached tokens 80, got %d", cacheUsage.CachedTokens)
	}
	if cacheUsage.CacheWriteTokens != 20 {
		t.Errorf("expected cache write tokens 20, got %d", cacheUsage.CacheWriteTokens)
	}
}

func TestResolver_StreamingEndpoint(t *testing.T) {
	t.Run("resolves to converse-stream when streaming", func(t *testing.T) {
		resolver := NewResolver("us-east-1")
		meta := llmproxy.BodyMetadata{Model: "anthropic.claude-3-sonnet-20240229-v1:0", Stream: true}

		u, err := resolver.Resolve(meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(u.String(), "/converse-stream") {
			t.Errorf("expected converse-stream in URL, got %s", u.String())
		}
	})

	t.Run("resolves to converse when not streaming", func(t *testing.T) {
		resolver := NewResolver("us-east-1")
		meta := llmproxy.BodyMetadata{Model: "anthropic.claude-3-sonnet-20240229-v1:0", Stream: false}

		u, err := resolver.Resolve(meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(u.String(), "converse-stream") {
			t.Errorf("expected converse (not stream) in URL, got %s", u.String())
		}
		if !strings.Contains(u.String(), "/converse") {
			t.Errorf("expected converse in URL, got %s", u.String())
		}
	})

	t.Run("invoke endpoint ignores streaming flag", func(t *testing.T) {
		resolver := NewInvokeResolver("us-east-1")
		meta := llmproxy.BodyMetadata{Model: "amazon.titan-text-express-v1", Stream: true}

		u, err := resolver.Resolve(meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(u.String(), "/invoke") {
			t.Errorf("expected invoke in URL, got %s", u.String())
		}
	})
}

func TestParseEventStreamHeaders(t *testing.T) {
	var buf bytes.Buffer
	writeEventStreamHeader(&buf, ":event-type", "contentBlockDelta")
	writeEventStreamHeader(&buf, ":content-type", "application/json")
	writeEventStreamHeader(&buf, ":message-type", "event")

	headers := parseEventStreamHeaders(buf.Bytes())

	if headers[":event-type"] != "contentBlockDelta" {
		t.Errorf("expected event-type 'contentBlockDelta', got %q", headers[":event-type"])
	}
	if headers[":content-type"] != "application/json" {
		t.Errorf("expected content-type 'application/json', got %q", headers[":content-type"])
	}
	if headers[":message-type"] != "event" {
		t.Errorf("expected message-type 'event', got %q", headers[":message-type"])
	}
}
