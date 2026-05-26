package llmproxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
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

type mockStreamingProvider struct {
	*mockProvider
	streamingExtractor *mockStreamingExtractor
}

func (m *mockStreamingProvider) ResponseExtractor() ResponseExtractor {
	return m.streamingExtractor
}

type mockStreamingExtractor struct {
	isStreaming        bool
	extractStreamingFn func(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (ResponseMetadata, error)
}

func (m *mockStreamingExtractor) Extract(resp *http.Response) (ResponseMetadata, []byte, error) {
	body, _ := io.ReadAll(resp.Body)
	return ResponseMetadata{}, body, nil
}

func (m *mockStreamingExtractor) IsStreamingResponse(resp *http.Response) bool {
	return m.isStreaming
}

func (m *mockStreamingExtractor) ExtractStreamingWithController(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (ResponseMetadata, error) {
	if m.extractStreamingFn != nil {
		return m.extractStreamingFn(resp, w, rc)
	}
	return ResponseMetadata{}, nil
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

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"Hello"}]}`)))
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

func TestAutoRouter_ForwardForcesIdentityAcceptEncoding(t *testing.T) {
	var upstreamAcceptEncoding string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAcceptEncoding = r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Type", "application/json")
		if upstreamAcceptEncoding != "identity" {
			w.Header().Set("Content-Encoding", "gzip")
			zw := gzip.NewWriter(w)
			_, _ = zw.Write([]byte(`{"id":"test","model":"gpt-4","choices":[]}`))
			_ = zw.Close()
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"test","model":"gpt-4","choices":[]}`))
	}))
	defer upstream.Close()

	provider := &mockProvider{
		name: "test-provider",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			return BodyMetadata{Model: "gpt-4"}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error {
			req.Header.Set("Accept-Encoding", "gzip")
			return nil
		},
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return ParseURL(upstream.URL + "/v1/chat/completions")
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return ResponseMetadata{}, nil, err
			}
			var raw map[string]any
			if err := json.Unmarshal(body, &raw); err != nil {
				return ResponseMetadata{}, nil, err
			}
			id, _ := raw["id"].(string)
			return ResponseMetadata{ID: id}, body, nil
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string {
			return "test-provider"
		})),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"Hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")

	resp, meta, err := router.Forward(context.Background(), req)
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if meta.ID != "test" {
		t.Errorf("ID = %q, want test", meta.ID)
	}
	if upstreamAcceptEncoding != "identity" {
		t.Errorf("upstream Accept-Encoding = %q, want identity", upstreamAcceptEncoding)
	}
}

func TestAutoRouter_ForwardPassesThroughUpstreamErrorBody(t *testing.T) {
	upstreamBody := `{"error":{"message":"upstream rejected request"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(upstreamBody))
	}))
	defer upstream.Close()

	extractorCalled := false
	provider := &mockProvider{
		name: "test-provider",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			return BodyMetadata{Model: "gpt-4"}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return ParseURL(upstream.URL + "/v1/chat/completions")
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			extractorCalled = true
			return ResponseMetadata{}, nil, nil
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string {
			return "test-provider"
		})),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-4"}`)))
	resp, meta, err := router.Forward(context.Background(), req)
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if extractorCalled {
		t.Fatal("response extractor should not be called for upstream error responses")
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("StatusCode = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != upstreamBody {
		t.Fatalf("body = %q, want %q", string(body), upstreamBody)
	}
	if meta.ID != "" || meta.Model != "" {
		t.Fatalf("metadata = %#v, want empty metadata", meta)
	}
}

func TestAutoRouter_DeepseekV4StripsProviderPrefixBeforeForwarding(t *testing.T) {
	var upstreamModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		upstreamModel = body.Model
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-deepseek","model":"deepseek-v4-pro","choices":[]}`))
	}))
	defer upstream.Close()

	provider := &mockProvider{
		name: "deepseek",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			var req struct {
				Model string `json:"model"`
			}
			if err := json.Unmarshal(data, &req); err != nil {
				return BodyMetadata{}, nil, err
			}
			return BodyMetadata{Model: req.Model}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return ParseURL(upstream.URL + "/v1/chat/completions")
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			body, _ := io.ReadAll(resp.Body)
			return ResponseMetadata{ID: "chatcmpl-deepseek", Model: "deepseek-v4-pro"}, body, nil
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string {
			return "deepseek"
		})),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/v1/chat/completions", bytes.NewReader([]byte(`{
		"model": "deepseek/deepseek-v4-pro",
		"messages": [{"role":"user","content":"Reply with OK and nothing else."}]
	}`)))
	resp, _, err := router.Forward(context.Background(), req)
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if upstreamModel != "deepseek-v4-pro" {
		t.Fatalf("upstream model = %q, want deepseek-v4-pro", upstreamModel)
	}
}

func TestAutoRouter_DeepseekReasoningOffDisablesThinking(t *testing.T) {
	var upstreamBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-deepseek","model":"deepseek-v4-pro","choices":[]}`))
	}))
	defer upstream.Close()

	provider := &mockProvider{
		name: "deepseek",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			var req struct {
				Model string `json:"model"`
			}
			if err := json.Unmarshal(data, &req); err != nil {
				return BodyMetadata{}, nil, err
			}
			return BodyMetadata{Model: req.Model}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return ParseURL(upstream.URL + "/v1/chat/completions")
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			body, _ := io.ReadAll(resp.Body)
			return ResponseMetadata{ID: "chatcmpl-deepseek", Model: "deepseek-v4-pro"}, body, nil
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string {
			return "deepseek"
		})),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/v1/chat/completions", bytes.NewReader([]byte(`{
		"model": "deepseek/deepseek-v4-pro",
		"reasoning": "off",
		"messages": [{"role":"user","content":"Reply with OK and nothing else."}]
	}`)))
	resp, _, err := router.Forward(context.Background(), req)
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if _, ok := upstreamBody["reasoning"]; ok {
		t.Fatalf("upstream reasoning should be removed: %#v", upstreamBody)
	}
	thinking, ok := upstreamBody["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("upstream thinking missing: %#v", upstreamBody)
	}
	if thinking["type"] != "disabled" {
		t.Fatalf("upstream thinking.type = %q, want disabled", thinking["type"])
	}
}

func TestAutoRouter_DeepseekReasoningHighEnablesThinking(t *testing.T) {
	var upstreamBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-deepseek","model":"deepseek-v4-pro","choices":[]}`))
	}))
	defer upstream.Close()

	provider := &mockProvider{
		name: "deepseek",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			var req struct {
				Model string `json:"model"`
			}
			if err := json.Unmarshal(data, &req); err != nil {
				return BodyMetadata{}, nil, err
			}
			return BodyMetadata{Model: req.Model}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return ParseURL(upstream.URL + "/v1/chat/completions")
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			body, _ := io.ReadAll(resp.Body)
			return ResponseMetadata{ID: "chatcmpl-deepseek", Model: "deepseek-v4-pro"}, body, nil
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string {
			return "deepseek"
		})),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/v1/chat/completions", bytes.NewReader([]byte(`{
		"model": "deepseek/deepseek-v4-pro",
		"reasoning": "high",
		"messages": [{"role":"user","content":"Reply with OK and nothing else."}]
	}`)))
	resp, _, err := router.Forward(context.Background(), req)
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if _, ok := upstreamBody["reasoning"]; ok {
		t.Fatalf("upstream reasoning should be removed: %#v", upstreamBody)
	}
	thinking, ok := upstreamBody["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("upstream thinking missing: %#v", upstreamBody)
	}
	if thinking["type"] != "enabled" {
		t.Fatalf("upstream thinking.type = %q, want enabled", thinking["type"])
	}
	if upstreamBody["reasoning_effort"] != "high" {
		t.Fatalf("upstream reasoning_effort = %q, want high", upstreamBody["reasoning_effort"])
	}
}

func TestAutoRouter_DeepseekReasoningOffDisablesThinkingForStreaming(t *testing.T) {
	var upstreamBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-deepseek\"}\n\ndata: [DONE]\n\n"))
	}))
	defer upstream.Close()

	provider := &mockStreamingProvider{
		mockProvider: &mockProvider{
			name: "deepseek",
			parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
				data, _ := io.ReadAll(body)
				var req struct {
					Model string `json:"model"`
				}
				if err := json.Unmarshal(data, &req); err != nil {
					return BodyMetadata{}, nil, err
				}
				return BodyMetadata{Model: req.Model, Stream: true}, data, nil
			},
			enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
			resolveFn: func(meta BodyMetadata) (*url.URL, error) {
				return ParseURL(upstream.URL + "/v1/chat/completions")
			},
		},
		streamingExtractor: &mockStreamingExtractor{
			isStreaming: true,
			extractStreamingFn: func(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (ResponseMetadata, error) {
				_, _ = io.Copy(w, resp.Body)
				_ = rc.Flush()
				return ResponseMetadata{ID: "chatcmpl-deepseek", Model: "deepseek-v4-pro"}, nil
			},
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string {
			return "deepseek"
		})),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/v1/chat/completions", bytes.NewReader([]byte(`{
		"model": "deepseek/deepseek-v4-pro",
		"reasoning": "off",
		"reasoning_effort": "high",
		"stream": true,
		"messages": [{"role":"user","content":"Reply with OK and nothing else."}]
	}`)))
	w := httptest.NewRecorder()
	_, err := router.ForwardStreaming(context.Background(), req, w)
	if err != nil {
		t.Fatalf("ForwardStreaming() error = %v", err)
	}

	if _, ok := upstreamBody["reasoning"]; ok {
		t.Fatalf("upstream reasoning should be removed: %#v", upstreamBody)
	}
	if _, ok := upstreamBody["reasoning_effort"]; ok {
		t.Fatalf("upstream reasoning_effort should be removed: %#v", upstreamBody)
	}
	thinking, ok := upstreamBody["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("upstream thinking missing: %#v", upstreamBody)
	}
	if thinking["type"] != "disabled" {
		t.Fatalf("upstream thinking.type = %q, want disabled", thinking["type"])
	}
}

func TestAutoRouter_DeepseekReasoningHighEnablesThinkingForStreaming(t *testing.T) {
	var upstreamBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-deepseek\"}\n\ndata: [DONE]\n\n"))
	}))
	defer upstream.Close()

	provider := &mockStreamingProvider{
		mockProvider: &mockProvider{
			name: "deepseek",
			parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
				data, _ := io.ReadAll(body)
				var req struct {
					Model string `json:"model"`
				}
				if err := json.Unmarshal(data, &req); err != nil {
					return BodyMetadata{}, nil, err
				}
				return BodyMetadata{Model: req.Model, Stream: true}, data, nil
			},
			enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
			resolveFn: func(meta BodyMetadata) (*url.URL, error) {
				return ParseURL(upstream.URL + "/v1/chat/completions")
			},
		},
		streamingExtractor: &mockStreamingExtractor{
			isStreaming: true,
			extractStreamingFn: func(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (ResponseMetadata, error) {
				_, _ = io.Copy(w, resp.Body)
				_ = rc.Flush()
				return ResponseMetadata{ID: "chatcmpl-deepseek", Model: "deepseek-v4-pro"}, nil
			},
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string {
			return "deepseek"
		})),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/v1/chat/completions", bytes.NewReader([]byte(`{
		"model": "deepseek/deepseek-v4-pro",
		"reasoning": "high",
		"stream": true,
		"messages": [{"role":"user","content":"Reply with OK and nothing else."}]
	}`)))
	w := httptest.NewRecorder()
	_, err := router.ForwardStreaming(context.Background(), req, w)
	if err != nil {
		t.Fatalf("ForwardStreaming() error = %v", err)
	}

	if _, ok := upstreamBody["reasoning"]; ok {
		t.Fatalf("upstream reasoning should be removed: %#v", upstreamBody)
	}
	thinking, ok := upstreamBody["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("upstream thinking missing: %#v", upstreamBody)
	}
	if thinking["type"] != "enabled" {
		t.Fatalf("upstream thinking.type = %q, want enabled", thinking["type"])
	}
	if upstreamBody["reasoning_effort"] != "high" {
		t.Fatalf("upstream reasoning_effort = %q, want high", upstreamBody["reasoning_effort"])
	}
}

func TestAutoRouter_DeepseekUnknownReasoningIsLeftUntouched(t *testing.T) {
	raw := map[string]any{
		"reasoning":        "experimental",
		"reasoning_effort": "medium",
	}

	normalizeProviderRequest(raw, "deepseek")

	if raw["reasoning"] != "experimental" {
		t.Fatalf("reasoning = %q, want experimental", raw["reasoning"])
	}
	if raw["reasoning_effort"] != "medium" {
		t.Fatalf("reasoning_effort = %q, want medium", raw["reasoning_effort"])
	}
	if _, ok := raw["thinking"]; ok {
		t.Fatalf("thinking should not be set for unknown reasoning: %#v", raw)
	}
}

func TestAutoRouter_CohereCommandRUpstreamEmptyErrorDoesNotBecomeExtractorError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		if body.Model != "command-r-08-2024" {
			t.Fatalf("upstream model = %q, want command-r-08-2024", body.Model)
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	extractorCalled := false
	provider := &mockProvider{
		name: "cohere",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			var req struct {
				Model string `json:"model"`
			}
			if err := json.Unmarshal(data, &req); err != nil {
				return BodyMetadata{}, nil, err
			}
			return BodyMetadata{Model: req.Model}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return ParseURL(upstream.URL + "/v1/chat/completions")
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			extractorCalled = true
			return ResponseMetadata{}, nil, errors.New("extractor should not parse upstream errors")
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string {
			return "cohere"
		})),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/v1/chat/completions", bytes.NewReader([]byte(`{
		"model": "command-r-08-2024",
		"messages": [{"role":"user","content":"Reply with OK and nothing else."}]
	}`)))
	resp, _, err := router.Forward(context.Background(), req)
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if extractorCalled {
		t.Fatal("response extractor should not be called for upstream error responses")
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("StatusCode = %d, want 500", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) != 0 {
		t.Fatalf("body = %q, want empty body", string(body))
	}
}

func TestProxy_ForwardPassesThroughUpstreamErrorBody(t *testing.T) {
	upstreamBody := `provider unavailable`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(upstreamBody))
	}))
	defer upstream.Close()

	extractorCalled := false
	provider := &mockProvider{
		name: "test-provider",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			return BodyMetadata{Model: "gpt-4"}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return ParseURL(upstream.URL)
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			extractorCalled = true
			return ResponseMetadata{}, nil, nil
		},
	}

	proxy := NewProxy(provider)
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-4"}`)))
	resp, _, err := proxy.Forward(context.Background(), req)
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if extractorCalled {
		t.Fatal("response extractor should not be called for upstream error responses")
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("StatusCode = %d, want 500", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != upstreamBody {
		t.Fatalf("body = %q, want %q", string(body), upstreamBody)
	}
}

func TestAutoRouter_NoProvider(t *testing.T) {
	detector := &mockDetector{
		detectFn: func(hint ProviderHint) string { return "" },
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(detector),
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"unknown-model"}`)))

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

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"unknown"}`)))

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

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/v1/chat/completions", bytes.NewReader([]byte(`{}`)))
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

func TestAutoRouter_ServeHTTPMapsUpstreamTimeoutToGatewayTimeout(t *testing.T) {
	provider := &mockProvider{
		name: "test",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			return BodyMetadata{Model: "gpt-5"}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return url.Parse("https://api.openai.com")
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			return ResponseMetadata{}, nil, nil
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(ProviderHint) string { return "test" })),
		WithAutoRouterHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("Post https://api.openai.com/v1/chat/completions: net/http: timeout awaiting response headers")
		})}),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`)))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("StatusCode = %d, want 504", w.Code)
	}
}

func TestAutoRouter_ServeHTTPMapsStreamingUpstreamTimeoutToGatewayTimeout(t *testing.T) {
	provider := &mockProvider{
		name: "test",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			return BodyMetadata{Model: "gpt-5", Stream: true}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return url.Parse("https://api.openai.com")
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			return ResponseMetadata{}, nil, nil
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(ProviderHint) string { return "test" })),
		WithAutoRouterHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, context.DeadlineExceeded
		})}),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-5","stream":true,"messages":[{"role":"user","content":"hi"}]}`)))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("StatusCode = %d, want 504", w.Code)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
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
		{"repeated perplexity prefix", "perplexity/perplexity/sonar", "sonar", true},
		{"bedrock prefix", "bedrock/anthropic.claude-3", "anthropic.claude-3", true},
		{"azure prefix", "azure/gpt-4-deployment", "gpt-4-deployment", true},
		{"mistral prefix", "mistral/codestral-2508", "codestral-2508", true},
		{"deepseek prefix", "deepseek/deepseek-v4-pro", "deepseek-v4-pro", true},
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

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"Hello"}]}`)))
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

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"Hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")

	resp, _, err := router.Forward(context.Background(), req)
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
}

func TestAutoRouter_StreamingInjectsStreamOptions(t *testing.T) {
	var receivedBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"id\":\"test\"}\n\ndata: [DONE]\n\n"))
	}))
	defer upstream.Close()

	provider := &mockStreamingProvider{
		mockProvider: &mockProvider{
			name: "test",
			parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
				data, _ := io.ReadAll(body)
				return BodyMetadata{Model: "gpt-4", Stream: true}, data, nil
			},
			enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
			resolveFn: func(meta BodyMetadata) (*url.URL, error) {
				return url.Parse(upstream.URL)
			},
		},
		streamingExtractor: &mockStreamingExtractor{
			isStreaming: true,
			extractStreamingFn: func(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (ResponseMetadata, error) {
				io.Copy(w, resp.Body)
				rc.Flush()
				return ResponseMetadata{ID: "test"}, nil
			},
		},
	}
	provider.mockProvider.extractFn = func(resp *http.Response) (ResponseMetadata, []byte, error) {
		body, _ := io.ReadAll(resp.Body)
		return ResponseMetadata{ID: "test"}, body, nil
	}

	billing := NewBillingCalculator(
		func(provider, model string) (CostInfo, bool) {
			return CostInfo{Input: 1, Output: 2}, true
		},
		nil,
	)

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string { return "test" })),
		WithAutoRouterBillingCalculator(billing),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/", bytes.NewReader([]byte(`{"model":"gpt-4","stream":true,"messages":[{"role":"user","content":"Hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", w.Code)
	}

	streamOpts, ok := receivedBody["stream_options"].(map[string]any)
	if !ok {
		t.Fatal("stream_options not injected")
	}
	if includeUsage, ok := streamOpts["include_usage"].(bool); !ok || !includeUsage {
		t.Errorf("stream_options.include_usage = %v, want true", streamOpts["include_usage"])
	}
}

func TestAutoRouter_StreamingOverridesStreamOptions(t *testing.T) {
	var receivedBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"id\":\"test\"}\n\ndata: [DONE]\n\n"))
	}))
	defer upstream.Close()

	provider := &mockStreamingProvider{
		mockProvider: &mockProvider{
			name: "test",
			parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
				data, _ := io.ReadAll(body)
				return BodyMetadata{Model: "gpt-4", Stream: true}, data, nil
			},
			enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
			resolveFn: func(meta BodyMetadata) (*url.URL, error) {
				return url.Parse(upstream.URL)
			},
		},
		streamingExtractor: &mockStreamingExtractor{
			isStreaming: true,
			extractStreamingFn: func(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (ResponseMetadata, error) {
				io.Copy(w, resp.Body)
				rc.Flush()
				return ResponseMetadata{ID: "test"}, nil
			},
		},
	}
	provider.mockProvider.extractFn = func(resp *http.Response) (ResponseMetadata, []byte, error) {
		body, _ := io.ReadAll(resp.Body)
		return ResponseMetadata{ID: "test"}, body, nil
	}

	billing := NewBillingCalculator(
		func(provider, model string) (CostInfo, bool) {
			return CostInfo{Input: 1, Output: 2}, true
		},
		nil,
	)

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string { return "test" })),
		WithAutoRouterBillingCalculator(billing),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/", bytes.NewReader([]byte(`{"model":"gpt-4","stream":true,"stream_options":{"include_usage":false},"messages":[{"role":"user","content":"Hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", w.Code)
	}

	streamOpts, ok := receivedBody["stream_options"].(map[string]any)
	if !ok {
		t.Fatal("stream_options not present")
	}
	if includeUsage, ok := streamOpts["include_usage"].(bool); !ok || !includeUsage {
		t.Errorf("stream_options.include_usage = %v, want true (should override false)", streamOpts["include_usage"])
	}
}

func TestAutoRouter_StreamingNoBillingNoStreamOptions(t *testing.T) {
	var receivedBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"id\":\"test\"}\n\ndata: [DONE]\n\n"))
	}))
	defer upstream.Close()

	provider := &mockStreamingProvider{
		mockProvider: &mockProvider{
			name: "test",
			parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
				data, _ := io.ReadAll(body)
				return BodyMetadata{Model: "gpt-4", Stream: true}, data, nil
			},
			enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
			resolveFn: func(meta BodyMetadata) (*url.URL, error) {
				return url.Parse(upstream.URL)
			},
		},
		streamingExtractor: &mockStreamingExtractor{
			isStreaming: true,
			extractStreamingFn: func(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (ResponseMetadata, error) {
				io.Copy(w, resp.Body)
				rc.Flush()
				return ResponseMetadata{ID: "test"}, nil
			},
		},
	}
	provider.mockProvider.extractFn = func(resp *http.Response) (ResponseMetadata, []byte, error) {
		body, _ := io.ReadAll(resp.Body)
		return ResponseMetadata{ID: "test"}, body, nil
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string { return "test" })),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/", bytes.NewReader([]byte(`{"model":"gpt-4","stream":true,"messages":[{"role":"user","content":"Hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", w.Code)
	}

	if _, ok := receivedBody["stream_options"]; ok {
		t.Error("stream_options should not be injected when no billing calculator")
	}
}

func TestAutoRouter_ForwardStreamingForcesIdentityAcceptEncoding(t *testing.T) {
	eventStream := "data: {\"id\":\"test\"}\n\ndata: [DONE]\n\n"
	var upstreamAcceptEncoding string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAcceptEncoding = r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(eventStream))
	}))
	defer upstream.Close()

	provider := &mockStreamingProvider{
		mockProvider: &mockProvider{
			name: "test",
			parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
				data, _ := io.ReadAll(body)
				return BodyMetadata{Model: "gpt-4", Stream: true}, data, nil
			},
			enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error {
				req.Header.Set("Accept-Encoding", "gzip")
				return nil
			},
			resolveFn: func(meta BodyMetadata) (*url.URL, error) {
				return url.Parse(upstream.URL)
			},
		},
		streamingExtractor: &mockStreamingExtractor{
			isStreaming: true,
			extractStreamingFn: func(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (ResponseMetadata, error) {
				if _, err := io.Copy(w, resp.Body); err != nil {
					return ResponseMetadata{}, err
				}
				_ = rc.Flush()
				return ResponseMetadata{ID: "test"}, nil
			},
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string { return "test" })),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/", bytes.NewReader([]byte(`{"model":"gpt-4","stream":true,"messages":[{"role":"user","content":"Hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	w := httptest.NewRecorder()

	meta, err := router.ForwardStreaming(context.Background(), req, w)
	if err != nil {
		t.Fatalf("ForwardStreaming() error = %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", w.Code)
	}
	if meta.ID != "test" {
		t.Errorf("ID = %q, want test", meta.ID)
	}
	if upstreamAcceptEncoding != "identity" {
		t.Errorf("upstream Accept-Encoding = %q, want identity", upstreamAcceptEncoding)
	}
	if w.Body.String() != eventStream {
		t.Errorf("Body = %q, want %q", w.Body.String(), eventStream)
	}
}

func TestAutoRouter_NonStreamingNoStreamOptions(t *testing.T) {
	var receivedBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"test"}`))
	}))
	defer upstream.Close()

	provider := &mockProvider{
		name: "test",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			return BodyMetadata{Model: "gpt-4"}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return url.Parse(upstream.URL)
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			body, _ := io.ReadAll(resp.Body)
			return ResponseMetadata{ID: "test"}, body, nil
		},
	}

	billing := NewBillingCalculator(
		func(provider, model string) (CostInfo, bool) {
			return CostInfo{Input: 1, Output: 2}, true
		},
		nil,
	)

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string { return "test" })),
		WithAutoRouterBillingCalculator(billing),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/", bytes.NewReader([]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"Hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", w.Code)
	}

	if _, ok := receivedBody["stream_options"]; ok {
		t.Error("stream_options should not be injected for non-streaming requests")
	}
}

func TestAutoRouter_AnthropicStreamingNoStreamOptions(t *testing.T) {
	var receivedBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"test\",\"usage\":{\"input_tokens\":100}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":50}}\n\n"))
	}))
	defer upstream.Close()

	provider := &mockStreamingProvider{
		mockProvider: &mockProvider{
			name: "anthropic",
			parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
				data, _ := io.ReadAll(body)
				return BodyMetadata{Model: "claude-3-opus", Stream: true}, data, nil
			},
			enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
			resolveFn: func(meta BodyMetadata) (*url.URL, error) {
				return url.Parse(upstream.URL)
			},
		},
		streamingExtractor: &mockStreamingExtractor{
			isStreaming: true,
			extractStreamingFn: func(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (ResponseMetadata, error) {
				io.Copy(w, resp.Body)
				rc.Flush()
				return ResponseMetadata{ID: "test", Usage: Usage{PromptTokens: 100, CompletionTokens: 50}}, nil
			},
		},
	}
	provider.mockProvider.extractFn = func(resp *http.Response) (ResponseMetadata, []byte, error) {
		body, _ := io.ReadAll(resp.Body)
		return ResponseMetadata{ID: "test"}, body, nil
	}

	billing := NewBillingCalculator(
		func(provider, model string) (CostInfo, bool) {
			return CostInfo{Input: 3, Output: 15}, true
		},
		nil,
	)

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string { return "anthropic" })),
		WithAutoRouterBillingCalculator(billing),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/", bytes.NewReader([]byte(`{"model":"claude-3-opus","stream":true,"max_tokens":1024,"messages":[{"role":"user","content":"Hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", w.Code)
	}

	if _, ok := receivedBody["stream_options"]; ok {
		t.Error("stream_options should NOT be injected for Anthropic (always sends usage in events)")
	}
}

func TestAutoRouter_AnthropicDefaultMaxTokens(t *testing.T) {
	var receivedBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_test","type":"message","model":"claude-3-opus","content":[{"type":"text","text":"Hello"}],"usage":{"input_tokens":8,"output_tokens":1}}`))
	}))
	defer upstream.Close()

	provider := &mockProvider{
		name: "anthropic",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			var raw map[string]any
			_ = json.Unmarshal(data, &raw)
			maxTokens, _ := raw["max_tokens"].(float64)
			return BodyMetadata{Model: "claude-3-opus", MaxTokens: int(maxTokens)}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return url.Parse(upstream.URL)
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			body, _ := io.ReadAll(resp.Body)
			return ResponseMetadata{ID: "msg_test"}, body, nil
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string { return "anthropic" })),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/", bytes.NewReader([]byte(`{"model":"claude-3-opus","messages":[{"role":"user","content":"Hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", w.Code)
	}
	if got := receivedBody["max_tokens"]; got != float64(defaultAnthropicMaxTokens) {
		t.Fatalf("max_tokens = %v, want %d", got, defaultAnthropicMaxTokens)
	}
}

func TestAutoRouter_AnthropicPreservesMaxTokens(t *testing.T) {
	var receivedBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_test","type":"message","model":"claude-3-opus","content":[{"type":"text","text":"Hello"}],"usage":{"input_tokens":8,"output_tokens":1}}`))
	}))
	defer upstream.Close()

	provider := &mockProvider{
		name: "anthropic",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			return BodyMetadata{Model: "claude-3-opus", MaxTokens: 64}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return url.Parse(upstream.URL)
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			body, _ := io.ReadAll(resp.Body)
			return ResponseMetadata{ID: "msg_test"}, body, nil
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string { return "anthropic" })),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/", bytes.NewReader([]byte(`{"model":"claude-3-opus","max_tokens":64,"messages":[{"role":"user","content":"Hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", w.Code)
	}
	if got := receivedBody["max_tokens"]; got != float64(64) {
		t.Fatalf("max_tokens = %v, want 64", got)
	}
}

func TestAutoRouter_AnthropicMovesSystemMessageToTopLevel(t *testing.T) {
	var receivedBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_test","type":"message","model":"claude-3-opus","content":[{"type":"text","text":"Hello"}],"usage":{"input_tokens":8,"output_tokens":1}}`))
	}))
	defer upstream.Close()

	provider := &mockProvider{
		name: "anthropic",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			return BodyMetadata{Model: "claude-3-opus"}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return url.Parse(upstream.URL)
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			body, _ := io.ReadAll(resp.Body)
			return ResponseMetadata{ID: "msg_test"}, body, nil
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string { return "anthropic" })),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/", bytes.NewReader([]byte(`{"model":"claude-3-opus","messages":[{"role":"system","content":"You are terse."},{"role":"user","content":"Hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", w.Code)
	}
	if got := receivedBody["system"]; got != "You are terse." {
		t.Fatalf("system = %v, want %q", got, "You are terse.")
	}
	messages, ok := receivedBody["messages"].([]any)
	if !ok {
		t.Fatalf("messages = %T, want []any", receivedBody["messages"])
	}
	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1", len(messages))
	}
	message, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("messages[0] = %T, want map[string]any", messages[0])
	}
	if got := message["role"]; got != "user" {
		t.Fatalf("messages[0].role = %v, want user", got)
	}
	if got := receivedBody["max_tokens"]; got != float64(defaultAnthropicMaxTokens) {
		t.Fatalf("max_tokens = %v, want %d", got, defaultAnthropicMaxTokens)
	}
}

func TestAutoRouter_AnthropicMergesSystemMessageWithExistingSystem(t *testing.T) {
	var receivedBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_test","type":"message","model":"claude-3-opus","content":[{"type":"text","text":"Hello"}],"usage":{"input_tokens":8,"output_tokens":1}}`))
	}))
	defer upstream.Close()

	provider := &mockProvider{
		name: "anthropic",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			return BodyMetadata{Model: "claude-3-opus"}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return url.Parse(upstream.URL)
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			body, _ := io.ReadAll(resp.Body)
			return ResponseMetadata{ID: "msg_test"}, body, nil
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string { return "anthropic" })),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/", bytes.NewReader([]byte(`{"model":"claude-3-opus","system":"Existing system.","messages":[{"role":"system","content":"Additional system."},{"role":"user","content":"Hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", w.Code)
	}
	if got := receivedBody["system"]; got != "Existing system.\n\nAdditional system." {
		t.Fatalf("system = %v, want merged system", got)
	}
	messages, ok := receivedBody["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages = %#v, want one non-system message", receivedBody["messages"])
	}
}

func TestAutoRouter_AnthropicUsesSystemMessageWhenExistingSystemEmpty(t *testing.T) {
	var receivedBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_test","type":"message","model":"claude-3-opus","content":[{"type":"text","text":"Hello"}],"usage":{"input_tokens":8,"output_tokens":1}}`))
	}))
	defer upstream.Close()

	provider := &mockProvider{
		name: "anthropic",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			return BodyMetadata{Model: "claude-3-opus"}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return url.Parse(upstream.URL)
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			body, _ := io.ReadAll(resp.Body)
			return ResponseMetadata{ID: "msg_test"}, body, nil
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string { return "anthropic" })),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/", bytes.NewReader([]byte(`{"model":"claude-3-opus","system":"","messages":[{"role":"system","content":"Use terse answers."},{"role":"user","content":"Hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", w.Code)
	}
	if got := receivedBody["system"]; got != "Use terse answers." {
		t.Fatalf("system = %v, want %q", got, "Use terse answers.")
	}
	messages, ok := receivedBody["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages = %#v, want one non-system message", receivedBody["messages"])
	}
	message, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("messages[0] = %T, want map[string]any", messages[0])
	}
	if got := message["role"]; got != "user" {
		t.Fatalf("messages[0].role = %v, want user", got)
	}
	if got := receivedBody["max_tokens"]; got != float64(defaultAnthropicMaxTokens) {
		t.Fatalf("max_tokens = %v, want %d", got, defaultAnthropicMaxTokens)
	}
}

func TestAutoRouter_AnthropicRemovesSystemMessageWithMissingContent(t *testing.T) {
	var receivedBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_test","type":"message","model":"claude-3-opus","content":[{"type":"text","text":"Hello"}],"usage":{"input_tokens":8,"output_tokens":1}}`))
	}))
	defer upstream.Close()

	provider := &mockProvider{
		name: "anthropic",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			return BodyMetadata{Model: "claude-3-opus"}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return url.Parse(upstream.URL)
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			body, _ := io.ReadAll(resp.Body)
			return ResponseMetadata{ID: "msg_test"}, body, nil
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string { return "anthropic" })),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/", bytes.NewReader([]byte(`{"model":"claude-3-opus","system":"Existing system.","messages":[{"role":"system"},{"role":"system","content":null},{"role":"user","content":"Hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", w.Code)
	}
	if got := receivedBody["system"]; got != "Existing system." {
		t.Fatalf("system = %v, want existing system unchanged", got)
	}
	messages, ok := receivedBody["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages = %#v, want one non-system message", receivedBody["messages"])
	}
	message, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("messages[0] = %T, want map[string]any", messages[0])
	}
	if got := message["role"]; got != "user" {
		t.Fatalf("messages[0].role = %v, want user", got)
	}
}

func TestAutoRouter_OpenAIGPT5UsesMaxCompletionTokens(t *testing.T) {
	var receivedBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"chatcmpl_test","object":"chat.completion","model":"gpt-5","choices":[{"message":{"role":"assistant","content":"Hello"}}],"usage":{"prompt_tokens":8,"completion_tokens":1,"total_tokens":9}}`))
	}))
	defer upstream.Close()

	provider := &mockProvider{
		name: "openai",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			return BodyMetadata{Model: "gpt-5", MaxTokens: 64}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return url.Parse(upstream.URL)
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			body, _ := io.ReadAll(resp.Body)
			return ResponseMetadata{ID: "chatcmpl_test"}, body, nil
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string { return "openai" })),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/", bytes.NewReader([]byte(`{"model":"gpt-5","max_tokens":64,"messages":[{"role":"user","content":"Hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", w.Code)
	}
	if _, exists := receivedBody["max_tokens"]; exists {
		t.Fatalf("max_tokens should be removed for gpt-5: %#v", receivedBody)
	}
	if got := receivedBody["max_completion_tokens"]; got != float64(64) {
		t.Fatalf("max_completion_tokens = %v, want 64", got)
	}
}

func TestAutoRouter_OpenAILegacyModelPreservesMaxTokens(t *testing.T) {
	var receivedBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"chatcmpl_test","object":"chat.completion","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"Hello"}}],"usage":{"prompt_tokens":8,"completion_tokens":1,"total_tokens":9}}`))
	}))
	defer upstream.Close()

	provider := &mockProvider{
		name: "openai",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			return BodyMetadata{Model: "gpt-4o", MaxTokens: 64}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return url.Parse(upstream.URL)
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			body, _ := io.ReadAll(resp.Body)
			return ResponseMetadata{ID: "chatcmpl_test"}, body, nil
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string { return "openai" })),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/", bytes.NewReader([]byte(`{"model":"gpt-4o","max_tokens":64,"messages":[{"role":"user","content":"Hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", w.Code)
	}
	if got := receivedBody["max_tokens"]; got != float64(64) {
		t.Fatalf("max_tokens = %v, want 64", got)
	}
	if _, exists := receivedBody["max_completion_tokens"]; exists {
		t.Fatalf("max_completion_tokens should not be injected for gpt-4o: %#v", receivedBody)
	}
}

func TestAutoRouter_OpenAIPreservesExplicitMaxCompletionTokens(t *testing.T) {
	var receivedBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"chatcmpl_test","object":"chat.completion","model":"o4-mini","choices":[{"message":{"role":"assistant","content":"Hello"}}],"usage":{"prompt_tokens":8,"completion_tokens":1,"total_tokens":9}}`))
	}))
	defer upstream.Close()

	provider := &mockProvider{
		name: "openai",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			return BodyMetadata{Model: "o4-mini", MaxTokens: 64}, data, nil
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			return url.Parse(upstream.URL)
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			body, _ := io.ReadAll(resp.Body)
			return ResponseMetadata{ID: "chatcmpl_test"}, body, nil
		},
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string { return "openai" })),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/", bytes.NewReader([]byte(`{"model":"o4-mini","max_tokens":64,"max_completion_tokens":32,"messages":[{"role":"user","content":"Hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", w.Code)
	}
	if _, exists := receivedBody["max_tokens"]; exists {
		t.Fatalf("max_tokens should be removed for o4-mini: %#v", receivedBody)
	}
	if got := receivedBody["max_completion_tokens"]; got != float64(32) {
		t.Fatalf("max_completion_tokens = %v, want 32", got)
	}
}

func TestAutoRouter_StreamingWritesGatewayMetadataEvent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"id\":\"test\"}\n\ndata: [DONE]\n\n"))
	}))
	defer upstream.Close()

	provider := &mockStreamingProvider{
		mockProvider: &mockProvider{
			name: "test",
			parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
				data, _ := io.ReadAll(body)
				return BodyMetadata{Model: "gpt-4", Stream: true}, data, nil
			},
			enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
			resolveFn: func(meta BodyMetadata) (*url.URL, error) {
				return url.Parse(upstream.URL)
			},
		},
		streamingExtractor: &mockStreamingExtractor{
			isStreaming: true,
			extractStreamingFn: func(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (ResponseMetadata, error) {
				_, _ = io.Copy(w, resp.Body)
				_ = rc.Flush()
				return ResponseMetadata{
					ID:    "test",
					Usage: Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
				}, nil
			},
		},
	}

	billing := NewBillingCalculator(
		func(provider, model string) (CostInfo, bool) {
			return CostInfo{Input: 1, Output: 2}, true
		},
		nil,
	)

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string { return "test" })),
		WithAutoRouterBillingCalculator(billing),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/", bytes.NewReader([]byte(`{"model":"gpt-4","stream":true,"messages":[{"role":"user","content":"Hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", w.Code)
	}

	body := w.Body.String()
	metadataIndex := strings.Index(body, "event: gateway.metadata")
	doneIndex := strings.Index(body, "data: [DONE]")
	if metadataIndex < 0 || doneIndex < 0 {
		t.Fatalf("stream body missing metadata or terminal event: %q", body)
	}
	if metadataIndex > doneIndex {
		t.Fatalf("metadata event should be written before terminal event: %q", body)
	}
	nextEventIndex := strings.Index(body[metadataIndex+1:], "\nevent: ")
	metadataEnd := doneIndex
	if nextEventIndex >= 0 {
		metadataEnd = metadataIndex + 1 + nextEventIndex
	}
	metadataChunk := body[metadataIndex:metadataEnd]
	if !strings.Contains(metadataChunk, `"type":"gateway.metadata"`) {
		t.Fatalf("metadata event missing type: %q", metadataChunk)
	}
	if !strings.Contains(metadataChunk, `"total":0.0002`) {
		t.Fatalf("metadata event missing total cost: %q", metadataChunk)
	}
	if !strings.Contains(metadataChunk, `"promptTokens":100`) {
		t.Fatalf("metadata event missing prompt tokens: %q", metadataChunk)
	}
	if !strings.Contains(metadataChunk, `"completionTokens":50`) {
		t.Fatalf("metadata event missing completion tokens: %q", metadataChunk)
	}
}

func TestAutoRouter_StreamingWritesGatewayMetadataEventWithoutTerminalMarker(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: response.completed\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":50}}}\n\n"))
	}))
	defer upstream.Close()

	provider := &mockStreamingProvider{
		mockProvider: &mockProvider{
			name: "test",
			parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
				data, _ := io.ReadAll(body)
				return BodyMetadata{Model: "gpt-4", Stream: true}, data, nil
			},
			enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
			resolveFn: func(meta BodyMetadata) (*url.URL, error) {
				return url.Parse(upstream.URL)
			},
		},
		streamingExtractor: &mockStreamingExtractor{
			isStreaming: true,
			extractStreamingFn: func(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (ResponseMetadata, error) {
				_, _ = io.Copy(w, resp.Body)
				_ = rc.Flush()
				return ResponseMetadata{
					ID:    "test",
					Usage: Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
				}, nil
			},
		},
	}

	billing := NewBillingCalculator(
		func(provider, model string) (CostInfo, bool) {
			return CostInfo{Input: 1, Output: 2}, true
		},
		nil,
	)

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string { return "test" })),
		WithAutoRouterBillingCalculator(billing),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/", bytes.NewReader([]byte(`{"model":"gpt-4","stream":true,"input":"Hello"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", w.Code)
	}

	body := w.Body.String()
	metadataIndex := strings.Index(body, "event: gateway.metadata")
	if metadataIndex < 0 {
		t.Fatalf("stream body missing metadata event: %q", body)
	}

	metadataChunk := body[metadataIndex:]
	if !strings.Contains(metadataChunk, `"type":"gateway.metadata"`) {
		t.Fatalf("metadata event missing type: %q", metadataChunk)
	}
	if !strings.Contains(metadataChunk, `"total":0.0002`) {
		t.Fatalf("metadata event missing total cost: %q", metadataChunk)
	}
	if !strings.Contains(metadataChunk, `"promptTokens":100`) {
		t.Fatalf("metadata event missing prompt tokens: %q", metadataChunk)
	}
	if !strings.Contains(metadataChunk, `"completionTokens":50`) {
		t.Fatalf("metadata event missing completion tokens: %q", metadataChunk)
	}
}

func TestAutoRouter_ResponsesAPIStreamingNoStreamOptions(t *testing.T) {
	var receivedBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("event: response.completed\ndata: {\"id\":\"resp_test\",\"output\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":20}}\n\n"))
	}))
	defer upstream.Close()

	provider := &mockStreamingProvider{
		mockProvider: &mockProvider{
			name: "test",
			parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
				data, _ := io.ReadAll(body)
				return BodyMetadata{Model: "gpt-4o", Stream: true}, data, nil
			},
			enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error { return nil },
			resolveFn: func(meta BodyMetadata) (*url.URL, error) {
				return url.Parse(upstream.URL)
			},
		},
		streamingExtractor: &mockStreamingExtractor{
			isStreaming: true,
			extractStreamingFn: func(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (ResponseMetadata, error) {
				io.Copy(w, resp.Body)
				rc.Flush()
				return ResponseMetadata{ID: "resp_test"}, nil
			},
		},
	}
	provider.mockProvider.extractFn = func(resp *http.Response) (ResponseMetadata, []byte, error) {
		body, _ := io.ReadAll(resp.Body)
		return ResponseMetadata{ID: "resp_test"}, body, nil
	}

	billing := NewBillingCalculator(
		func(provider, model string) (CostInfo, bool) {
			return CostInfo{Input: 1, Output: 2}, true
		},
		nil,
	)

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(hint ProviderHint) string { return "test" })),
		WithAutoRouterBillingCalculator(billing),
	)
	router.RegisterProvider(provider)

	t.Run("path-based detection", func(t *testing.T) {
		receivedBody = nil
		req := httptest.NewRequestWithContext(context.Background(), "POST", "/v1/responses", bytes.NewReader([]byte(`{"model":"gpt-4o","stream":true,"input":"Hello"}`)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("StatusCode = %d, want 200", w.Code)
		}

		if _, ok := receivedBody["stream_options"]; ok {
			t.Error("stream_options should NOT be injected for Responses API requests")
		}
	})

	t.Run("body-based detection", func(t *testing.T) {
		receivedBody = nil
		req := httptest.NewRequestWithContext(context.Background(), "POST", "/", bytes.NewReader([]byte(`{"model":"gpt-4o","stream":true,"input":"Hello"}`)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("StatusCode = %d, want 200", w.Code)
		}

		if _, ok := receivedBody["stream_options"]; ok {
			t.Error("stream_options should NOT be injected for Responses API requests (body-detected)")
		}
	})
}

func TestNormalizeProviderRequest_GoogleAI(t *testing.T) {
	raw := map[string]any{
		"model":              "googleai/gemini-2.5-flash",
		"max_tokens":         float64(256),
		"stream":             true,
		"system_instruction": "You are concise.",
		"messages": []any{
			map[string]any{"role": "user", "content": "Say hello"},
			map[string]any{"role": "assistant", "content": "Hello"},
		},
	}

	normalizeProviderRequest(raw, "googleai")

	if _, ok := raw["stream"]; ok {
		t.Fatal("stream should be removed from Google AI upstream body")
	}
	if _, ok := raw["max_tokens"]; ok {
		t.Fatal("max_tokens should be converted for Google AI")
	}
	if _, ok := raw["system_instruction"]; ok {
		t.Fatal("system_instruction should be converted for Google AI")
	}
	if _, ok := raw["messages"]; ok {
		t.Fatal("messages should be converted for Google AI")
	}

	generationConfig, ok := raw["generationConfig"].(map[string]any)
	if !ok {
		t.Fatalf("generationConfig missing or invalid: %#v", raw["generationConfig"])
	}
	if generationConfig["maxOutputTokens"] != float64(256) {
		t.Fatalf("maxOutputTokens = %#v, want 256", generationConfig["maxOutputTokens"])
	}

	systemInstruction, ok := raw["systemInstruction"].(map[string]any)
	if !ok {
		t.Fatalf("systemInstruction missing or invalid: %#v", raw["systemInstruction"])
	}
	systemParts, ok := systemInstruction["parts"].([]any)
	if !ok || len(systemParts) != 1 {
		t.Fatalf("systemInstruction parts invalid: %#v", systemInstruction["parts"])
	}

	contents, ok := raw["contents"].([]any)
	if !ok || len(contents) != 2 {
		t.Fatalf("contents missing or invalid: %#v", raw["contents"])
	}
	second, ok := contents[1].(map[string]any)
	if !ok {
		t.Fatalf("second content invalid: %#v", contents[1])
	}
	if second["role"] != "model" {
		t.Fatalf("assistant role should map to model, got %#v", second["role"])
	}
}

func TestAutoRouter_copyResponseHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	copyResponseHeaders(w, http.Header{})
	var sw strings.Builder
	w.Header().Write(&sw)
	if sw.Len() != 0 {
		t.Errorf("headers should have been empty but was: %s", sw.String())
	}
	sw.Reset()
	w = httptest.NewRecorder()

	copyResponseHeaders(w, http.Header{"A": []string{"B"}})
	w.Header().Write(&sw)
	if sw.Len() == 0 {
		t.Error("headers should have content but was empty")
	}
	val := strings.TrimSpace(sw.String())
	if val != "A: B" {
		t.Errorf("headers should have A: B but was %s", val)
	}
	sw.Reset()
	w = httptest.NewRecorder()

	copyResponseHeaders(w, http.Header{"A": []string{"B"}, "Content-Encoding": []string{"gzip"}})
	w.Header().Write(&sw)
	if sw.Len() == 0 {
		t.Error("headers should have content but was empty")
	}
	val = strings.TrimSpace(sw.String())
	if val != "A: B" {
		t.Errorf("headers should have A: B but was %s", val)
	}
	sw.Reset()
	w = httptest.NewRecorder()

	copyResponseHeaders(w, http.Header{"A": []string{"B"}, "content-encoding": []string{"gzip"}})
	w.Header().Write(&sw)
	if sw.Len() == 0 {
		t.Error("headers should have content but was empty")
	}
	val = strings.TrimSpace(sw.String())
	if val != "A: B" {
		t.Errorf("headers should have A: B but was %s", val)
	}
	sw.Reset()
	w = httptest.NewRecorder()

	copyResponseHeaders(w, http.Header{"A": []string{"B"}, "Content-Length": []string{"1"}})
	w.Header().Write(&sw)
	if sw.Len() == 0 {
		t.Error("headers should have content but was empty")
	}
	val = strings.TrimSpace(sw.String())
	if val != "A: B" {
		t.Errorf("headers should have A: B but was %s", val)
	}
	sw.Reset()
	w = httptest.NewRecorder()

	copyResponseHeaders(w, http.Header{"A": []string{"B"}, "content-length": []string{"1"}})
	w.Header().Write(&sw)
	if sw.Len() == 0 {
		t.Error("headers should have content but was empty")
	}
	val = strings.TrimSpace(sw.String())
	if val != "A: B" {
		t.Errorf("headers should have A: B but was %s", val)
	}
	sw.Reset()
	w = httptest.NewRecorder()

}

func TestExtractModelFromPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/v1beta/models/gemini-3.1-flash-lite:generateContent", "gemini-3.1-flash-lite"},
		{"/v1beta/models/veo-3.1-generate-preview:predictLongRunning", "veo-3.1-generate-preview"},
		{"/v1/chat/completions", ""},
	}
	for _, tt := range tests {
		if got := extractModelFromPath(tt.path); got != tt.want {
			t.Fatalf("extractModelFromPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestAutoRouter_ModelMetadataValidationRejectsUnsupportedSurface(t *testing.T) {
	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(h ProviderHint) string {
			return "googleai"
		})),
		WithAutoRouterFallbackProvider(&mockProvider{name: "googleai"}),
		WithAutoRouterModelMetadataLookup(func(provider, model string) (ModelMetadata, bool) {
			return ModelMetadata{
				APICompatibility: "google-generative-ai-long-running",
				InputModalities:  []string{"text", "image"},
				OutputModalities: []string{"video"},
			}, true
		}),
	)
	router.RegisterProvider(&mockProvider{name: "googleai"})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"googleai/veo-3.1-generate-preview","messages":[{"role":"user","content":"hello"}]}`))
	_, _, err := router.Forward(context.Background(), req)
	if err == nil {
		t.Fatal("expected unsupported surface error")
	}
	if !strings.Contains(err.Error(), "does not support chat_completions") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAutoRouter_ModelMetadataValidationAllowsGoogleLongRunningPathModel(t *testing.T) {
	var parsedModel string
	provider := &mockProvider{
		name: "googleai",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, err := io.ReadAll(body)
			return BodyMetadata{Custom: map[string]any{}}, data, err
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error {
			return nil
		},
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			parsedModel = meta.Model
			return url.Parse("https://example.com/v1beta/models/" + meta.Model + ":predictLongRunning")
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			body, err := io.ReadAll(resp.Body)
			return ResponseMetadata{Custom: map[string]any{}}, body, err
		},
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"operations/test"}`))
	}))
	defer upstream.Close()
	provider.resolveFn = func(meta BodyMetadata) (*url.URL, error) {
		parsedModel = meta.Model
		return url.Parse(upstream.URL + "/v1beta/models/" + meta.Model + ":predictLongRunning")
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(h ProviderHint) string {
			return "googleai"
		})),
		WithAutoRouterHTTPClient(upstream.Client()),
		WithAutoRouterFallbackProvider(provider),
		WithAutoRouterModelMetadataLookup(func(provider, model string) (ModelMetadata, bool) {
			return ModelMetadata{
				APICompatibility: "google-generative-ai-long-running",
				InputModalities:  []string{"text", "image"},
				OutputModalities: []string{"video"},
			}, true
		}),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/veo-3.1-generate-preview:predictLongRunning", strings.NewReader(`{"instances":[{"prompt":"hello"}]}`))
	resp, _, err := router.Forward(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if parsedModel != "veo-3.1-generate-preview" {
		t.Fatalf("parsed model = %q, want veo-3.1-generate-preview", parsedModel)
	}
}

func TestAutoRouter_GoogleNativePathSelectsGoogleProviderWithoutModelPrefix(t *testing.T) {
	var parsedModel string
	provider := &mockProvider{
		name: "googleai",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, err := io.ReadAll(body)
			return BodyMetadata{Custom: map[string]any{}}, data, err
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error {
			return nil
		},
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			parsedModel = meta.Model
			return url.Parse("https://example.com/v1beta/models/" + meta.Model + ":generateContent")
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			body, err := io.ReadAll(resp.Body)
			return ResponseMetadata{Custom: map[string]any{}}, body, err
		},
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"pong"}]}}]}`))
	}))
	defer upstream.Close()
	provider.resolveFn = func(meta BodyMetadata) (*url.URL, error) {
		parsedModel = meta.Model
		return url.Parse(upstream.URL + "/v1beta/models/" + meta.Model + ":generateContent")
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(h ProviderHint) string {
			return ""
		})),
		WithAutoRouterHTTPClient(upstream.Client()),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-3.1-flash-lite:generateContent", strings.NewReader(`{
		"contents": [
			{"role": "user", "parts": [{"text": "Say pong in one word."}]}
		]
	}`))
	resp, _, err := router.Forward(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if parsedModel != "gemini-3.1-flash-lite" {
		t.Fatalf("parsed model = %q, want gemini-3.1-flash-lite", parsedModel)
	}
}

func TestAutoRouter_GoogleNativeStreamPathUsesStreamingForwarder(t *testing.T) {
	var parsedModel string
	provider := &mockStreamingProvider{
		mockProvider: &mockProvider{
			name: "googleai",
			parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
				data, err := io.ReadAll(body)
				return BodyMetadata{Custom: map[string]any{}}, data, err
			},
			enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error {
				return nil
			},
			resolveFn: func(meta BodyMetadata) (*url.URL, error) {
				parsedModel = meta.Model
				return url.Parse("https://example.com/v1beta/models/" + meta.Model + ":streamGenerateContent?alt=sse")
			},
		},
		streamingExtractor: &mockStreamingExtractor{
			isStreaming: true,
			extractStreamingFn: func(resp *http.Response, w http.ResponseWriter, rc *http.ResponseController) (ResponseMetadata, error) {
				_, _ = io.Copy(w, resp.Body)
				_ = rc.Flush()
				return ResponseMetadata{Custom: map[string]any{}}, nil
			},
		},
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Pong\"}]}}]}\n\n"))
	}))
	defer upstream.Close()
	provider.resolveFn = func(meta BodyMetadata) (*url.URL, error) {
		parsedModel = meta.Model
		return url.Parse(upstream.URL + "/v1beta/models/" + meta.Model + ":streamGenerateContent?alt=sse")
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(h ProviderHint) string {
			return ""
		})),
		WithAutoRouterHTTPClient(upstream.Client()),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-3.1-flash-lite:streamGenerateContent", strings.NewReader(`{
		"contents": [
			{"role": "user", "parts": [{"text": "Say pong in one word."}]}
		]
	}`))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	if parsedModel != "gemini-3.1-flash-lite" {
		t.Fatalf("parsed model = %q, want gemini-3.1-flash-lite", parsedModel)
	}
	if !strings.Contains(recorder.Body.String(), `"Pong"`) {
		t.Fatalf("stream body = %q, want Pong token", recorder.Body.String())
	}
}

func TestAutoRouter_OpenAIAudioTranscriptionMultipartPassesThrough(t *testing.T) {
	var upstreamBody []byte
	var upstreamContentType string
	var resolvedModel string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamContentType = r.Header.Get("Content-Type")
		upstreamBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"hello"}`))
	}))
	defer upstream.Close()

	provider := &mockProvider{
		name: "openai",
		parseFn: func(body io.ReadCloser) (BodyMetadata, []byte, error) {
			data, _ := io.ReadAll(body)
			return BodyMetadata{}, data, errors.New("json parser should not block multipart pass-through")
		},
		enrichFn: func(req *http.Request, meta BodyMetadata, body []byte) error {
			return nil
		},
		resolveFn: func(meta BodyMetadata) (*url.URL, error) {
			resolvedModel = meta.Model
			return url.Parse(upstream.URL + "/v1/audio/transcriptions")
		},
		extractFn: func(resp *http.Response) (ResponseMetadata, []byte, error) {
			body, err := io.ReadAll(resp.Body)
			return ResponseMetadata{Custom: map[string]any{}}, body, err
		},
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", "openai/whisper-1"); err != nil {
		t.Fatal(err)
	}
	file, err := writer.CreateFormFile("file", "hello.wav")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("fake wav data")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	router := NewAutoRouter(
		WithAutoRouterDetector(ProviderDetectorFunc(func(h ProviderHint) string {
			return "openai"
		})),
		WithAutoRouterHTTPClient(upstream.Client()),
	)
	router.RegisterProvider(provider)

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, _, err := router.Forward(context.Background(), req)
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}
	defer resp.Body.Close()

	if resolvedModel != "whisper-1" {
		t.Fatalf("resolved model = %q, want whisper-1", resolvedModel)
	}

	reader := multipart.NewReader(bytes.NewReader(upstreamBody), boundaryFromContentType(t, upstreamContentType))
	form, err := reader.ReadForm(1024 * 1024)
	if err != nil {
		t.Fatalf("ReadForm() error = %v", err)
	}
	if got := form.Value["model"][0]; got != "whisper-1" {
		t.Fatalf("upstream model = %q, want whisper-1", got)
	}
	if len(form.File["file"]) != 1 {
		t.Fatalf("upstream file parts = %d, want 1", len(form.File["file"]))
	}
}

func boundaryFromContentType(t *testing.T, contentType string) string {
	t.Helper()
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("ParseMediaType() error = %v", err)
	}
	return params["boundary"]
}
