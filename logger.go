package llmproxy

// Logger is an interface for logging.
// It matches the interface from github.com/agentuity/go-common/logger.
// Any logger implementing this interface can be used with interceptors.
type Logger interface {
	// Debug level logging
	Debug(msg string, args ...interface{})
	// Info level logging
	Info(msg string, args ...interface{})
	// Warning level logging
	Warn(msg string, args ...interface{})
	// Error level logging
	Error(msg string, args ...interface{})
}

// LoggerFunc is an adapter to allow using ordinary functions as loggers.
type LoggerFunc func(level string, msg string, args ...interface{})

func (f LoggerFunc) Debug(msg string, args ...interface{}) { f("debug", msg, args...) }
func (f LoggerFunc) Info(msg string, args ...interface{})  { f("info", msg, args...) }
func (f LoggerFunc) Warn(msg string, args ...interface{})  { f("warn", msg, args...) }
func (f LoggerFunc) Error(msg string, args ...interface{}) { f("error", msg, args...) }
