//go:build integration

package llmproxy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agentuity/llmproxy"
	"github.com/agentuity/llmproxy/providers/anthropic"
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

func TestLiveAnthropicMessagesStreamCompletes(t *testing.T) {
	if os.Getenv("LLMPROXY_LIVE_AIGATEWAY_REGRESSION") != "1" {
		t.Skip("set LLMPROXY_LIVE_AIGATEWAY_REGRESSION=1 to run live AI Gateway regression checks")
	}

	apiKey := firstNonEmptyLiveEnv("ANTHROPIC_API_KEY", "GATEWAY_ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("set ANTHROPIC_API_KEY or GATEWAY_ANTHROPIC_API_KEY to run this live regression")
	}

	model := os.Getenv("LLMPROXY_LIVE_ANTHROPIC_MODEL")
	if model == "" {
		model = "anthropic/claude-haiku-4-5-20251001"
	}

	provider, err := anthropic.New(apiKey)
	if err != nil {
		t.Fatalf("create anthropic provider: %v", err)
	}

	router := llmproxy.NewAutoRouter(
		llmproxy.WithAutoRouterDetector(llmproxy.ProviderDetectorFunc(func(hint llmproxy.ProviderHint) string {
			return "anthropic"
		})),
		llmproxy.WithAutoRouterHTTPClient(&http.Client{Timeout: 60 * time.Second}),
	)
	router.RegisterProvider(provider)

	body := `{"model":"` + model + `","stream":true,"max_tokens":512,"thinking":{"type":"disabled"},"messages":[{"role":"user","content":[{"type":"text","text":"Reply with GENESIS_DRIVER_SMOKE_OK and nothing else."}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("status %d: %s", resp.StatusCode, truncateLiveRegressionBody(raw))
	}

	assertAnthropicLiveStream(t, raw)
}

func TestLiveAgentuityAIGatewayAnthropicMessagesStreamCompletes(t *testing.T) {
	if os.Getenv("LLMPROXY_LIVE_AIGATEWAY_REGRESSION") != "1" {
		t.Skip("set LLMPROXY_LIVE_AIGATEWAY_REGRESSION=1 to run live AI Gateway regression checks")
	}

	apiKey := firstNonEmptyLiveEnv(
		"AGENTUITY_AIGATEWAY_KEY",
		"AIGATEWAY_API_KEY",
		"AGENTUITY_SDK_KEY",
		"AGENTUITY_CODER_API_KEY",
		"AGENTUITY_CLI_API_KEY",
		"AGENTUITY_CLI_KEY",
	)
	if apiKey == "" {
		t.Skip("set an Agentuity AI Gateway or SDK API key to run this live regression")
	}

	baseURL := firstNonEmptyLiveEnv("AGENTUITY_AIGATEWAY_URL", "AIGATEWAY_URL")
	if baseURL == "" {
		baseURL = "https://aigateway-usc.agentuity.cloud"
	}
	model := os.Getenv("LLMPROXY_LIVE_ANTHROPIC_MODEL")
	if model == "" {
		model = "anthropic/claude-haiku-4-5-20251001"
	}

	body := map[string]any{
		"model":      model,
		"stream":     true,
		"max_tokens": 512,
		"thinking": map[string]any{
			"type": "disabled",
		},
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": "Reply with GENESIS_DRIVER_SMOKE_OK and nothing else."},
				},
			},
		},
	}
	rawBody, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/messages", bytes.NewReader(rawBody))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("x-api-key", apiKey)
	if orgID := firstNonEmptyLiveEnv("AGENTUITY_AIGATEWAY_ORGID", "AGENTUITY_ORG_ID", "AGENTUITY_CLOUD_ORG_ID"); orgID != "" {
		req.Header.Set("x-agentuity-orgid", orgID)
	}

	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("status %d: %s", resp.StatusCode, truncateLiveRegressionBody(raw))
	}

	assertAnthropicLiveStream(t, raw)
}

func assertAnthropicLiveStream(t *testing.T, raw []byte) {
	t.Helper()
	summary := summarizeAnthropicLiveStream(raw)
	if summary.EventTypes["message_stop"] == 0 {
		t.Fatalf("stream did not include message_stop: %s\n%s", summary.String(), truncateLiveRegressionBody(raw))
	}
	if summary.TextDeltas == 0 {
		t.Fatalf("stream did not include any text deltas: %s\n%s", summary.String(), truncateLiveRegressionBody(raw))
	}
	if !strings.Contains(summary.Text, "GENESIS_DRIVER_SMOKE_OK") {
		t.Fatalf("stream text did not include expected sentinel: %s\ntext=%q\n%s", summary.String(), summary.Text, truncateLiveRegressionBody(raw))
	}
	if summary.ThinkingDeltas > 0 {
		t.Fatalf("stream included thinking_delta despite thinking disabled: %s\n%s", summary.String(), truncateLiveRegressionBody(raw))
	}
}

type anthropicLiveStreamSummary struct {
	EventTypes     map[string]int
	TextDeltas     int
	ThinkingDeltas int
	Text           string
}

func (s anthropicLiveStreamSummary) String() string {
	eventTypes := make([]string, 0, len(s.EventTypes))
	for eventType, count := range s.EventTypes {
		eventTypes = append(eventTypes, eventType+"="+fmtInt(count))
	}
	sort.Strings(eventTypes)
	return "events=[" + strings.Join(eventTypes, ",") + "] text_deltas=" + fmtInt(s.TextDeltas) + " thinking_deltas=" + fmtInt(s.ThinkingDeltas)
}

func summarizeAnthropicLiveStream(raw []byte) anthropicLiveStreamSummary {
	summary := anthropicLiveStreamSummary{
		EventTypes: make(map[string]int),
	}
	for _, block := range strings.Split(string(raw), "\n\n") {
		var data string
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "data:") {
				data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
		if data == "" {
			continue
		}
		var payload struct {
			Type  string `json:"type"`
			Delta struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				Thinking string `json:"thinking"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			continue
		}
		if payload.Type != "" {
			summary.EventTypes[payload.Type]++
		}
		switch payload.Delta.Type {
		case "text_delta":
			summary.TextDeltas++
			summary.Text += payload.Delta.Text
		case "thinking_delta":
			summary.ThinkingDeltas++
		}
	}
	return summary
}

func fmtInt(value int) string {
	return strconv.Itoa(value)
}

func truncateLiveRegressionBody(body []byte) string {
	value := strings.TrimSpace(string(body))
	if len(value) <= 500 {
		return value
	}
	return value[:500] + "..."
}

func firstNonEmptyLiveEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}
