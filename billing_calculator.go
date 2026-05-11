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
	if c.lookup == nil || respMeta == nil {
		return nil
	}
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

	meteredUsage := mergeMeteredUsage(meta.MeteredUsage, respMeta.MeteredUsage)
	result := CalculateCostWithMeteredUsage(provider, meta.Model, costInfo, respMeta.Usage.PromptTokens, respMeta.Usage.CompletionTokens, cacheUsage, meteredUsage)

	if respMeta.Custom == nil {
		respMeta.Custom = make(map[string]any)
	}
	respMeta.Custom["billing_result"] = result

	if c.onResult != nil {
		c.onResult(result)
	}

	return &result
}

func mergeMeteredUsage(requestUsage MeteredUsage, responseUsage MeteredUsage) MeteredUsage {
	return MeteredUsage{
		InputCharacters:    firstNonZeroInt(responseUsage.InputCharacters, requestUsage.InputCharacters),
		OutputCharacters:   firstNonZeroInt(responseUsage.OutputCharacters, requestUsage.OutputCharacters),
		InputAudioSeconds:  firstNonZeroFloat(responseUsage.InputAudioSeconds, requestUsage.InputAudioSeconds),
		OutputAudioSeconds: firstNonZeroFloat(responseUsage.OutputAudioSeconds, requestUsage.OutputAudioSeconds),
		OutputVideoSeconds: firstNonZeroFloat(responseUsage.OutputVideoSeconds, requestUsage.OutputVideoSeconds),
		GeneratedImages:    firstNonZeroInt(responseUsage.GeneratedImages, requestUsage.GeneratedImages),
	}
}

func firstNonZeroInt(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func firstNonZeroFloat(values ...float64) float64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func (c *BillingCalculator) Lookup() CostLookup {
	return c.lookup
}

func (c *BillingCalculator) OnResult() func(BillingResult) {
	return c.onResult
}
