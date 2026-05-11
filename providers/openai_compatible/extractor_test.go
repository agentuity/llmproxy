package openai_compatible

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/agentuity/llmproxy"
)

func TestExtractor_ReasoningTokens(t *testing.T) {
	body := `{
		"id": "chatcmpl-abc",
		"object": "chat.completion",
		"model": "o1",
		"usage": {
			"prompt_tokens": 75,
			"completion_tokens": 1186,
			"total_tokens": 1261,
			"completion_tokens_details": {
				"reasoning_tokens": 1024
			}
		},
		"choices": []
	}`

	extractor := NewExtractor()
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	if meta.Usage.PromptTokens != 75 {
		t.Errorf("PromptTokens = %d, want 75", meta.Usage.PromptTokens)
	}
	if meta.Usage.CompletionTokens != 1186 {
		t.Errorf("CompletionTokens = %d, want 1186", meta.Usage.CompletionTokens)
	}

	rt, ok := meta.Custom["reasoning_tokens"].(int)
	if !ok {
		t.Fatal("expected reasoning_tokens in custom metadata")
	}
	if rt != 1024 {
		t.Errorf("reasoning_tokens = %d, want 1024", rt)
	}
}

func TestExtractor_ImageGenerationUsage(t *testing.T) {
	body := `{
		"created": 1778342333,
		"data": [{"b64_json": "abc"}],
		"usage": {
			"input_tokens": 15,
			"input_tokens_details": {
				"image_tokens": 0,
				"text_tokens": 15
			},
			"output_tokens": 272,
			"output_tokens_details": {
				"image_tokens": 272,
				"text_tokens": 0
			},
			"total_tokens": 287
		}
	}`

	extractor := NewExtractor()
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if meta.Usage.PromptTokens != 15 {
		t.Errorf("PromptTokens = %d, want 15", meta.Usage.PromptTokens)
	}
	if meta.Usage.CompletionTokens != 272 {
		t.Errorf("CompletionTokens = %d, want 272", meta.Usage.CompletionTokens)
	}
	if meta.Usage.TotalTokens != 287 {
		t.Errorf("TotalTokens = %d, want 287", meta.Usage.TotalTokens)
	}
	if meta.MeteredUsage.GeneratedImages != 1 {
		t.Errorf("GeneratedImages = %d, want 1", meta.MeteredUsage.GeneratedImages)
	}
}

func TestExtractor_DurationUsage(t *testing.T) {
	body := `{"text":"You","usage":{"type":"duration","seconds":1}}`

	extractor := NewExtractor()
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if meta.MeteredUsage.InputAudioSeconds != 1 {
		t.Errorf("InputAudioSeconds = %f, want 1", meta.MeteredUsage.InputAudioSeconds)
	}
}

func TestExtractor_NonJSONResponsePassesThrough(t *testing.T) {
	body := []byte{0xff, 0xfb, 0x90, 0x64}

	extractor := NewExtractor()
	resp := &http.Response{
		StatusCode: 200,
		Header: http.Header{
			"Content-Type": []string{"audio/mpeg"},
		},
		Body: io.NopCloser(bytes.NewReader(body)),
	}

	meta, raw, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if !bytes.Equal(raw, body) {
		t.Fatalf("raw body = %v, want %v", raw, body)
	}
	if meta.Custom == nil {
		t.Fatal("expected custom metadata map")
	}
}

func TestExtractor_ReasoningTokensZero(t *testing.T) {
	body := `{
		"id": "chatcmpl-abc",
		"object": "chat.completion",
		"model": "gpt-4o",
		"usage": {
			"prompt_tokens": 10,
			"completion_tokens": 20,
			"total_tokens": 30,
			"completion_tokens_details": {
				"reasoning_tokens": 0
			}
		},
		"choices": []
	}`

	extractor := NewExtractor()
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	if _, ok := meta.Custom["reasoning_tokens"]; ok {
		t.Error("expected no reasoning_tokens when value is 0")
	}
}

func TestExtractor_CacheAndReasoningTokens(t *testing.T) {
	body := `{
		"id": "chatcmpl-abc",
		"object": "chat.completion",
		"model": "o1",
		"usage": {
			"prompt_tokens": 100,
			"completion_tokens": 500,
			"total_tokens": 600,
			"prompt_tokens_details": {
				"cached_tokens": 80
			},
			"completion_tokens_details": {
				"reasoning_tokens": 256
			}
		},
		"choices": []
	}`

	extractor := NewExtractor()
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
	}

	meta, _, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	cu, ok := meta.Custom["cache_usage"].(llmproxy.CacheUsage)
	if !ok {
		t.Fatal("expected cache_usage in custom metadata")
	}
	if cu.CachedTokens != 80 {
		t.Errorf("CachedTokens = %d, want 80", cu.CachedTokens)
	}

	rt, ok := meta.Custom["reasoning_tokens"].(int)
	if !ok {
		t.Fatal("expected reasoning_tokens in custom metadata")
	}
	if rt != 256 {
		t.Errorf("reasoning_tokens = %d, want 256", rt)
	}
}

func TestExtractor_ResponseMessageContentArray(t *testing.T) {
	body := `{
		"id": "chatcmpl-abc",
		"object": "chat.completion",
		"model": "magistral-medium-latest",
		"usage": {
			"prompt_tokens": 10,
			"completion_tokens": 2,
			"total_tokens": 12
		},
		"choices": [
			{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": [
						{"type": "text", "text": "O"},
						{"type": "text", "text": "K"}
					]
				},
				"finish_reason": "stop"
			}
		]
	}`

	extractor := NewExtractor()
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
	}

	meta, rawBody, err := extractor.Extract(resp)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if string(rawBody) != body {
		t.Fatal("expected raw response body to be preserved")
	}
	if len(meta.Choices) != 1 || meta.Choices[0].Message == nil {
		t.Fatalf("expected one message choice, got %#v", meta.Choices)
	}
	if meta.Choices[0].Message.Content != "OK" {
		t.Errorf("message content = %v, want OK", meta.Choices[0].Message.Content)
	}
}
