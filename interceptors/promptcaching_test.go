package interceptors

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentuity/llmproxy"
)

func TestPromptCachingInterceptor_AnthropicSystemString(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(`"cache_control"`)) {
			t.Error("Request body should contain cache_control")
		}
		if !bytes.Contains(body, []byte(`"ephemeral"`)) {
			t.Error("Request body should contain type ephemeral")
		}
		if !bytes.Contains(body, []byte(`"system"`)) {
			t.Error("Request body should contain system field")
		}
		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("Failed to parse request body: %v", err)
		}
		system, ok := req["system"].([]interface{})
		if !ok {
			t.Fatal("System should be an array")
		} else if len(system) != 1 {
			t.Fatalf("System array should have 1 block, got %d", len(system))
		}
		block, ok := system[0].(map[string]interface{})
		if !ok {
			t.Fatal("System block should be an object")
		}
		if block["text"] != "You are helpful." {
			t.Errorf("System block text = %q, want %q", block["text"], "You are helpful.")
		}
		if _, has := block["cache_control"]; !has {
			t.Error("System block should have cache_control directly on the content block")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewAnthropicPromptCaching(CacheRetentionDefault)

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"claude-3-opus","system":"You are helpful.","messages":[{"role":"user","content":"Hello"}]}`)))
	meta := llmproxy.BodyMetadata{Model: "claude-3-opus"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"claude-3-opus","system":"You are helpful.","messages":[{"role":"user","content":"Hello"}]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_AnthropicSystemArray(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("Failed to parse: %v", err)
		}
		system, ok := req["system"].([]interface{})
		if !ok {
			t.Fatal("System should be an array")
		}
		lastBlock, ok := system[len(system)-1].(map[string]interface{})
		if !ok {
			t.Fatal("Last block should be an object")
		}
		if _, has := lastBlock["cache_control"]; !has {
			t.Error("Last system block should have cache_control")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewAnthropicPromptCaching(CacheRetentionDefault)

	reqBody := `{"model":"claude-3-opus","system":[{"type":"text","text":"You are helpful."}],"messages":[{"role":"user","content":"Hello"}]}`
	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(reqBody)))
	meta := llmproxy.BodyMetadata{Model: "claude-3-opus"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(reqBody), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_AnthropicLastMessage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("Failed to parse: %v", err)
		}
		messages, ok := req["messages"].([]interface{})
		if !ok {
			t.Fatal("Messages should be an array")
		}
		lastMsg, ok := messages[len(messages)-1].(map[string]interface{})
		if !ok {
			t.Fatal("Last message should be an object")
		}
		content, ok := lastMsg["content"].([]interface{})
		if !ok {
			t.Fatal("Last message content should be an array")
		}
		lastBlock, ok := content[len(content)-1].(map[string]interface{})
		if !ok {
			t.Fatal("Last content block should be an object")
		}
		if _, has := lastBlock["cache_control"]; !has {
			t.Error("Last message content block should have cache_control")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewAnthropicPromptCaching(CacheRetentionDefault)

	reqBody := `{"model":"claude-3-opus","messages":[{"role":"user","content":"Hello"}]}`
	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(reqBody)))
	meta := llmproxy.BodyMetadata{Model: "claude-3-opus"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(reqBody), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_Anthropic1hRetention(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(`"ttl":"1h"`)) {
			t.Error("Request body should contain ttl 1h")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewAnthropicPromptCaching(CacheRetention1h)

	reqBody := `{"model":"claude-3-opus","system":"You are helpful.","messages":[{"role":"user","content":"Hello"}]}`
	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(reqBody)))
	meta := llmproxy.BodyMetadata{Model: "claude-3-opus"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(reqBody), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_OpenAIAddsCacheKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(`"prompt_cache_key"`)) {
			t.Error("Request body should contain prompt_cache_key")
		}
		if !bytes.Contains(body, []byte(`"my-cache-key"`)) {
			t.Error("Request body should contain the cache key value")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewOpenAIPromptCaching(CacheRetentionDefault, "my-cache-key")

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"gpt-4","messages":[]}`)))
	meta := llmproxy.BodyMetadata{Model: "gpt-4"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"gpt-4","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_OpenAI24hRetention(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(`"prompt_cache_retention":"24h"`)) {
			t.Error("Request body should contain prompt_cache_retention 24h")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewOpenAIPromptCaching(CacheRetention24h, "my-key")

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"gpt-5.1","messages":[]}`)))
	meta := llmproxy.BodyMetadata{Model: "gpt-5.1"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"gpt-5.1","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_OpenAICacheUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	var cacheUsage llmproxy.CacheUsage
	caching := NewOpenAIPromptCachingWithResult(CacheRetentionDefault, "test-key", func(u llmproxy.CacheUsage) {
		cacheUsage = u
	})

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"gpt-4","messages":[]}`)))
	meta := llmproxy.BodyMetadata{Model: "gpt-4"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		respMeta := llmproxy.ResponseMetadata{
			Usage: llmproxy.Usage{PromptTokens: 2006, CompletionTokens: 300},
			Custom: map[string]any{
				"cache_usage": llmproxy.CacheUsage{
					CachedTokens: 1920,
				},
			},
		}
		return resp, respMeta, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"gpt-4","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}

	if cacheUsage.CachedTokens != 1920 {
		t.Errorf("CachedTokens = %d, want 1920", cacheUsage.CachedTokens)
	}
}

func TestPromptCachingInterceptor_SkipsNonAnthropic(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"cache_control"`)) {
			t.Error("Request body should NOT contain cache_control for non-Anthropic model")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewAnthropicPromptCaching(CacheRetentionDefault)

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"gpt-4","messages":[]}`)))
	meta := llmproxy.BodyMetadata{Model: "gpt-4"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"gpt-4","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_SkipsNonOpenAI(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"prompt_cache_key"`)) {
			t.Error("Request body should NOT contain prompt_cache_key for non-OpenAI model")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewOpenAIPromptCaching(CacheRetentionDefault, "test-key")

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"claude-3-opus","messages":[]}`)))
	meta := llmproxy.BodyMetadata{Model: "claude-3-opus"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"claude-3-opus","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_AnthropicExistingCacheControl(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Count(body, []byte(`"cache_control"`)) > 1 {
			t.Error("Request body should not have additional cache_control added")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewAnthropicPromptCaching(CacheRetentionDefault)

	reqBody := `{"model":"claude-3-opus","system":[{"type":"text","text":"You are helpful.","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"Hello"}]}`
	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(reqBody)))
	meta := llmproxy.BodyMetadata{Model: "claude-3-opus"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(reqBody), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_OpenAIExistingCacheKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Count(body, []byte(`"prompt_cache_key"`)) > 1 {
			t.Error("Request body should not have duplicate prompt_cache_key")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewOpenAIPromptCaching(CacheRetentionDefault, "new-key")

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"gpt-4","prompt_cache_key":"existing-key","messages":[]}`)))
	meta := llmproxy.BodyMetadata{Model: "gpt-4"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"gpt-4","prompt_cache_key":"existing-key","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_Disabled(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"cache_control"`)) {
			t.Error("Request body should NOT contain cache_control when disabled")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewPromptCaching("anthropic", PromptCachingConfig{Enabled: false})

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"claude-3-opus","messages":[]}`)))
	meta := llmproxy.BodyMetadata{Model: "claude-3-opus"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"claude-3-opus","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_ErrorPassthrough(t *testing.T) {
	caching := NewAnthropicPromptCaching(CacheRetentionDefault)

	req, _ := http.NewRequest("POST", "http://example.com", nil)
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		return nil, llmproxy.ResponseMetadata{}, nil, http.ErrHandlerTimeout
	}

	_, _, _, err := caching.Intercept(req, llmproxy.BodyMetadata{Model: "claude-3-opus"}, []byte(`{"model":"claude-3-opus"}`), next)
	if err != http.ErrHandlerTimeout {
		t.Errorf("Error should pass through, got %v", err)
	}
}

func TestPromptCachingInterceptor_CacheControlNoCache(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"cache_control"`)) {
			t.Error("Request body should NOT contain cache_control when Cache-Control: no-cache is set")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewAnthropicPromptCaching(CacheRetentionDefault)

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"claude-3-opus","messages":[]}`)))
	req.Header.Set("Cache-Control", "no-cache")
	meta := llmproxy.BodyMetadata{Model: "claude-3-opus"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"claude-3-opus","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_CacheControlNoCacheOpenAI(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"prompt_cache_key"`)) {
			t.Error("Request body should NOT contain prompt_cache_key when Cache-Control: no-cache is set")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewOpenAIPromptCaching(CacheRetentionDefault, "my-key")

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"gpt-4","messages":[]}`)))
	req.Header.Set("Cache-Control", "no-cache")
	meta := llmproxy.BodyMetadata{Model: "gpt-4"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"gpt-4","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestIsOpenAIModel(t *testing.T) {
	tests := []struct {
		model    string
		expected bool
	}{
		{"gpt-4", true},
		{"gpt-3.5-turbo", true},
		{"gpt-5.1", true},
		{"gpt-5-codex", true},
		{"o1-preview", true},
		{"o3-mini", true},
		{"chatgpt-4o", true},
		{"claude-3-opus", false},
		{"gemini-pro", false},
		{"llama-3", false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			result := isOpenAIModel(tt.model)
			if result != tt.expected {
				t.Errorf("isOpenAIModel(%q) = %v, want %v", tt.model, result, tt.expected)
			}
		})
	}
}

func TestHasExistingCacheControl(t *testing.T) {
	caching := &PromptCachingInterceptor{config: PromptCachingConfig{Enabled: true}}

	tests := []struct {
		name     string
		req      map[string]interface{}
		expected bool
	}{
		{
			name:     "no cache_control",
			req:      map[string]interface{}{"model": "claude-3-opus"},
			expected: false,
		},
		{
			name: "cache_control in system array",
			req: map[string]interface{}{
				"system": []interface{}{
					map[string]interface{}{"type": "text", "text": "Hello", "cache_control": map[string]interface{}{"type": "ephemeral"}},
				},
			},
			expected: true,
		},
		{
			name: "cache_control in message content",
			req: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{
						"role": "user",
						"content": []interface{}{
							map[string]interface{}{"type": "text", "text": "Hi", "cache_control": map[string]interface{}{"type": "ephemeral"}},
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := caching.hasExistingCacheControl(tt.req)
			if result != tt.expected {
				t.Errorf("hasExistingCacheControl() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestBuildCacheControl(t *testing.T) {
	tests := []struct {
		name      string
		retention CacheRetention
		wantTTL   bool
	}{
		{
			name:      "default retention",
			retention: CacheRetentionDefault,
			wantTTL:   false,
		},
		{
			name:      "1h retention",
			retention: CacheRetention1h,
			wantTTL:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caching := &PromptCachingInterceptor{config: PromptCachingConfig{Enabled: true, Retention: tt.retention}}
			cc := caching.buildCacheControl()
			if cc["type"] != "ephemeral" {
				t.Error("cache_control should have type ephemeral")
			}
			if tt.wantTTL {
				if cc["ttl"] != "1h" {
					t.Error("cache_control should have ttl 1h")
				}
			} else {
				if _, has := cc["ttl"]; has {
					t.Error("cache_control should not have ttl for default retention")
				}
			}
		})
	}
}

func TestCheckOpenAI(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		cacheKey  string
		retention CacheRetention
		wantKey   bool
		wantRet   bool
	}{
		{
			name:      "with cache key only",
			input:     `{"model":"gpt-4","messages":[]}`,
			cacheKey:  "my-key",
			retention: "",
			wantKey:   true,
			wantRet:   false,
		},
		{
			name:      "with retention only",
			input:     `{"model":"gpt-4","messages":[]}`,
			cacheKey:  "",
			retention: CacheRetention24h,
			wantKey:   false,
			wantRet:   true,
		},
		{
			name:      "with both",
			input:     `{"model":"gpt-4","messages":[]}`,
			cacheKey:  "my-key",
			retention: CacheRetention24h,
			wantKey:   true,
			wantRet:   true,
		},
		{
			name:      "with neither",
			input:     `{"model":"gpt-4","messages":[]}`,
			cacheKey:  "",
			retention: "",
			wantKey:   false,
			wantRet:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caching := &PromptCachingInterceptor{
				provider: "openai",
				config: PromptCachingConfig{
					Enabled:   true,
					CacheKey:  tt.cacheKey,
					Retention: tt.retention,
				},
			}
			req, _ := http.NewRequest("POST", "http://example.com", bytes.NewReader([]byte(tt.input)))
			meta := llmproxy.BodyMetadata{Model: "gpt-4"}
			modified, shouldSkip := caching.checkOpenAI(req, meta, []byte(tt.input))

			if tt.wantKey || tt.wantRet {
				if shouldSkip {
					t.Error("checkOpenAI should return false when modifications are needed (should not skip)")
				}
			} else {
				if !shouldSkip {
					t.Error("checkOpenAI should return true when no modifications needed (should skip)")
				}
				return
			}

			if tt.wantKey {
				if !bytes.Contains(modified, []byte(`"prompt_cache_key"`)) {
					t.Error("Modified body should contain prompt_cache_key")
				}
			}
			if tt.wantRet {
				if !bytes.Contains(modified, []byte(`"prompt_cache_retention"`)) {
					t.Error("Modified body should contain prompt_cache_retention")
				}
			}
		})
	}
}

func TestPromptCachingInterceptor_OpenAICacheKeyHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("Failed to parse body: %v", err)
		}
		if req["prompt_cache_key"] != "header-key" {
			t.Errorf("prompt_cache_key = %v, want header-key", req["prompt_cache_key"])
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewOpenAIPromptCaching(CacheRetentionDefault, "config-key")

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"gpt-4","messages":[]}`)))
	req.Header.Set(HeaderCacheKey, "header-key")
	meta := llmproxy.BodyMetadata{Model: "gpt-4"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"gpt-4","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_OpenAINamespace(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("Failed to parse body: %v", err)
		}
		if req["prompt_cache_key"] != "tenant123:my-key" {
			t.Errorf("prompt_cache_key = %v, want tenant123:my-key", req["prompt_cache_key"])
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewOpenAIPromptCachingWithNamespace("tenant123", CacheRetentionDefault, "my-key")

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"gpt-4","messages":[]}`)))
	meta := llmproxy.BodyMetadata{Model: "gpt-4"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"gpt-4","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_OpenAIAutoDerive(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("Failed to parse body: %v", err)
		}
		key, ok := req["prompt_cache_key"].(string)
		if !ok || key == "" {
			t.Error("prompt_cache_key should be auto-derived and not empty")
		}
		if !strings.HasPrefix(key, "tenant:") {
			t.Errorf("prompt_cache_key should have namespace prefix, got %q", key)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewOpenAIPromptCachingAuto("tenant", CacheRetentionDefault)

	reqBody := `{"model":"gpt-4","system":"You are helpful.","messages":[{"role":"user","content":"Hello"}]}`
	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(reqBody)))
	meta := llmproxy.BodyMetadata{Model: "gpt-4"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(reqBody), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestDeriveCacheKeyFromPrefix(t *testing.T) {
	key1 := DeriveCacheKeyFromPrefix(llmproxy.BodyMetadata{}, []byte(`{"model":"gpt-4","system":"You are helpful.","messages":[{"role":"user","content":"Hello"}]}`))
	key2 := DeriveCacheKeyFromPrefix(llmproxy.BodyMetadata{}, []byte(`{"model":"gpt-4","system":"You are helpful.","messages":[{"role":"user","content":"Hello"}]}`))
	key3 := DeriveCacheKeyFromPrefix(llmproxy.BodyMetadata{}, []byte(`{"model":"gpt-4","system":"Different system.","messages":[{"role":"user","content":"Hello"}]}`))

	if key1 == "" {
		t.Error("DeriveCacheKeyFromPrefix should return non-empty key for valid input")
	}
	if key1 != key2 {
		t.Error("Same prefix should derive same key")
	}
	if key1 == key3 {
		t.Error("Different prefix should derive different key")
	}
}

func TestPromptCachingInterceptor_OpenAIExistingKeyInBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("Failed to parse body: %v", err)
		}
		if req["prompt_cache_key"] != "existing-key" {
			t.Errorf("prompt_cache_key = %v, want existing-key (should not be modified)", req["prompt_cache_key"])
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewOpenAIPromptCaching(CacheRetentionDefault, "new-key")

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"gpt-4","prompt_cache_key":"existing-key","messages":[]}`)))
	meta := llmproxy.BodyMetadata{Model: "gpt-4"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"gpt-4","prompt_cache_key":"existing-key","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_XAIAddsHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-grok-conv-id") != "my-conv-123" {
			t.Errorf("x-grok-conv-id header = %q, want my-conv-123", r.Header.Get("x-grok-conv-id"))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewXAIPromptCaching("my-conv-123")

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"grok-2-1212","messages":[]}`)))
	meta := llmproxy.BodyMetadata{Model: "grok-2-1212"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"grok-2-1212","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_XAISkipsNonXAI(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-grok-conv-id") != "" {
			t.Error("x-grok-conv-id header should NOT be set for non-xAI model")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewXAIPromptCaching("my-conv-123")

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"gpt-4","messages":[]}`)))
	meta := llmproxy.BodyMetadata{Model: "gpt-4"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"gpt-4","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_XAIExistingHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-grok-conv-id") != "existing-conv-id" {
			t.Errorf("x-grok-conv-id header = %q, want existing-conv-id", r.Header.Get("x-grok-conv-id"))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewXAIPromptCaching("new-conv-id")

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"grok-2-1212","messages":[]}`)))
	req.Header.Set("x-grok-conv-id", "existing-conv-id")
	meta := llmproxy.BodyMetadata{Model: "grok-2-1212"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"grok-2-1212","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_XAINoCacheKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-grok-conv-id") != "" {
			t.Error("x-grok-conv-id header should NOT be set when no cache key provided")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewXAIPromptCaching("")

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"grok-2-1212","messages":[]}`)))
	meta := llmproxy.BodyMetadata{Model: "grok-2-1212"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"grok-2-1212","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_XAICacheControlNoCache(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-grok-conv-id") != "" {
			t.Error("x-grok-conv-id header should NOT be set when Cache-Control: no-cache")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewXAIPromptCaching("my-conv-123")

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"grok-2-1212","messages":[]}`)))
	req.Header.Set("Cache-Control", "no-cache")
	meta := llmproxy.BodyMetadata{Model: "grok-2-1212"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"grok-2-1212","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestIsXAIModel(t *testing.T) {
	tests := []struct {
		model    string
		expected bool
	}{
		{"grok-2-1212", true},
		{"grok-3", true},
		{"grok-beta", true},
		{"grok-2-latest", true},
		{"gpt-4", false},
		{"claude-3-opus", false},
		{"gemini-pro", false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			result := isXAIModel(tt.model)
			if result != tt.expected {
				t.Errorf("isXAIModel(%q) = %v, want %v", tt.model, result, tt.expected)
			}
		})
	}
}

func TestPromptCachingInterceptor_OpenAIOrgIDFromHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("Failed to parse body: %v", err)
		}
		if req["prompt_cache_key"] != "org-abc:my-key" {
			t.Errorf("prompt_cache_key = %v, want org-abc:my-key", req["prompt_cache_key"])
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewOpenAIPromptCaching(CacheRetentionDefault, "my-key")

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"gpt-4","messages":[]}`)))
	req.Header.Set(HeaderOrgID, "org-abc")
	meta := llmproxy.BodyMetadata{Model: "gpt-4"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"gpt-4","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_OpenAIOrgIDFromMetaCustom(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("Failed to parse body: %v", err)
		}
		if req["prompt_cache_key"] != "tenant-xyz:my-key" {
			t.Errorf("prompt_cache_key = %v, want tenant-xyz:my-key", req["prompt_cache_key"])
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewOpenAIPromptCaching(CacheRetentionDefault, "my-key")

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"gpt-4","messages":[]}`)))
	meta := llmproxy.BodyMetadata{
		Model: "gpt-4",
		Custom: map[string]any{
			"org_id": "tenant-xyz",
		},
	}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"gpt-4","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_OpenAIOrgIDExtractor(t *testing.T) {
	customExtractor := func(ctx context.Context, req *http.Request, meta llmproxy.BodyMetadata) string {
		return "custom-org-123"
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("Failed to parse body: %v", err)
		}
		if req["prompt_cache_key"] != "custom-org-123:my-key" {
			t.Errorf("prompt_cache_key = %v, want custom-org-123:my-key", req["prompt_cache_key"])
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewOpenAIPromptCachingWithOrgExtractor(CacheRetentionDefault, "my-key", customExtractor)

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"gpt-4","messages":[]}`)))
	meta := llmproxy.BodyMetadata{Model: "gpt-4"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"gpt-4","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_OpenAICacheKeyHeaderOverridesOrgID(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("Failed to parse body: %v", err)
		}
		if req["prompt_cache_key"] != "org-abc:header-key" {
			t.Errorf("prompt_cache_key = %v, want org-abc:header-key", req["prompt_cache_key"])
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewOpenAIPromptCaching(CacheRetentionDefault, "config-key")

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"gpt-4","messages":[]}`)))
	req.Header.Set(HeaderOrgID, "org-abc")
	req.Header.Set(HeaderCacheKey, "header-key")
	meta := llmproxy.BodyMetadata{Model: "gpt-4"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"gpt-4","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestDefaultOrgIDExtractor(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*http.Request, *llmproxy.BodyMetadata)
		expected string
	}{
		{
			name:     "from header",
			setup:    func(req *http.Request, _ *llmproxy.BodyMetadata) { req.Header.Set(HeaderOrgID, "org-header") },
			expected: "org-header",
		},
		{
			name:     "from meta custom",
			setup:    func(_ *http.Request, meta *llmproxy.BodyMetadata) { meta.Custom = map[string]any{"org_id": "org-meta"} },
			expected: "org-meta",
		},
		{
			name:     "none",
			setup:    func(*http.Request, *llmproxy.BodyMetadata) {},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", "http://example.com", nil)
			meta := llmproxy.BodyMetadata{}
			tt.setup(req, &meta)
			result := DefaultOrgIDExtractor(req.Context(), req, meta)
			if result != tt.expected {
				t.Errorf("DefaultOrgIDExtractor() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestPromptCachingInterceptor_FireworksAddsHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(HeaderFireworksSessionAffinity) != "session-123" {
			t.Errorf("x-session-affinity header = %q, want session-123", r.Header.Get(HeaderFireworksSessionAffinity))
		}
		if r.Header.Get(HeaderFireworksPromptCacheIsolation) != "org-abc" {
			t.Errorf("x-prompt-cache-isolation-key header = %q, want org-abc", r.Header.Get(HeaderFireworksPromptCacheIsolation))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewFireworksPromptCachingWithOrgExtractor("session-123", func(ctx context.Context, req *http.Request, meta llmproxy.BodyMetadata) string {
		return "org-abc"
	})

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"accounts/fireworks/models/llama-v3-70b-instruct","messages":[]}`)))
	meta := llmproxy.BodyMetadata{Model: "accounts/fireworks/models/llama-v3-70b-instruct"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"accounts/fireworks/models/llama-v3-70b-instruct","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_FireworksSkipsNonFireworks(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(HeaderFireworksSessionAffinity) != "" {
			t.Error("x-session-affinity header should NOT be set for non-Fireworks model")
		}
		if r.Header.Get(HeaderFireworksPromptCacheIsolation) != "" {
			t.Error("x-prompt-cache-isolation-key header should NOT be set for non-Fireworks model")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewFireworksPromptCaching("session-123")

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"gpt-4","messages":[]}`)))
	meta := llmproxy.BodyMetadata{Model: "gpt-4"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"gpt-4","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_FireworksExistingHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(HeaderFireworksSessionAffinity) != "existing-session" {
			t.Errorf("x-session-affinity header = %q, want existing-session", r.Header.Get(HeaderFireworksSessionAffinity))
		}
		if r.Header.Get(HeaderFireworksPromptCacheIsolation) != "existing-org" {
			t.Errorf("x-prompt-cache-isolation-key header = %q, want existing-org", r.Header.Get(HeaderFireworksPromptCacheIsolation))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewFireworksPromptCachingWithOrgExtractor("new-session", func(ctx context.Context, req *http.Request, meta llmproxy.BodyMetadata) string {
		return "new-org"
	})

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"accounts/fireworks/models/llama-v3-70b-instruct","messages":[]}`)))
	req.Header.Set(HeaderFireworksSessionAffinity, "existing-session")
	req.Header.Set(HeaderFireworksPromptCacheIsolation, "existing-org")
	meta := llmproxy.BodyMetadata{Model: "accounts/fireworks/models/llama-v3-70b-instruct"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"accounts/fireworks/models/llama-v3-70b-instruct","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_FireworksNoSessionID(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(HeaderFireworksSessionAffinity) != "" {
			t.Error("x-session-affinity header should NOT be set when no session ID provided")
		}
		if r.Header.Get(HeaderFireworksPromptCacheIsolation) != "" {
			t.Error("x-prompt-cache-isolation-key should NOT be set when no org ID")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewFireworksPromptCaching("")

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"accounts/fireworks/models/llama-v3-70b-instruct","messages":[]}`)))
	meta := llmproxy.BodyMetadata{Model: "accounts/fireworks/models/llama-v3-70b-instruct"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"accounts/fireworks/models/llama-v3-70b-instruct","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_FireworksCacheUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("fireworks-prompt-tokens", "2006")
		w.Header().Set("fireworks-cached-prompt-tokens", "1920")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	var cacheUsage llmproxy.CacheUsage
	caching := NewFireworksPromptCachingWithResult("session-123", func(u llmproxy.CacheUsage) {
		cacheUsage = u
	})

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"accounts/fireworks/models/llama-v3-70b-instruct","messages":[]}`)))
	meta := llmproxy.BodyMetadata{Model: "accounts/fireworks/models/llama-v3-70b-instruct"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"accounts/fireworks/models/llama-v3-70b-instruct","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}

	if cacheUsage.CachedTokens != 1920 {
		t.Errorf("CachedTokens = %d, want 1920", cacheUsage.CachedTokens)
	}
}

func TestPromptCachingInterceptor_FireworksCacheControlNoCache(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(HeaderFireworksSessionAffinity) != "" {
			t.Error("x-session-affinity header should NOT be set when Cache-Control: no-cache")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewFireworksPromptCaching("session-123")

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"accounts/fireworks/models/llama-v3-70b-instruct","messages":[]}`)))
	req.Header.Set("Cache-Control", "no-cache")
	meta := llmproxy.BodyMetadata{Model: "accounts/fireworks/models/llama-v3-70b-instruct"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"accounts/fireworks/models/llama-v3-70b-instruct","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestIsFireworksModel(t *testing.T) {
	tests := []struct {
		model    string
		expected bool
	}{
		{"accounts/fireworks/models/llama-v3-70b-instruct", true},
		{"accounts/fireworks/models/qwen2p5-72b", true},
		{"fireworks-model", true},
		{"gpt-4", false},
		{"claude-3-opus", false},
		{"grok-2", false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			result := isFireworksModel(tt.model)
			if result != tt.expected {
				t.Errorf("isFireworksModel(%q) = %v, want %v", tt.model, result, tt.expected)
			}
		})
	}
}

func TestIsBedrockModel(t *testing.T) {
	tests := []struct {
		model    string
		expected bool
	}{
		{"anthropic.claude-3-sonnet-20240229-v1:0", true},
		{"anthropic.claude-3-opus-20240229-v1:0", true},
		{"anthropic.claude-opus-4-5-20251101-v1:0", true},
		{"amazon.nova-micro-v1:0", true},
		{"amazon.nova-lite-v1:0", true},
		{"amazon.nova-pro-v1:0", true},
		{"amazon.titan-text-express-v1", true},
		{"gpt-4", false},
		{"claude-3-opus", false},
		{"grok-2", false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			result := isBedrockModel(tt.model)
			if result != tt.expected {
				t.Errorf("isBedrockModel(%q) = %v, want %v", tt.model, result, tt.expected)
			}
		})
	}
}

func TestPromptCachingInterceptor_BedrockSystemCachePoint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("Failed to parse: %v", err)
		}
		system, ok := req["system"].([]interface{})
		if !ok {
			t.Fatal("System should be an array")
		}
		lastBlock, ok := system[len(system)-1].(map[string]interface{})
		if !ok {
			t.Fatal("Last block should be an object")
		}
		if cp, ok := lastBlock["cachePoint"].(map[string]interface{}); ok {
			if cp["type"] != "default" {
				t.Errorf("cachePoint type = %v, want default", cp["type"])
			}
		} else {
			t.Error("Last system block should have cachePoint")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewBedrockPromptCaching(CacheRetentionDefault)

	reqBody := `{"modelId":"anthropic.claude-3-sonnet-20240229-v1:0","system":[{"text":"You are helpful."}],"messages":[{"role":"user","content":[{"text":"Hello"}]}]}`
	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(reqBody)))
	meta := llmproxy.BodyMetadata{Model: "anthropic.claude-3-sonnet-20240229-v1:0"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(reqBody), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_BedrockMessagesCachePoint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("Failed to parse: %v", err)
		}
		messages, ok := req["messages"].([]interface{})
		if !ok {
			t.Fatal("Messages should be an array")
		}
		lastMsg, ok := messages[len(messages)-1].(map[string]interface{})
		if !ok {
			t.Fatal("Last message should be an object")
		}
		content, ok := lastMsg["content"].([]interface{})
		if !ok {
			t.Fatal("Last message content should be an array")
		}
		lastBlock, ok := content[len(content)-1].(map[string]interface{})
		if !ok {
			t.Fatal("Last content block should be an object")
		}
		if _, has := lastBlock["cachePoint"]; !has {
			t.Error("Last message content block should have cachePoint")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewBedrockPromptCaching(CacheRetentionDefault)

	reqBody := `{"modelId":"anthropic.claude-3-sonnet-20240229-v1:0","messages":[{"role":"user","content":[{"text":"Hello"}]}]}`
	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(reqBody)))
	meta := llmproxy.BodyMetadata{Model: "anthropic.claude-3-sonnet-20240229-v1:0"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(reqBody), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_BedrockToolsCachePoint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("Failed to parse: %v", err)
		}
		toolConfig, ok := req["toolConfig"].(map[string]interface{})
		if !ok {
			t.Fatal("toolConfig should be an object")
		}
		tools, ok := toolConfig["tools"].([]interface{})
		if !ok {
			t.Fatal("tools should be an array")
		}
		lastBlock, ok := tools[len(tools)-1].(map[string]interface{})
		if !ok {
			t.Fatal("Last tool block should be an object")
		}
		if _, has := lastBlock["cachePoint"]; !has {
			t.Error("Last tool block should have cachePoint")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewBedrockPromptCaching(CacheRetentionDefault)

	reqBody := `{"modelId":"anthropic.claude-3-sonnet-20240229-v1:0","messages":[{"role":"user","content":[{"text":"Hello"}]}],"toolConfig":{"tools":[{"toolSpec":{"name":"get_weather","description":"Get weather","inputSchema":{"json":{"type":"object"}}}}]}}`
	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(reqBody)))
	meta := llmproxy.BodyMetadata{Model: "anthropic.claude-3-sonnet-20240229-v1:0"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(reqBody), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_Bedrock1hRetention(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(`"ttl":"1h"`)) {
			t.Error("Request body should contain ttl 1h")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewBedrockPromptCaching(CacheRetention1h)

	reqBody := `{"modelId":"anthropic.claude-opus-4-5-20251101-v1:0","system":[{"text":"You are helpful."}],"messages":[{"role":"user","content":[{"text":"Hello"}]}]}`
	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(reqBody)))
	meta := llmproxy.BodyMetadata{Model: "anthropic.claude-opus-4-5-20251101-v1:0"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(reqBody), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_BedrockExistingCachePoint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Count(body, []byte(`"cachePoint"`)) > 1 {
			t.Error("Request body should not have additional cachePoint added")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewBedrockPromptCaching(CacheRetentionDefault)

	reqBody := `{"modelId":"anthropic.claude-3-sonnet-20240229-v1:0","system":[{"text":"You are helpful.","cachePoint":{"type":"default"}}],"messages":[{"role":"user","content":[{"text":"Hello"}]}]}`
	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(reqBody)))
	meta := llmproxy.BodyMetadata{Model: "anthropic.claude-3-sonnet-20240229-v1:0"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(reqBody), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_BedrockSkipsNonBedrock(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"cachePoint"`)) {
			t.Error("Request body should NOT contain cachePoint for non-Bedrock model")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewBedrockPromptCaching(CacheRetentionDefault)

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"gpt-4","messages":[]}`)))
	meta := llmproxy.BodyMetadata{Model: "gpt-4"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"gpt-4","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_BedrockCacheControlNoCache(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"cachePoint"`)) {
			t.Error("Request body should NOT contain cachePoint when Cache-Control: no-cache is set")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewBedrockPromptCaching(CacheRetentionDefault)

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"modelId":"anthropic.claude-3-sonnet-20240229-v1:0","messages":[]}`)))
	req.Header.Set("Cache-Control", "no-cache")
	meta := llmproxy.BodyMetadata{Model: "anthropic.claude-3-sonnet-20240229-v1:0"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"modelId":"anthropic.claude-3-sonnet-20240229-v1:0","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_BedrockCacheUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"usage":{"inputTokens":100,"outputTokens":50,"totalTokens":150,"cacheReadInputTokens":80,"cacheWriteInputTokens":20,"cacheDetails":[{"ttl":"5m","cacheWriteInputTokens":20}]}}`))
	}))
	defer upstream.Close()

	var cacheUsage llmproxy.CacheUsage
	caching := NewBedrockPromptCachingWithResult(CacheRetentionDefault, func(u llmproxy.CacheUsage) {
		cacheUsage = u
	})

	reqBody := `{"modelId":"anthropic.claude-3-sonnet-20240229-v1:0","system":[{"text":"You are helpful."}],"messages":[{"role":"user","content":[{"text":"Hello"}]}]}`
	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(reqBody)))
	meta := llmproxy.BodyMetadata{Model: "anthropic.claude-3-sonnet-20240229-v1:0"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		respMeta := llmproxy.ResponseMetadata{
			Custom: map[string]any{
				"cache_usage": llmproxy.CacheUsage{
					CachedTokens:     80,
					CacheWriteTokens: 20,
					CacheDetails: []llmproxy.CacheDetail{
						{TTL: "5m", CacheWriteTokens: 20},
					},
				},
			},
		}
		return resp, respMeta, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(reqBody), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}

	if cacheUsage.CachedTokens != 80 {
		t.Errorf("CachedTokens = %d, want 80", cacheUsage.CachedTokens)
	}
	if cacheUsage.CacheWriteTokens != 20 {
		t.Errorf("CacheWriteTokens = %d, want 20", cacheUsage.CacheWriteTokens)
	}
}

func TestPromptCachingInterceptor_XAIAutoDerive(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		convID := r.Header.Get("x-grok-conv-id")
		if convID == "" {
			t.Error("x-grok-conv-id header should be auto-derived")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewXAIPromptCachingAuto()

	reqBody := `{"model":"grok-2-1212","system":"You are helpful.","messages":[{"role":"user","content":"Hello"}]}`
	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(reqBody)))
	meta := llmproxy.BodyMetadata{Model: "grok-2-1212"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(reqBody), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_XAIWithTraceID(t *testing.T) {
	traceID := [16]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	expectedTraceIDHex := hex.EncodeToString(traceID[:])

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		convID := r.Header.Get("x-grok-conv-id")
		if convID != expectedTraceIDHex {
			t.Errorf("x-grok-conv-id = %q, want %q", convID, expectedTraceIDHex)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	traceExtractor := func(ctx context.Context) TraceInfo {
		return TraceInfo{TraceID: traceID}
	}

	caching := NewXAIPromptCachingWithTraceID(traceExtractor)

	reqBody := `{"model":"grok-2-1212","messages":[]}`
	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(reqBody)))
	meta := llmproxy.BodyMetadata{Model: "grok-2-1212"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(reqBody), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_FireworksAutoDerive(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.Header.Get(HeaderFireworksSessionAffinity)
		if sessionID == "" {
			t.Error("x-session-affinity header should be auto-derived")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewFireworksPromptCachingAuto()

	reqBody := `{"model":"accounts/fireworks/models/llama-v3-70b-instruct","system":"You are helpful.","messages":[{"role":"user","content":"Hello"}]}`
	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(reqBody)))
	meta := llmproxy.BodyMetadata{Model: "accounts/fireworks/models/llama-v3-70b-instruct"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(reqBody), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_FireworksWithTraceID(t *testing.T) {
	traceID := [16]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	expectedTraceIDHex := hex.EncodeToString(traceID[:])

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.Header.Get(HeaderFireworksSessionAffinity)
		if sessionID != expectedTraceIDHex {
			t.Errorf("x-session-affinity = %q, want %q", sessionID, expectedTraceIDHex)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	traceExtractor := func(ctx context.Context) TraceInfo {
		return TraceInfo{TraceID: traceID}
	}

	caching := NewFireworksPromptCachingWithTraceID(traceExtractor)

	reqBody := `{"model":"accounts/fireworks/models/llama-v3-70b-instruct","messages":[]}`
	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(reqBody)))
	meta := llmproxy.BodyMetadata{Model: "accounts/fireworks/models/llama-v3-70b-instruct"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(reqBody), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_XAICacheKeyHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		convID := r.Header.Get("x-grok-conv-id")
		if convID != "header-key" {
			t.Errorf("x-grok-conv-id = %q, want header-key", convID)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewXAIPromptCaching("config-key")

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"grok-2-1212","messages":[]}`)))
	req.Header.Set(HeaderCacheKey, "header-key")
	meta := llmproxy.BodyMetadata{Model: "grok-2-1212"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"grok-2-1212","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestPromptCachingInterceptor_FireworksCacheKeyHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.Header.Get(HeaderFireworksSessionAffinity)
		if sessionID != "header-key" {
			t.Errorf("x-session-affinity = %q, want header-key", sessionID)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	caching := NewFireworksPromptCaching("config-key")

	req, _ := http.NewRequest("POST", upstream.URL, bytes.NewReader([]byte(`{"model":"accounts/fireworks/models/llama-v3-70b-instruct","messages":[]}`)))
	req.Header.Set(HeaderCacheKey, "header-key")
	meta := llmproxy.BodyMetadata{Model: "accounts/fireworks/models/llama-v3-70b-instruct"}

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		return resp, llmproxy.ResponseMetadata{}, body, nil
	}

	_, _, _, err := caching.Intercept(req, meta, []byte(`{"model":"accounts/fireworks/models/llama-v3-70b-instruct","messages":[]}`), next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}
