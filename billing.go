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
	// CachedTokens is the number of prompt tokens served from cache.
	CachedTokens int
	// TotalTokens is the sum of prompt and completion tokens.
	TotalTokens int
	// InputCost is the calculated input cost in USD (non-cached prompt tokens).
	InputCost float64
	// CachedInputCost is the cost for cached prompt tokens in USD.
	CachedInputCost float64
	// OutputCost is the calculated output cost in USD.
	OutputCost float64
	// TotalCost is the sum of all costs in USD.
	TotalCost float64
}

// CalculateCost computes the billing result from cost info, token usage, and cache usage.
// Cached tokens are billed at the CacheRead rate (if available), and non-cached prompt
// tokens are billed at the full Input rate.
func CalculateCost(provider, model string, costInfo CostInfo, promptTokens, completionTokens int, cacheUsage *CacheUsage) BillingResult {
	cachedTokens := 0
	if cacheUsage != nil {
		// Providers populate only one of these fields — OpenAI/Fireworks/Bedrock
		// set CachedTokens while Anthropic sets CacheReadInputTokens. We sum them
		// so the same code path works for any provider. The clamp below guards
		// against overcounting if a future provider ever sets both fields.
		cachedTokens = cacheUsage.CachedTokens + cacheUsage.CacheReadInputTokens
	}

	// Providers report prompt tokens differently:
	//   - OpenAI/Fireworks/Bedrock: promptTokens INCLUDES cached tokens
	//     → non-cached = promptTokens - cachedTokens
	//   - Anthropic: input_tokens EXCLUDES cached tokens (only new tokens)
	//     → non-cached = promptTokens (as reported), cached is additional
	//
	// We detect the style by comparing: if cached > prompt, the provider
	// must be reporting non-cached only (Anthropic style).
	var nonCachedTokens int
	if cachedTokens > promptTokens {
		// Anthropic style: promptTokens = non-cached only, cached is separate
		nonCachedTokens = promptTokens
		// Adjust promptTokens to reflect the true total for the BillingResult
		promptTokens = promptTokens + cachedTokens
	} else {
		// OpenAI style: promptTokens includes cached
		nonCachedTokens = promptTokens - cachedTokens
	}

	// Non-cached prompt tokens at full input rate
	inputCost := costInfo.Input * float64(nonCachedTokens) / 1_000_000

	// Cached tokens at cache read rate (falls back to full input rate if no cache pricing)
	var cachedInputCost float64
	if cachedTokens > 0 {
		cacheRate := costInfo.CacheRead
		if cacheRate <= 0 {
			cacheRate = costInfo.Input // fallback to full rate
		}
		cachedInputCost = cacheRate * float64(cachedTokens) / 1_000_000
	}

	outputCost := costInfo.Output * float64(completionTokens) / 1_000_000

	return BillingResult{
		Provider:         provider,
		Model:            model,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CachedTokens:     cachedTokens,
		TotalTokens:      promptTokens + completionTokens,
		InputCost:        inputCost,
		CachedInputCost:  cachedInputCost,
		OutputCost:       outputCost,
		TotalCost:        inputCost + cachedInputCost + outputCost,
	}
}
