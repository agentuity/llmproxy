package fastjson

import (
	"encoding/json"
	"io"

	"github.com/agentuity/llmproxy"
	"github.com/minio/simdjson-go"
)

type UsageExtractor struct {
	simdSupported bool
}

func NewUsageExtractor() *UsageExtractor {
	return &UsageExtractor{
		simdSupported: simdjson.SupportedCPU(),
	}
}

func (e *UsageExtractor) ExtractOpenAI(data []byte) (*llmproxy.Usage, *llmproxy.CacheUsage, error) {
	if e.simdSupported && len(data) > 1024 {
		return e.extractOpenAISimd(data)
	}
	return e.extractOpenAIStd(data)
}

func (e *UsageExtractor) extractOpenAISimd(data []byte) (*llmproxy.Usage, *llmproxy.CacheUsage, error) {
	pj, err := simdjson.Parse(data, nil, simdjson.WithCopyStrings(false))
	if err != nil {
		return nil, nil, err
	}

	iter := pj.Iter()

	var elem *simdjson.Element

	usageElem, err := iter.FindElement(elem, "usage")
	if err != nil || usageElem == nil {
		return &llmproxy.Usage{}, nil, nil
	}

	usage := &llmproxy.Usage{}
	var cacheUsage *llmproxy.CacheUsage

	usageIter := usageElem.Iter
	obj, err := usageIter.Object(nil)
	if err != nil {
		return usage, nil, nil
	}

	var tmpIter simdjson.Iter
	for {
		name, t, err := obj.NextElement(&tmpIter)
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		switch name {
		case "prompt_tokens":
			if t == simdjson.TypeInt {
				v, _ := tmpIter.Int()
				usage.PromptTokens = int(v)
			}
		case "completion_tokens":
			if t == simdjson.TypeInt {
				v, _ := tmpIter.Int()
				usage.CompletionTokens = int(v)
			}
		case "total_tokens":
			if t == simdjson.TypeInt {
				v, _ := tmpIter.Int()
				usage.TotalTokens = int(v)
			}
		case "prompt_tokens_details":
			if t == simdjson.TypeObject {
				cacheUsage = e.extractOpenAICacheUsage(&tmpIter)
			}
		}
	}

	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}

	return usage, cacheUsage, nil
}

func (e *UsageExtractor) extractOpenAICacheUsage(iter *simdjson.Iter) *llmproxy.CacheUsage {
	obj, err := iter.Object(nil)
	if err != nil {
		return nil
	}

	cacheUsage := &llmproxy.CacheUsage{}
	var tmpIter simdjson.Iter
	found := false

	for {
		name, t, err := obj.NextElement(&tmpIter)
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		if name == "cached_tokens" && t == simdjson.TypeInt {
			v, _ := tmpIter.Int()
			cacheUsage.CachedTokens = int(v)
			found = true
		}
	}

	if !found {
		return nil
	}
	return cacheUsage
}

func (e *UsageExtractor) extractOpenAIStd(data []byte) (*llmproxy.Usage, *llmproxy.CacheUsage, error) {
	var resp struct {
		Usage *struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			TotalTokens         int `json:"total_tokens"`
			PromptTokensDetails *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, nil, err
	}

	if resp.Usage == nil {
		return &llmproxy.Usage{}, nil, nil
	}

	usage := &llmproxy.Usage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}

	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}

	var cacheUsage *llmproxy.CacheUsage
	if resp.Usage.PromptTokensDetails != nil && resp.Usage.PromptTokensDetails.CachedTokens > 0 {
		cacheUsage = &llmproxy.CacheUsage{
			CachedTokens: resp.Usage.PromptTokensDetails.CachedTokens,
		}
	}

	return usage, cacheUsage, nil
}

func (e *UsageExtractor) ExtractAnthropic(data []byte) (*llmproxy.Usage, *llmproxy.CacheUsage, error) {
	if e.simdSupported && len(data) > 1024 {
		return e.extractAnthropicSimd(data)
	}
	return e.extractAnthropicStd(data)
}

func (e *UsageExtractor) extractAnthropicSimd(data []byte) (*llmproxy.Usage, *llmproxy.CacheUsage, error) {
	pj, err := simdjson.Parse(data, nil, simdjson.WithCopyStrings(false))
	if err != nil {
		return nil, nil, err
	}

	iter := pj.Iter()

	var elem *simdjson.Element

	usageElem, err := iter.FindElement(elem, "usage")
	if err != nil || usageElem == nil {
		return &llmproxy.Usage{}, nil, nil
	}

	usage := &llmproxy.Usage{}
	cacheUsage := &llmproxy.CacheUsage{}

	usageIter := usageElem.Iter
	obj, err := usageIter.Object(nil)
	if err != nil {
		return usage, nil, nil
	}

	var tmpIter simdjson.Iter
	for {
		name, t, err := obj.NextElement(&tmpIter)
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		switch name {
		case "input_tokens":
			if t == simdjson.TypeInt {
				v, _ := tmpIter.Int()
				usage.PromptTokens = int(v)
			}
		case "output_tokens":
			if t == simdjson.TypeInt {
				v, _ := tmpIter.Int()
				usage.CompletionTokens = int(v)
			}
		case "cache_creation_input_tokens":
			if t == simdjson.TypeInt {
				v, _ := tmpIter.Int()
				cacheUsage.CacheCreationInputTokens = int(v)
			}
		case "cache_read_input_tokens":
			if t == simdjson.TypeInt {
				v, _ := tmpIter.Int()
				cacheUsage.CacheReadInputTokens = int(v)
			}
		case "ephemeral_5m_input_tokens":
			if t == simdjson.TypeInt {
				v, _ := tmpIter.Int()
				cacheUsage.Ephemeral5mInputTokens = int(v)
			}
		case "ephemeral_1h_input_tokens":
			if t == simdjson.TypeInt {
				v, _ := tmpIter.Int()
				cacheUsage.Ephemeral1hInputTokens = int(v)
			}
		}
	}

	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens

	hasCache := cacheUsage.CacheCreationInputTokens > 0 || cacheUsage.CacheReadInputTokens > 0 || cacheUsage.Ephemeral5mInputTokens > 0 || cacheUsage.Ephemeral1hInputTokens > 0
	if !hasCache {
		cacheUsage = nil
	}

	return usage, cacheUsage, nil
}

func (e *UsageExtractor) extractAnthropicStd(data []byte) (*llmproxy.Usage, *llmproxy.CacheUsage, error) {
	var resp struct {
		Usage *struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			Ephemeral5mInputTokens   int `json:"ephemeral_5m_input_tokens"`
			Ephemeral1hInputTokens   int `json:"ephemeral_1h_input_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, nil, err
	}

	if resp.Usage == nil {
		return &llmproxy.Usage{}, nil, nil
	}

	usage := &llmproxy.Usage{
		PromptTokens:     resp.Usage.InputTokens,
		CompletionTokens: resp.Usage.OutputTokens,
		TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
	}

	var cacheUsage *llmproxy.CacheUsage
	if resp.Usage.CacheCreationInputTokens > 0 || resp.Usage.CacheReadInputTokens > 0 || resp.Usage.Ephemeral5mInputTokens > 0 || resp.Usage.Ephemeral1hInputTokens > 0 {
		cacheUsage = &llmproxy.CacheUsage{
			CacheCreationInputTokens: resp.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     resp.Usage.CacheReadInputTokens,
			Ephemeral5mInputTokens:   resp.Usage.Ephemeral5mInputTokens,
			Ephemeral1hInputTokens:   resp.Usage.Ephemeral1hInputTokens,
		}
	}

	return usage, cacheUsage, nil
}
