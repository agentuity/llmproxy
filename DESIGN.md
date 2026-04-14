# DESIGN

**Module:** `github.com/agentuity/llmproxy`

A pluggable, composable library for proxying requests to upstream LLM providers. The core lifecycle follows a five-stage pipeline:

```
Parse --> Enrich --> Resolve --> Forward --> Extract
```

Callers construct a `Proxy` with a `Provider` and optional `Interceptor` chain, then call `Forward(ctx, req)` to proxy a single request through the full lifecycle.

---

## Architecture

### Core Interfaces

#### BodyParser

Extracts `BodyMetadata` from a raw HTTP request body. Returns structured metadata (model name, messages, max tokens, stream flag, custom fields) alongside the raw body bytes. The raw bytes are returned because `http.Request.Body` is a `ReadCloser` that can only be read once — downstream stages need access to the original payload.

#### RequestEnricher

Modifies the outgoing upstream request with provider-specific headers. Receives the parsed `BodyMetadata` and raw body bytes. For most providers this sets `Authorization: Bearer <key>`. AWS Bedrock computes an AWS Signature V4 instead.

#### URLResolver

Determines the upstream URL from metadata. Each provider maps to its own endpoint scheme:

| Provider | URL Pattern |
|----------|-------------|
| OpenAI | `https://api.openai.com/v1/chat/completions` |
| Bedrock | `https://bedrock-runtime.{region}.amazonaws.com/model/{modelId}/converse` |
| Google AI | `https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent` |

#### ResponseExtractor

Parses the upstream HTTP response into `ResponseMetadata` (ID, model, token usage, choices). Reads and returns the raw response body bytes so they can be re-attached to the response, preserving any custom JSON fields the provider may include.

#### Provider

Composes the four interfaces above into a single unit:

```
Name() string
BodyParser() BodyParser
RequestEnricher() RequestEnricher
ResponseExtractor() ResponseExtractor
URLResolver() URLResolver
```

`BaseProvider` supplies a configurable default implementation via functional options. Providers that share the OpenAI chat completions format embed the `openai_compatible` base and only override what differs (name, URL, auth).

#### Interceptor

Wraps the request/response cycle for cross-cutting concerns. Signature:

```
Intercept(req, meta, rawBody, next) -> (resp, respMeta, rawRespBody, err)
```

The chain wraps in reverse order so that given interceptors `[A, B, C]`, execution flows as:

```
A -> B -> C -> upstream -> C -> B -> A
```

Each interceptor can inspect/modify the request before calling `next`, and inspect/modify the response after `next` returns.

#### Registry

Manages a collection of named providers. `MapRegistry` provides a thread-safe, map-based lookup by provider name.

#### Proxy

The main entry point. `Forward(ctx, req)` orchestrates the full request lifecycle. Configurable with:

- `WithInterceptor(i)` — adds an interceptor to the chain
- `WithHTTPClient(c)` — sets a custom `*http.Client` for upstream calls

#### AutoRouter

An HTTP handler that provides automatic provider and API type detection from a single endpoint. Implements `http.Handler` for easy integration.

```text
Forward(ctx, req) -> (resp, meta, err)
ServeHTTP(w, r)
```

**Detection Flow:**

1. **Parse body** - Extract model name and request structure
2. **Detect provider** - From `X-Provider` header, model prefix (`openai/gpt-4`), or model pattern (`gpt-*`)
3. **Strip provider prefix** - If model has known provider prefix, strip before forwarding
4. **Detect API type** - From path (`/v1/messages`) or body+provider (`input` → Responses)
5. **Route to provider** - Forward to detected provider with correct endpoint

**Configuration options:**

- `WithAutoRouterRegistry(r)` — Use custom registry
- `WithAutoRouterDetector(d)` — Custom provider detection logic
- `WithAutoRouterModelProviderLookup(lookup)` — Hook for model→provider mapping (e.g., models.dev-backed detection); called when model pattern detection fails
- `WithAutoRouterInterceptor(i)` — Add interceptor to chain
- `WithAutoRouterHTTPClient(c)` — Custom HTTP client
- `WithAutoRouterFallbackProvider(p)` — Provider when detection fails

**Example:**

```go
// Basic setup
router := llmproxy.NewAutoRouter(
    llmproxy.WithAutoRouterFallbackProvider(openaiProvider),
    llmproxy.WithAutoRouterInterceptor(interceptors.NewLogging(logger)),
)
router.RegisterProvider(openaiProvider)
router.RegisterProvider(anthropicProvider)

http.Handle("/", router)
```

```go
// With models.dev-backed provider detection
adapter, _ := modelsdev.LoadFromURL()
router := llmproxy.NewAutoRouter(
    llmproxy.WithAutoRouterModelProviderLookup(adapter.FindProviderForModel),
    llmproxy.WithAutoRouterFallbackProvider(openaiProvider),
)
```

---

## Data Types

### BodyMetadata

| Field | Type | Description |
|-------|------|-------------|
| model | string | Target model identifier |
| messages | []Message | Conversation messages |
| maxTokens | int | Token generation limit |
| stream | bool | Whether to stream the response |
| custom | map[string]any | Provider-specific fields |

### ResponseMetadata

| Field | Type | Description |
|-------|------|-------------|
| id | string | Response identifier |
| object | string | Object type |
| model | string | Model that generated the response |
| usage | Usage | Token counts |
| choices | []Choice | Response choices |
| custom | map[string]any | Provider-specific fields |

### Message

| Field | Type | Description |
|-------|------|-------------|
| role | string | `system`, `user`, `assistant`, `tool` |
| content | string | Message content |

### Usage

| Field | Type | Description |
|-------|------|-------------|
| promptTokens | int | Input tokens consumed |
| completionTokens | int | Output tokens generated |
| totalTokens | int | Sum of prompt + completion |

### CacheUsage

| Field | Type | Description |
|-------|------|-------------|
| cachedTokens | int | Tokens served from cache (OpenAI, Azure) |
| cacheCreationInputTokens | int | Tokens written to cache (Anthropic) |
| cacheReadInputTokens | int | Tokens read from cache (Anthropic) |
| ephemeral5mInputTokens | int | 5-minute cache write tokens (Anthropic) |
| ephemeral1hInputTokens | int | 1-hour cache write tokens (Anthropic) |
| cacheWriteTokens | int | Tokens written to cache (Bedrock) |
| cacheDetails | []CacheDetail | TTL-based cache write breakdown (Bedrock) |

### CacheDetail

| Field | Type | Description |
|-------|------|-------------|
| ttl | string | Time-to-live for cache entry (e.g., "5m", "1h") |
| cacheWriteTokens | int | Tokens written to cache at this TTL |

### Choice

| Field | Type | Description |
|-------|------|-------------|
| index | int | Choice index |
| message | Message | Non-streaming response content |
| delta | Message | Streaming response content |
| finishReason | string | Why generation stopped |

### CostInfo

Per-model pricing in USD per 1M tokens:

| Field | Description |
|-------|-------------|
| input | Input token cost |
| output | Output token cost |
| cacheRead | Cached input read cost |
| cacheWrite | Cached input write cost |

### BillingResult

| Field | Description |
|-------|-------------|
| provider | Provider name |
| model | Model name |
| tokens | Usage breakdown |
| costs | Computed costs |

---

## Request Lifecycle

The full flow through `Proxy.Forward`:

```
                    +------------------+
                    |  Incoming HTTP   |
                    |    Request       |
                    +--------+---------+
                             |
                    1. Read request body
                             |
                    +--------v---------+
                    |   BodyParser     |
                    |  Parse -> Meta   |
                    |  + raw body bytes|
                    +--------+---------+
                             |
                    2. Resolve upstream URL
                             |
                    +--------v---------+
                    |   URLResolver    |
                    +--------+---------+
                             |
                    3. Create upstream request,
                       copy headers
                             |
                    4. Enrich request
                             |
                    +--------v---------+
                    |  RequestEnricher |
                    |  (auth headers)  |
                    +--------+---------+
                             |
                    5. Store metadata in
                       request context
                             |
                    6. Execute interceptor chain
                             |
              +--------------v--------------+
              |  Interceptor A              |
              |   +- Interceptor B          |
              |   |   +- Interceptor C      |
              |   |   |   +- HTTP call ---->| upstream
              |   |   |   +- response <----+|
              |   |   +- post-process       |
              |   +- post-process           |
              +- post-process               |
              +-----------------------------+
                             |
                    7. Extract ResponseMetadata
                             |
                    +--------v---------+
                    | ResponseExtractor|
                    +--------+---------+
                             |
                    8. Re-attach raw body
                       to response
                             |
                    9. Return response
                       + metadata
```

Steps in detail:

1. Read and parse the request body into `BodyMetadata`. The raw bytes are preserved for later use.
2. Resolve the upstream URL from the metadata (model name, provider config).
3. Create a new HTTP request for the upstream provider and copy relevant headers from the original request.
4. Enrich the upstream request with provider-specific authentication (Bearer token, API key, AWS Signature V4).
5. Store `BodyMetadata` in the request context so interceptors can access it.
6. Execute the request through the interceptor chain. Each interceptor wraps the next, forming an onion-style pipeline.
7. Extract `ResponseMetadata` from the upstream response.
8. Re-attach the raw response body so custom JSON fields from the provider are preserved.
9. Return the response and metadata to the caller.

---

## Auto-Routing

The `AutoRouter` enables automatic provider and API detection from a single endpoint. POST to `/` with any LLM request and routing happens automatically.

### API Type Detection

Detection happens in two phases:

**Phase 1: Path-based detection**

| Path | API Type |
|------|----------|
| `/v1/chat/completions` | Chat Completions |
| `/v1/responses` | Responses |
| `/v1/completions` | Legacy Completions |
| `/v1/messages` | Anthropic Messages |
| `:generateContent` | Gemini GenerateContent |
| `/converse` | Bedrock Converse |

**Phase 2: Body + Provider detection** (when path is `/` or unknown)

| Body Field | Provider | API Type |
|------------|----------|----------|
| `input` | any | Responses |
| `prompt` | any | Completions |
| `contents` | any | GenerateContent |
| `messages` | anthropic | Messages |
| `messages` | other | Chat Completions |

### Provider Detection

Provider is detected in priority order:

1. **X-Provider header** — Explicit override
   ```bash
   curl -X POST http://localhost:8080/ \
     -H 'X-Provider: anthropic' \
     -d '{"model":"claude-3-opus",...}'
   ```

2. **Model prefix** — Provider prefix in model name (stripped before forwarding)
   ```bash
   # Model "openai/gpt-4" routes to OpenAI, forwards "gpt-4"
   curl -X POST http://localhost:8080/ \
     -d '{"model":"anthropic/claude-3-opus",...}'
   ```

3. **Model pattern** — Match against known patterns
   | Pattern | Provider |
   |---------|----------|
   | `gpt-*`, `o1-*`, `o3-*`, `chatgpt-*` | OpenAI |
   | `claude-*` | Anthropic |
   | `gemini-*`, `gemma-*` | Google AI |
   | `grok-*` | x.AI |
   | `accounts/fireworks/*` | Fireworks |
   | `sonar*` | Perplexity |
   | `anthropic.claude-*`, `amazon.*` | Bedrock |

### Provider Prefix Stripping

Only known provider prefixes are stripped:

```go
// Stripped (known providers)
"openai/gpt-4"           -> "gpt-4"
"anthropic/claude-3"     -> "claude-3"
"fireworks/models/llama" -> "models/llama"

// Preserved (unknown or model-native paths)
"accounts/fireworks/models/llama" -> "accounts/fireworks/models/llama"
"some-unknown/model"              -> "some-unknown/model"
```

### Usage Examples

```bash
# Auto-detect everything - POST to /
curl -X POST http://localhost:8080/ \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4","messages":[{"role":"user","content":"Hello"}]}'

# Auto-detect Anthropic from model name
curl -X POST http://localhost:8080/ \
  -H 'Content-Type: application/json' \
  -d '{"model":"claude-3-opus","max_tokens":1024,"messages":[{"role":"user","content":"Hello"}]}'

# Auto-detect Responses API from input field
curl -X POST http://localhost:8080/ \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o","input":"Hello"}'

# Traditional path-based routing still works
curl -X POST http://localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4","messages":[{"role":"user","content":"Hello"}]}'
```

---

## Providers

Nine providers are included. Six share the OpenAI-compatible base; three have fully custom implementations.

### OpenAI-Compatible Base

`providers/openai_compatible` implements `BodyParser`, `ResponseExtractor`, `URLResolver`, and `RequestEnricher` for the OpenAI chat completions format. Providers that speak this protocol embed the base and override only what differs (name, base URL, auth configuration).

The OpenAI provider also supports the **Responses API** (`/v1/responses`) with automatic detection based on the `input` field in the request body.

### Provider Table

| Provider | Package | Auth | API Format | Notes |
|----------|---------|------|------------|-------|
| OpenAI | `providers/openai` | Bearer token | Chat completions, Responses | Supports both APIs with auto-detection |
| Anthropic | `providers/anthropic` | `x-api-key` header + `anthropic-version` | Anthropic Messages API | Custom parser/extractor |
| Groq | `providers/groq` | Bearer token | OpenAI-compatible | Wraps `openai_compatible` |
| Fireworks | `providers/fireworks` | Bearer token | OpenAI-compatible | Wraps `openai_compatible` |
| x.AI | `providers/xai` | Bearer token | OpenAI-compatible | Wraps `openai_compatible` |
| Google AI | `providers/googleai` | API key in URL query param | Gemini generateContent | Custom parser/extractor/resolver |
| AWS Bedrock | `providers/bedrock` | AWS Signature V4 | Converse API | Custom everything; supports both Converse and InvokeModel |
| Azure OpenAI | `providers/azure` | `api-key` header or Azure AD Bearer token | OpenAI chat completions | Uses deployments instead of models |
| OpenAI-compatible | `providers/openai_compatible` | Bearer token | OpenAI chat completions | Reusable base for OpenAI-compatible providers |

### Provider Details

**OpenAI** — Wraps `openai_compatible` with support for multiple APIs:
- **Chat Completions** (`/v1/chat/completions`) — Standard messages-based API
- **Responses** (`/v1/responses`) — Newer API with `input` field, built-in tools support
- **Legacy Completions** (`/v1/completions`) — Older prompt-based API

The provider auto-detects the API type from the request body:
- `input` field → Responses API
- `prompt` field → Completions API
- `messages` field → Chat Completions API

**Anthropic** — Custom body parser translates between the proxy's canonical format and Anthropic's Messages API. Custom extractor maps Anthropic's response shape (content blocks, stop_reason) back to `ResponseMetadata`. Auth uses the `x-api-key` header alongside an `anthropic-version` header.

**Groq, Fireworks, x.AI** — Each wraps `openai_compatible` with its own base URL and provider name. No custom parsing or extraction logic needed.

**Google AI** — Custom body parser converts to the Gemini `generateContent` format (contents/parts). Custom URL resolver appends the API key as a query parameter rather than using a header. Custom extractor maps Gemini's response (candidates/content/parts) back to `ResponseMetadata`.

**AWS Bedrock** — Fully custom implementation. The body parser converts to the Bedrock Converse API format. The enricher computes AWS Signature V4 signing using region, access key, and secret key. The URL resolver constructs region-specific endpoints. Supports both the Converse and InvokeModel API paths.

**Azure OpenAI** — Uses deployments instead of direct model access. Two construction modes:
- `New(resourceName, deploymentID, apiVersion, opts...)` — Fixed deployment, uses configured deploymentID for all requests
- `NewWithDynamicDeployment(resourceName, apiVersion, opts...)` — Dynamic deployment, uses the model field from each request as the deployment name

Authentication via functional options:
- `WithAPIKey(apiKey)` — Sets `api-key` header
- `WithAzureADToken(token)` — Sets `Authorization: Bearer` header
- `WithAzureADTokenRefresher(fn)` — Token refresh callback for expiring Azure AD tokens

URL format: `https://{resource}.openai.azure.com/openai/deployments/{deployment}/chat/completions?api-version={version}`

---

## Interceptors

Eight built-in interceptors are provided in the `interceptors/` package.

### Logging

`NewLogging(logger)` — Logs each request/response cycle with:

- Model name
- HTTP method and URL
- Response status
- Latency
- Token usage (prompt, completion, total)

Accepts an `llmproxy.Logger` interface, which is compatible with `github.com/agentuity/go-common/logger` without requiring the dependency.

### Metrics

`NewMetrics(m)` — Tracks aggregate statistics:

| Field | Description |
|-------|-------------|
| TotalRequests | Number of requests processed |
| TotalTokens | Total tokens consumed |
| TotalPromptTokens | Total input tokens |
| TotalCompletionTokens | Total output tokens |
| TotalLatency | Cumulative request duration |
| Errors | Number of failed requests |

All fields use `sync/atomic` operations for thread safety. The `Metrics` struct can be read concurrently from monitoring goroutines.

### Retry

`NewRetry(maxAttempts, delay)` — Retries failed requests under specific conditions:

- **Retries on:** HTTP 429 (rate limit) and 5xx (server errors)
- **Does NOT retry:** `context.Canceled`, `context.DeadlineExceeded`
- **Body handling:** Reconstructs the request body from raw bytes on each retry attempt
- **Custom predicate:** `NewRetryWithPredicate(maxAttempts, delay, predicate)` allows callers to supply a custom function that decides whether a given response should be retried

`NewRetryWithRateLimitHeaders(maxAttempts, defaultDelay)` — Retries with rate limit header support:

- **Retry-After header:** Parses both seconds (integer) and HTTP date formats
- **X-RateLimit-Reset header:** Fallback if Retry-After not present
- **Max delay:** Values over 24 hours are ignored (fallback to defaultDelay); exactly 24 hours is accepted
- **Precedence:** Retry-After takes precedence over X-RateLimit-Reset

Example:

```go
// Use rate limit headers from provider
retry := interceptors.NewRetryWithRateLimitHeaders(3, time.Second)

// If provider returns 429 with Retry-After: 30, waits 30s instead of 1s
```

### Billing

`NewBilling(lookup, onResult)` — Calculates the cost of each request:

- Uses a `CostLookup` function to retrieve per-model pricing
- Detects the provider from the model name
- Computes input/output/cache costs based on token usage
- Calls the `onResult` callback with a `BillingResult` after each request
- Stores `BillingResult` in `ResponseMetadata.Custom["billing_result"]` for downstream access

When using `AutoRouter`, billing results are automatically added as response headers:
- `X-Gateway-Cost` — Total cost in USD
- `X-Gateway-Prompt-Tokens` — Input token count
- `X-Gateway-Completion-Tokens` — Output token count

### Tracing

`NewTracing(extractor)` — Propagates OpenTelemetry trace context:

- **Upstream headers:**
  - `X-Request-ID`: the trace ID (32 hex chars)
  - `traceparent`: W3C Trace Context format (`00-{traceid}-{spanid}-{flags}`)
- **Response header:** `X-Request-ID` for correlation
- **Extractor function:** Pulls trace info from request context

The extractor function signature:

```
func(ctx context.Context) TraceInfo
```

For OpenTelemetry:

```go
func otelExtractor(ctx context.Context) interceptors.TraceInfo {
    span := trace.SpanFromContext(ctx)
    if !span.SpanContext().IsValid() {
        return interceptors.TraceInfo{}
    }
    return interceptors.TraceInfo{
        TraceID: span.SpanContext().TraceID(),
        SpanID:  span.SpanContext().SpanID(),
        Sampled: span.SpanContext().IsSampled(),
    }
}
```

Use `NewTracingWithHeader(extractor, headerName)` to customize the response header name.

### HeaderBan

`NewHeaderBan(requestHeaders, responseHeaders)` — Strips specified headers:

- **Request headers:** Removed before forwarding upstream
- **Response headers:** Removed before returning to caller
- **Case-insensitive:** HTTP header matching is case-insensitive

Convenience constructors:

- `NewResponseHeaderBan(headers...)` — Strip only response headers
- `NewRequestHeaderBan(headers...)` — Strip only request headers

Example:

```go
ban := interceptors.NewResponseHeaderBan("Openai-Organization", "Openai-Project")
proxy := llmproxy.NewProxy(provider, llmproxy.WithInterceptor(ban))
```

### AddHeader

`NewAddHeader(requestHeaders, responseHeaders)` — Adds custom headers to requests and responses:

- **Request headers:** Added before forwarding upstream
- **Response headers:** Added before returning to caller

Convenience constructors:

- `NewAddResponseHeader(headers...)` — Add only response headers
- `NewAddRequestHeader(headers...)` — Add only request headers

Example:

```go
add := interceptors.NewAddResponseHeader(
    interceptors.NewHeader("X-Gateway-Version", "1.0"),
    interceptors.NewHeader("X-Served-By", "llmproxy"),
)
proxy := llmproxy.NewProxy(provider, llmproxy.WithInterceptor(add))
```

### PromptCaching

Provider-specific prompt caching interceptors for Anthropic, OpenAI, xAI, Fireworks, and AWS Bedrock.

#### Common Behavior

- **Cache-Control header:** If the incoming request has `Cache-Control: no-cache`, the interceptor skips entirely — letting clients disable caching per-request
- **Provider detection:** Only applies to matching models:
  - Anthropic: `claude-*`
  - OpenAI: `gpt-*`, `o1-*`, `o3-*`, `o4-*`, `chatgpt-*`
  - xAI: `grok-*`
  - Fireworks: `accounts/fireworks/*`, `fireworks*`
  - Bedrock: `anthropic.claude-*`, `amazon.nova-*`, `amazon.titan-*`
- **Cache usage tracking:** Response metadata includes `CacheUsage` in `Custom["cache_usage"]`

#### Anthropic

`NewAnthropicPromptCaching(retention)` — Enables Anthropic prompt caching:

- **Automatic caching:** Adds `cache_control` at the top level of requests
- **Retention options:** 
  - `CacheRetentionDefault` (default, 5 min) — no TTL field, free, auto-refreshed on use
  - `CacheRetention1h` — adds `ttl: "1h"`, costs more, longer cache lifetime
- **User-controlled caching:** If request already has `cache_control`, the interceptor skips entirely — letting you control caching explicitly via block-level breakpoints

Example:

```go
// Enable prompt caching for Anthropic (default 5 min, free)
caching := interceptors.NewAnthropicPromptCaching(interceptors.CacheRetentionDefault)
proxy := llmproxy.NewProxy(provider, llmproxy.WithInterceptor(caching))

// With 1h retention (costs more) and cache usage callback
caching := interceptors.NewAnthropicPromptCachingWithResult(interceptors.CacheRetention1h, func(u llmproxy.CacheUsage) {
    log.Printf("Cache read: %d tokens, Cache write: %d tokens", u.CacheReadInputTokens, u.CacheCreationInputTokens)
})
```

#### OpenAI

`NewOpenAIPromptCaching(retention, cacheKey)` — Enables OpenAI prompt caching:

- **Automatic caching:** OpenAI caches prompts ≥ 1024 tokens automatically
- **Cache routing:** Adds `prompt_cache_key` to improve cache hit rates for requests with common prefixes
- **Retention options:** 
  - `CacheRetentionDefault` (default, in-memory, 5-10 min) — no retention field
  - `CacheRetention24h` — adds `prompt_cache_retention: "24h"` for GPT-5.x and GPT-4.1
- **Cache key sources (in priority order):**
  1. `X-Cache-Key` header from incoming request
  2. Configured `CacheKey` in PromptCachingConfig
  3. Auto-derived from static content prefix via `DeriveCacheKeyFromPrefix()`
- **Tenant namespacing:** Cache keys are automatically prefixed with org/tenant ID from:
  1. Custom `OrgIDExtractor` function
  2. `OrgID` in `MetaContextValue` stored in request context
  3. `X-Org-ID` header
  4. `org_id` in `BodyMetadata.Custom`
  5. Configured `Namespace` fallback

Example:

```go
// Enable prompt caching for OpenAI with a cache key (default retention)
caching := interceptors.NewOpenAIPromptCaching(interceptors.CacheRetentionDefault, "my-app-session-123")
proxy := llmproxy.NewProxy(provider, llmproxy.WithInterceptor(caching))

// With 24h retention and cache usage callback
caching := interceptors.NewOpenAIPromptCachingWithResult(interceptors.CacheRetention24h, "my-key", func(u llmproxy.CacheUsage) {
    log.Printf("Cached tokens: %d", u.CachedTokens)
})

// Auto-derive cache key from static content, namespace by tenant
caching := interceptors.NewOpenAIPromptCachingAuto("tenant-123", interceptors.CacheRetentionDefault)

// Custom org ID extractor (e.g., from auth context)
caching := interceptors.NewOpenAIPromptCachingWithOrgExtractor(
    interceptors.CacheRetentionDefault, 
    "my-key",
    func(ctx context.Context, req *http.Request, meta llmproxy.BodyMetadata) string {
        return getOrgFromAuthContext(ctx)
    },
)
```

#### xAI (Grok)

`NewXAIPromptCaching(convID)` — Enables xAI/Grok prompt caching:

- **Automatic prefix caching:** xAI caches from the start of the messages array automatically
- **Cache routing:** Adds `x-grok-conv-id` HTTP header to route requests to the same server where cache lives
- **Conversation ID:** Use a stable value (conversation ID, session ID, or deterministic hash of static content)
- **Key rule:** Never reorder or modify earlier messages — only append

Example:

```go
// Enable prompt caching for xAI with a conversation ID
caching := interceptors.NewXAIPromptCaching("conv-abc123-tenant456")
proxy := llmproxy.NewProxy(provider, llmproxy.WithInterceptor(caching))

// With cache usage callback
caching := interceptors.NewXAIPromptCachingWithResult("my-conv-id", func(u llmproxy.CacheUsage) {
    log.Printf("Cached tokens: %d", u.CachedTokens)
})
```

#### Fireworks

`NewFireworksPromptCaching(sessionID)` — Enables Fireworks prompt caching:

- **Automatic caching:** Fireworks caches prompts with shared prefixes automatically (enabled by default)
- **Cache routing:** Adds `x-session-affinity` HTTP header to route requests to the same replica
- **Tenant isolation:** Adds `x-prompt-cache-isolation-key` header set to org/tenant ID for multi-tenant isolation
- **Cache usage:** Reads `fireworks-cached-prompt-tokens` response header for cache hit tracking

Example:

```go
// Enable prompt caching for Fireworks with session affinity
caching := interceptors.NewFireworksPromptCaching("session-abc123")
proxy := llmproxy.NewProxy(provider, llmproxy.WithInterceptor(caching))

// With org ID extractor for tenant isolation
caching := interceptors.NewFireworksPromptCachingWithOrgExtractor("session-abc123", func(ctx context.Context, req *http.Request, meta llmproxy.BodyMetadata) string {
    return getOrgFromAuthContext(ctx)
})

// With cache usage callback
caching := interceptors.NewFireworksPromptCachingWithResult("session-abc123", func(u llmproxy.CacheUsage) {
    log.Printf("Cached tokens: %d", u.CachedTokens)
})
```

#### AWS Bedrock

`NewBedrockPromptCaching(retention)` — Enables AWS Bedrock prompt caching via the Converse API:

- **Cache checkpoints:** Adds `cachePoint` objects to system, messages, and toolConfig
- **Retention options:**
  - `CacheRetentionDefault` (default, 5 min) — no TTL field
  - `CacheRetention1h` — adds `ttl: "1h"` for Claude Opus 4.5, Haiku 4.5, and Sonnet 4.5
- **Minimum tokens:** 1,024 tokens per cache checkpoint (varies by model)
- **Maximum checkpoints:** 4 per request
- **Supported models:** Claude models (anthropic.claude-*), Nova models (amazon.nova-*), Titan models (amazon.titan-*)
- **Cache usage:** Reads `cacheReadInputTokens`, `cacheWriteInputTokens`, and `cacheDetails` from response

Example:

```go
// Enable prompt caching for Bedrock (default 5 min)
caching := interceptors.NewBedrockPromptCaching(interceptors.CacheRetentionDefault)
proxy := llmproxy.NewProxy(bedrockProvider, llmproxy.WithInterceptor(caching))

// With 1h retention for Claude Opus 4.5
caching := interceptors.NewBedrockPromptCaching(interceptors.CacheRetention1h)

// With cache usage callback
caching := interceptors.NewBedrockPromptCachingWithResult(interceptors.CacheRetentionDefault, func(u llmproxy.CacheUsage) {
    log.Printf("Cache read: %d tokens, Cache write: %d tokens", u.CachedTokens, u.CacheWriteTokens)
    for _, detail := range u.CacheDetails {
        log.Printf("  TTL %s: %d tokens written", detail.TTL, detail.CacheWriteTokens)
    }
})
```

#### Azure OpenAI

Azure OpenAI uses the same `prompt_cache_key` body parameter as OpenAI. **Use the OpenAI interceptor** for Azure OpenAI:

```go
// Azure OpenAI prompt caching uses the OpenAI interceptor
caching := interceptors.NewOpenAIPromptCaching(interceptors.CacheRetentionDefault, "my-cache-key")
proxy := llmproxy.NewProxy(azureProvider, llmproxy.WithInterceptor(caching))
```

**Note:** Azure OpenAI caches prompts ≥ 1,024 tokens automatically. The `prompt_cache_key` parameter is combined with the prefix hash to improve cache hit rates. Cache hits appear as `cached_tokens` in `prompt_tokens_details` in the response.

#### Generic constructor

`NewPromptCaching(provider, config)` — Creates a caching interceptor for any provider:

```go
// Anthropic with 1h retention
caching := interceptors.NewPromptCaching("anthropic", interceptors.PromptCachingConfig{
    Enabled:   true,
    Retention: interceptors.CacheRetention1h,
})

// OpenAI with 24h retention
caching := interceptors.NewPromptCaching("openai", interceptors.PromptCachingConfig{
    Enabled:   true,
    Retention: interceptors.CacheRetention24h,
    CacheKey:  "my-cache-key",
})

// xAI with conversation ID
caching := interceptors.NewPromptCaching("xai", interceptors.PromptCachingConfig{
    Enabled:  true,
    CacheKey: "my-conv-id",
})

// Fireworks with session ID and org extractor
caching := interceptors.NewPromptCaching("fireworks", interceptors.PromptCachingConfig{
    Enabled:  true,
    CacheKey: "my-session-id",
    OrgIDExtractor: interceptors.DefaultOrgIDExtractor,
})

// Bedrock with 1h retention
caching := interceptors.NewPromptCaching("bedrock", interceptors.PromptCachingConfig{
    Enabled:   true,
    Retention: interceptors.CacheRetention1h,
})
```

---

## Pricing System

`pricing/modelsdev/` provides an adapter that loads pricing data from [models.dev](https://models.dev).

### Loading

Three source options:

- `LoadFromFile(path)` — Load from a local JSON file
- `LoadFromURL()` — Load from the default models.dev URL
- Custom URL via `WithURL(url)` option

### Options

- `WithMarkup(multiplier)` — Apply a markup for reselling (e.g., `1.2` = 20% markup on all prices)

### Integration

```
adapter := modelsdev.LoadFromURL()
lookup := adapter.GetCostLookup()
billing := interceptors.NewBilling(lookup, func(result llmproxy.BillingResult) {
    // handle billing result
})
```

`GetCostLookup()` returns a `CostLookup` function suitable for the billing interceptor.

### Caching

The adapter supports TTL-based refresh so pricing data stays current without restarting the process.

---

## Logger Interface

```go
type Logger interface {
    Debug(msg string, args ...interface{})
    Info(msg string, args ...interface{})
    Warn(msg string, args ...interface{})
    Error(msg string, args ...interface{})
}
```

Matches the signature of `github.com/agentuity/go-common/logger` without requiring it as a dependency. Any logger implementing these four methods works.

`LoggerFunc` is an adapter that wraps a plain function as a `Logger`, useful for testing or simple setups.

---

## Design Principles

1. **Small, focused interfaces** — Each interface (`BodyParser`, `RequestEnricher`, `URLResolver`, `ResponseExtractor`) does exactly one thing. This keeps implementations simple and testable.

2. **Composition over inheritance** — Providers compose the four core interfaces rather than inheriting from a base class. `BaseProvider` wires them together via functional options. OpenAI-compatible providers embed a shared base and override only what differs.

3. **Raw body preservation** — Both request and response bodies are preserved as raw bytes throughout the lifecycle. This avoids losing custom JSON fields that providers may include but that the library's typed structs don't model.

4. **Function-based lookup** — `CostLookup` is a function type, not an interface with a concrete implementation. This allows callers to manage pricing data externally (database, config file, remote service) without coupling to a specific source.

5. **Interceptor chain** — Cross-cutting concerns (logging, metrics, retry, billing) wrap the request lifecycle without modifying providers. Interceptors compose independently and execute in a predictable onion order.

---

## Directory Structure

```
llmproxy/
├── apitype.go              # API type detection and constants
├── autorouter.go           # AutoRouter, provider/API auto-detection
├── billing.go              # CostInfo, CostLookup, BillingResult, CalculateCost
├── detection.go            # Provider detection from model/header
├── enricher.go             # RequestEnricher interface
├── extractor.go            # ResponseExtractor interface
├── interceptor.go          # Interceptor, InterceptorChain, RoundTripFunc
├── logger.go               # Logger interface, LoggerFunc adapter
├── metadata.go             # BodyMetadata, ResponseMetadata, Message, Usage, Choice
├── parser.go               # BodyParser interface
├── provider.go             # Provider interface, BaseProvider
├── proxy.go                # Proxy struct, Forward method
├── registry.go             # Registry interface, MapRegistry
├── resolver.go             # URLResolver interface
├── interceptors/
│   ├── addheader.go       # AddHeaderInterceptor
│   ├── billing.go          # BillingInterceptor
│   ├── headerban.go        # HeaderBanInterceptor
│   ├── logging.go          # LoggingInterceptor
│   ├── metrics.go          # MetricsInterceptor, Metrics
│   ├── promptcaching.go    # PromptCachingInterceptor
│   ├── retry.go            # RetryInterceptor
│   └── tracing.go          # TracingInterceptor
├── pricing/
│   └── modelsdev/
│       └── adapter.go      # models.dev pricing adapter
├── providers/
│   ├── anthropic/          # Anthropic Messages API
│   ├── azure/              # Azure OpenAI
│   ├── bedrock/            # AWS Bedrock Converse API
│   ├── fireworks/          # Fireworks (OpenAI-compatible)
│   ├── googleai/           # Google AI Gemini
│   ├── groq/               # Groq (OpenAI-compatible)
│   ├── openai/             # OpenAI (Chat Completions + Responses)
│   ├── openai_compatible/  # Base for OpenAI-compatible providers
│   │   ├── multiapi.go           # Multi-API parser/extractor
│   │   ├── responses_parser.go   # Responses API parser
│   │   └── responses_extractor.go # Responses API extractor
│   └── xai/                # x.AI (OpenAI-compatible)
└── examples/
    └── basic/              # Multi-provider proxy server example (uses AutoRouter)
```
