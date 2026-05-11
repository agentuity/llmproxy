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
		InputCharacters:       selectInt(responseUsage.InputCharacters, responseUsage.HasInputCharacters, requestUsage.InputCharacters),
		OutputCharacters:      selectInt(responseUsage.OutputCharacters, responseUsage.HasOutputCharacters, requestUsage.OutputCharacters),
		InputAudioSeconds:     selectFloat(responseUsage.InputAudioSeconds, responseUsage.HasInputAudioSeconds, requestUsage.InputAudioSeconds),
		OutputAudioSeconds:    selectFloat(responseUsage.OutputAudioSeconds, responseUsage.HasOutputAudioSeconds, requestUsage.OutputAudioSeconds),
		OutputVideoSeconds:    selectFloat(responseUsage.OutputVideoSeconds, responseUsage.HasOutputVideoSeconds, requestUsage.OutputVideoSeconds),
		GeneratedImages:       selectInt(responseUsage.GeneratedImages, responseUsage.HasGeneratedImages, requestUsage.GeneratedImages),
		HasInputCharacters:    responseUsage.HasInputCharacters || requestUsage.HasInputCharacters,
		HasOutputCharacters:   responseUsage.HasOutputCharacters || requestUsage.HasOutputCharacters,
		HasInputAudioSeconds:  responseUsage.HasInputAudioSeconds || requestUsage.HasInputAudioSeconds,
		HasOutputAudioSeconds: responseUsage.HasOutputAudioSeconds || requestUsage.HasOutputAudioSeconds,
		HasOutputVideoSeconds: responseUsage.HasOutputVideoSeconds || requestUsage.HasOutputVideoSeconds,
		HasGeneratedImages:    responseUsage.HasGeneratedImages || requestUsage.HasGeneratedImages,
	}
}

func selectInt(responseValue int, responsePresent bool, requestValue int) int {
	if responsePresent {
		return responseValue
	}
	return requestValue
}

func selectFloat(responseValue float64, responsePresent bool, requestValue float64) float64 {
	if responsePresent {
		return responseValue
	}
	return requestValue
}

func (c *BillingCalculator) Lookup() CostLookup {
	return c.lookup
}

func (c *BillingCalculator) OnResult() func(BillingResult) {
	return c.onResult
}
