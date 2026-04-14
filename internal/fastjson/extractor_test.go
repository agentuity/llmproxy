package fastjson

import (
	"testing"

	"github.com/agentuity/llmproxy"
)

func TestUsageExtractor_ExtractOpenAI(t *testing.T) {
	tests := []struct {
		name               string
		input              string
		expectedPrompt     int
		expectedCompletion int
		expectedCached     int
	}{
		{
			name:               "basic usage",
			input:              `{"id":"test","usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}}`,
			expectedPrompt:     100,
			expectedCompletion: 50,
			expectedCached:     0,
		},
		{
			name:               "with cache",
			input:              `{"id":"test","usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":80}}}`,
			expectedPrompt:     100,
			expectedCompletion: 50,
			expectedCached:     80,
		},
		{
			name:               "no usage",
			input:              `{"id":"test"}`,
			expectedPrompt:     0,
			expectedCompletion: 0,
			expectedCached:     0,
		},
	}

	extractor := NewUsageExtractor()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage, cacheUsage, err := extractor.ExtractOpenAI([]byte(tt.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if usage.PromptTokens != tt.expectedPrompt {
				t.Errorf("expected prompt tokens %d, got %d", tt.expectedPrompt, usage.PromptTokens)
			}
			if usage.CompletionTokens != tt.expectedCompletion {
				t.Errorf("expected completion tokens %d, got %d", tt.expectedCompletion, usage.CompletionTokens)
			}

			if tt.expectedCached > 0 {
				if cacheUsage == nil {
					t.Error("expected cache usage, got nil")
				} else if cacheUsage.CachedTokens != tt.expectedCached {
					t.Errorf("expected cached tokens %d, got %d", tt.expectedCached, cacheUsage.CachedTokens)
				}
			}
		})
	}
}

func TestUsageExtractor_ExtractAnthropic(t *testing.T) {
	tests := []struct {
		name               string
		input              string
		expectedPrompt     int
		expectedCompletion int
		expectedCacheRead  int
	}{
		{
			name:               "basic usage",
			input:              `{"id":"test","usage":{"input_tokens":100,"output_tokens":50}}`,
			expectedPrompt:     100,
			expectedCompletion: 50,
			expectedCacheRead:  0,
		},
		{
			name:               "with cache",
			input:              `{"id":"test","usage":{"input_tokens":50,"output_tokens":100,"cache_read_input_tokens":2000,"cache_creation_input_tokens":500}}`,
			expectedPrompt:     50,
			expectedCompletion: 100,
			expectedCacheRead:  2000,
		},
		{
			name:               "no usage",
			input:              `{"id":"test"}`,
			expectedPrompt:     0,
			expectedCompletion: 0,
			expectedCacheRead:  0,
		},
	}

	extractor := NewUsageExtractor()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage, cacheUsage, err := extractor.ExtractAnthropic([]byte(tt.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if usage.PromptTokens != tt.expectedPrompt {
				t.Errorf("expected prompt tokens %d, got %d", tt.expectedPrompt, usage.PromptTokens)
			}
			if usage.CompletionTokens != tt.expectedCompletion {
				t.Errorf("expected completion tokens %d, got %d", tt.expectedCompletion, usage.CompletionTokens)
			}

			if tt.expectedCacheRead > 0 {
				if cacheUsage == nil {
					t.Error("expected cache usage, got nil")
				} else if cacheUsage.CacheReadInputTokens != tt.expectedCacheRead {
					t.Errorf("expected cache read tokens %d, got %d", tt.expectedCacheRead, cacheUsage.CacheReadInputTokens)
				}
			}
		})
	}
}

func BenchmarkUsageExtractor_ExtractOpenAI_Std(b *testing.B) {
	extractor := NewUsageExtractor()
	data := []byte(`{"id":"test","usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":80}}}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = extractor.ExtractOpenAI(data)
	}
}

func BenchmarkUsageExtractor_ExtractAnthropic_Std(b *testing.B) {
	extractor := NewUsageExtractor()
	data := []byte(`{"id":"test","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":2000}}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = extractor.ExtractAnthropic(data)
	}
}

func TestBillingCalculator(t *testing.T) {
	lookup := func(provider, model string) (llmproxy.CostInfo, bool) {
		if model == "gpt-4" {
			return llmproxy.CostInfo{Input: 30, Output: 60}, true
		}
		return llmproxy.CostInfo{}, false
	}

	var results []llmproxy.BillingResult
	onResult := func(r llmproxy.BillingResult) {
		results = append(results, r)
	}

	calculator := llmproxy.NewBillingCalculator(lookup, onResult)

	meta := llmproxy.BodyMetadata{
		Model:  "gpt-4",
		Custom: map[string]any{"provider": "openai"},
	}

	respMeta := &llmproxy.ResponseMetadata{
		Usage: llmproxy.Usage{
			PromptTokens:     100,
			CompletionTokens: 50,
		},
	}

	result := calculator.Calculate(meta, respMeta)

	if result == nil {
		t.Fatal("expected result, got nil")
	}

	if result.PromptTokens != 100 {
		t.Errorf("expected prompt tokens 100, got %d", result.PromptTokens)
	}
	if result.CompletionTokens != 50 {
		t.Errorf("expected completion tokens 50, got %d", result.CompletionTokens)
	}

	expectedCost := (30.0 * 100 / 1_000_000) + (60.0 * 50 / 1_000_000)
	if result.TotalCost != expectedCost {
		t.Errorf("expected cost %.6f, got %.6f", expectedCost, result.TotalCost)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 result callback, got %d", len(results))
	}

	if _, ok := respMeta.Custom["billing_result"]; !ok {
		t.Error("expected billing_result in custom map")
	}
}
