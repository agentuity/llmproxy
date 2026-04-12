# llmproxy

A Go library for proxying requests to upstream LLM providers with pluggable, composable architecture.

## Install

```bash
go get github.com/agentuity/llmproxy
```

## Quick Start

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

## Features

- **9 Provider Implementations**: OpenAI, Anthropic, Groq, Fireworks, x.AI, Google AI, AWS Bedrock, Azure OpenAI, OpenAI-compatible base
- **7 Built-in Interceptors**: Logging, Metrics, Retry, Billing, Tracing (OTel), HeaderBan, AddHeader
- **Pricing Integration**: models.dev adapter with markup support
- **Raw Body Preservation**: Custom JSON fields pass through unchanged

## Providers

| Provider     | Auth                  | API Format                     |
| ------------ | --------------------- | ------------------------------ |
| OpenAI       | Bearer token          | Chat completions               |
| Anthropic    | `x-api-key`           | Messages API                   |
| Groq         | Bearer token          | OpenAI-compatible              |
| Fireworks    | Bearer token          | OpenAI-compatible              |
| x.AI         | Bearer token          | OpenAI-compatible              |
| Google AI    | API key query param   | Gemini generateContent         |
| AWS Bedrock  | AWS Signature V4      | Converse API                   |
| Azure OpenAI | `api-key` or Azure AD | Chat completions (deployments) |

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
