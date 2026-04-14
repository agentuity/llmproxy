package interceptors

import (
	"net/http"
	"strings"

	"github.com/agentuity/llmproxy"
)

// BillingInterceptor calculates and records the cost of each request.
// It uses a CostLookup function to determine pricing for each model.
type BillingInterceptor struct {
	// Lookup is the function that returns pricing for a provider/model.
	Lookup llmproxy.CostLookup
	// OnResult is called with the billing result after each successful request.
	// This can be used to log, record to a database, or aggregate metrics.
	OnResult func(llmproxy.BillingResult)
}

// Intercept calculates the cost after a successful request and calls OnResult.
// If the model is not found in the lookup, no billing is recorded.
func (i *BillingInterceptor) Intercept(req *http.Request, meta llmproxy.BodyMetadata, rawBody []byte, next llmproxy.RoundTripFunc) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
	resp, respMeta, rawRespBody, err := next(req)
	if err != nil {
		return resp, respMeta, rawRespBody, err
	}

	// Try to get provider name from model prefix
	provider := detectProvider(meta.Model)

	// Look up pricing with provider first
	costInfo, found := i.Lookup(provider, meta.Model)
	if !found {
		// Try without provider (search all providers)
		costInfo, found = i.Lookup("", meta.Model)
	}

	if found {
		// Extract cache usage from response metadata if available
		var cacheUsage *llmproxy.CacheUsage
		if cu, ok := respMeta.Custom["cache_usage"]; ok {
			if usage, ok := cu.(llmproxy.CacheUsage); ok {
				cacheUsage = &usage
			}
		}
		result := llmproxy.CalculateCost(provider, meta.Model, costInfo, respMeta.Usage.PromptTokens, respMeta.Usage.CompletionTokens, cacheUsage)
		if respMeta.Custom == nil {
			respMeta.Custom = make(map[string]any)
		}
		respMeta.Custom["billing_result"] = result
		if i.OnResult != nil {
			i.OnResult(result)
		}
	}

	return resp, respMeta, rawRespBody, nil
}

// detectProvider attempts to determine the provider from the model name.
func detectProvider(model string) string {
	modelLower := strings.ToLower(model)
	switch {
	case strings.Contains(modelLower, "gpt-") || strings.Contains(modelLower, "o1-") || strings.Contains(modelLower, "o3-") || strings.Contains(modelLower, "chatgpt"):
		return "openai"
	case strings.Contains(modelLower, "claude"):
		return "anthropic"
	case strings.Contains(modelLower, "gemini"):
		return "google"
	case strings.Contains(modelLower, "llama") || strings.Contains(modelLower, "mixtral"):
		return "groq"
	}
	return ""
}

// NewBilling creates a new billing interceptor with the given lookup function.
//
// Example:
//
//	lookup := func(provider, model string) (llmproxy.CostInfo, bool) {
//	    // Your pricing database lookup
//	    if model == "gpt-4" {
//	        return llmproxy.CostInfo{Input: 30, Output: 60}, true
//	    }
//	    return llmproxy.CostInfo{}, false
//	}
//
//	billing := interceptors.NewBilling(lookup, func(r llmproxy.BillingResult) {
//	    log.Printf("Cost: $%.6f for %s", r.TotalCost, r.Model)
//	})
func NewBilling(lookup llmproxy.CostLookup, onResult func(llmproxy.BillingResult)) *BillingInterceptor {
	return &BillingInterceptor{
		Lookup:   lookup,
		OnResult: onResult,
	}
}
