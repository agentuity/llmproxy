package interceptors

import (
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

	if result.TotalCost != expectedTotal {
		t.Errorf("TotalCost = %f, want %f", result.TotalCost, expectedTotal)
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
