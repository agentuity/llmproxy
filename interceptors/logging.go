package interceptors

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/agentuity/llmproxy"
)

// LoggingInterceptor logs request and response details.
// It records the model, method, URL, latency, and token usage.
type LoggingInterceptor struct {
	// Logger is the destination for log output.
	// If nil, a default logger is used.
	Logger llmproxy.Logger
}

// Intercept logs the request before execution and the response after.
// Log format:
//   - Request: [model] METHOD /path
//   - Success: [model] OK: tokens=prompt/completion (duration)
//   - Error: [model] ERROR: err (duration)
func (i *LoggingInterceptor) Intercept(req *http.Request, meta llmproxy.BodyMetadata, rawBody []byte, next llmproxy.RoundTripFunc) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
	start := time.Now()
	logger := i.Logger
	if logger == nil {
		logger = &defaultLogger{}
	}

	logger.Debug("[%s] %s %s", meta.Model, req.Method, req.URL.Path)

	resp, respMeta, rawRespBody, err := next(req)

	duration := time.Since(start)
	if err != nil {
		logger.Error("[%s] ERROR: %v (%v)", meta.Model, err, duration)
		return resp, respMeta, rawRespBody, err
	}

	logger.Info("[%s] OK: tokens=%d/%d (%v)", meta.Model, respMeta.Usage.PromptTokens, respMeta.Usage.CompletionTokens, duration)
	return resp, respMeta, rawRespBody, err
}

// NewLogging creates a new logging interceptor with the given logger.
// Pass nil to use a default logger that wraps log.Default().
func NewLogging(logger llmproxy.Logger) *LoggingInterceptor {
	return &LoggingInterceptor{Logger: logger}
}

// defaultLogger wraps log.Default() to implement llmproxy.Logger.
type defaultLogger struct{}

func (d *defaultLogger) Debug(msg string, args ...interface{}) {
	log.Default().Println("[DEBUG] " + fmt.Sprintf(msg, args...))
}

func (d *defaultLogger) Info(msg string, args ...interface{}) {
	log.Default().Println("[INFO] " + fmt.Sprintf(msg, args...))
}

func (d *defaultLogger) Warn(msg string, args ...interface{}) {
	log.Default().Println("[WARN] " + fmt.Sprintf(msg, args...))
}

func (d *defaultLogger) Error(msg string, args ...interface{}) {
	log.Default().Println("[ERROR] " + fmt.Sprintf(msg, args...))
}
