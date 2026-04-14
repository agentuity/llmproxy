package llmproxy

import (
	"bytes"
	"io"
	"testing"
)

func TestSSEParser(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []*SSEEvent
	}{
		{
			name:  "simple event",
			input: "data: {\"test\":\"value\"}\n\n",
			expected: []*SSEEvent{
				{Data: []byte(`{"test":"value"}`)},
			},
		},
		{
			name:  "event with type",
			input: "event: message\ndata: {\"test\":\"value\"}\n\n",
			expected: []*SSEEvent{
				{Event: []byte("message"), Data: []byte(`{"test":"value"}`)},
			},
		},
		{
			name:  "multiple events",
			input: "data: first\n\ndata: second\n\n",
			expected: []*SSEEvent{
				{Data: []byte("first")},
				{Data: []byte("second")},
			},
		},
		{
			name:  "multiline data",
			input: "data: line1\ndata: line2\n\n",
			expected: []*SSEEvent{
				{Data: []byte("line1\nline2")},
			},
		},
		{
			name:  "OpenAI streaming format",
			input: "data: {\"id\":\"chatcmpl-123\",\"object\":\"chat.completion.chunk\",\"created\":1234567890,\"model\":\"gpt-4\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello\"},\"finish_reason\":null}]}\n\ndata: [DONE]\n\n",
			expected: []*SSEEvent{
				{Data: []byte(`{"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`)},
				{Data: []byte("[DONE]")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewSSEParser(bytes.NewReader([]byte(tt.input)))

			var events []*SSEEvent
			for {
				event, err := parser.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				events = append(events, event)
			}

			if len(events) != len(tt.expected) {
				t.Fatalf("expected %d events, got %d", len(tt.expected), len(events))
			}

			for i, event := range events {
				if !bytes.Equal(event.Data, tt.expected[i].Data) {
					t.Errorf("event %d: expected data %q, got %q", i, tt.expected[i].Data, event.Data)
				}
				if !bytes.Equal(event.Event, tt.expected[i].Event) {
					t.Errorf("event %d: expected event %q, got %q", i, tt.expected[i].Event, event.Event)
				}
			}
		})
	}
}

func TestParseOpenAISSEEvent(t *testing.T) {
	tests := []struct {
		name        string
		input       []byte
		expectError bool
		expectDone  bool
	}{
		{
			name:        "valid chunk",
			input:       []byte(`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"gpt-4","choices":[{"index":0,"delta":{"content":"Hello"}}]}`),
			expectError: false,
		},
		{
			name:        "done marker",
			input:       []byte("[DONE]"),
			expectDone:  true,
			expectError: false,
		},
		{
			name:        "empty input",
			input:       []byte{},
			expectError: false,
		},
		{
			name:        "invalid JSON",
			input:       []byte(`{invalid}`),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunk, err := ParseOpenAISSEEvent(tt.input)

			if tt.expectDone {
				if err != ErrStreamComplete {
					t.Errorf("expected ErrStreamComplete, got %v", err)
				}
				return
			}

			if tt.expectError {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil && !tt.expectError {
				t.Errorf("unexpected error: %v", err)
			}

			if chunk != nil && tt.input != nil && len(tt.input) > 0 {
				_ = chunk.ID
			}
		})
	}
}

func TestExtractUsageFromOpenAIChunk(t *testing.T) {
	tests := []struct {
		name     string
		chunk    *OpenAIStreamChunk
		expected *StreamingUsage
	}{
		{
			name:     "nil chunk",
			chunk:    nil,
			expected: nil,
		},
		{
			name:     "chunk without usage",
			chunk:    &OpenAIStreamChunk{ID: "test"},
			expected: nil,
		},
		{
			name: "chunk with basic usage",
			chunk: &OpenAIStreamChunk{
				Usage: &OpenAIStreamUsage{
					PromptTokens:     100,
					CompletionTokens: 50,
					TotalTokens:      150,
				},
			},
			expected: &StreamingUsage{
				PromptTokens:     100,
				CompletionTokens: 50,
				TotalTokens:      150,
			},
		},
		{
			name: "chunk with cache usage",
			chunk: &OpenAIStreamChunk{
				Usage: &OpenAIStreamUsage{
					PromptTokens:     100,
					CompletionTokens: 50,
					TotalTokens:      150,
					PromptTokensDetails: &OpenAIStreamPromptDetails{
						CachedTokens: 80,
					},
				},
			},
			expected: &StreamingUsage{
				PromptTokens:     100,
				CompletionTokens: 50,
				TotalTokens:      150,
				CacheUsage: &CacheUsage{
					CachedTokens: 80,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractUsageFromOpenAIChunk(tt.chunk)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("expected nil, got %+v", result)
				}
				return
			}

			if result == nil {
				t.Fatal("expected result, got nil")
			}

			if result.PromptTokens != tt.expected.PromptTokens {
				t.Errorf("expected PromptTokens %d, got %d", tt.expected.PromptTokens, result.PromptTokens)
			}
			if result.CompletionTokens != tt.expected.CompletionTokens {
				t.Errorf("expected CompletionTokens %d, got %d", tt.expected.CompletionTokens, result.CompletionTokens)
			}
			if result.TotalTokens != tt.expected.TotalTokens {
				t.Errorf("expected TotalTokens %d, got %d", tt.expected.TotalTokens, result.TotalTokens)
			}

			if tt.expected.CacheUsage != nil {
				if result.CacheUsage == nil {
					t.Error("expected CacheUsage, got nil")
				} else if result.CacheUsage.CachedTokens != tt.expected.CacheUsage.CachedTokens {
					t.Errorf("expected CachedTokens %d, got %d", tt.expected.CacheUsage.CachedTokens, result.CacheUsage.CachedTokens)
				}
			}
		})
	}
}

func TestIsSSEStream(t *testing.T) {
	tests := []struct {
		contentType string
		expected    bool
	}{
		{"text/event-stream", true},
		{"text/event-stream; charset=utf-8", true},
		{"application/json", false},
		{"text/plain", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			result := IsSSEStream(tt.contentType)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestFormatSSEEvent(t *testing.T) {
	tests := []struct {
		name     string
		event    string
		data     []byte
		expected []byte
	}{
		{
			name:     "data only",
			event:    "",
			data:     []byte(`{"test":"value"}`),
			expected: []byte("data: {\"test\":\"value\"}\n\n"),
		},
		{
			name:     "event and data",
			event:    "message",
			data:     []byte(`{"test":"value"}`),
			expected: []byte("event: message\ndata: {\"test\":\"value\"}\n\n"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatSSEEvent(tt.event, tt.data)
			if !bytes.Equal(result, tt.expected) {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}
