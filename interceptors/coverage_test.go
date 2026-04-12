package interceptors

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentuity/llmproxy"
)

func TestMetricsInterceptor_Success(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	m := &Metrics{}
	metrics := NewMetrics(m)

	req, _ := http.NewRequest("POST", upstream.URL, nil)
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		respMeta := llmproxy.ResponseMetadata{
			Usage: llmproxy.Usage{
				PromptTokens:     100,
				CompletionTokens: 50,
				TotalTokens:      150,
			},
		}
		return resp, respMeta, nil, nil
	}

	_, _, _, err := metrics.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}

	if m.TotalRequests != 1 {
		t.Errorf("TotalRequests = %d, want 1", m.TotalRequests)
	}
	if m.Errors != 0 {
		t.Errorf("Errors = %d, want 0", m.Errors)
	}
	if m.TotalTokens != 150 {
		t.Errorf("TotalTokens = %d, want 150", m.TotalTokens)
	}
	if m.TotalPromptTokens != 100 {
		t.Errorf("TotalPromptTokens = %d, want 100", m.TotalPromptTokens)
	}
	if m.TotalCompletionTokens != 50 {
		t.Errorf("TotalCompletionTokens = %d, want 50", m.TotalCompletionTokens)
	}
	if m.TotalLatency <= 0 {
		t.Errorf("TotalLatency = %d, want > 0", m.TotalLatency)
	}
}

func TestMetricsInterceptor_Error(t *testing.T) {
	m := &Metrics{}
	metrics := NewMetrics(m)

	req, _ := http.NewRequest("POST", "http://example.com", nil)
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		return nil, llmproxy.ResponseMetadata{}, nil, http.ErrHandlerTimeout
	}

	_, _, _, err := metrics.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	if err == nil {
		t.Fatal("expected error")
	}

	if m.TotalRequests != 1 {
		t.Errorf("TotalRequests = %d, want 1", m.TotalRequests)
	}
	if m.Errors != 1 {
		t.Errorf("Errors = %d, want 1", m.Errors)
	}
	if m.TotalTokens != 0 {
		t.Errorf("TotalTokens = %d, want 0 (no tokens on error)", m.TotalTokens)
	}
}

func TestMetricsInterceptor_MultipleRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	m := &Metrics{}
	metrics := NewMetrics(m)

	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		respMeta := llmproxy.ResponseMetadata{
			Usage: llmproxy.Usage{TotalTokens: 100},
		}
		return resp, respMeta, nil, nil
	}

	for i := 0; i < 5; i++ {
		req, _ := http.NewRequest("POST", upstream.URL, nil)
		_, _, _, _ = metrics.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	}

	if m.TotalRequests != 5 {
		t.Errorf("TotalRequests = %d, want 5", m.TotalRequests)
	}
	if m.TotalTokens != 500 {
		t.Errorf("TotalTokens = %d, want 500", m.TotalTokens)
	}
}

func TestRetryInterceptor_Success(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	retry := NewRetry(3, time.Millisecond)

	req, _ := http.NewRequest("POST", upstream.URL, http.NoBody)
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	_, _, _, err := retry.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestRetryInterceptor_RetriesOn5xx(t *testing.T) {
	callCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount < 3 {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer upstream.Close()

	retry := NewRetry(3, time.Millisecond)

	req, _ := http.NewRequest("POST", upstream.URL, http.NoBody)
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	resp, _, _, err := retry.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}

	if callCount != 3 {
		t.Errorf("callCount = %d, want 3", callCount)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
}

func TestRetryInterceptor_RetriesOn429(t *testing.T) {
	callCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer upstream.Close()

	retry := NewRetry(3, time.Millisecond)

	req, _ := http.NewRequest("POST", upstream.URL, http.NoBody)
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	resp, _, _, err := retry.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}

	if callCount != 2 {
		t.Errorf("callCount = %d, want 2", callCount)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
}

func TestRetryInterceptor_ExhaustedAttempts(t *testing.T) {
	callCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	retry := NewRetry(3, time.Millisecond)

	req, _ := http.NewRequest("POST", upstream.URL, http.NoBody)
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	resp, _, _, _ := retry.Intercept(req, llmproxy.BodyMetadata{}, nil, next)

	if callCount != 3 {
		t.Errorf("callCount = %d, want 3", callCount)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", resp.StatusCode)
	}
}

func TestRetryInterceptor_NoRetryOn200(t *testing.T) {
	callCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	retry := NewRetry(3, time.Millisecond)

	req, _ := http.NewRequest("POST", upstream.URL, http.NoBody)
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	_, _, _, _ = retry.Intercept(req, llmproxy.BodyMetadata{}, nil, next)

	if callCount != 1 {
		t.Errorf("callCount = %d, want 1 (no retry on success)", callCount)
	}
}

func TestRetryInterceptor_CustomPredicate(t *testing.T) {
	callCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.WriteHeader(http.StatusBadGateway)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer upstream.Close()

	retry := NewRetryWithPredicate(3, time.Millisecond, func(resp *http.Response, err error) bool {
		return resp.StatusCode == http.StatusBadGateway
	})

	req, _ := http.NewRequest("POST", upstream.URL, http.NoBody)
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	resp, _, _, _ := retry.Intercept(req, llmproxy.BodyMetadata{}, nil, next)

	if callCount != 2 {
		t.Errorf("callCount = %d, want 2", callCount)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
}

func TestRetryInterceptor_RetryAfterHeader(t *testing.T) {
	callCount := 0
	var retryAfter int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer upstream.Close()

	retry := NewRetryWithRateLimitHeaders(3, 5*time.Second)

	start := time.Now()
	req, _ := http.NewRequest("POST", upstream.URL, http.NoBody)
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	_, _, _, _ = retry.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	elapsed := time.Since(start)

	if callCount != 2 {
		t.Errorf("callCount = %d, want 2", callCount)
	}
	if elapsed < time.Duration(retryAfter)*time.Second {
		t.Errorf("elapsed = %v, should have waited at least %v", elapsed, time.Duration(retryAfter)*time.Second)
	}
}

func TestRetryInterceptor_RetryAfterDateHeader(t *testing.T) {
	callCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			retryTime := time.Now().Add(500 * time.Millisecond)
			w.Header().Set("Retry-After", retryTime.UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusTooManyRequests)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer upstream.Close()

	retry := NewRetryWithRateLimitHeaders(3, 5*time.Second)

	start := time.Now()
	req, _ := http.NewRequest("POST", upstream.URL, http.NoBody)
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	_, _, _, _ = retry.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	elapsed := time.Since(start)

	if callCount != 2 {
		t.Errorf("callCount = %d, want 2", callCount)
	}
	if elapsed < 400*time.Millisecond {
		t.Errorf("elapsed = %v, should have waited for Retry-After date", elapsed)
	}
}

func TestRetryInterceptor_XRateLimitReset(t *testing.T) {
	callCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.Header().Set("X-RateLimit-Reset", "1")
			w.WriteHeader(http.StatusTooManyRequests)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer upstream.Close()

	retry := NewRetryWithRateLimitHeaders(3, 5*time.Second)

	start := time.Now()
	req, _ := http.NewRequest("POST", upstream.URL, http.NoBody)
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	_, _, _, _ = retry.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	elapsed := time.Since(start)

	if callCount != 2 {
		t.Errorf("callCount = %d, want 2", callCount)
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("elapsed = %v, should have used X-RateLimit-Reset header", elapsed)
	}
}

func TestRetryInterceptor_RateLimitHeadersFallback(t *testing.T) {
	callCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer upstream.Close()

	retry := NewRetryWithRateLimitHeaders(3, 10*time.Millisecond)

	start := time.Now()
	req, _ := http.NewRequest("POST", upstream.URL, http.NoBody)
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	_, _, _, _ = retry.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	elapsed := time.Since(start)

	if callCount != 2 {
		t.Errorf("callCount = %d, want 2", callCount)
	}
	if elapsed < 8*time.Millisecond {
		t.Errorf("elapsed = %v, should have used default delay as fallback", elapsed)
	}
}

func TestParseRetryAfterHeader_Seconds(t *testing.T) {
	resp := &http.Response{Header: make(http.Header)}
	resp.Header.Set("Retry-After", "30")

	delay := parseRetryAfterHeader(resp)
	if delay != 30*time.Second {
		t.Errorf("delay = %v, want 30s", delay)
	}
}

func TestParseRetryAfterHeader_Date(t *testing.T) {
	future := time.Now().Add(60 * time.Second)
	resp := &http.Response{Header: make(http.Header)}
	resp.Header.Set("Retry-After", future.UTC().Format(http.TimeFormat))

	delay := parseRetryAfterHeader(resp)
	if delay < 50*time.Second || delay > 70*time.Second {
		t.Errorf("delay = %v, want ~60s", delay)
	}
}

func TestParseRetryAfterHeader_XRateLimitReset(t *testing.T) {
	resp := &http.Response{Header: make(http.Header)}
	resp.Header.Set("X-RateLimit-Reset", "45")

	delay := parseRetryAfterHeader(resp)
	if delay != 45*time.Second {
		t.Errorf("delay = %v, want 45s", delay)
	}
}

func TestParseRetryAfterHeader_Empty(t *testing.T) {
	resp := &http.Response{Header: make(http.Header)}

	delay := parseRetryAfterHeader(resp)
	if delay != 0 {
		t.Errorf("delay = %v, want 0 (no header)", delay)
	}
}

func TestParseRetryAfterHeader_Invalid(t *testing.T) {
	resp := &http.Response{Header: make(http.Header)}
	resp.Header.Set("Retry-After", "invalid")

	delay := parseRetryAfterHeader(resp)
	if delay != 0 {
		t.Errorf("delay = %v, want 0 (invalid header)", delay)
	}
}

func TestParseRetryAfterHeader_TooLarge(t *testing.T) {
	resp := &http.Response{Header: make(http.Header)}
	resp.Header.Set("Retry-After", "86401")

	delay := parseRetryAfterHeader(resp)
	if delay != 0 {
		t.Errorf("delay = %v, want 0 (>24h is ignored)", delay)
	}
}

func TestParseRetryAfterHeader_RetryAfterPreferred(t *testing.T) {
	resp := &http.Response{Header: make(http.Header)}
	resp.Header.Set("Retry-After", "10")
	resp.Header.Set("X-RateLimit-Reset", "20")

	delay := parseRetryAfterHeader(resp)
	if delay != 10*time.Second {
		t.Errorf("delay = %v, want 10s (Retry-After takes precedence)", delay)
	}
}

func TestNewRetryWithRateLimitHeaders(t *testing.T) {
	retry := NewRetryWithRateLimitHeaders(5, time.Second)
	if retry.MaxAttempts != 5 {
		t.Errorf("MaxAttempts = %d, want 5", retry.MaxAttempts)
	}
	if retry.Delay != time.Second {
		t.Errorf("Delay = %v, want 1s", retry.Delay)
	}
	if !retry.UseRateLimitHeaders {
		t.Error("UseRateLimitHeaders should be true")
	}
}

func TestLoggingInterceptor_Success(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	var loggedMessages []string
	logger := llmproxy.LoggerFunc(func(level, msg string, args ...interface{}) {
		loggedMessages = append(loggedMessages, level+":"+msg)
	})

	logging := NewLogging(logger)

	req, _ := http.NewRequest("POST", upstream.URL, nil)
	meta := llmproxy.BodyMetadata{Model: "gpt-4"}
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		respMeta := llmproxy.ResponseMetadata{
			Usage: llmproxy.Usage{PromptTokens: 100, CompletionTokens: 50},
		}
		return resp, respMeta, nil, nil
	}

	_, _, _, err := logging.Intercept(req, meta, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}

	if len(loggedMessages) < 2 {
		t.Errorf("expected at least 2 log messages, got %d", len(loggedMessages))
	}
}

func TestLoggingInterceptor_Error(t *testing.T) {
	var loggedMessages []string
	logger := llmproxy.LoggerFunc(func(level, msg string, args ...interface{}) {
		loggedMessages = append(loggedMessages, level+":"+msg)
	})

	logging := NewLogging(logger)

	req, _ := http.NewRequest("POST", "http://example.com", nil)
	meta := llmproxy.BodyMetadata{Model: "gpt-4"}
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		return nil, llmproxy.ResponseMetadata{}, nil, http.ErrHandlerTimeout
	}

	_, _, _, err := logging.Intercept(req, meta, nil, next)
	if err == nil {
		t.Fatal("expected error")
	}

	var hasErrorLog bool
	for _, msg := range loggedMessages {
		if len(msg) > 5 && msg[:5] == "error" {
			hasErrorLog = true
			break
		}
	}
	if !hasErrorLog {
		t.Error("expected error log message")
	}
}

func TestLoggingInterceptor_NilLogger(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	logging := NewLogging(nil)

	req, _ := http.NewRequest("POST", upstream.URL, nil)
	next := func(req *http.Request) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, llmproxy.ResponseMetadata{}, nil, err
		}
		return resp, llmproxy.ResponseMetadata{}, nil, nil
	}

	_, _, _, err := logging.Intercept(req, llmproxy.BodyMetadata{}, nil, next)
	if err != nil {
		t.Fatalf("Intercept returned error: %v", err)
	}
}

func TestLoggerFunc(t *testing.T) {
	var calls []string
	logger := llmproxy.LoggerFunc(func(level, msg string, args ...interface{}) {
		calls = append(calls, level)
	})

	logger.Debug("test")
	logger.Info("test")
	logger.Warn("test")
	logger.Error("test")

	expected := []string{"debug", "info", "warn", "error"}
	for i, exp := range expected {
		if calls[i] != exp {
			t.Errorf("call %d = %s, want %s", i, calls[i], exp)
		}
	}
}
