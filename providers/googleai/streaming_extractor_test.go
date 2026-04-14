package googleai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentuity/llmproxy"
)

func TestStreamingExtractor_ExtractStreaming(t *testing.T) {
	streamData := `data: {"candidates":[{"content":{"parts":[{"text":"Hello"}],"role":"model"}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2,"totalTokenCount":12}}

data: {"candidates":[{"content":{"parts":[{"text":" World"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}

`

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(streamData)),
	}

	recorder := httptest.NewRecorder()
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
	output := recorder.Body.String()
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

	var mu sync.Mutex
	var firstChunkTime time.Time
	var streamDoneTime time.Time

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

	recorder := httptest.NewRecorder()
	rc := http.NewResponseController(recorder)

	extractor := NewStreamingExtractor()

	// Monitor when data arrives at the recorder
	go func() {
		for {
			mu.Lock()
			if recorder.Body.Len() > 0 && firstChunkTime.IsZero() {
				firstChunkTime = time.Now()
			}
			mu.Unlock()
			time.Sleep(10 * time.Millisecond)
		}
	}()

	meta, err := extractor.ExtractStreamingWithController(resp, recorder, rc)
	mu.Lock()
	streamDoneTime = time.Now()
	mu.Unlock()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify first data arrived before stream completed
	mu.Lock()
	defer mu.Unlock()
	if firstChunkTime.IsZero() {
		t.Fatal("no data was received")
	}
	// The stream takes ~100ms. First data should arrive well before completion.
	timeDiff := streamDoneTime.Sub(firstChunkTime)
	if timeDiff < 50*time.Millisecond {
		t.Errorf("data did not arrive incrementally: first chunk and completion were only %v apart", timeDiff)
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

	recorder := httptest.NewRecorder()
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
	output := recorder.Body.String()
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

	recorder := httptest.NewRecorder()
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

	recorder := httptest.NewRecorder()
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
