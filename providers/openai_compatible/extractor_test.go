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
