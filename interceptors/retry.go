package interceptors

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	"github.com/agentuity/llmproxy"
)

// RetryInterceptor automatically retries failed requests.
// It handles transient failures like rate limits (429) and server errors (5xx).
type RetryInterceptor struct {
	// MaxAttempts is the maximum number of request attempts (including initial).
	MaxAttempts int
	// Delay is the wait time between retry attempts.
	Delay time.Duration
	// IsRetryable is a custom predicate to determine if a request should be retried.
	// If nil, the default predicate is used (retries on 429 and 5xx responses,
	// and on network errors except context cancellation).
	IsRetryable func(*http.Response, error) bool
}

// Intercept attempts the request up to MaxAttempts times.
// Between retries, it waits for the configured Delay.
// Context cancellation (context.Canceled, context.DeadlineExceeded) is not retried.
//
// The rawBody is used to reconstruct the request body for each retry attempt,
// since HTTP request bodies can only be read once.
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
			select {
			case <-time.After(i.Delay):
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

func cloneRequest(req *http.Request, body []byte) *http.Request {
	cloned := req.Clone(req.Context())
	cloned.Body = io.NopCloser(bytes.NewReader(body))
	return cloned
}

// NewRetry creates a retry interceptor with the given configuration.
//
// Parameters:
//   - maxAttempts: Maximum number of attempts (e.g., 3 = initial + 2 retries)
//   - delay: Time to wait between retry attempts
//
// Example:
//
//	retry := interceptors.NewRetry(3, time.Second)
func NewRetry(maxAttempts int, delay time.Duration) *RetryInterceptor {
	return &RetryInterceptor{
		MaxAttempts: maxAttempts,
		Delay:       delay,
	}
}

// NewRetryWithPredicate creates a retry interceptor with a custom retry predicate.
// Use this to customize which responses or errors should be retried.
//
// Example:
//
//	retry := interceptors.NewRetryWithPredicate(3, time.Second, func(resp *http.Response, err error) bool {
//	    // Only retry on specific error conditions
//	    return err != nil || resp.StatusCode == 503
//	})
func NewRetryWithPredicate(maxAttempts int, delay time.Duration, isRetryable func(*http.Response, error) bool) *RetryInterceptor {
	return &RetryInterceptor{
		MaxAttempts: maxAttempts,
		Delay:       delay,
		IsRetryable: isRetryable,
	}
}
