# LLM Proxy Examples

## Basic Example

A multi-provider proxy that routes requests to different LLM providers based on the endpoint.
Includes automatic billing using models.dev pricing data.

### Environment Variables

Set one or more of these environment variables:

| Variable | Provider |
|----------|----------|
| `OPENAI_API_KEY` | OpenAI |
| `ANTHROPIC_API_KEY` | Anthropic |
| `GROQ_API_KEY` | Groq |
| `FIREWORKS_API_KEY` | Fireworks AI |
| `XAI_API_KEY` | x.AI |
| `GOOGLE_AI_API_KEY` | Google AI |

For billing/cost tracking (optional):

| Variable | Description |
|----------|-------------|
| `MODELS_DEV_JSON` | Path to local models.dev JSON file |
| `MODELS_DEV_URL` | Custom URL for models.dev JSON |

### Running

```bash
# Set your API keys
export OPENAI_API_KEY=sk-your-key
export ANTHROPIC_API_KEY=sk-ant-your-key

# Run (billing will auto-fetch from models.dev)
cd examples/basic
go run main.go
```

Or with local pricing data:

```bash
# Download pricing data once
curl -o models.json https://models.dev/api.json
export MODELS_DEV_JSON=models.json

go run main.go
```

### Endpoints

| Endpoint | Provider |
|----------|----------|
| `POST /v1/chat/completions` | OpenAI-compatible (OpenAI, Groq, Fireworks, x.AI) |
| `POST /v1/messages` | Anthropic |
| `POST /v1beta/models/{model}:generateContent` | Google AI |

### Example Requests

```bash
# OpenAI
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}'

# Anthropic
curl http://localhost:8080/v1/messages \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-3-opus-20240229","max_tokens":100,"messages":[{"role":"user","content":"hello"}]}'

# Google AI
curl http://localhost:8080/v1beta/models/gemini-pro:generateContent \
  -H "Content-Type: application/json" \
  -d '{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}'
```

### Billing Output

When billing is enabled, you'll see output like:

```
[INFO] [llmproxy] Billing: model=gpt-4 tokens=10/5 cost=$0.000350
```

Cost is calculated using pricing data from models.dev.
