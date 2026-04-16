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
		name            string
		input           []byte
		expectError     bool
		expectDone      bool
		expectedID      string
		expectedContent string
	}{
		{
			name:            "valid chunk",
			input:           []byte(`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"gpt-4","choices":[{"index":0,"delta":{"content":"Hello"}}]}`),
			expectError:     false,
			expectedID:      "chatcmpl-123",
			expectedContent: "Hello",
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
				return
			}

			if len(tt.input) == 0 {
				if chunk != nil {
					t.Error("expected nil chunk for empty input")
				}
				return
			}

			if chunk == nil {
				t.Fatal("expected non-nil chunk for non-empty input")
			}

			if chunk.ID != tt.expectedID {
				t.Errorf("expected ID %q, got %q", tt.expectedID, chunk.ID)
			}

			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != tt.expectedContent {
				t.Errorf("expected content %q, got %q", tt.expectedContent, chunk.Choices[0].Delta.Content)
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
		{
			name: "chunk with reasoning tokens",
			chunk: &OpenAIStreamChunk{
				Usage: &OpenAIStreamUsage{
					PromptTokens:     75,
					CompletionTokens: 1186,
					TotalTokens:      1261,
					CompletionTokensDetails: &OpenAIStreamCompletionDetails{
						ReasoningTokens: 1024,
					},
				},
			},
			expected: &StreamingUsage{
				PromptTokens:     75,
				CompletionTokens: 1186,
				TotalTokens:      1261,
				ReasoningTokens:  1024,
			},
		},
		{
			name: "chunk with both cache and reasoning tokens",
			chunk: &OpenAIStreamChunk{
				Usage: &OpenAIStreamUsage{
					PromptTokens:     100,
					CompletionTokens: 200,
					TotalTokens:      300,
					PromptTokensDetails: &OpenAIStreamPromptDetails{
						CachedTokens: 50,
					},
					CompletionTokensDetails: &OpenAIStreamCompletionDetails{
						ReasoningTokens: 128,
					},
				},
			},
			expected: &StreamingUsage{
				PromptTokens:     100,
				CompletionTokens: 200,
				TotalTokens:      300,
				CacheUsage: &CacheUsage{
					CachedTokens: 50,
				},
				ReasoningTokens: 128,
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

			if result.ReasoningTokens != tt.expected.ReasoningTokens {
				t.Errorf("expected ReasoningTokens %d, got %d", tt.expected.ReasoningTokens, result.ReasoningTokens)
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

func TestParseAnthropicSSEEvent(t *testing.T) {
	tests := []struct {
		name              string
		input             []byte
		expectError       bool
		eventType         string
		expectedInputTok  int
		expectedOutputTok int
	}{
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
		{
			name:              "message_start event",
			input:             []byte(`{"type":"message_start","message":{"id":"msg_123","type":"message","role":"assistant","model":"claude-3-opus-20240229","usage":{"input_tokens":150,"cache_read_input_tokens":1000}}}`),
			eventType:         "message_start",
			expectedInputTok:  150,
			expectedOutputTok: 0,
		},
		{
			name:              "message_delta event",
			input:             []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":75}}`),
			eventType:         "message_delta",
			expectedInputTok:  0,
			expectedOutputTok: 75,
		},
		{
			name:      "content_block_start event",
			input:     []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
			eventType: "content_block_start",
		},
		{
			name:      "content_block_delta event",
			input:     []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`),
			eventType: "content_block_delta",
		},
		{
			name:      "message_stop event",
			input:     []byte(`{"type":"message_stop"}`),
			eventType: "message_stop",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, err := ParseAnthropicSSEEvent(tt.input)

			if tt.expectError {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if len(tt.input) == 0 {
				if event != nil {
					t.Error("expected nil event for empty input")
				}
				return
			}

			if event.Type != tt.eventType {
				t.Errorf("expected type %q, got %q", tt.eventType, event.Type)
			}

			if tt.eventType == "message_start" && event.Message != nil {
				if event.Message.Usage == nil {
					t.Error("expected usage in message_start")
				} else {
					if event.Message.Usage.InputTokens != tt.expectedInputTok {
						t.Errorf("expected input tokens %d, got %d", tt.expectedInputTok, event.Message.Usage.InputTokens)
					}
				}
			}

			if tt.eventType == "message_delta" && event.Usage != nil {
				if event.Usage.OutputTokens != tt.expectedOutputTok {
					t.Errorf("expected output tokens %d, got %d", tt.expectedOutputTok, event.Usage.OutputTokens)
				}
			}
		})
	}
}

func TestExtractUsageFromAnthropicEvent(t *testing.T) {
	tests := []struct {
		name                string
		event               *AnthropicStreamEvent
		expectedPrompt      int
		expectedCompletion  int
		expectedCacheRead   int
		expectedCacheCreate int
	}{
		{
			name:  "nil event",
			event: nil,
		},
		{
			name:  "message_stop returns nil",
			event: &AnthropicStreamEvent{Type: "message_stop"},
		},
		{
			name: "message_start with usage",
			event: &AnthropicStreamEvent{
				Type: "message_start",
				Message: &AnthropicStreamMessage{
					Usage: &AnthropicStreamUsage{
						InputTokens:          100,
						CacheReadInputTokens: 500,
					},
				},
			},
			expectedPrompt:    100,
			expectedCacheRead: 500,
		},
		{
			name: "message_delta with usage",
			event: &AnthropicStreamEvent{
				Type: "message_delta",
				Usage: &AnthropicStreamUsage{
					OutputTokens:             50,
					CacheCreationInputTokens: 200,
				},
			},
			expectedCompletion:  50,
			expectedCacheCreate: 200,
		},
		{
			name: "content_block_delta returns nil",
			event: &AnthropicStreamEvent{
				Type: "content_block_delta",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractUsageFromAnthropicEvent(tt.event)

			if tt.expectedPrompt == 0 && tt.expectedCompletion == 0 {
				if result != nil {
					t.Errorf("expected nil result, got %+v", result)
				}
				return
			}

			if result == nil {
				t.Fatal("expected result, got nil")
			}

			if result.PromptTokens != tt.expectedPrompt {
				t.Errorf("expected prompt tokens %d, got %d", tt.expectedPrompt, result.PromptTokens)
			}
			if result.CompletionTokens != tt.expectedCompletion {
				t.Errorf("expected completion tokens %d, got %d", tt.expectedCompletion, result.CompletionTokens)
			}

			if tt.expectedCacheRead > 0 || tt.expectedCacheCreate > 0 {
				if result.CacheUsage == nil {
					t.Fatal("expected cache usage")
				}
				if result.CacheUsage.CacheReadInputTokens != tt.expectedCacheRead {
					t.Errorf("expected cache read %d, got %d", tt.expectedCacheRead, result.CacheUsage.CacheReadInputTokens)
				}
				if result.CacheUsage.CacheCreationInputTokens != tt.expectedCacheCreate {
					t.Errorf("expected cache create %d, got %d", tt.expectedCacheCreate, result.CacheUsage.CacheCreationInputTokens)
				}
			}
		})
	}
}

func TestAnthropicSSEParser(t *testing.T) {
	// Realistic Anthropic streaming format
	input := `event: message_start
data: {"type":"message_start","message":{"id":"msg_1a2b3c","type":"message","role":"assistant","model":"claude-3-opus-20240229","content":[],"stop_reason":null,"usage":{"input_tokens":150,"cache_read_input_tokens":1000}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":25}}

event: message_stop
data: {"type":"message_stop"}

`

	parser := NewSSEParser(bytes.NewReader([]byte(input)))

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

	if len(events) != 7 {
		t.Fatalf("expected 7 events, got %d", len(events))
	}

	// Verify message_start
	if string(events[0].Event) != "message_start" {
		t.Errorf("expected event 'message_start', got %q", events[0].Event)
	}
	startEvent, err := ParseAnthropicSSEEvent(events[0].Data)
	if err != nil {
		t.Fatalf("ParseAnthropicSSEEvent failed for events[0]: %v", err)
	}
	if startEvent.Message.Usage.InputTokens != 150 {
		t.Errorf("expected 150 input tokens, got %d", startEvent.Message.Usage.InputTokens)
	}
	if startEvent.Message.Usage.CacheReadInputTokens != 1000 {
		t.Errorf("expected 1000 cache read tokens, got %d", startEvent.Message.Usage.CacheReadInputTokens)
	}

	// Verify message_delta
	if string(events[5].Event) != "message_delta" {
		t.Errorf("expected event 'message_delta', got %q", events[5].Event)
	}
	deltaEvent, err := ParseAnthropicSSEEvent(events[5].Data)
	if err != nil {
		t.Fatalf("ParseAnthropicSSEEvent failed for events[5]: %v", err)
	}
	if deltaEvent.Usage.OutputTokens != 25 {
		t.Errorf("expected 25 output tokens, got %d", deltaEvent.Usage.OutputTokens)
	}
}

func TestParseResponsesSSEEvent_Created(t *testing.T) {
	data := []byte(`{"type":"response.created","response":{"id":"resp_123","object":"response","model":"gpt-4o","status":"in_progress"}}`)

	event, err := ParseResponsesSSEEvent(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected non-nil event")
	}
	if event.Type != "response.created" {
		t.Errorf("Type = %q, want response.created", event.Type)
	}
	if len(event.Response) == 0 {
		t.Fatal("expected response payload")
	}
}

func TestParseResponsesSSEEvent_TextDelta(t *testing.T) {
	data := []byte(`{"type":"response.output_text.delta","delta":"Hello","content_index":0,"output_index":0}`)

	event, err := ParseResponsesSSEEvent(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected non-nil event")
	}
	if event.Type != "response.output_text.delta" {
		t.Errorf("Type = %q, want response.output_text.delta", event.Type)
	}
}

func TestParseResponsesSSEEvent_Completed(t *testing.T) {
	data := []byte(`{"type":"response.completed","response":{"id":"resp_123","model":"gpt-4o","status":"completed","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`)

	event, err := ParseResponsesSSEEvent(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected non-nil event")
	}
	if event.Type != "response.completed" {
		t.Errorf("Type = %q, want response.completed", event.Type)
	}
}

func TestParseResponsesSSEEvent_Empty(t *testing.T) {
	event, err := ParseResponsesSSEEvent([]byte("  \n\t  "))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event != nil {
		t.Fatalf("expected nil event, got %+v", event)
	}
}

func TestParseResponsesSSEEvent_Done(t *testing.T) {
	event, err := ParseResponsesSSEEvent([]byte("[DONE]"))
	if err != ErrStreamComplete {
		t.Fatalf("expected ErrStreamComplete, got %v", err)
	}
	if event != nil {
		t.Fatalf("expected nil event for done marker, got %+v", event)
	}
}

func TestParseResponsesSSEEvent_MalformedJSON(t *testing.T) {
	event, err := ParseResponsesSSEEvent([]byte(`{"type":"response.created",`))
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if event != nil {
		t.Fatalf("expected nil event on malformed input, got %+v", event)
	}
}

func TestExtractUsageFromResponsesEvent(t *testing.T) {
	tests := []struct {
		name               string
		event              *ResponsesStreamEvent
		expectedPrompt     int
		expectedCompletion int
		expectedTotal      int
		expectedCached     int
		expectedReasoning  int
		expectNil          bool
	}{
		{
			name:      "nil event",
			event:     nil,
			expectNil: true,
		},
		{
			name: "completed with usage",
			event: &ResponsesStreamEvent{
				Type:     "response.completed",
				Response: []byte(`{"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`),
			},
			expectedPrompt:     10,
			expectedCompletion: 5,
			expectedTotal:      15,
		},
		{
			name: "completed without usage",
			event: &ResponsesStreamEvent{
				Type:     "response.completed",
				Response: []byte(`{"id":"resp_1","status":"completed"}`),
			},
			expectNil: true,
		},
		{
			name: "non-completed event",
			event: &ResponsesStreamEvent{
				Type:     "response.created",
				Response: []byte(`{"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`),
			},
			expectNil: true,
		},
		{
			name: "usage with cached tokens",
			event: &ResponsesStreamEvent{
				Type: "response.completed",
				Response: []byte(`{
					"usage":{
						"input_tokens":100,
						"output_tokens":20,
						"total_tokens":120,
						"input_tokens_details":{"cached_tokens":80}
					}
				}`),
			},
			expectedPrompt:     100,
			expectedCompletion: 20,
			expectedTotal:      120,
			expectedCached:     80,
		},
		{
			name: "usage with reasoning tokens",
			event: &ResponsesStreamEvent{
				Type: "response.completed",
				Response: []byte(`{
					"usage":{
						"input_tokens":30,
						"output_tokens":10,
						"total_tokens":40,
						"output_tokens_details":{"reasoning_tokens":7}
					}
				}`),
			},
			expectedPrompt:     30,
			expectedCompletion: 10,
			expectedTotal:      40,
			expectedReasoning:  7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractUsageFromResponsesEvent(tt.event)
			if tt.expectNil {
				if result != nil {
					t.Fatalf("expected nil, got %+v", result)
				}
				return
			}

			if result == nil {
				t.Fatal("expected non-nil usage")
			}
			if result.PromptTokens != tt.expectedPrompt {
				t.Errorf("PromptTokens = %d, want %d", result.PromptTokens, tt.expectedPrompt)
			}
			if result.CompletionTokens != tt.expectedCompletion {
				t.Errorf("CompletionTokens = %d, want %d", result.CompletionTokens, tt.expectedCompletion)
			}
			if result.TotalTokens != tt.expectedTotal {
				t.Errorf("TotalTokens = %d, want %d", result.TotalTokens, tt.expectedTotal)
			}

			if tt.expectedCached > 0 {
				if result.CacheUsage == nil {
					t.Fatal("expected cache usage")
				}
				if result.CacheUsage.CachedTokens != tt.expectedCached {
					t.Errorf("CachedTokens = %d, want %d", result.CacheUsage.CachedTokens, tt.expectedCached)
				}
			}

			if tt.expectedReasoning > 0 {
				if result.ReasoningTokens != tt.expectedReasoning {
					t.Errorf("ReasoningTokens = %d, want %d", result.ReasoningTokens, tt.expectedReasoning)
				}
			}
		})
	}
}
