package openai_compatible

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/agentuity/llmproxy"
)

func TestResponsesParser(t *testing.T) {
	body := `{
		"model": "gpt-4o",
		"input": "Hello, world!",
		"instructions": "Be helpful",
		"max_output_tokens": 100,
		"temperature": 0.7,
		"tools": [{"type": "web_search_preview"}]
	}`

	parser := &ResponsesParser{}
	meta, data, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if meta.Model != "gpt-4o" {
		t.Errorf("Model = %q, want %q", meta.Model, "gpt-4o")
	}

	if meta.MaxTokens != 100 {
		t.Errorf("MaxTokens = %d, want 100", meta.MaxTokens)
	}

	if len(meta.Messages) != 1 {
		t.Errorf("Messages length = %d, want 1", len(meta.Messages))
	} else {
		if meta.Messages[0].Role != "user" {
			t.Errorf("Message role = %q, want user", meta.Messages[0].Role)
		}
		if meta.Messages[0].Content != "Hello, world!" {
			t.Errorf("Message content = %q, want 'Hello, world!'", meta.Messages[0].Content)
		}
	}

	if meta.Custom["instructions"] != "Be helpful" {
		t.Errorf("instructions = %v, want 'Be helpful'", meta.Custom["instructions"])
	}

	if meta.Custom["api_type"] != llmproxy.APITypeResponses {
		t.Errorf("api_type = %v, want responses", meta.Custom["api_type"])
	}

	if len(data) == 0 {
		t.Error("data is empty")
	}
}

func TestResponsesParser_InputArray(t *testing.T) {
	body := `{
		"model": "gpt-4o",
		"input": [
			{"role": "system", "content": "You are helpful"},
			{"role": "user", "content": "Hello"}
		]
	}`

	parser := &ResponsesParser{}
	meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if len(meta.Messages) != 2 {
		t.Fatalf("Messages length = %d, want 2", len(meta.Messages))
	}

	if meta.Messages[0].Role != "system" {
		t.Errorf("First message role = %q, want system", meta.Messages[0].Role)
	}
	if meta.Messages[1].Role != "user" {
		t.Errorf("Second message role = %q, want user", meta.Messages[1].Role)
	}
}

func TestResponsesExtractor(t *testing.T) {
	respBody := `{
		"id": "resp_abc123",
		"object": "response",
		"created": 1234567890,
		"model": "gpt-4o",
		"status": "completed",
		"output": [
			{
				"id": "msg_123",
				"type": "message",
				"status": "completed",
				"role": "assistant",
				"content": [
					{
						"type": "output_text",
						"text": "Hello! How can I help you?"
					}
				]
			}
		],
		"usage": {
			"input_tokens": 10,
			"output_tokens": 20,
			"total_tokens": 30,
			"input_tokens_details": {
				"cached_tokens": 5
			}
		}
	}`

	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(respBody))),
	}

	extractor := &ResponsesExtractor{}
	meta, rawBody, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	if meta.ID != "resp_abc123" {
		t.Errorf("ID = %q, want resp_abc123", meta.ID)
	}

	if meta.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", meta.Model)
	}

	if meta.Usage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10", meta.Usage.PromptTokens)
	}
	if meta.Usage.CompletionTokens != 20 {
		t.Errorf("CompletionTokens = %d, want 20", meta.Usage.CompletionTokens)
	}
	if meta.Usage.TotalTokens != 30 {
		t.Errorf("TotalTokens = %d, want 30", meta.Usage.TotalTokens)
	}

	if len(meta.Choices) != 1 {
		t.Fatalf("Choices length = %d, want 1", len(meta.Choices))
	}

	if meta.Choices[0].Message == nil {
		t.Fatal("Message is nil")
	}
	if meta.Choices[0].Message.Content != "Hello! How can I help you?" {
		t.Errorf("Message content = %q, want 'Hello! How can I help you?'", meta.Choices[0].Message.Content)
	}

	if meta.Custom["status"] != "completed" {
		t.Errorf("status = %v, want completed", meta.Custom["status"])
	}

	cacheUsage, ok := meta.Custom["cache_usage"].(llmproxy.CacheUsage)
	if !ok {
		t.Fatal("cache_usage not found or wrong type")
	}
	if cacheUsage.CachedTokens != 5 {
		t.Errorf("CachedTokens = %d, want 5", cacheUsage.CachedTokens)
	}

	if len(rawBody) == 0 {
		t.Error("rawBody is empty")
	}
}

func TestMultiAPIParser_ChatCompletions(t *testing.T) {
	body := `{
		"model": "gpt-4",
		"messages": [{"role": "user", "content": "Hello"}],
		"max_tokens": 100
	}`

	parser := NewMultiAPIParser()
	meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if meta.Model != "gpt-4" {
		t.Errorf("Model = %q, want gpt-4", meta.Model)
	}

	if len(meta.Messages) != 1 {
		t.Errorf("Messages length = %d, want 1", len(meta.Messages))
	}
}

func TestMultiAPIParser_Responses(t *testing.T) {
	body := `{
		"model": "gpt-4o",
		"input": "Hello",
		"instructions": "Be helpful"
	}`

	parser := NewMultiAPIParser()
	meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if meta.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", meta.Model)
	}

	if meta.Custom["api_type"] != llmproxy.APITypeResponses {
		t.Errorf("api_type = %v, want responses", meta.Custom["api_type"])
	}
}

func TestResolver_ResponsesAPI(t *testing.T) {
	resolver, err := NewResolver("https://api.openai.com")
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}

	meta := llmproxy.BodyMetadata{
		Model:  "gpt-4o",
		Custom: map[string]any{"api_type": llmproxy.APITypeResponses},
	}

	url, err := resolver.Resolve(meta)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	expected := "https://api.openai.com/v1/responses"
	if url.String() != expected {
		t.Errorf("URL = %q, want %q", url.String(), expected)
	}
}

func TestResolver_ChatCompletionsAPI(t *testing.T) {
	resolver, err := NewResolver("https://api.openai.com")
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}

	meta := llmproxy.BodyMetadata{
		Model:  "gpt-4",
		Custom: map[string]any{"api_type": llmproxy.APITypeChatCompletions},
	}

	url, err := resolver.Resolve(meta)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	expected := "https://api.openai.com/v1/chat/completions"
	if url.String() != expected {
		t.Errorf("URL = %q, want %q", url.String(), expected)
	}
}

func TestNewMultiAPI(t *testing.T) {
	provider, err := NewMultiAPI("test", "api-key", "https://api.example.com")
	if err != nil {
		t.Fatalf("NewMultiAPI() error = %v", err)
	}

	if provider.Name() != "test" {
		t.Errorf("Name() = %q, want test", provider.Name())
	}

	if provider.BodyParser() == nil {
		t.Error("BodyParser() is nil")
	}

	if provider.ResponseExtractor() == nil {
		t.Error("ResponseExtractor() is nil")
	}

	if provider.RequestEnricher() == nil {
		t.Error("RequestEnricher() is nil")
	}

	if provider.URLResolver() == nil {
		t.Error("URLResolver() is nil")
	}
}

func TestResponsesExtractor_ArrayWithLeadingWhitespace(t *testing.T) {
	respBody := `

  [
    {
      "id": "msg_123",
      "type": "message",
      "role": "assistant",
      "content": [
        {"type": "output_text", "text": "Hello"}
      ]
    }
  ]`

	extractor := &ResponsesExtractor{}
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(respBody))),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	if len(meta.Choices) != 1 {
		t.Errorf("Choices length = %d, want 1", len(meta.Choices))
	}

	if meta.Choices[0].Message.Content != "Hello" {
		t.Errorf("Content = %v, want Hello", meta.Choices[0].Message.Content)
	}
}

func TestResponsesExtractor_WebSearchWithAnnotations(t *testing.T) {
	respBody := `[
  {
    "id": "ws_67bd64fe91f081919bec069ad65797f1",
    "status": "completed",
    "type": "web_search_call"
  },
  {
    "id": "msg_67bd6502568c8191a2cbb154fa3fbf4c",
    "content": [
      {
        "annotations": [
          {
            "index": null,
            "title": "Huawei improves AI chip production",
            "type": "url_citation",
            "url": "https://www.ft.com/content/example"
          }
        ],
        "text": "As of February 25, 2025, several significant developments have emerged in the field of artificial intelligence (AI).",
        "type": "output_text",
        "logprobs": null
      }
    ],
    "role": "assistant",
    "type": "message"
  }
]`

	extractor := &ResponsesExtractor{}
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(respBody))),
	}

	meta, raw, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	if len(meta.Choices) != 1 {
		t.Errorf("Choices length = %d, want 1", len(meta.Choices))
	}

	if meta.Choices[0].Message.Content != "As of February 25, 2025, several significant developments have emerged in the field of artificial intelligence (AI)." {
		t.Errorf("Content = %v, want AI developments text", meta.Choices[0].Message.Content)
	}

	output, ok := meta.Custom["output"].([]ResponsesOutputItem)
	if !ok {
		t.Fatalf("output in Custom should be []ResponsesOutputItem, got %T", meta.Custom["output"])
	}

	if len(output) != 2 {
		t.Errorf("Output items = %d, want 2", len(output))
	}

	if output[0].Type != "web_search_call" {
		t.Errorf("First output type = %q, want web_search_call", output[0].Type)
	}
	if output[1].Type != "message" {
		t.Errorf("Second output type = %q, want message", output[1].Type)
	}

	if len(output[1].Content) == 0 {
		t.Fatal("Message content should not be empty")
	}

	if len(output[1].Content[0].Annotations) != 1 {
		t.Errorf("Annotations count = %d, want 1", len(output[1].Content[0].Annotations))
	}

	annotation := output[1].Content[0].Annotations[0]
	if annotation.Type != "url_citation" {
		t.Errorf("Annotation type = %q, want url_citation", annotation.Type)
	}
	if annotation.Title != "Huawei improves AI chip production" {
		t.Errorf("Annotation title = %q, want Huawei title", annotation.Title)
	}
	if annotation.URL != "https://www.ft.com/content/example" {
		t.Errorf("Annotation URL = %q, want ft.com URL", annotation.URL)
	}

	if string(raw) != respBody {
		t.Error("Raw body not preserved")
	}
}

func TestResponsesExtractor_FullResponseWithWebSearch(t *testing.T) {
	respBody := `{
    "id": "resp_67bd65392a088191a3b802a61f4fba14",
    "created_at": 1740465465.0,
    "error": null,
    "metadata": {},
    "model": "gpt-4o-2024-08-06",
    "object": "response",
    "output": [
        {
            "id": "msg_67bd653ab9cc81918db973f0c1af9fbb",
            "content": [
                {
                    "annotations": [],
                    "text": "Based on the image of a cat, some relevant keywords could be:\n\n- Cat\n- Feline\n- Pet",
                    "type": "output_text",
                    "logprobs": null
                }
            ],
            "role": "assistant",
            "type": "message"
        },
        {
            "id": "ws_67bd653c7a548191af86757fbbca96e1",
            "status": "completed",
            "type": "web_search_call"
        },
        {
            "id": "msg_67bd653f34fc8191989241b2659fd1b5",
            "content": [
                {
                    "annotations": [
                        {
                            "index": null,
                            "title": "Cat miraculously survives 3 weeks trapped in sofa",
                            "type": "url_citation",
                            "url": "https://nypost.com/2025/02/24/us-news/cat-survives/"
                        },
                        {
                            "index": null,
                            "title": "Another cat story",
                            "type": "url_citation",
                            "url": "https://example.com/cat-story"
                        }
                    ],
                    "text": "Here are some recent news stories related to cats:\n\n**1. Cat Survives Three Weeks**",
                    "type": "output_text",
                    "logprobs": null
                }
            ],
            "role": "assistant",
            "type": "message"
        }
    ],
    "temperature": 1.0,
    "tool_choice": "auto",
    "tools": [
        {
            "type": "web_search",
            "location": null,
            "sites": null
        }
    ],
    "top_p": 1.0,
    "max_completion_tokens": null,
    "previous_response_id": null,
    "reasoning_effort": null,
    "text": {
        "format": {
            "type": "text"
        },
        "stop": null
    },
    "top_logprobs": null,
    "truncation": "disabled",
    "usage": {
        "completion_tokens": null,
        "prompt_tokens": null,
        "total_tokens": 1370,
        "completion_tokens_details": null,
        "prompt_tokens_details": null
    }
}`

	extractor := &ResponsesExtractor{}
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(respBody))),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	if meta.ID != "resp_67bd65392a088191a3b802a61f4fba14" {
		t.Errorf("ID = %q, want resp_67bd65392a088191a3b802a61f4fba14", meta.ID)
	}

	if meta.Model != "gpt-4o-2024-08-06" {
		t.Errorf("Model = %q, want gpt-4o-2024-08-06", meta.Model)
	}

	if meta.Usage.TotalTokens != 1370 {
		t.Errorf("TotalTokens = %d, want 1370", meta.Usage.TotalTokens)
	}

	output := meta.Custom["output"].([]ResponsesOutputItem)
	if len(output) != 3 {
		t.Fatalf("Output items = %d, want 3", len(output))
	}

	if output[0].Type != "message" || output[1].Type != "web_search_call" || output[2].Type != "message" {
		t.Errorf("Output types: %q, %q, %q - want message, web_search_call, message", output[0].Type, output[1].Type, output[2].Type)
	}

	annotations := output[2].Content[0].Annotations
	if len(annotations) != 2 {
		t.Errorf("Annotations count = %d, want 2", len(annotations))
	}

	if annotations[0].Title != "Cat miraculously survives 3 weeks trapped in sofa" {
		t.Errorf("First annotation title = %q", annotations[0].Title)
	}
}

func TestResponsesExtractor_MultipleTextSegments(t *testing.T) {
	respBody := `{
		"id": "resp_test",
		"object": "response",
		"model": "gpt-4o",
		"status": "completed",
		"output": [
			{
				"id": "msg_1",
				"type": "message",
				"role": "assistant",
				"content": [
					{"type": "output_text", "text": "First segment"}
				]
			},
			{
				"id": "ws_1",
				"type": "web_search_call",
				"status": "completed"
			},
			{
				"id": "msg_2",
				"type": "message",
				"role": "assistant",
				"content": [
					{"type": "output_text", "text": "Second segment"}
				]
			}
		],
		"usage": {"total_tokens": 100}
	}`

	extractor := &ResponsesExtractor{}
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(respBody))),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	expected := "First segment\nSecond segment"
	if meta.Choices[0].Message.Content != expected {
		t.Errorf("Content = %v, want %q", meta.Choices[0].Message.Content, expected)
	}
}

func TestResponsesExtractor_EmptyAnnotations(t *testing.T) {
	respBody := `{
		"id": "resp_test",
		"object": "response",
		"model": "gpt-4o",
		"status": "completed",
		"output": [
			{
				"id": "msg_1",
				"type": "message",
				"role": "assistant",
				"content": [
					{
						"type": "output_text", 
						"text": "Hello",
						"annotations": []
					}
				]
			}
		],
		"usage": {"total_tokens": 50}
	}`

	extractor := &ResponsesExtractor{}
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(respBody))),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	output := meta.Custom["output"].([]ResponsesOutputItem)
	if len(output[0].Content[0].Annotations) != 0 {
		t.Errorf("Expected empty annotations, got %d", len(output[0].Content[0].Annotations))
	}
}

func TestResponsesExtractor_LogprobsField(t *testing.T) {
	respBody := `{
		"id": "resp_test",
		"object": "response",
		"model": "gpt-4o",
		"status": "completed",
		"output": [
			{
				"id": "msg_1",
				"type": "message",
				"role": "assistant",
				"content": [
					{
						"type": "output_text", 
						"text": "Hello",
						"logprobs": null
					}
				]
			}
		],
		"usage": {"total_tokens": 50}
	}`

	extractor := &ResponsesExtractor{}
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(respBody))),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	output := meta.Custom["output"].([]ResponsesOutputItem)
	if output[0].Content[0].Logprobs != nil {
		t.Errorf("Logprobs should be nil, got %v", output[0].Content[0].Logprobs)
	}
}

func TestResponsesParser_InputWithContentArray(t *testing.T) {
	body := `{
		"model": "gpt-4o",
		"input": [
			{
				"role": "user", 
				"content": [
					{"type": "text", "text": "What's in this image?"},
					{"type": "image_url", "image_url": {"url": "https://example.com/cat.jpg"}}
				]
			}
		]
	}`

	parser := &ResponsesParser{}
	meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if len(meta.Messages) != 0 {
		t.Errorf("Expected 0 messages for array content (not string), got %d", len(meta.Messages))
	}
}

func TestResponsesParser_InputWithMixedMessages(t *testing.T) {
	body := `{
		"model": "gpt-4o",
		"input": [
			{"role": "system", "content": "You are helpful"},
			{"role": "user", "content": "Hello"},
			{"role": "assistant", "content": "Hi there"},
			{
				"role": "user", 
				"content": [
					{"type": "text", "text": "Describe this"},
					{"type": "image_url", "image_url": {"url": "data:image/png;base64,abc"}}
				]
			}
		]
	}`

	parser := &ResponsesParser{}
	meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if len(meta.Messages) != 3 {
		t.Errorf("Expected 3 messages with string content, got %d", len(meta.Messages))
	}
}

func TestResponsesExtractor_WithReasoningContent(t *testing.T) {
	respBody := `{
		"id": "resp_test",
		"object": "response",
		"model": "o1",
		"status": "completed",
		"output": [
			{
				"id": "msg_1",
				"type": "reasoning",
				"role": "assistant",
				"content": []
			},
			{
				"id": "msg_2",
				"type": "message",
				"role": "assistant",
				"content": [
					{"type": "output_text", "text": "The answer is 42"}
				]
			}
		],
		"usage": {"total_tokens": 100}
	}`

	extractor := &ResponsesExtractor{}
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(respBody))),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	if meta.Choices[0].Message.Content != "The answer is 42" {
		t.Errorf("Content = %v, want 'The answer is 42'", meta.Choices[0].Message.Content)
	}
}

func TestResponsesExtractor_ToolUse(t *testing.T) {
	respBody := `{
		"id": "resp_test",
		"object": "response",
		"model": "gpt-4o",
		"status": "completed",
		"output": [
			{
				"id": "fc_123",
				"type": "function_call",
				"status": "completed",
				"name": "get_weather",
				"arguments": "{\"location\":\"SF\"}"
			}
		],
		"usage": {"total_tokens": 50}
	}`

	extractor := &ResponsesExtractor{}
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(respBody))),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	output := meta.Custom["output"].([]ResponsesOutputItem)
	if len(output) != 1 {
		t.Fatalf("Expected 1 output item, got %d", len(output))
	}
	if output[0].Type != "function_call" {
		t.Errorf("Output type = %q, want function_call", output[0].Type)
	}
}

func TestResponsesExtractor_Error(t *testing.T) {
	respBody := `{
		"id": "resp_test",
		"object": "response",
		"model": "gpt-4o",
		"status": "failed",
		"error": {
			"type": "invalid_request_error",
			"code": "context_length_exceeded",
			"message": "The context length exceeds the maximum"
		},
		"output": [],
		"usage": {"total_tokens": 0}
	}`

	extractor := &ResponsesExtractor{}
	resp := &http.Response{
		StatusCode: 400,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(respBody))),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	if meta.Custom["status"] != "failed" {
		t.Errorf("Status = %v, want failed", meta.Custom["status"])
	}

	errObj, ok := meta.Custom["error"].(*ResponsesError)
	if !ok {
		t.Fatalf("Error should be *ResponsesError, got %T", meta.Custom["error"])
	}
	if errObj.Code != "context_length_exceeded" {
		t.Errorf("Error code = %q, want context_length_exceeded", errObj.Code)
	}
}

func TestResponsesParser_Tools(t *testing.T) {
	body := `{
		"model": "gpt-4o",
		"input": "Search for news",
		"tools": [
			{
				"type": "web_search",
				"location": null,
				"sites": null
			},
			{
				"type": "function",
				"name": "get_weather",
				"description": "Get weather info"
			}
		]
	}`

	parser := &ResponsesParser{}
	meta, _, err := parser.Parse(io.NopCloser(bytes.NewReader([]byte(body))))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	tools, ok := meta.Custom["tools"].([]interface{})
	if !ok {
		t.Fatalf("tools should be []interface{}, got %T", meta.Custom["tools"])
	}
	if len(tools) != 2 {
		t.Errorf("Tools count = %d, want 2", len(tools))
	}
}

func TestResponsesExtractor_ReasoningWithSummary(t *testing.T) {
	respBody := `{
  "id": "resp_6820f382ee1c8191bc096bee70894d040ac5ba57aafcbac7",
  "created_at": 1746989954.0,
  "error": null,
  "incomplete_details": null,
  "instructions": null,
  "metadata": {},
  "model": "o4-mini-2025-04-16",
  "object": "response",
  "output": [
    {
      "id": "rs_6820f383d7c08191846711c5df8233bc0ac5ba57aafcbac7",
      "summary": [],
      "type": "reasoning",
      "status": null
    },
    {
      "id": "msg_6820f3854688819187769ff582b170a60ac5ba57aafcbac7",
      "content": [
        {
          "annotations": [],
          "text": "Why don't scientists trust atoms? Because they make up everything!",
          "type": "output_text"
        }
      ],
      "role": "assistant",
      "status": "completed",
      "type": "message"
    }
  ],
  "parallel_tool_calls": true,
  "temperature": 1.0,
  "tool_choice": "auto",
  "tools": [],
  "top_p": 1.0,
  "max_output_tokens": null,
  "previous_response_id": null,
  "reasoning": {
    "effort": "medium",
    "generate_summary": null,
    "summary": null
  },
  "status": "completed",
  "text": {
    "format": {
      "type": "text"
    }
  },
  "truncation": "disabled",
  "usage": {
    "input_tokens": 10,
    "input_tokens_details": {
      "cached_tokens": 0
    },
    "output_tokens": 148,
    "output_tokens_details": {
      "reasoning_tokens": 128
    },
    "total_tokens": 158
  },
  "user": null,
  "service_tier": "default",
  "store": true
}`

	extractor := &ResponsesExtractor{}
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(respBody))),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	if meta.ID != "resp_6820f382ee1c8191bc096bee70894d040ac5ba57aafcbac7" {
		t.Errorf("ID = %q", meta.ID)
	}

	if meta.Model != "o4-mini-2025-04-16" {
		t.Errorf("Model = %q, want o4-mini-2025-04-16", meta.Model)
	}

	if meta.Usage.TotalTokens != 158 {
		t.Errorf("TotalTokens = %d, want 158", meta.Usage.TotalTokens)
	}

	if meta.Usage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10", meta.Usage.PromptTokens)
	}

	if meta.Usage.CompletionTokens != 148 {
		t.Errorf("CompletionTokens = %d, want 148", meta.Usage.CompletionTokens)
	}

	output := meta.Custom["output"].([]ResponsesOutputItem)
	if len(output) != 2 {
		t.Fatalf("Output items = %d, want 2", len(output))
	}

	if output[0].Type != "reasoning" {
		t.Errorf("First output type = %q, want reasoning", output[0].Type)
	}

	if output[1].Type != "message" {
		t.Errorf("Second output type = %q, want message", output[1].Type)
	}

	expectedContent := "Why don't scientists trust atoms? Because they make up everything!"
	if meta.Choices[0].Message.Content != expectedContent {
		t.Errorf("Content = %v, want %q", meta.Choices[0].Message.Content, expectedContent)
	}
}

func TestResponsesExtractor_ReasoningTokensInUsage(t *testing.T) {
	respBody := `{
		"id": "resp_test",
		"object": "response",
		"model": "o1",
		"status": "completed",
		"output": [
			{
				"id": "rs_123",
				"type": "reasoning",
				"summary": []
			},
			{
				"id": "msg_456",
				"type": "message",
				"role": "assistant",
				"content": [
					{"type": "output_text", "text": "The answer is 42"}
				]
			}
		],
		"usage": {
			"input_tokens": 50,
			"output_tokens": 200,
			"output_tokens_details": {
				"reasoning_tokens": 150
			},
			"total_tokens": 250
		}
	}`

	extractor := &ResponsesExtractor{}
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(respBody))),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	if meta.Usage.CompletionTokens != 200 {
		t.Errorf("CompletionTokens = %d, want 200", meta.Usage.CompletionTokens)
	}

	output := meta.Custom["output"].([]ResponsesOutputItem)
	if output[0].Type != "reasoning" {
		t.Errorf("First output should be reasoning, got %q", output[0].Type)
	}
}

func TestResponsesExtractor_CachedTokensInUsage(t *testing.T) {
	respBody := `{
		"id": "resp_test",
		"object": "response",
		"model": "gpt-4o",
		"status": "completed",
		"output": [
			{
				"id": "msg_123",
				"type": "message",
				"role": "assistant",
				"content": [
					{"type": "output_text", "text": "Hello"}
				]
			}
		],
		"usage": {
			"input_tokens": 100,
			"input_tokens_details": {
				"cached_tokens": 80
			},
			"output_tokens": 50,
			"total_tokens": 150
		}
	}`

	extractor := &ResponsesExtractor{}
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(respBody))),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	cacheUsage, ok := meta.Custom["cache_usage"].(llmproxy.CacheUsage)
	if !ok {
		t.Fatal("Expected cache_usage in Custom")
	}
	if cacheUsage.CachedTokens != 80 {
		t.Errorf("CachedTokens = %d, want 80", cacheUsage.CachedTokens)
	}
}

func TestResponsesExtractor_WithServiceTier(t *testing.T) {
	respBody := `{
		"id": "resp_test",
		"object": "response",
		"model": "gpt-4o",
		"status": "completed",
		"service_tier": "default",
		"store": true,
		"output": [
			{
				"id": "msg_123",
				"type": "message",
				"role": "assistant",
				"content": [
					{"type": "output_text", "text": "Hello"}
				]
			}
		],
		"usage": {"total_tokens": 50}
	}`

	extractor := &ResponsesExtractor{}
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(respBody))),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	if meta.Custom["status"] != "completed" {
		t.Errorf("Status = %v, want completed", meta.Custom["status"])
	}
}

func TestResponsesExtractor_WithReasoningEffort(t *testing.T) {
	respBody := `{
		"id": "resp_test",
		"object": "response",
		"model": "o4-mini",
		"status": "completed",
		"reasoning": {
			"effort": "medium",
			"generate_summary": null,
			"summary": null
		},
		"output": [
			{
				"id": "rs_123",
				"type": "reasoning",
				"summary": []
			},
			{
				"id": "msg_456",
				"type": "message",
				"role": "assistant",
				"content": [
					{"type": "output_text", "text": "Result"}
				]
			}
		],
		"usage": {"total_tokens": 100}
	}`

	extractor := &ResponsesExtractor{}
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(respBody))),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	output := meta.Custom["output"].([]ResponsesOutputItem)
	if len(output) != 2 {
		t.Errorf("Expected 2 output items, got %d", len(output))
	}
}

func TestResponsesExtractor_StatusInOutputMessage(t *testing.T) {
	respBody := `{
		"id": "resp_test",
		"object": "response",
		"model": "gpt-4o",
		"status": "completed",
		"output": [
			{
				"id": "msg_123",
				"type": "message",
				"role": "assistant",
				"status": "completed",
				"content": [
					{"type": "output_text", "text": "Hello"}
				]
			}
		],
		"usage": {"total_tokens": 50}
	}`

	extractor := &ResponsesExtractor{}
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(respBody))),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	if meta.Choices[0].FinishReason != "completed" {
		t.Errorf("FinishReason = %q, want completed", meta.Choices[0].FinishReason)
	}
}

func TestResponsesExtractor_OutputSummary(t *testing.T) {
	respBody := `{
		"id": "resp_test",
		"object": "response",
		"model": "o4-mini",
		"status": "completed",
		"output": [
			{
				"id": "rs_123",
				"type": "reasoning",
				"summary": [
					{"type": "summary_text", "text": "Analyzed the problem"}
				]
			},
			{
				"id": "msg_456",
				"type": "message",
				"role": "assistant",
				"content": [
					{"type": "output_text", "text": "The answer is 42"}
				]
			}
		],
		"usage": {"total_tokens": 100}
	}`

	extractor := &ResponsesExtractor{}
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(respBody))),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	output := meta.Custom["output"].([]ResponsesOutputItem)
	if output[0].Type != "reasoning" {
		t.Errorf("First output type = %q, want reasoning", output[0].Type)
	}
}
