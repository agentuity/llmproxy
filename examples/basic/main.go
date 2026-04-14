package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/agentuity/llmproxy"
	"github.com/agentuity/llmproxy/interceptors"
	"github.com/agentuity/llmproxy/pricing/modelsdev"
	"github.com/agentuity/llmproxy/providers/anthropic"
	"github.com/agentuity/llmproxy/providers/azure"
	"github.com/agentuity/llmproxy/providers/bedrock"
	"github.com/agentuity/llmproxy/providers/fireworks"
	"github.com/agentuity/llmproxy/providers/googleai"
	"github.com/agentuity/llmproxy/providers/groq"
	"github.com/agentuity/llmproxy/providers/openai"
	"github.com/agentuity/llmproxy/providers/perplexity"
	"github.com/agentuity/llmproxy/providers/xai"
	"go.opentelemetry.io/otel/trace"
)

type consoleLogger struct {
	prefix string
}

func (l *consoleLogger) Debug(msg string, args ...interface{}) {
	log.Println("[DEBUG]", l.prefix, fmt.Sprintf(msg, args...))
}

func (l *consoleLogger) Info(msg string, args ...interface{}) {
	log.Println("[INFO]", l.prefix, fmt.Sprintf(msg, args...))
}

func (l *consoleLogger) Warn(msg string, args ...interface{}) {
	log.Println("[WARN]", l.prefix, fmt.Sprintf(msg, args...))
}

func (l *consoleLogger) Error(msg string, args ...interface{}) {
	log.Println("[ERROR]", l.prefix, fmt.Sprintf(msg, args...))
}

func main() {
	logr := &consoleLogger{prefix: "[llmproxy]"}

	var providers []llmproxy.Provider

	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		provider, err := openai.New(apiKey)
		if err != nil {
			log.Fatalf("failed to create openai provider: %v", err)
		}
		providers = append(providers, provider)
		logr.Info("Registered: OpenAI")
	}

	if apiKey := os.Getenv("ANTHROPIC_API_KEY"); apiKey != "" {
		provider, err := anthropic.New(apiKey)
		if err != nil {
			log.Fatalf("failed to create anthropic provider: %v", err)
		}
		providers = append(providers, provider)
		logr.Info("Registered: Anthropic")
	}

	if apiKey := os.Getenv("GROQ_API_KEY"); apiKey != "" {
		provider, err := groq.New(apiKey)
		if err != nil {
			log.Fatalf("failed to create groq provider: %v", err)
		}
		providers = append(providers, provider)
		logr.Info("Registered: Groq")
	}

	if apiKey := os.Getenv("FIREWORKS_API_KEY"); apiKey != "" {
		provider, err := fireworks.New(apiKey)
		if err != nil {
			log.Fatalf("failed to create fireworks provider: %v", err)
		}
		providers = append(providers, provider)
		logr.Info("Registered: Fireworks")
	}

	if apiKey := os.Getenv("XAI_API_KEY"); apiKey != "" {
		provider, err := xai.New(apiKey)
		if err != nil {
			log.Fatalf("failed to create xai provider: %v", err)
		}
		providers = append(providers, provider)
		logr.Info("Registered: x.AI")
	}

	if apiKey := os.Getenv("PERPLEXITY_API_KEY"); apiKey != "" {
		provider, err := perplexity.New(apiKey)
		if err != nil {
			log.Fatalf("failed to create perplexity provider: %v", err)
		}
		providers = append(providers, provider)
		logr.Info("Registered: Perplexity")
	}

	if apiKey := os.Getenv("GOOGLE_AI_API_KEY"); apiKey != "" {
		provider, err := googleai.New(apiKey)
		if err != nil {
			log.Fatalf("failed to create googleai provider: %v", err)
		}
		providers = append(providers, provider)
		logr.Info("Registered: Google AI")
	}

	if resourceName := os.Getenv("AZURE_OPENAI_RESOURCE"); resourceName != "" {
		deploymentID := os.Getenv("AZURE_OPENAI_DEPLOYMENT")
		apiVersion := os.Getenv("AZURE_OPENAI_API_VERSION")
		if apiVersion == "" {
			apiVersion = azure.DefaultAPIVersion()
		}

		var opts []azure.Option
		if apiKey := os.Getenv("AZURE_OPENAI_API_KEY"); apiKey != "" {
			opts = append(opts, azure.WithAPIKey(apiKey))
		}

		if len(opts) > 0 {
			provider, err := azure.New(resourceName, deploymentID, apiVersion, opts...)
			if err != nil {
				log.Fatalf("failed to create azure provider: %v", err)
			}
			providers = append(providers, provider)
			logr.Info("Registered: Azure OpenAI")
		}
	}

	if region := os.Getenv("AWS_REGION"); region != "" {
		accessKeyID := os.Getenv("AWS_ACCESS_KEY_ID")
		secretAccessKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
		sessionToken := os.Getenv("AWS_SESSION_TOKEN")

		if accessKeyID != "" && secretAccessKey != "" {
			provider, err := bedrock.New(region, accessKeyID, secretAccessKey, sessionToken)
			if err != nil {
				log.Fatalf("failed to create bedrock provider: %v", err)
			}
			providers = append(providers, provider)
			logr.Info("Registered: AWS Bedrock")
		}
	}

	if len(providers) == 0 {
		log.Fatal("No providers configured. Set at least one API key environment variable.")
	}

	var costLookup llmproxy.CostLookup
	var modelProviderLookup llmproxy.ModelProviderLookup
	modelsFile := os.Getenv("MODELS_DEV_JSON")
	modelsURL := os.Getenv("MODELS_DEV_URL")
	var adapter *modelsdev.Adapter
	var err error

	if modelsFile != "" {
		adapter, err = modelsdev.LoadFromFile(modelsFile)
		if err != nil {
			log.Printf("Warning: could not load models.dev from file: %v", err)
		} else {
			costLookup = adapter.GetCostLookup()
			modelProviderLookup = adapter.FindProviderForModel
			logr.Info("Billing enabled from file: %s", modelsFile)
		}
	} else if modelsURL != "" {
		adapter = modelsdev.New(modelsdev.WithURL(modelsURL))
		if err := adapter.Load(nil); err != nil {
			log.Printf("Warning: could not load models.dev from URL: %v", err)
		} else {
			costLookup = adapter.GetCostLookup()
			modelProviderLookup = adapter.FindProviderForModel
			logr.Info("Billing enabled from URL: %s", modelsURL)
		}
	} else {
		adapter, err = modelsdev.LoadFromURL()
		if err != nil {
			log.Printf("Warning: could not fetch models.dev: %v (billing disabled)", err)
		} else {
			costLookup = adapter.GetCostLookup()
			modelProviderLookup = adapter.FindProviderForModel
			logr.Info("Billing enabled from https://models.dev/api.json")
		}
	}

	metrics := &interceptors.Metrics{}
	loggingInterceptor := interceptors.NewLogging(logr)

	tracingInterceptor := interceptors.NewTracing(func(ctx context.Context) interceptors.TraceInfo {
		span := trace.SpanFromContext(ctx)
		if !span.SpanContext().IsValid() {
			return interceptors.TraceInfo{}
		}
		return interceptors.TraceInfo{
			TraceID: span.SpanContext().TraceID(),
			SpanID:  span.SpanContext().SpanID(),
			Sampled: span.SpanContext().IsSampled(),
		}
	})

	opts := []llmproxy.AutoRouterOption{
		llmproxy.WithAutoRouterInterceptor(interceptors.NewRetryWithRateLimitHeaders(3, time.Millisecond*250)),
		llmproxy.WithAutoRouterInterceptor(tracingInterceptor),
		llmproxy.WithAutoRouterInterceptor(loggingInterceptor),
		llmproxy.WithAutoRouterInterceptor(interceptors.NewMetrics(metrics)),
		llmproxy.WithAutoRouterInterceptor(interceptors.NewResponseHeaderBan("Openai-Organization", "Openai-Project", "Set-Cookie")),
		llmproxy.WithAutoRouterInterceptor(interceptors.NewAddRequestHeader(interceptors.NewHeader("User-Agent", "Agentuity AI Gateway/1.0"))),
		llmproxy.WithAutoRouterInterceptor(interceptors.NewAddResponseHeader(interceptors.NewHeader("Server", "Agentuity AI Gateway/1.0"))),
		llmproxy.WithAutoRouterFallbackProvider(providers[0]),
	}

	if modelProviderLookup != nil {
		opts = append(opts, llmproxy.WithAutoRouterModelProviderLookup(modelProviderLookup))
	}

	if costLookup != nil {
		opts = append(opts, llmproxy.WithAutoRouterInterceptor(interceptors.NewBilling(costLookup, func(r llmproxy.BillingResult) {
			logr.Info("Billing: provider=%s model=%s tokens=%d/%d cost=$%.6f", r.Provider, r.Model, r.PromptTokens, r.CompletionTokens, r.TotalCost)
		})))
	}

	router := llmproxy.NewAutoRouter(opts...)

	for _, p := range providers {
		router.RegisterProvider(p)
	}

	http.Handle("/", router)

	logr.Info("Proxy listening on :8080")
	logr.Info("")
	logr.Info("Auto-routing enabled - POST to any endpoint or just '/'")
	logr.Info("Provider detected from:")
	logr.Info("  1. X-Provider header (explicit override)")
	logr.Info("  2. Model name pattern (gpt-* -> OpenAI, claude-* -> Anthropic, etc.)")
	if modelProviderLookup != nil {
		logr.Info("  3. models.dev registry (fallback for unknown models)")
	}
	logr.Info("")
	logr.Info("API type detected from:")
	logr.Info("  1. Request path (/v1/messages, /v1/responses, etc.)")
	logr.Info("  2. Request body shape (input -> Responses, messages -> Chat/Messages)")
	logr.Info("")
	logr.Info("Supported endpoints (all optional - POST to / works too):")
	logr.Info("  POST /                      (auto-detect from body)")
	logr.Info("  POST /v1/chat/completions   (OpenAI Chat Completions API)")
	logr.Info("  POST /v1/responses          (OpenAI Responses API)")
	logr.Info("  POST /v1/messages           (Anthropic Messages API)")
	logr.Info("  POST /v1/completions        (Legacy OpenAI Completions API)")
	logr.Info("")
	logr.Info("Example requests:")
	logr.Info("  # Auto-detect everything - POST to /")
	logr.Info("  curl -X POST http://localhost:8080/ \\")
	logr.Info("    -H 'Content-Type: application/json' \\")
	logr.Info("    -d '{\"model\":\"gpt-4\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello\"}]}'")
	logr.Info("")
	logr.Info("  # Auto-detect Anthropic from model name")
	logr.Info("  curl -X POST http://localhost:8080/ \\")
	logr.Info("    -H 'Content-Type: application/json' \\")
	logr.Info("    -d '{\"model\":\"claude-3-opus\",\"max_tokens\":1024,\"messages\":[{\"role\":\"user\",\"content\":\"Hello\"}]}'")
	logr.Info("")
	logr.Info("  # Use Responses API with input field")
	logr.Info("  curl -X POST http://localhost:8080/ \\")
	logr.Info("    -H 'Content-Type: application/json' \\")
	logr.Info("    -d '{\"model\":\"gpt-4o\",\"input\":\"Hello\"}'")

	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
