//go:build integration

package llmproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const defaultLiveModelsURL = "https://aigateway-usw.agentuity.cloud/models"

func TestLiveModelsSmoke(t *testing.T) {
	if os.Getenv("LLMPROXY_LIVE_MODEL_SMOKE") != "1" {
		t.Skip("set LLMPROXY_LIVE_MODEL_SMOKE=1 to run live model smoke tests")
	}

	modelsURL := envOrDefault("LLMPROXY_MODELS_URL", defaultLiveModelsURL)
	client := &http.Client{Timeout: envDuration("LLMPROXY_LIVE_MODEL_TIMEOUT", 60*time.Second)}

	models, err := fetchLiveModels(client, modelsURL)
	if err != nil {
		t.Fatalf("fetch models: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("models API returned no models")
	}

	if limit := envInt("LLMPROXY_LIVE_MODEL_LIMIT", 0); limit > 0 && limit < len(models) {
		models = models[:limit]
	}
	if allowlist := envSet("LLMPROXY_LIVE_MODEL_IDS"); len(allowlist) > 0 {
		models = filterLiveModels(models, allowlist)
		if len(models) == 0 {
			t.Fatalf("no models matched LLMPROXY_LIVE_MODEL_IDS")
		}
	}

	envByProvider, missing := liveProviderEnv(models)
	if len(missing) > 0 {
		t.Fatalf("missing provider env vars:\n%s", strings.Join(missing, "\n"))
	}

	concurrency := envInt("LLMPROXY_LIVE_MODEL_CONCURRENCY", 3)
	if concurrency < 1 {
		concurrency = 1
	}
	sem := make(chan struct{}, concurrency)

	for _, model := range models {
		model := model
		t.Run(model.ID, func(t *testing.T) {
			t.Parallel()
			if reason := liveSkipReason(model); reason != "" {
				t.Skip(reason)
			}
			sem <- struct{}{}
			defer func() { <-sem }()

			apiKey := envByProvider[model.ProviderName]
			if apiKey == "" {
				t.Fatalf("no API key resolved for provider %q", model.ProviderName)
			}

			req, err := newLiveModelRequest(t.Context(), model, apiKey)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				t.Fatalf("status %d: %s", resp.StatusCode, truncateForLog(body))
			}
			if err := validateLiveModelResponseShape(model, body); err != nil {
				t.Fatalf("invalid response shape: %v\nbody: %s", err, truncateForLog(body))
			}
		})
	}
}

type liveModelsResponse struct {
	Data map[string][]liveModel `json:"data"`
}

type liveModel struct {
	ID               string            `json:"id"`
	API              string            `json:"api"`
	InputModalities  []string          `json:"input_modalities"`
	OutputModalities []string          `json:"output_modalities"`
	Provider         liveModelProvider `json:"provider"`
	ProviderName     string            `json:"-"`
}

type liveModelProvider struct {
	Env []string `json:"env"`
	API string   `json:"api"`
}

func fetchLiveModels(client *http.Client, modelsURL string) ([]liveModel, error) {
	req, err := http.NewRequest(http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, truncateForLog(body))
	}

	var parsed liveModelsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}

	var models []liveModel
	for provider, providerModels := range parsed.Data {
		for _, model := range providerModels {
			model.ProviderName = provider
			models = append(models, model)
		}
	}
	slices.SortFunc(models, func(a, b liveModel) int {
		return strings.Compare(a.ID, b.ID)
	})
	return models, nil
}

func liveProviderEnv(models []liveModel) (map[string]string, []string) {
	envByProvider := make(map[string]string)
	missingByProvider := make(map[string][]string)

	for _, model := range models {
		if envByProvider[model.ProviderName] != "" {
			continue
		}
		envNames := liveEnvNames(model)
		for _, name := range envNames {
			if value := os.Getenv(name); value != "" {
				envByProvider[model.ProviderName] = value
				break
			}
		}
		if envByProvider[model.ProviderName] == "" {
			missingByProvider[model.ProviderName] = envNames
		}
	}

	var missing []string
	for provider, envNames := range missingByProvider {
		missing = append(missing, fmt.Sprintf("%s: set one of %s", provider, strings.Join(envNames, ", ")))
	}
	slices.Sort(missing)
	return envByProvider, missing
}

func liveEnvNames(model liveModel) []string {
	envNames := append([]string(nil), model.Provider.Env...)
	if model.ProviderName == "googleai" {
		envNames = append(envNames, "GOOGLE_AI_API_KEY")
	}
	slices.Sort(envNames)
	return slices.Compact(envNames)
}

func newLiveModelRequest(ctx context.Context, model liveModel, apiKey string) (*http.Request, error) {
	upstreamModel := stripLiveProviderPrefix(model.ID)
	endpoint, body, err := liveModelEndpointAndBody(model, upstreamModel)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	setLiveAuthHeaders(req, model.ProviderName, apiKey)
	return req, nil
}

func liveModelEndpointAndBody(model liveModel, upstreamModel string) (string, []byte, error) {
	if liveUseChatCompletions(model) {
		body := map[string]any{
			"model":      upstreamModel,
			"max_tokens": 16,
			"messages": []map[string]any{
				{"role": "user", "content": "hi"},
			},
		}
		return liveChatCompletionsURL(model), mustJSON(body), nil
	}
	if liveUseCompletions(model) {
		body := map[string]any{
			"model":      upstreamModel,
			"max_tokens": 16,
			"prompt":     "hi",
		}
		return liveCompletionsURL(model), mustJSON(body), nil
	}

	switch model.API {
	case "anthropic-messages":
		body := map[string]any{
			"model":      upstreamModel,
			"max_tokens": 16,
			"messages": []map[string]any{
				{"role": "user", "content": "hi"},
			},
		}
		return joinLiveURLPath(model.Provider.API, "v1", "messages"), mustJSON(body), nil
	case "google-generative-ai":
		body := map[string]any{
			"contents": []map[string]any{
				{
					"role": "user",
					"parts": []map[string]any{
						{"text": "hi"},
					},
				},
			},
			"generationConfig": map[string]any{
				"maxOutputTokens": 16,
			},
		}
		return joinLiveURLPath(model.Provider.API, "v1beta", "models", upstreamModel+":generateContent"), mustJSON(body), nil
	case "mistral-conversations", "openai-completions":
		body := map[string]any{
			"model":      upstreamModel,
			"max_tokens": 16,
			"messages": []map[string]any{
				{"role": "user", "content": "hi"},
			},
		}
		return liveChatCompletionsURL(model), mustJSON(body), nil
	case "openai-codex-responses", "openai-responses":
		body := map[string]any{
			"model":             upstreamModel,
			"input":             "hi",
			"max_output_tokens": liveMaxOutputTokens(model),
		}
		if liveIsOpenAIReasoningModel(model) {
			setLiveDefaultReasoningEffort(body, liveReasoningEffort(model))
		}
		return joinLiveURLPath(model.Provider.API, "v1", "responses"), mustJSON(body), nil
	default:
		return "", nil, fmt.Errorf("unsupported API %q for model %s", model.API, model.ID)
	}
}

func liveChatCompletionsURL(model liveModel) string {
	switch model.ProviderName {
	case "cohere":
		return joinLiveURLPath(model.Provider.API, "compatibility", "v1", "chat", "completions")
	case "deepseek":
		return joinLiveURLPath(model.Provider.API, "chat", "completions")
	case "perplexity":
		return joinLiveURLPath(model.Provider.API, "v1", "sonar")
	default:
		return joinLiveURLPath(model.Provider.API, "v1", "chat", "completions")
	}
}

func liveCompletionsURL(model liveModel) string {
	return joinLiveURLPath(model.Provider.API, "v1", "completions")
}

func setLiveAuthHeaders(req *http.Request, provider, apiKey string) {
	switch provider {
	case "anthropic":
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	case "googleai":
		req.Header.Set("x-goog-api-key", apiKey)
	default:
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
}

func validateLiveModelResponseShape(model liveModel, body []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return err
	}
	if raw["error"] != nil {
		return fmt.Errorf("response contains error: %v", raw["error"])
	}

	if liveUseChatCompletions(model) || liveUseCompletions(model) {
		if nonEmptyArray(raw["choices"]) {
			return nil
		}
		return fmt.Errorf("missing expected fields for %s", model.API)
	}

	switch model.API {
	case "anthropic-messages":
		if nonEmptyString(raw["id"]) && nonEmptyArray(raw["content"]) {
			return nil
		}
	case "google-generative-ai":
		if nonEmptyArray(raw["candidates"]) {
			return nil
		}
	case "mistral-conversations", "openai-completions":
		if nonEmptyArray(raw["choices"]) || nonEmptyArray(raw["outputs"]) {
			return nil
		}
	case "openai-codex-responses", "openai-responses":
		if nonEmptyString(raw["id"]) && (nonEmptyString(raw["output_text"]) || nonEmptyArray(raw["output"])) {
			return nil
		}
	default:
		return fmt.Errorf("unsupported API %q", model.API)
	}

	return fmt.Errorf("missing expected fields for %s", model.API)
}

func liveUseChatCompletions(model liveModel) bool {
	if model.ProviderName == "perplexity" {
		return true
	}
	return model.ProviderName == "openai" && strings.Contains(model.ID, "search-preview")
}

func liveUseCompletions(model liveModel) bool {
	return model.ProviderName == "openai" && strings.HasSuffix(model.ID, "-instruct")
}

func liveMaxOutputTokens(model liveModel) int {
	if liveIsOpenAIReasoningModel(model) {
		return 1024
	}
	return 16
}

func liveIsOpenAIReasoningModel(model liveModel) bool {
	return model.ProviderName == "openai" && (strings.Contains(model.ID, "codex") || strings.Contains(model.ID, "-pro"))
}

func liveReasoningEffort(model liveModel) string {
	if strings.Contains(model.ID, "-pro") {
		return "high"
	}
	return "low"
}

func setLiveDefaultReasoningEffort(body map[string]any, effort string) {
	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok {
		body["reasoning"] = map[string]any{"effort": effort}
		return
	}
	if _, exists := reasoning["effort"]; !exists {
		reasoning["effort"] = effort
	}
}

func liveSkipReason(model liveModel) string {
	switch {
	case strings.Contains(model.ID, "-tts"):
		return "model is TTS/audio-only and does not support a text response shape"
	case strings.Contains(model.ID, "mistral-embed"):
		return "embedding model is not callable through chat/completion smoke test"
	case strings.Contains(model.ID, "deep-research"):
		return "deep research model requires web_search_preview, mcp, or file_search tools"
	case strings.Contains(model.ID, "multi-agent"):
		return "multi-agent model is not callable through chat completions"
	default:
		return ""
	}
}

func stripLiveProviderPrefix(model string) string {
	idx := strings.Index(model, "/")
	if idx < 0 {
		return model
	}
	prefix := model[:idx]
	stripped := model[idx+1:]
	if strings.HasPrefix(stripped, prefix+"/") {
		stripped = strings.TrimPrefix(stripped, prefix+"/")
	}
	return stripped
}

func joinLiveURLPath(base string, elems ...string) string {
	u, err := url.Parse(base)
	if err != nil {
		panic(err)
	}
	if len(elems) > 0 && elems[0] == "v1" && strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/v1") {
		elems = elems[1:]
	}
	all := []string{strings.TrimRight(u.Path, "/")}
	all = append(all, elems...)
	u.Path = pathJoin(all...)
	u.RawQuery = ""
	return u.String()
}

func pathJoin(elems ...string) string {
	var parts []string
	for _, elem := range elems {
		elem = strings.Trim(elem, "/")
		if elem != "" {
			parts = append(parts, elem)
		}
	}
	return "/" + strings.Join(parts, "/")
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func nonEmptyString(v any) bool {
	s, ok := v.(string)
	return ok && s != ""
}

func nonEmptyArray(v any) bool {
	items, ok := v.([]any)
	return ok && len(items) > 0
}

func truncateForLog(body []byte) string {
	const max = 2048
	if len(body) <= max {
		return string(body)
	}
	return string(body[:max]) + "...<truncated>"
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envSet(name string) map[string]bool {
	value := os.Getenv(name)
	if value == "" {
		return nil
	}
	result := make(map[string]bool)
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			result[item] = true
		}
	}
	return result
}

func filterLiveModels(models []liveModel, allowlist map[string]bool) []liveModel {
	filtered := make([]liveModel, 0, len(allowlist))
	for _, model := range models {
		if allowlist[model.ID] {
			filtered = append(filtered, model)
		}
	}
	return filtered
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err == nil {
		return parsed
	}
	seconds, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}
