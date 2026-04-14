# llmproxy

A Go library for proxying requests to upstream LLM providers with pluggable, composable architecture.

## Install

```bash
go get github.com/agentuity/llmproxy
```

## Quick Start

### Simple Proxy

```go
package main

import (
    "context"
    "io"
    "net/http"

    "github.com/agentuity/llmproxy"
    "github.com/agentuity/llmproxy/interceptors"
    "github.com/agentuity/llmproxy/providers/openai"
)

func main() {
    ctx := context.Background()

    provider, _ := openai.New("sk-your-key")

    proxy := llmproxy.NewProxy(provider,
        llmproxy.WithInterceptor(interceptors.NewLogging(nil)),
    )

    http.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
        resp, meta, err := proxy.Forward(ctx, r)
        if err != nil {
            http.Error(w, err.Error(), 500)
            return
        }
        defer resp.Body.Close()

        // Response includes token usage
        _ = meta.Usage.PromptTokens
        _ = meta.Usage.CompletionTokens

        io.Copy(w, resp.Body)
    })

    http.ListenAndServe(":8080", nil)
}
```

### AutoRouter (Recommended)

Single endpoint that auto-detects provider and API type:

```go
package main

import (
    "net/http"

    "github.com/agentuity/llmproxy"
    "github.com/agentuity/llmproxy/providers/openai"
    "github.com/agentuity/llmproxy/providers/anthropic"
)

func main() {
    openaiProvider, _ := openai.New("sk-openai-key")
    anthropicProvider, _ := anthropic.New("sk-ant-key")

    router := llmproxy.NewAutoRouter(
        llmproxy.WithAutoRouterFallbackProvider(openaiProvider),
    )
    router.RegisterProvider(openaiProvider)
    router.RegisterProvider(anthropicProvider)

    // Single endpoint handles all providers and APIs
    http.Handle("/", router)
    http.ListenAndServe(":8080", nil)
}
```

POST to `/` with any model - provider and API are auto-detected:

```bash
# Auto-detect OpenAI from gpt-4 model name
curl -X POST http://localhost:8080/ \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4","messages":[{"role":"user","content":"Hello"}]}'

# Auto-detect Anthropic from claude model name  
curl -X POST http://localhost:8080/ \
  -H 'Content-Type: application/json' \
  -d '{"model":"claude-3-opus","max_tokens":1024,"messages":[{"role":"user","content":"Hello"}]}'

# Auto-detect Responses API from input field
curl -X POST http://localhost:8080/ \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o","input":"Hello"}'
```

## Features

- **9 Provider Implementations**: OpenAI, Anthropic, Groq, Fireworks, x.AI, Google AI, AWS Bedrock, Azure OpenAI, OpenAI-compatible base
- **AutoRouter**: Single endpoint with automatic provider/API detection
- **Responses API**: Full support for OpenAI's new Responses API
- **SSE Streaming**: Full streaming support with efficient token usage extraction
- **8 Built-in Interceptors**: Logging, Metrics, Retry, Billing, Tracing (OTel), HeaderBan, AddHeader, PromptCaching
- **Pricing Integration**: models.dev adapter with markup support
- **Prompt Caching**: prompt caching support for Anthropic, OpenAI, xAI, Fireworks, and Bedrock
- **Raw Body Preservation**: Custom JSON fields pass through unchanged

## AutoRouter

The `AutoRouter` provides automatic routing from a single endpoint:

### Detection Order

1. **Path-based** - `/v1/messages` → Messages API, `/v1/responses` → Responses API
2. **Body + Provider** - When path is `/` or unknown:
   - `input` field → Responses API
   - `prompt` field → Completions API  
   - `contents` field → GenerateContent API
   - `messages` + Anthropic → Messages API
   - `messages` + other → Chat Completions

### Provider Detection

1. **X-Provider header** - Explicit override
2. **Model prefix** - `openai/gpt-4` → OpenAI (strips prefix before forwarding)
3. **Model pattern** - `gpt-*` → OpenAI, `claude-*` → Anthropic, etc.

### Examples

```bash
# Explicit provider via header
curl -X POST http://localhost:8080/ \
  -H 'Content-Type: application/json' \
  -H 'X-Provider: anthropic' \
  -d '{"model":"claude-3-opus","max_tokens":1024,"messages":[{"role":"user","content":"Hello"}]}'

# Provider prefix in model (gets stripped)
curl -X POST http://localhost:8080/ \
  -H 'Content-Type: application/json' \
  -d '{"model":"anthropic/claude-3-opus","max_tokens":1024,"messages":[{"role":"user","content":"Hello"}]}'

# Traditional path still works
curl -X POST http://localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4","messages":[{"role":"user","content":"Hello"}]}'
```

## Streaming

SSE streaming is fully supported with automatic token usage extraction for billing:

```bash
# Streaming with automatic usage extraction
curl -X POST http://localhost:8080/ \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4","stream":true,"messages":[{"role":"user","content":"Hello"}]}'
```

**Key Features:**

- **Efficient flushing**: Uses `http.ResponseController` for immediate SSE delivery
- **Token extraction**: Extracts usage from streaming responses for billing
- **Auto stream_options**: Automatically injects `stream_options.include_usage` when billing is configured
- **Works with billing**: Billing is calculated after stream completes

**Example with billing:**

```go
adapter, _ := modelsdev.LoadFromURL()
billingCallback := func(r llmproxy.BillingResult) {
    log.Printf("Cost: $%.6f (tokens: %d/%d)", r.TotalCost, r.PromptTokens, r.CompletionTokens)
}

router := llmproxy.NewAutoRouter(
    llmproxy.WithAutoRouterBillingCalculator(llmproxy.NewBillingCalculator(adapter.GetCostLookup(), billingCallback)),
)
```

## Providers

| Provider     | Auth                  | API Format                     | Notes |
| ------------ | --------------------- | ------------------------------ | ----- |
| OpenAI       | Bearer token          | Chat completions, Responses    | Supports both `/v1/chat/completions` and `/v1/responses` |
| Anthropic    | `x-api-key`           | Messages API                   | |
| Groq         | Bearer token          | OpenAI-compatible              | |
| Fireworks    | Bearer token          | OpenAI-compatible              | |
| x.AI         | Bearer token          | OpenAI-compatible              | |
| Google AI    | API key query param   | Gemini generateContent         | |
| AWS Bedrock  | AWS Signature V4      | Converse API                   | |
| Azure OpenAI | `api-key` or Azure AD | Chat completions (deployments) | |

## Interceptors

```go
// Logging
llmproxy.WithInterceptor(interceptors.NewLogging(logger))

// Metrics (thread-safe)
metrics := &interceptors.Metrics{}
llmproxy.WithInterceptor(interceptors.NewMetrics(metrics))

// Retry on 429/5xx
llmproxy.WithInterceptor(interceptors.NewRetry(3, time.Second))

// Billing with models.dev pricing
adapter, _ := modelsdev.LoadFromURL()
llmproxy.WithInterceptor(interceptors.NewBilling(adapter.GetCostLookup(), func(r llmproxy.BillingResult) {
    log.Printf("Cost: $%.6f", r.TotalCost)
}))

// OTel tracing
llmproxy.WithInterceptor(interceptors.NewTracing(otelExtractor))

// Strip sensitive headers
llmproxy.WithInterceptor(interceptors.NewResponseHeaderBan("Openai-Organization"))

// Add custom headers
llmproxy.WithInterceptor(interceptors.NewAddResponseHeader(
    interceptors.NewHeader("X-Gateway", "llmproxy"),
))

// Anthropic prompt caching (default 5 min, free)
llmproxy.WithInterceptor(interceptors.NewAnthropicPromptCaching(interceptors.CacheRetentionDefault))

// Anthropic prompt caching with 1h retention (costs more)
llmproxy.WithInterceptor(interceptors.NewAnthropicPromptCaching(interceptors.CacheRetention1h))

// OpenAI prompt caching with explicit cache key
llmproxy.WithInterceptor(interceptors.NewOpenAIPromptCaching(interceptors.CacheRetention24h, "my-cache-key"))

// OpenAI prompt caching with auto-derived key and tenant namespace
llmproxy.WithInterceptor(interceptors.NewOpenAIPromptCachingAuto("tenant-123", interceptors.CacheRetentionDefault))

// xAI/Grok prompt caching (uses x-grok-conv-id header)
llmproxy.WithInterceptor(interceptors.NewXAIPromptCaching("conv-abc123"))

// Fireworks prompt caching (uses x-session-affinity and x-prompt-cache-isolation-key headers)
llmproxy.WithInterceptor(interceptors.NewFireworksPromptCaching("session-123"))
```

## Architecture

The library uses small, focused interfaces that compose into providers:

```
Parse → Enrich → Resolve → Forward → Extract
```

- **BodyParser** — Extract metadata from request body
- **RequestEnricher** — Add auth headers
- **URLResolver** — Determine upstream URL
- **ResponseExtractor** — Parse response metadata
- **Provider** — Composes the above
- **Interceptor** — Wrap request/response for cross-cutting concerns

See [DESIGN.md](DESIGN.md) for full architecture details.

## Example

A complete multi-provider proxy server:

```bash
cd examples/basic
go run main.go
```

Environment variables:

| Variable                                                     | Provider     |
| ------------------------------------------------------------ | ------------ |
| `OPENAI_API_KEY`                                             | OpenAI       |
| `ANTHROPIC_API_KEY`                                          | Anthropic    |
| `GROQ_API_KEY`                                               | Groq         |
| `FIREWORKS_API_KEY`                                          | Fireworks    |
| `XAI_API_KEY`                                                | x.AI         |
| `GOOGLE_AI_API_KEY`                                          | Google AI    |
| `AZURE_OPENAI_RESOURCE`                                      | Azure OpenAI |
| `AZURE_OPENAI_API_KEY`                                       | Azure OpenAI |
| `AWS_REGION` + `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY` | AWS Bedrock  |

## License

[MIT](./LICENSE)
