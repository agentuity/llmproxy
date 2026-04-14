package llmproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type mockProvider struct {
	name      string
	parseFn   func(io.ReadCloser) (BodyMetadata, []byte, error)
	enrichFn  func(*http.Request, BodyMetadata, []byte) error
	resolveFn func(BodyMetadata) (*url.URL, error)
	extractFn func(*http.Response) (ResponseMetadata, []byte, error)
}

func (m *mockProvider) Name() string { return m.name }
func (m *mockProvider) BodyParser() BodyParser {
	return &mockBodyParser{parseFn: m.parseFn}
}
func (m *mockProvider) RequestEnricher() RequestEnricher {
	return &mockEnricher{enrichFn: m.enrichFn}
}
func (m *mockProvider) ResponseExtractor() ResponseExtractor {
	return &mockExtractor{extractFn: m.extractFn}
}
func (m *mockProvider) URLResolver() URLResolver {
	return &mockResolver{resolveFn: m.resolveFn}
}

type mockBodyParser struct {
	parseFn func(io.ReadCloser) (BodyMetadata, []byte, error)
}

func (m *mockBodyParser) Parse(body io.ReadCloser) (BodyMetadata, []byte, error) {
	return m.parseFn(body)
}

type mockEnricher struct {
	enrichFn func(*http.Request, BodyMetadata, []byte) error
}

func (m *mockEnricher) Enrich(req *http.Request, meta BodyMetadata, body []byte) error {
	return m.enrichFn(req, meta, body)
}

type mockResolver struct {
	resolveFn func(BodyMetadata) (*url.URL, error)
}

func (m *mockResolver) Resolve(meta BodyMetadata) (*url.URL, error) {
	return m.resolveFn(meta)
}

type mockExtractor struct {
	extractFn func(*http.Response) (ResponseMetadata, []byte, error)
}

func (m *mockExtractor) Extract(resp *http.Response) (ResponseMetadata, []byte, error) {
	return m.extractFn(resp)
}

type mockDetector struct{ detectFn func(ProviderHint) string }

func (m *mockDetector) Detect(hint ProviderHint) string { return m.detectFn(hint) }

func TestAutoRouter_Forward(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"test","model":"gpt-4","choices":[{"message":{"role":"assistant","content":"Hello"}}]}`))
	}))
	defer upstream.Close()

	provider := &mockProvider{
		name: "test-provider",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			return BodyMetadata{Model: "gpt-4"}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error {
			req.Header.Set("Authorization", "Bearer test-key")
			return nil
		},
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return ParseURL(upstream.URL + "/v1/chat/completions")
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			body, _ := io.ReadAll(resp.Body)
			return ResponseMetadata{ID: "test", Model: "gpt-4"}, body, nil
		},
	}

	detector := &mockDetector{
		detectFn: func(hint ProviderHint) string { return "test-provider" },
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(detector),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"Hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")

	resp, meta, err := router.Forward(context.Background(), req)
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}

	if meta.ID != "test" {
		t.Errorf("ID = %q, want test", meta.ID)
	}
}

func TestAutoRouter_NoProvider(t *testing.T) {
	detector := &mockDetector{
		detectFn: func(hint ProviderHint) string { return "" },
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(detector),
	)

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"unknown-model"}`)))

	_, _, err := router.Forward(context.Background(), req)
	if err == nil {
		t.Fatal("Forward() expected error, got nil")
	}
	if err != ErrNoProvider {
		t.Errorf("error = %v, want ErrNoProvider", err)
	}
}

func TestAutoRouter_FallbackProvider(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"fallback"}`))
	}))
	defer upstream.Close()

	fallback := &mockProvider{
		name: "fallback",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			return BodyMetadata{}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return ParseURL(upstream.URL)
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			body, _ := io.ReadAll(resp.Body)
			return ResponseMetadata{ID: "fallback"}, body, nil
		},
	}

	detector := &mockDetector{
		detectFn: func(hint ProviderHint) string { return "" },
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(detector),
		WithAutoRouterFallbackProvider(fallback),
	)

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"unknown"}`)))

	resp, meta, err := router.Forward(context.Background(), req)
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}

	if meta.ID != "fallback" {
		t.Errorf("ID = %q, want fallback", meta.ID)
	}
}

func TestAutoRouter_ServeHTTP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Custom", "value")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"test"}`))
	}))
	defer upstream.Close()

	provider := &mockProvider{
		name: "test",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			return BodyMetadata{}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return ParseURL(upstream.URL)
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			body, _ := io.ReadAll(resp.Body)
			return ResponseMetadata{}, body, nil
		},
	}

	detector := &mockDetector{
		detectFn: func(hint ProviderHint) string { return "test" },
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(detector),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", w.Code)
	}

	if w.Header().Get("X-Custom") != "value" {
		t.Errorf("X-Custom header = %q, want value", w.Header().Get("X-Custom"))
	}

	if w.Body.String() != `{"id":"test"}` {
		t.Errorf("Body = %q, want {\"id\":\"test\"}", w.Body.String())
	}
}

func ParseURL(s string) (*url.URL, error) {
	return url.Parse(s)
}

func TestStripProviderPrefix(t *testing.T) {
	tests := []struct {
		name          string
		model         string
		wantStripped  string
		wantHasPrefix bool
	}{
		{"no prefix", "gpt-4", "gpt-4", false},
		{"openai prefix", "openai/gpt-4", "gpt-4", true},
		{"anthropic prefix", "anthropic/claude-3-opus", "claude-3-opus", true},
		{"googleai prefix", "googleai/gemini-pro", "gemini-pro", true},
		{"groq prefix", "groq/llama-3-70b", "llama-3-70b", true},
		{"fireworks prefix", "fireworks/accounts/fireworks/models/llama", "accounts/fireworks/models/llama", true},
		{"xai prefix", "xai/grok-1", "grok-1", true},
		{"perplexity prefix", "perplexity/sonar-small", "sonar-small", true},
		{"bedrock prefix", "bedrock/anthropic.claude-3", "anthropic.claude-3", true},
		{"azure prefix", "azure/gpt-4-deployment", "gpt-4-deployment", true},
		{"multiple slashes preserved", "openai/gpt-4/turbo", "gpt-4/turbo", true},
		{"empty string", "", "", false},
		{"slash only - not a provider", "/", "/", false},
		{"openai slash at end", "openai/", "", true},
		{"non-provider prefix preserved", "accounts/fireworks/models/llama", "accounts/fireworks/models/llama", false},
		{"unknown prefix", "unknown/model-name", "unknown/model-name", false},
		{"model with slash not stripped", "some/path/to/model", "some/path/to/model", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stripped, hasPrefix := stripProviderPrefix(tt.model)
			if stripped != tt.wantStripped {
				t.Errorf("stripProviderPrefix(%q) stripped = %q, want %q", tt.model, stripped, tt.wantStripped)
			}
			if hasPrefix != tt.wantHasPrefix {
				t.Errorf("stripProviderPrefix(%q) hasPrefix = %v, want %v", tt.model, hasPrefix, tt.wantHasPrefix)
			}
		})
	}
}

func TestAutoRouter_StripsProviderPrefixFromBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)
		model := req["model"].(string)
		if strings.Contains(model, "/") {
			t.Errorf("model sent to upstream contains slash: %q", model)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"test","model":"gpt-4","choices":[]}`))
	}))
	defer upstream.Close()

	provider := &mockProvider{
		name: "openai",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			return BodyMetadata{Model: "gpt-4"}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error {
			req.Header.Set("Authorization", "Bearer test-key")
			return nil
		},
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return url.Parse(upstream.URL + "/v1/chat/completions")
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			body, _ := io.ReadAll(resp.Body)
			return ResponseMetadata{ID: "test"}, body, nil
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string {
			return "openai"
		})),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"Hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")

	resp, _, err := router.Forward(context.Background(), req)
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
}

func TestAutoRouter_PreservesModelWithoutPrefix(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)
		model := req["model"].(string)
		if model != "gpt-4" {
			t.Errorf("model sent to upstream = %q, want gpt-4", model)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"test","model":"gpt-4","choices":[]}`))
	}))
	defer upstream.Close()

	provider := &mockProvider{
		name: "openai",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			return BodyMetadata{Model: "gpt-4"}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return url.Parse(upstream.URL + "/v1/chat/completions")
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			body, _ := io.ReadAll(resp.Body)
			return ResponseMetadata{ID: "test"}, body, nil
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string {
			return "openai"
		})),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"Hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")

	resp, _, err := router.Forward(context.Background(), req)
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
}
