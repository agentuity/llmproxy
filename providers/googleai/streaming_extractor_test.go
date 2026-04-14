package googleai

import (
	"bytes"
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

func (w *threadSafeResponseWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func TestStreamingExtractor_ExtractStreaming(t *testing.T) {
	streamData := `data: {"candidates":[{"content":{"parts":[{"text":"Hello"}],"role":"model"}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2,"totalTokenCount":12}}

data: {"candidates":[{"content":{"parts":[{"text":" World"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}

`

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(streamData)),
	}

	recorder := newThreadSafeResponseWriter()
	rc := http.NewResponseController(recorder)

	extractor := NewStreamingExtractor()

	meta, err := extractor.ExtractStreamingWithController(resp, recorder, rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify usage extracted from last chunk
	if meta.Usage.PromptTokens != 10 {
		t.Errorf("expected prompt tokens 10, got %d", meta.Usage.PromptTokens)
	}
	if meta.Usage.CompletionTokens != 5 {
		t.Errorf("expected completion tokens 5, got %d", meta.Usage.CompletionTokens)
	}
	if meta.Usage.TotalTokens != 15 {
		t.Errorf("expected total tokens 15, got %d", meta.Usage.TotalTokens)
	}

	// Verify choices extracted
	if len(meta.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(meta.Choices))
	}
	if meta.Choices[0].Message.Content != "Hello World" {
		t.Errorf("expected content 'Hello World', got %q", meta.Choices[0].Message.Content)
	}
	if meta.Choices[0].FinishReason != "stop" {
		t.Errorf("expected finish_reason 'stop', got %q", meta.Choices[0].FinishReason)
	}

	// Verify data was forwarded to client
	output := recorder.String()
	if !strings.Contains(output, "data: ") {
		t.Error("expected SSE data format in output")
	}
	if !strings.Contains(output, `"Hello"`) {
		t.Error("expected Hello text in output")
	}
	if !strings.Contains(output, `" World"`) {
		t.Error("expected World text in output")
	}
}

func TestStreamingExtractor_StreamsIncrementally(t *testing.T) {
	// Use a pipe to simulate slow upstream that sends data over time
	pr, pw := io.Pipe()

	// Simulate upstream sending events with delay
	go func() {
		defer pw.Close()
		pw.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Hello\"}],\"role\":\"model\"}}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":2,\"totalTokenCount\":12}}\n\n"))
		time.Sleep(100 * time.Millisecond)
		pw.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\" World\"}],\"role\":\"model\"},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":5,\"totalTokenCount\":15}}\n\n"))
	}()

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(pr),
	}

	recorder := newThreadSafeResponseWriter()
	rc := http.NewResponseController(recorder)

	extractor := NewStreamingExtractor()

	// Use a channel to safely receive the first write time
	firstChunkTimeCh := make(chan time.Time, 1)
	go func() {
		<-recorder.firstWrite
		firstChunkTimeCh <- time.Now()
	}()

	meta, err := extractor.ExtractStreamingWithController(resp, recorder, rc)
	streamDoneTime := time.Now()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify first data arrived before stream completed
	select {
	case firstChunkTime := <-firstChunkTimeCh:
		// The stream takes ~100ms. First data should arrive well before completion.
		timeDiff := streamDoneTime.Sub(firstChunkTime)
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

	respBody := `{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello!"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}`

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

	// Verify body was forwarded
	output := recorder.String()
	if output != respBody {
		t.Errorf("expected body to be forwarded, got %q", output)
	}
}

func TestStreamingExtractor_ModelExtraction(t *testing.T) {
	streamData := `data: {"candidates":[{"content":{"parts":[{"text":"Hi"}],"role":"model"}}],"model":"gemini-1.5-flash","usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1,"totalTokenCount":6}}

`

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(streamData)),
	}

	recorder := newThreadSafeResponseWriter()
	rc := http.NewResponseController(recorder)

	extractor := NewStreamingExtractor()
	meta, err := extractor.ExtractStreamingWithController(resp, recorder, rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if meta.Model != "gemini-1.5-flash" {
		t.Errorf("expected model 'gemini-1.5-flash', got %q", meta.Model)
	}
}

func TestStreamingExtractor_EmptyStream(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("")),
	}

	recorder := newThreadSafeResponseWriter()
	rc := http.NewResponseController(recorder)

	extractor := NewStreamingExtractor()
	_, err := extractor.ExtractStreamingWithController(resp, recorder, rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolver_StreamingEndpoint(t *testing.T) {
	resolver, err := NewResolver("https://generativelanguage.googleapis.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Run("resolves to streamGenerateContent when streaming", func(t *testing.T) {
		meta := llmproxy.BodyMetadata{Model: "gemini-pro", Stream: true}
		u, err := resolver.Resolve(meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := "https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:streamGenerateContent?alt=sse"
		if u.String() != expected {
			t.Errorf("expected %s, got %s", expected, u.String())
		}
	})

	t.Run("resolves to generateContent when not streaming", func(t *testing.T) {
		meta := llmproxy.BodyMetadata{Model: "gemini-pro", Stream: false}
		u, err := resolver.Resolve(meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := "https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent"
		if u.String() != expected {
			t.Errorf("expected %s, got %s", expected, u.String())
		}
	})

	t.Run("defaults to gemini-pro when streaming with empty model", func(t *testing.T) {
		meta := llmproxy.BodyMetadata{Stream: true}
		u, err := resolver.Resolve(meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := "https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:streamGenerateContent?alt=sse"
		if u.String() != expected {
			t.Errorf("expected %s, got %s", expected, u.String())
		}
	})
}
