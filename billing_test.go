package llmproxy

import (
	"math"
	"testing"
)

const epsilon = 1e-9

func assertFloat(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > epsilon {
		t.Errorf("%s = %f, want %f (diff: %e)", name, got, want, math.Abs(got-want))
	}
}

func TestCalculateCost_NoCacheUsage(t *testing.T) {
	costInfo := CostInfo{Input: 3.0, Output: 15.0, CacheRead: 1.5}
	result := CalculateCost("openai", "gpt-4o", costInfo, 1000, 500, nil)

	if result.Provider != "openai" {
		t.Errorf("Provider = %q, want %q", result.Provider, "openai")
	}
	if result.Model != "gpt-4o" {
		t.Errorf("Model = %q, want %q", result.Model, "gpt-4o")
	}
	if result.PromptTokens != 1000 {
		t.Errorf("PromptTokens = %d, want 1000", result.PromptTokens)
	}
	if result.CompletionTokens != 500 {
		t.Errorf("CompletionTokens = %d, want 500", result.CompletionTokens)
	}
	if result.CachedTokens != 0 {
		t.Errorf("CachedTokens = %d, want 0", result.CachedTokens)
	}
	if result.TotalTokens != 1500 {
		t.Errorf("TotalTokens = %d, want 1500", result.TotalTokens)
	}

	expectedInput := 3.0 * 1000 / 1_000_000
	expectedOutput := 15.0 * 500 / 1_000_000
	assertFloat(t, "InputCost", result.InputCost, expectedInput)
	assertFloat(t, "CachedInputCost", result.CachedInputCost, 0)
	assertFloat(t, "OutputCost", result.OutputCost, expectedOutput)
	assertFloat(t, "TotalCost", result.TotalCost, expectedInput+expectedOutput)
}

func TestCalculateCost_WithOpenAICacheHit(t *testing.T) {
	costInfo := CostInfo{Input: 3.0, Output: 15.0, CacheRead: 1.5}
	cacheUsage := &CacheUsage{CachedTokens: 800}

	result := CalculateCost("openai", "gpt-4o", costInfo, 1000, 500, cacheUsage)

	if result.CachedTokens != 800 {
		t.Errorf("CachedTokens = %d, want 800", result.CachedTokens)
	}

	// 200 non-cached at full rate, 800 cached at cache rate
	expectedInput := 3.0 * 200 / 1_000_000
	expectedCached := 1.5 * 800 / 1_000_000
	expectedOutput := 15.0 * 500 / 1_000_000

	assertFloat(t, "InputCost", result.InputCost, expectedInput)
	assertFloat(t, "CachedInputCost", result.CachedInputCost, expectedCached)
	assertFloat(t, "OutputCost", result.OutputCost, expectedOutput)
	assertFloat(t, "TotalCost", result.TotalCost, expectedInput+expectedCached+expectedOutput)
}

func TestCalculateCost_WithAnthropicCacheHit(t *testing.T) {
	costInfo := CostInfo{Input: 3.0, Output: 15.0, CacheRead: 0.3}
	cacheUsage := &CacheUsage{CacheReadInputTokens: 2000}

	result := CalculateCost("anthropic", "claude-sonnet-4", costInfo, 2500, 100, cacheUsage)

	if result.CachedTokens != 2000 {
		t.Errorf("CachedTokens = %d, want 2000", result.CachedTokens)
	}

	// 500 non-cached at full rate, 2000 cached at cache rate
	expectedInput := 3.0 * 500 / 1_000_000
	expectedCached := 0.3 * 2000 / 1_000_000
	expectedOutput := 15.0 * 100 / 1_000_000

	assertFloat(t, "InputCost", result.InputCost, expectedInput)
	assertFloat(t, "CachedInputCost", result.CachedInputCost, expectedCached)
	assertFloat(t, "TotalCost", result.TotalCost, expectedInput+expectedCached+expectedOutput)
}

func TestCalculateCost_CacheUsageWithZeroTokens(t *testing.T) {
	costInfo := CostInfo{Input: 3.0, Output: 15.0, CacheRead: 1.5}
	// CacheUsage present but all fields are zero
	cacheUsage := &CacheUsage{}

	result := CalculateCost("openai", "gpt-4o", costInfo, 1000, 500, cacheUsage)

	// Should behave exactly like no cache usage
	if result.CachedTokens != 0 {
		t.Errorf("CachedTokens = %d, want 0", result.CachedTokens)
	}
	assertFloat(t, "CachedInputCost", result.CachedInputCost, 0)

	expectedInput := 3.0 * 1000 / 1_000_000
	expectedOutput := 15.0 * 500 / 1_000_000
	assertFloat(t, "InputCost", result.InputCost, expectedInput)
	assertFloat(t, "TotalCost", result.TotalCost, expectedInput+expectedOutput)
}

func TestCalculateCost_CacheUsageExceedsPromptTokens(t *testing.T) {
	costInfo := CostInfo{Input: 3.0, Output: 15.0, CacheRead: 1.5}
	// More cached tokens than prompt tokens (shouldn't happen, but defensive)
	cacheUsage := &CacheUsage{CachedTokens: 5000}

	result := CalculateCost("openai", "gpt-4o", costInfo, 1000, 500, cacheUsage)

	// Cached tokens should be clamped to prompt tokens
	if result.CachedTokens != 1000 {
		t.Errorf("CachedTokens = %d, want 1000 (clamped)", result.CachedTokens)
	}

	// All prompt tokens at cache rate, none at full rate
	assertFloat(t, "InputCost", result.InputCost, 0)
	expectedCached := 1.5 * 1000 / 1_000_000
	assertFloat(t, "CachedInputCost", result.CachedInputCost, expectedCached)
}

func TestCalculateCost_NoCacheReadPrice(t *testing.T) {
	// Provider doesn't have cache pricing — should fall back to full input rate
	costInfo := CostInfo{Input: 3.0, Output: 15.0}
	cacheUsage := &CacheUsage{CachedTokens: 800}

	result := CalculateCost("groq", "llama-3.3-70b", costInfo, 1000, 500, cacheUsage)

	if result.CachedTokens != 800 {
		t.Errorf("CachedTokens = %d, want 800", result.CachedTokens)
	}

	// Cached tokens should fall back to full input rate
	expectedInput := 3.0 * 200 / 1_000_000
	expectedCached := 3.0 * 800 / 1_000_000 // same as input rate
	expectedOutput := 15.0 * 500 / 1_000_000

	assertFloat(t, "InputCost", result.InputCost, expectedInput)
	assertFloat(t, "CachedInputCost", result.CachedInputCost, expectedCached)
	assertFloat(t, "TotalCost", result.TotalCost, expectedInput+expectedCached+expectedOutput)
}

func TestCalculateCost_AllTokensCached(t *testing.T) {
	costInfo := CostInfo{Input: 3.0, Output: 15.0, CacheRead: 1.5}
	cacheUsage := &CacheUsage{CachedTokens: 1000}

	result := CalculateCost("openai", "gpt-4o", costInfo, 1000, 500, cacheUsage)

	if result.CachedTokens != 1000 {
		t.Errorf("CachedTokens = %d, want 1000", result.CachedTokens)
	}

	// All prompt tokens cached — zero non-cached input cost
	assertFloat(t, "InputCost", result.InputCost, 0)
	expectedCached := 1.5 * 1000 / 1_000_000
	assertFloat(t, "CachedInputCost", result.CachedInputCost, expectedCached)
}

func TestCalculateCost_ZeroTokens(t *testing.T) {
	costInfo := CostInfo{Input: 3.0, Output: 15.0, CacheRead: 1.5}
	result := CalculateCost("openai", "gpt-4o", costInfo, 0, 0, nil)

	assertFloat(t, "InputCost", result.InputCost, 0)
	assertFloat(t, "CachedInputCost", result.CachedInputCost, 0)
	assertFloat(t, "OutputCost", result.OutputCost, 0)
	assertFloat(t, "TotalCost", result.TotalCost, 0)
}

func TestCalculateCost_MixedProviderCacheFields(t *testing.T) {
	// Both CachedTokens and CacheReadInputTokens set (shouldn't happen, but test summing)
	costInfo := CostInfo{Input: 3.0, Output: 15.0, CacheRead: 1.5}
	cacheUsage := &CacheUsage{
		CachedTokens:         300,
		CacheReadInputTokens: 200,
	}

	result := CalculateCost("test", "model", costInfo, 1000, 100, cacheUsage)

	// Should sum both fields: 300 + 200 = 500
	if result.CachedTokens != 500 {
		t.Errorf("CachedTokens = %d, want 500", result.CachedTokens)
	}

	expectedInput := 3.0 * 500 / 1_000_000
	expectedCached := 1.5 * 500 / 1_000_000
	assertFloat(t, "InputCost", result.InputCost, expectedInput)
	assertFloat(t, "CachedInputCost", result.CachedInputCost, expectedCached)
}
