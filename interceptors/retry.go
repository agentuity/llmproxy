package interceptors

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/agentuity/llmproxy"
)

type RetryInterceptor struct {
	MaxAttempts         int
	Delay               time.Duration
	IsRetryable         func(*http.Response, error) bool
	UseRateLimitHeaders bool
}

func (i *RetryInterceptor) Intercept(req *http.Request, meta llmproxy.BodyMetadata, rawBody []byte, next llmproxy.RoundTripFunc) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
	var lastErr error
	var lastResp *http.Response
	var lastMeta llmproxy.ResponseMetadata
	var lastRawRespBody []byte

	isRetryable := i.IsRetryable
	if isRetryable == nil {
		isRetryable = defaultIsRetryable
	}

	for attempt := 1; attempt <= i.MaxAttempts; attempt++ {
		if attempt > 1 {
			delay := i.Delay
			if i.UseRateLimitHeaders && lastResp != nil {
				if headerDelay := parseRetryAfterHeader(lastResp); headerDelay > 0 {
					delay = headerDelay
				}
			}

			select {
			case <-time.After(delay):
			case <-req.Context().Done():
				return nil, lastMeta, lastRawRespBody, req.Context().Err()
			}
		}

		req.Body.Close()
		req = cloneRequest(req, rawBody)

		lastResp, lastMeta, lastRawRespBody, lastErr = next(req)
		if !isRetryable(lastResp, lastErr) {
			return lastResp, lastMeta, lastRawRespBody, lastErr
		}
	}

	return lastResp, lastMeta, lastRawRespBody, lastErr
}

func defaultIsRetryable(resp *http.Response, err error) bool {
	if err != nil {
		if isContextError(err) {
			return false
		}
		return true
	}
	return resp.StatusCode == 429 || resp.StatusCode >= 500
}

func isContextError(err error) bool {
	return err == context.Canceled || err == context.DeadlineExceeded
}

func parseRetryAfterHeader(resp *http.Response) time.Duration {
	retryAfter := resp.Header.Get("Retry-After")
	if retryAfter == "" {
		retryAfter = resp.Header.Get("X-RateLimit-Reset")
	}
	if retryAfter == "" {
		return 0
	}

	if seconds, err := strconv.Atoi(retryAfter); err == nil {
		if seconds > 0 && seconds <= 86400 {
			return time.Duration(seconds) * time.Second
		}
	}

	if t, err := http.ParseTime(retryAfter); err == nil {
		delay := time.Until(t)
		if delay > 0 && delay <= 24*time.Hour {
			return delay
		}
	}

	return 0
}

func cloneRequest(req *http.Request, body []byte) *http.Request {
	cloned := req.Clone(req.Context())
	cloned.Body = io.NopCloser(bytes.NewReader(body))
	return cloned
}

func NewRetry(maxAttempts int, delay time.Duration) *RetryInterceptor {
	return &RetryInterceptor{
		MaxAttempts: maxAttempts,
		Delay:       delay,
	}
}

func NewRetryWithRateLimitHeaders(maxAttempts int, defaultDelay time.Duration) *RetryInterceptor {
	return &RetryInterceptor{
		MaxAttempts:         maxAttempts,
		Delay:               defaultDelay,
		UseRateLimitHeaders: true,
	}
}

func NewRetryWithPredicate(maxAttempts int, delay time.Duration, isRetryable func(*http.Response, error) bool) *RetryInterceptor {
	return &RetryInterceptor{
		MaxAttempts: maxAttempts,
		Delay:       delay,
		IsRetryable: isRetryable,
	}
}
