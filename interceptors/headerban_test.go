package interceptors

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentuity/llmproxy"
)

func TestHeaderBanInterceptor_ResponseHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Openai-Organization", "org-123")
		w.Header().Set("Openai-Project", "proj-456")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	ban := NewResponseHeaderBan("Openai-Organization", "Openai-Project")

	req, _ := http.NewRequest("POST", upstream.URL, nil)
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	resp, _, _, err := ban.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}

	if got := resp.Header.Get("Openai-Organization"); got != "" {
		t.Errorf("Openai-Organization header should be stripped, got %q", got)
	}
	if got := resp.Header.Get("Openai-Project"); got != "" {
		t.Errorf("Openai-Project header should be stripped, got %q", got)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type header should be preserved, got %q", got)
	}
}

func TestHeaderBanInterceptor_RequestHeaders(t *testing.T) {
	var capturedReq *http.Request
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	ban := NewRequestHeaderBan("Authorization", "Cookie")

	req, _ := http.NewRequest("POST", upstream.URL, nil)
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Cookie", "session=abc")
	req.Header.Set("Content-Type", "application/json")

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	_, _, _, err := ban.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}

	if got := capturedReq.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization header should be stripped, got %q", got)
	}
	if got := capturedReq.Header.Get("Cookie"); got != "" {
		t.Errorf("Cookie header should be stripped, got %q", got)
	}
	if got := capturedReq.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type header should be preserved, got %q", got)
	}
}

func TestHeaderBanInterceptor_Both(t *testing.T) {
	var capturedReq *http.Request
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		w.Header().Set("X-Sensitive", "secret")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	ban := NewHeaderBan([]string{"Authorization"}, []string{"X-Sensitive"})

	req, _ := http.NewRequest("POST", upstream.URL, nil)
	req.Header.Set("Authorization", "Bearer secret")

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	resp, _, _, err := ban.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}

	if got := capturedReq.Header.Get("Authorization"); got != "" {
		t.Errorf("Request Authorization header should be stripped, got %q", got)
	}
	if got := resp.Header.Get("X-Sensitive"); got != "" {
		t.Errorf("Response X-Sensitive header should be stripped, got %q", got)
	}
}

func TestHeaderBanInterceptor_CaseInsensitive(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("OPENAI-ORGANIZATION", "org-123")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	ban := NewResponseHeaderBan("openai-organization")

	req, _ := http.NewRequest("POST", upstream.URL, nil)
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	resp, _, _, err := ban.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}

	if got := resp.Header.Get("Openai-Organization"); got != "" {
		t.Errorf("Header should be stripped case-insensitively, got %q", got)
	}
}
