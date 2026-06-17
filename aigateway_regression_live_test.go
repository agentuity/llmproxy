//go:build integration

package llmproxy_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agentuity/llmproxy"
	"github.com/agentuity/llmproxy/providers/openai_compatible"
)

func TestLiveAIGatewayRegressionModels(t *testing.T) {
	if os.Getenv("LLMPROXY_LIVE_AIGATEWAY_REGRESSION") != "1" {
		t.Skip("set LLMPROXY_LIVE_AIGATEWAY_REGRESSION=1 to run live AI Gateway regression checks")
	}

	tests := []struct {
		name     string
		provider string
		baseURL  string
		envKey   string
		model    string
	}{
		{
			name:     "deepseek strips provider-prefixed model",
			provider: "deepseek",
			baseURL:  "https://api.deepseek.com",
			envKey:   "GATEWAY_DEEPSEEK_API_KEY",
			model:    "deepseek/deepseek-v4-pro",
		},
		{
			name:     "mistral accepts magistral structured response content",
			provider: "mistral",
			baseURL:  "https://api.mistral.ai",
			envKey:   "GATEWAY_MISTRAL_API_KEY",
			model:    "magistral-medium-latest",
		},
		{
			name:     "cohere strips provider-prefixed model",
			provider: "cohere",
			baseURL:  "https://api.cohere.com/compatibility",
			envKey:   "GATEWAY_COHERE_API_KEY",
			model:    "cohere/command-a-plus-05-2026",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiKey := os.Getenv(tt.envKey)
			if apiKey == "" {
				t.Skipf("set %s to run this live regression", tt.envKey)
			}

			provider, err := openai_compatible.New(tt.provider, apiKey, tt.baseURL)
			if err != nil {
				t.Fatalf("create provider: %v", err)
			}

			router := llmproxy.NewAutoRouter(
				llmproxy.WithAutoRouterDetector(llmproxy.ProviderDetectorFunc(func(hint llmproxy.ProviderHint) string {
					return tt.provider
				})),
				llmproxy.WithAutoRouterHTTPClient(&http.Client{Timeout: 60 * time.Second}),
			)
			router.RegisterProvider(provider)

			body := `{"model":"` + tt.model + `","max_tokens":16,"messages":[{"role":"user","content":"Reply with OK and nothing else."}]}`
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(body)))
			req.Header.Set("Content-Type", "application/json")

			resp, _, err := router.Forward(context.Background(), req)
			if err != nil {
				t.Fatalf("forward: %v", err)
			}
			defer resp.Body.Close()

			raw, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				t.Fatalf("status %d: %s", resp.StatusCode, truncateLiveRegressionBody(raw))
			}
		})
	}
}

func truncateLiveRegressionBody(body []byte) string {
	value := strings.TrimSpace(string(body))
	if len(value) <= 500 {
		return value
	}
	return value[:500] + "..."
}
