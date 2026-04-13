package interceptors

import (
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentuity/llmproxy"
)

func TestBillingInterceptor_Success(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"chatcmpl-123","model":"gpt-4","usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}}`))
	}))
	defer upstream.Close()

	var result llmproxy.BillingResult
	lookup := func(provider, model string) (llmproxy.CostInfo, bool) {
		if model == "gpt-4" {
			return llmproxy.CostInfo{Input: 30, Output: 60}, true
		}
		return llmproxy.CostInfo{}, false
	}

	billing := NewBilling(lookup, func(r llmproxy.BillingResult) {
		result = r
	})

	req, _ := http.NewRequest("POST", upstream.URL, nil)
	meta := llmproxy.BodyMetadata{Model: "gpt-4"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body := []byte(`{"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}}`)
		respMeta := llmproxy.ResponseMetadata{
			Usage: llmproxy.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
		}
		return resp, respMeta, body, nil
	}

	_, _, _, err := billing.Intercept(req, meta, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}

	if result.Model != "gpt-4" {
		t.Errorf("Model = %q, want %q", result.Model, "gpt-4")
	}
	if result.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", result.PromptTokens)
	}
	if result.CompletionTokens != 50 {
		t.Errorf("CompletionTokens = %d, want 50", result.CompletionTokens)
	}

	expectedInputCost := 30.0 * 100 / 1_000_000
	expectedOutputCost := 60.0 * 50 / 1_000_000
	expectedTotal := expectedInputCost + expectedOutputCost

	epsilon := 1e-9
	if math.Abs(result.TotalCost-expectedTotal) > epsilon {
		t.Errorf("TotalCost = %f, want %f (diff: %e)", result.TotalCost, expectedTotal, math.Abs(result.TotalCost-expectedTotal))
	}
}

func TestBillingInterceptor_ModelNotFound(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	called := false
	lookup := func(provider, model string) (llmproxy.CostInfo, bool) {
		return llmproxy.CostInfo{}, false
	}

	billing := NewBilling(lookup, func(r llmproxy.BillingResult) {
		called = true
	})

	req, _ := http.NewRequest("POST", upstream.URL, nil)
	meta := llmproxy.BodyMetadata{Model: "unknown-model"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{Usage: llmproxy.Usage{PromptTokens: 100, CompletionTokens: 50}}, nil, nil
	}

	_, _, _, err := billing.Intercept(req, meta, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}

	if called {
		t.Error("OnResult should not be called when model not found")
	}
}

func TestBillingInterceptor_ErrorPassthrough(t *testing.T) {
	billing := NewBilling(nil, nil)

	req, _ := http.NewRequest("POST", "http://example.com", nil)
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		return nil, llmproxy.ResponseMetadata{}, nil, http.ErrHandlerTimeout
	}

	_, _, _, err := billing.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	if err != http.ErrHandlerTimeout {
		t.Errorf("Error should pass through, got %v", err)
	}
}

func TestBillingInterceptor_WithCacheUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	var result llmproxy.BillingResult
	lookup := func(provider, model string) (llmproxy.CostInfo, bool) {
		return llmproxy.CostInfo{Input: 3.0, Output: 15.0, CacheRead: 1.5}, true
	}

	billing := NewBilling(lookup, func(r llmproxy.BillingResult) {
		result = r
	})

	req, _ := http.NewRequest("POST", upstream.URL, nil)
	meta := llmproxy.BodyMetadata{Model: "gpt-4o"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		respMeta := llmproxy.ResponseMetadata{
			Usage:  llmproxy.Usage{PromptTokens: 2000, CompletionTokens: 100, TotalTokens: 2100},
			Custom: map[string]any{"cache_usage": llmproxy.CacheUsage{CachedTokens: 1920}},
		}
		return resp, respMeta, nil, nil
	}

	_, _, _, err := billing.Intercept(req, meta, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}

	if result.CachedTokens != 1920 {
		t.Errorf("CachedTokens = %d, want 1920", result.CachedTokens)
	}

	// 80 non-cached at $3/M, 1920 cached at $1.5/M
	expectedInput := 3.0 * 80 / 1_000_000
	expectedCached := 1.5 * 1920 / 1_000_000
	expectedOutput := 15.0 * 100 / 1_000_000

	if math.Abs(result.InputCost-expectedInput) > 1e-9 {
		t.Errorf("InputCost = %f, want %f", result.InputCost, expectedInput)
	}
	if math.Abs(result.CachedInputCost-expectedCached) > 1e-9 {
		t.Errorf("CachedInputCost = %f, want %f", result.CachedInputCost, expectedCached)
	}
	if math.Abs(result.TotalCost-(expectedInput+expectedCached+expectedOutput)) > 1e-9 {
		t.Errorf("TotalCost = %f, want %f", result.TotalCost, expectedInput+expectedCached+expectedOutput)
	}
}

func TestBillingInterceptor_CacheUsageZeroTokens(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	var result llmproxy.BillingResult
	lookup := func(provider, model string) (llmproxy.CostInfo, bool) {
		return llmproxy.CostInfo{Input: 3.0, Output: 15.0, CacheRead: 1.5}, true
	}

	billing := NewBilling(lookup, func(r llmproxy.BillingResult) {
		result = r
	})

	req, _ := http.NewRequest("POST", upstream.URL, nil)
	meta := llmproxy.BodyMetadata{Model: "gpt-4o"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		// Cache usage present but zero tokens
		respMeta := llmproxy.ResponseMetadata{
			Usage:  llmproxy.Usage{PromptTokens: 1000, CompletionTokens: 50},
			Custom: map[string]any{"cache_usage": llmproxy.CacheUsage{}},
		}
		return resp, respMeta, nil, nil
	}

	_, _, _, err := billing.Intercept(req, meta, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}

	if result.CachedTokens != 0 {
		t.Errorf("CachedTokens = %d, want 0", result.CachedTokens)
	}
	if result.CachedInputCost != 0 {
		t.Errorf("CachedInputCost = %f, want 0", result.CachedInputCost)
	}
	// All tokens at full input rate
	expectedInput := 3.0 * 1000 / 1_000_000
	if math.Abs(result.InputCost-expectedInput) > 1e-9 {
		t.Errorf("InputCost = %f, want %f", result.InputCost, expectedInput)
	}
}

func TestBillingInterceptor_NoCacheUsageInCustom(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	var result llmproxy.BillingResult
	lookup := func(provider, model string) (llmproxy.CostInfo, bool) {
		return llmproxy.CostInfo{Input: 3.0, Output: 15.0, CacheRead: 1.5}, true
	}

	billing := NewBilling(lookup, func(r llmproxy.BillingResult) {
		result = r
	})

	req, _ := http.NewRequest("POST", upstream.URL, nil)
	meta := llmproxy.BodyMetadata{Model: "gpt-4o"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		// No Custom map at all
		respMeta := llmproxy.ResponseMetadata{
			Usage: llmproxy.Usage{PromptTokens: 1000, CompletionTokens: 50},
		}
		return resp, respMeta, nil, nil
	}

	_, _, _, err := billing.Intercept(req, meta, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}

	if result.CachedTokens != 0 {
		t.Errorf("CachedTokens = %d, want 0", result.CachedTokens)
	}
	expectedInput := 3.0 * 1000 / 1_000_000
	if math.Abs(result.InputCost-expectedInput) > 1e-9 {
		t.Errorf("InputCost = %f, want %f", result.InputCost, expectedInput)
	}
}

func TestDetectProvider(t *testing.T) {
	tests := []struct {
		model    string
		expected string
	}{
		{"gpt-4", "openai"},
		{"gpt-3.5-turbo", "openai"},
		{"o1-preview", "openai"},
		{"o3-mini", "openai"},
		{"chatgpt-4o", "openai"},
		{"claude-3-opus", "anthropic"},
		{"claude-3-sonnet", "anthropic"},
		{"gemini-pro", "google"},
		{"gemini-1.5-flash", "google"},
		{"llama-3-70b", "groq"},
		{"mixtral-8x7b", "groq"},
		{"unknown-model", ""},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := detectProvider(tt.model)
			if got != tt.expected {
				t.Errorf("detectProvider(%q) = %q, want %q", tt.model, got, tt.expected)
			}
		})
	}
}
