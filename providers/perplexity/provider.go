package perplexity

import (
	"github.com/agentuity/llmproxy/providers/openai_compatible"
)

func New(apiKey string) (*openai_compatible.Provider, error) {
	return openai_compatible.New("perplexity", apiKey, "https://api.perplexity.ai")
}
