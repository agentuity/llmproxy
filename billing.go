package llmproxy

// CostInfo contains pricing information for a model.
type CostInfo struct {
	// Input is the cost per 1M input tokens in USD.
	Input float64
	// Output is the cost per 1M output tokens in USD.
	Output float64
	// CacheRead is the cost per 1M cached input tokens (optional).
	CacheRead float64
	// CacheWrite is the cost per 1M cache write tokens (optional, Anthropic).
	CacheWrite float64
}

// CostLookup is a function that returns the cost for a given provider and model.
// It should return the pricing info or false if the model is not found.
//
// The lookup function allows the pricing data to be managed externally,
// such as downloading from models.dev or using a custom pricing database.
type CostLookup func(provider string, model string) (CostInfo, bool)

// BillingResult contains the calculated cost for a request.
type BillingResult struct {
	// Provider is the provider name.
	Provider string
	// Model is the model identifier.
	Model string
	// PromptTokens is the number of input tokens.
	PromptTokens int
	// CompletionTokens is the number of output tokens.
	CompletionTokens int
	// TotalTokens is the sum of prompt and completion tokens.
	TotalTokens int
	// InputCost is the calculated input cost in USD.
	InputCost float64
	// OutputCost is the calculated output cost in USD.
	OutputCost float64
	// TotalCost is the sum of input and output cost in USD.
	TotalCost float64
}

// CalculateCost computes the billing result from cost info and token usage.
func CalculateCost(provider, model string, costInfo CostInfo, promptTokens, completionTokens int) BillingResult {
	inputCost := costInfo.Input * float64(promptTokens) / 1_000_000
	outputCost := costInfo.Output * float64(completionTokens) / 1_000_000

	return BillingResult{
		Provider:         provider,
		Model:            model,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
		InputCost:        inputCost,
		OutputCost:       outputCost,
		TotalCost:        inputCost + outputCost,
	}
}
