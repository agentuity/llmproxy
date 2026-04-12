// Example_basic demonstrates a basic proxy setup with multiple providers.
// Providers are configured from standard environment variables.
//
// Usage:
//
//	export OPENAI_API_KEY=sk-your-key
//	export MODELS_DEV_JSON=/path/to/models.json  # optional, for billing
//	go run main.go
//
// With OpenTelemetry tracing:
//
//	otelExporter, _ := otlptracehttp.New(ctx)
//	tp := tracesdk.NewTracerProvider(tracesdk.WithBatcher(otelExporter))
//	defer tp.Shutdown(ctx)
//	otel.SetTracerProvider(tp)
//	# Then run the example - traces will be propagated upstream
package main

import (
	"context"
	"fmt"
	"io"
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
	"github.com/agentuity/llmproxy/providers/xai"
	"go.opentelemetry.io/otel/trace"
)

// consoleLogger implements llmproxy.Logger using log.Default()
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
	ctx := context.Background()
	logr := &consoleLogger{prefix: "[llmproxy]"}

	registry := llmproxy.NewRegistry()

	// Register providers from environment variables
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		provider, err := openai.New(apiKey)
		if err != nil {
			log.Fatalf("failed to create openai provider: %v", err)
		}
		registry.Register(provider)
		logr.Info("Registered: OpenAI")
	}

	if apiKey := os.Getenv("ANTHROPIC_API_KEY"); apiKey != "" {
		provider, err := anthropic.New(apiKey)
		if err != nil {
			log.Fatalf("failed to create anthropic provider: %v", err)
		}
		registry.Register(provider)
		logr.Info("Registered: Anthropic")
	}

	if apiKey := os.Getenv("GROQ_API_KEY"); apiKey != "" {
		provider, err := groq.New(apiKey)
		if err != nil {
			log.Fatalf("failed to create groq provider: %v", err)
		}
		registry.Register(provider)
		logr.Info("Registered: Groq")
	}

	if apiKey := os.Getenv("FIREWORKS_API_KEY"); apiKey != "" {
		provider, err := fireworks.New(apiKey)
		if err != nil {
			log.Fatalf("failed to create fireworks provider: %v", err)
		}
		registry.Register(provider)
		logr.Info("Registered: Fireworks")
	}

	if apiKey := os.Getenv("XAI_API_KEY"); apiKey != "" {
		provider, err := xai.New(apiKey)
		if err != nil {
			log.Fatalf("failed to create xai provider: %v", err)
		}
		registry.Register(provider)
		logr.Info("Registered: x.AI")
	}

	if apiKey := os.Getenv("GOOGLE_AI_API_KEY"); apiKey != "" {
		provider, err := googleai.New(apiKey)
		if err != nil {
			log.Fatalf("failed to create googleai provider: %v", err)
		}
		registry.Register(provider)
		logr.Info("Registered: Google AI")
	}

	// Azure OpenAI
	if resourceName := os.Getenv("AZURE_OPENAI_RESOURCE"); resourceName != "" {
		deploymentID := os.Getenv("AZURE_OPENAI_DEPLOYMENT") // optional, uses model from request if empty
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
			registry.Register(provider)
			logr.Info("Registered: Azure OpenAI")
		}
	}

	// AWS Bedrock requires multiple environment variables
	if region := os.Getenv("AWS_REGION"); region != "" {
		accessKeyID := os.Getenv("AWS_ACCESS_KEY_ID")
		secretAccessKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
		sessionToken := os.Getenv("AWS_SESSION_TOKEN") // Optional

		if accessKeyID != "" && secretAccessKey != "" {
			provider, err := bedrock.New(region, accessKeyID, secretAccessKey, sessionToken)
			if err != nil {
				log.Fatalf("failed to create bedrock provider: %v", err)
			}
			registry.Register(provider)
			logr.Info("Registered: AWS Bedrock")
		}
	}

	// Check we have at least one provider
	openaiProvider, hasOpenAI := registry.Get("openai")
	if !hasOpenAI {
		openaiProvider, _ = registry.Get("groq")
	}
	if openaiProvider == nil {
		for _, name := range []string{"anthropic", "fireworks", "xai", "googleai"} {
			if p, ok := registry.Get(name); ok {
				openaiProvider = p
				break
			}
		}
	}

	if openaiProvider == nil {
		log.Fatal("No providers configured. Set at least one API key environment variable.")
	}

	// Load pricing data from models.dev (optional)
	var costLookup llmproxy.CostLookup
	modelsFile := os.Getenv("MODELS_DEV_JSON")
	modelsURL := os.Getenv("MODELS_DEV_URL")

	if modelsFile != "" {
		// Load from local file
		adapter, err := modelsdev.LoadFromFile(modelsFile)
		if err != nil {
			log.Printf("Warning: could not load models.dev from file: %v", err)
		} else {
			costLookup = adapter.GetCostLookup()
			logr.Info("Billing enabled from file: %s", modelsFile)
		}
	} else if modelsURL != "" {
		// Load from custom URL
		adapter := modelsdev.New(modelsdev.WithURL(modelsURL))
		if err := adapter.Load(nil); err != nil {
			log.Printf("Warning: could not load models.dev from URL: %v", err)
		} else {
			costLookup = adapter.GetCostLookup()
			logr.Info("Billing enabled from URL: %s", modelsURL)
		}
	} else {
		// Try to fetch from models.dev directly
		adapter, err := modelsdev.LoadFromURL()
		if err != nil {
			log.Printf("Warning: could not fetch models.dev: %v (billing disabled)", err)
		} else {
			costLookup = adapter.GetCostLookup()
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

	// OpenAI-compatible endpoint
	http.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		provider := openaiProvider
		opts := []llmproxy.ProxyOption{
			llmproxy.WithInterceptor(interceptors.NewRetry(3, time.Millisecond*250)),
			llmproxy.WithInterceptor(tracingInterceptor),
			llmproxy.WithInterceptor(loggingInterceptor),
			llmproxy.WithInterceptor(interceptors.NewMetrics(metrics)),
			llmproxy.WithInterceptor(interceptors.NewResponseHeaderBan("Openai-Organization", "Openai-Project", "Set-Cookie")),
			llmproxy.WithInterceptor(interceptors.NewAddRequestHeader(interceptors.NewHeader("User-Agent", "Agentuity AI Gateway/1.0"))),
			llmproxy.WithInterceptor(interceptors.NewAddResponseHeader(interceptors.NewHeader("Server", "Agentuity AI Gateway/1.0"))),
		}
		if costLookup != nil {
			opts = append(opts, llmproxy.WithInterceptor(interceptors.NewBilling(costLookup, func(r llmproxy.BillingResult) {
				logr.Info("Billing: model=%s tokens=%d/%d cost=$%.6f", r.Model, r.PromptTokens, r.CompletionTokens, r.TotalCost)
				w.Header().Set("agentuity-gateway-cost", fmt.Sprintf("%f", r.TotalCost))
				w.Header().Set("agentuity-gateway-prompt-tokens", fmt.Sprintf("%d", r.PromptTokens))
				w.Header().Set("agentuity-gateway-completion-tokens", fmt.Sprintf("%d", r.CompletionTokens))
			})))
		}
		proxy := llmproxy.NewProxy(provider, opts...)
		resp, _, err := proxy.Forward(ctx, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})

	// Anthropic endpoint
	anthropicProvider, hasAnthropic := registry.Get("anthropic")
	if hasAnthropic {
		http.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
			opts := []llmproxy.ProxyOption{
				llmproxy.WithInterceptor(tracingInterceptor),
				llmproxy.WithInterceptor(loggingInterceptor),
			}
			if costLookup != nil {
				opts = append(opts, llmproxy.WithInterceptor(interceptors.NewBilling(costLookup, func(r llmproxy.BillingResult) {
					logr.Info("Billing: model=%s tokens=%d/%d cost=$%.6f", r.Model, r.PromptTokens, r.CompletionTokens, r.TotalCost)
				})))
			}
			proxy := llmproxy.NewProxy(anthropicProvider, opts...)
			resp, _, err := proxy.Forward(ctx, r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer resp.Body.Close()

			for k, v := range resp.Header {
				w.Header()[k] = v
			}
			w.WriteHeader(resp.StatusCode)
			io.Copy(w, resp.Body)
		})
	}

	// Google AI endpoint
	googleaiProvider, hasGoogleAI := registry.Get("googleai")
	if hasGoogleAI {
		http.HandleFunc("/v1beta/models/", func(w http.ResponseWriter, r *http.Request) {
			opts := []llmproxy.ProxyOption{
				llmproxy.WithInterceptor(tracingInterceptor),
				llmproxy.WithInterceptor(loggingInterceptor),
			}
			if costLookup != nil {
				opts = append(opts, llmproxy.WithInterceptor(interceptors.NewBilling(costLookup, func(r llmproxy.BillingResult) {
					logr.Info("Billing: model=%s tokens=%d/%d cost=$%.6f", r.Model, r.PromptTokens, r.CompletionTokens, r.TotalCost)
				})))
			}
			proxy := llmproxy.NewProxy(googleaiProvider, opts...)
			resp, _, err := proxy.Forward(ctx, r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer resp.Body.Close()

			for k, v := range resp.Header {
				w.Header()[k] = v
			}
			w.WriteHeader(resp.StatusCode)
			io.Copy(w, resp.Body)
		})
	}

	// Azure OpenAI endpoint
	azureProvider, hasAzure := registry.Get("azure")
	if hasAzure {
		http.HandleFunc("/azure/openai/deployments/", func(w http.ResponseWriter, r *http.Request) {
			opts := []llmproxy.ProxyOption{
				llmproxy.WithInterceptor(tracingInterceptor),
				llmproxy.WithInterceptor(loggingInterceptor),
			}
			if costLookup != nil {
				opts = append(opts, llmproxy.WithInterceptor(interceptors.NewBilling(costLookup, func(r llmproxy.BillingResult) {
					logr.Info("Billing: model=%s tokens=%d/%d cost=$%.6f", r.Model, r.PromptTokens, r.CompletionTokens, r.TotalCost)
				})))
			}
			proxy := llmproxy.NewProxy(azureProvider, opts...)
			resp, _, err := proxy.Forward(ctx, r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer resp.Body.Close()

			for k, v := range resp.Header {
				w.Header()[k] = v
			}
			w.WriteHeader(resp.StatusCode)
			io.Copy(w, resp.Body)
		})
	}

	logr.Info("Proxy listening on :8080")
	logr.Info("Endpoints:")
	logr.Info("  POST /v1/chat/completions -> OpenAI-compatible providers")
	if hasAnthropic {
		logr.Info("  POST /v1/messages -> Anthropic")
	}
	if hasGoogleAI {
		logr.Info("  POST /v1beta/models/{model}:generateContent -> Google AI")
	}
	if hasAzure {
		logr.Info("  POST /azure/openai/deployments/{deployment}/chat/completions -> Azure OpenAI")
	}

	// Bedrock endpoint
	bedrockProvider, hasBedrock := registry.Get("bedrock")
	if hasBedrock {
		http.HandleFunc("/model/", func(w http.ResponseWriter, r *http.Request) {
			// Extract model ID from path: /model/{modelId}/converse or /model/{modelId}/invoke
			opts := []llmproxy.ProxyOption{
				llmproxy.WithInterceptor(tracingInterceptor),
				llmproxy.WithInterceptor(loggingInterceptor),
			}
			if costLookup != nil {
				opts = append(opts, llmproxy.WithInterceptor(interceptors.NewBilling(costLookup, func(r llmproxy.BillingResult) {
					logr.Info("Billing: model=%s tokens=%d/%d cost=$%.6f", r.Model, r.PromptTokens, r.CompletionTokens, r.TotalCost)
				})))
			}
			proxy := llmproxy.NewProxy(bedrockProvider, opts...)
			resp, _, err := proxy.Forward(ctx, r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer resp.Body.Close()

			for k, v := range resp.Header {
				w.Header()[k] = v
			}
			w.WriteHeader(resp.StatusCode)
			io.Copy(w, resp.Body)
		})
		if hasBedrock {
			logr.Info("  POST /model/{modelId}/converse -> AWS Bedrock (Converse API)")
		}
	}

	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
