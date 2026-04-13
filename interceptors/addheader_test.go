package interceptors

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentuity/llmproxy"
)

func TestAddHeaderInterceptor_ResponseHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	add := NewAddResponseHeader(
		NewHeader("X-Gateway-Version", "1.0"),
		NewHeader("X-Served-By", "llmproxy"),
	)

	req, _ := http.NewRequest("POST", upstream.URL, nil)
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	resp, _, _, err := add.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("X-Gateway-Version"); got != "1.0" {
		t.Errorf("X-Gateway-Version header = %q, want %q", got, "1.0")
	}
	if got := resp.Header.Get("X-Served-By"); got != "llmproxy" {
		t.Errorf("X-Served-By header = %q, want %q", got, "llmproxy")
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type header should be preserved, got %q", got)
	}
}

func TestAddHeaderInterceptor_RequestHeaders(t *testing.T) {
	var capturedReq *http.Request
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	add := NewAddRequestHeader(
		NewHeader("X-Client-ID", "my-app"),
		NewHeader("X-Request-Source", "gateway"),
	)

	req, _ := http.NewRequest("POST", upstream.URL, nil)
	req.Header.Set("Content-Type", "application/json")

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	_, _, _, err := add.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}

	if got := capturedReq.Header.Get("X-Client-ID"); got != "my-app" {
		t.Errorf("X-Client-ID header = %q, want %q", got, "my-app")
	}
	if got := capturedReq.Header.Get("X-Request-Source"); got != "gateway" {
		t.Errorf("X-Request-Source header = %q, want %q", got, "gateway")
	}
	if got := capturedReq.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type header should be preserved, got %q", got)
	}
}

func TestAddHeaderInterceptor_Both(t *testing.T) {
	var capturedReq *http.Request
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	add := NewAddHeader(
		[]Header{NewHeader("X-Request-ID", "req-123")},
		[]Header{NewHeader("X-Response-Time", "50ms")},
	)

	req, _ := http.NewRequest("POST", upstream.URL, nil)
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	resp, _, _, err := add.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
	defer resp.Body.Close()

	if got := capturedReq.Header.Get("X-Request-ID"); got != "req-123" {
		t.Errorf("Request X-Request-ID header = %q, want %q", got, "req-123")
	}
	if got := resp.Header.Get("X-Response-Time"); got != "50ms" {
		t.Errorf("Response X-Response-Time header = %q, want %q", got, "50ms")
	}
}

func TestAddHeaderInterceptor_Empty(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	add := &AddHeaderInterceptor{}

	req, _ := http.NewRequest("POST", upstream.URL, nil)
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	_, _, _, err := add.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestAddHeaderInterceptor_ErrorPassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	add := NewAddResponseHeader(NewHeader("X-Test", "value"))

	req, _ := http.NewRequest("POST", upstream.URL, nil)
	expectedErr := http.ErrHandlerTimeout
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		return nil, llmproxy.ResponseMetadata{}, nil, expectedErr
	}

	resp, _, _, err := add.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	if err != expectedErr {
		t.Errorf("Error should pass through, got %v, want %v", err, expectedErr)
	}
	if resp != nil {
		t.Errorf("Response should be nil on error, got %v", resp)
	}
}

func TestNewHeader(t *testing.T) {
	h := NewHeader("X-Custom", "value")
	if h.Key != "X-Custom" {
		t.Errorf("Key = %q, want %q", h.Key, "X-Custom")
	}
	if h.Value != "value" {
		t.Errorf("Value = %q, want %q", h.Value, "value")
	}
}
