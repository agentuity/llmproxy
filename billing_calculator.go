package llmproxy

type BillingCalculator struct {
	lookup   CostLookup
	onResult func(BillingResult)
}

func NewBillingCalculator(lookup CostLookup, onResult func(BillingResult)) *BillingCalculator {
	return &BillingCalculator{
		lookup:   lookup,
		onResult: onResult,
	}
}

func (c *BillingCalculator) Calculate(meta BodyMetadata, respMeta *ResponseMetadata) *BillingResult {
	var provider string
	if meta.Custom != nil {
		if p, ok := meta.Custom["provider"].(string); ok && p != "" {
			provider = p
		}
	}
	if provider == "" {
		provider = DetectProviderFromModel(meta.Model)
	}

	costInfo, found := c.lookup(provider, meta.Model)
	if !found {
		costInfo, found = c.lookup("", meta.Model)
	}

	if !found {
		return nil
	}

	var cacheUsage *CacheUsage
	if cu, ok := respMeta.Custom["cache_usage"]; ok {
		if usage, ok := cu.(CacheUsage); ok {
			cacheUsage = &usage
		}
	}

	result := CalculateCost(provider, meta.Model, costInfo, respMeta.Usage.PromptTokens, respMeta.Usage.CompletionTokens, cacheUsage)

	if respMeta.Custom == nil {
		respMeta.Custom = make(map[string]any)
	}
	respMeta.Custom["billing_result"] = result

	if c.onResult != nil {
		c.onResult(result)
	}

	return &result
}

func (c *BillingCalculator) Lookup() CostLookup {
	return c.lookup
}

func (c *BillingCalculator) OnResult() func(BillingResult) {
	return c.onResult
}
