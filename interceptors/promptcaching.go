package interceptors

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/agentuity/llmproxy"
)

type CacheRetention string

const (
	CacheRetentionDefault CacheRetention = ""
	CacheRetention1h      CacheRetention = "1h"
	CacheRetention24h     CacheRetention = "24h"
)

const (
	HeaderCacheKey                      = "X-Cache-Key"
	HeaderOrgID                         = "X-Org-ID"
	HeaderFireworksSessionAffinity      = "X-Session-Affinity"
	HeaderFireworksPromptCacheIsolation = "X-Prompt-Cache-Isolation-Key"
)

type CacheKeyFunc func(meta llmproxy.BodyMetadata, rawBody []byte) string

type CacheKeyExtractor func(ctx context.Context, req *http.Request, meta llmproxy.BodyMetadata, rawBody []byte) string

type OrgIDExtractor func(ctx context.Context, req *http.Request, meta llmproxy.BodyMetadata) string

type PromptCachingConfig struct {
	Enabled           bool
	Retention         CacheRetention
	CacheKey          string
	Namespace         string
	CacheKeyFn        CacheKeyFunc
	CacheKeyExtractor CacheKeyExtractor
	OrgIDExtractor    OrgIDExtractor
}

type PromptCachingInterceptor struct {
	provider string
	config   PromptCachingConfig
	onResult func(llmproxy.CacheUsage)
}

func (i *PromptCachingInterceptor) Intercept(req *http.Request, meta llmproxy.BodyMetadata, rawBody []byte, next llmproxy.RoundTripFunc) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
	if !i.config.Enabled {
		return next(req)
	}

	if cacheControl := req.Header.Get("Cache-Control"); strings.Contains(cacheControl, "no-cache") {
		return next(req)
	}

	if i.provider != "" {
		modelLower := strings.ToLower(meta.Model)
		shouldApply := false
		switch i.provider {
		case "anthropic":
			shouldApply = strings.Contains(modelLower, "claude")
		case "openai":
			shouldApply = isOpenAIModel(modelLower)
		case "xai":
			shouldApply = isXAIModel(modelLower)
		case "fireworks":
			shouldApply = isFireworksModel(modelLower)
		case "bedrock":
			shouldApply = isBedrockModel(modelLower)
		default:
			shouldApply = strings.Contains(modelLower, i.provider)
		}
		if !shouldApply {
			return next(req)
		}
	}

	if i.provider == "xai" {
		return i.interceptXAI(req, meta, rawBody, next)
	}

	if i.provider == "fireworks" {
		return i.interceptFireworks(req, meta, rawBody, next)
	}

	if i.provider == "bedrock" {
		return i.interceptBedrock(req, meta, rawBody, next)
	}

	modifiedBody, shouldSkip := i.checkSkipOrModify(req, meta, rawBody)
	if shouldSkip {
		return next(req)
	}

	if req.Body != nil {
		req.Body.Close()
	}
	req = cloneRequestWithBody(req, modifiedBody)

	resp, respMeta, rawRespBody, err := next(req)
	if err != nil {
		return resp, respMeta, rawRespBody, err
	}

	if i.onResult != nil {
		if cacheUsage, ok := respMeta.Custom["cache_usage"].(llmproxy.CacheUsage); ok {
			i.onResult(cacheUsage)
		}
	}

	return resp, respMeta, rawRespBody, err
}

func (i *PromptCachingInterceptor) interceptXAI(req *http.Request, meta llmproxy.BodyMetadata, rawBody []byte, next llmproxy.RoundTripFunc) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
	if req.Header.Get("x-grok-conv-id") != "" {
		return next(req)
	}

	cacheKey := i.resolveDynamicCacheKey(req, meta, rawBody)
	if cacheKey == "" {
		return next(req)
	}

	req.Header.Set("x-grok-conv-id", cacheKey)

	resp, respMeta, rawRespBody, err := next(req)
	if err != nil {
		return resp, respMeta, rawRespBody, err
	}

	if i.onResult != nil {
		if cacheUsage, ok := respMeta.Custom["cache_usage"].(llmproxy.CacheUsage); ok {
			i.onResult(cacheUsage)
		}
	}

	return resp, respMeta, rawRespBody, err
}

func (i *PromptCachingInterceptor) interceptFireworks(req *http.Request, meta llmproxy.BodyMetadata, rawBody []byte, next llmproxy.RoundTripFunc) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
	orgID := i.extractOrgID(req, meta)

	if req.Header.Get(HeaderFireworksPromptCacheIsolation) == "" && orgID != "" {
		req.Header.Set(HeaderFireworksPromptCacheIsolation, orgID)
	}

	if req.Header.Get(HeaderFireworksSessionAffinity) == "" {
		if sessionID := i.resolveDynamicCacheKey(req, meta, rawBody); sessionID != "" {
			req.Header.Set(HeaderFireworksSessionAffinity, sessionID)
		}
	}

	resp, respMeta, rawRespBody, err := next(req)
	if err != nil {
		return resp, respMeta, rawRespBody, err
	}

	if cached := resp.Header.Get("fireworks-cached-prompt-tokens"); cached != "" {
		if respMeta.Custom == nil {
			respMeta.Custom = make(map[string]any)
		}
		var cachedTokens int
		if err := json.Unmarshal([]byte(cached), &cachedTokens); err == nil && cachedTokens > 0 {
			respMeta.Custom["cache_usage"] = llmproxy.CacheUsage{
				CachedTokens: cachedTokens,
			}
		}
	}

	if i.onResult != nil {
		if cacheUsage, ok := respMeta.Custom["cache_usage"].(llmproxy.CacheUsage); ok {
			i.onResult(cacheUsage)
		}
	}

	return resp, respMeta, rawRespBody, err
}

func (i *PromptCachingInterceptor) interceptBedrock(req *http.Request, meta llmproxy.BodyMetadata, rawBody []byte, next llmproxy.RoundTripFunc) (*http.Response, llmproxy.ResponseMetadata, []byte, error) {
	modifiedBody, shouldSkip := i.checkBedrock(rawBody)
	if shouldSkip {
		return next(req)
	}

	if req.Body != nil {
		req.Body.Close()
	}
	req = cloneRequestWithBody(req, modifiedBody)

	resp, respMeta, rawRespBody, err := next(req)
	if err != nil {
		return resp, respMeta, rawRespBody, err
	}

	if i.onResult != nil {
		if cacheUsage, ok := respMeta.Custom["cache_usage"].(llmproxy.CacheUsage); ok {
			i.onResult(cacheUsage)
		}
	}

	return resp, respMeta, rawRespBody, err
}

func (i *PromptCachingInterceptor) checkSkipOrModify(req *http.Request, meta llmproxy.BodyMetadata, rawBody []byte) ([]byte, bool) {
	switch i.provider {
	case "anthropic":
		return i.checkAnthropic(rawBody)
	case "openai":
		return i.checkOpenAI(req, meta, rawBody)
	case "bedrock":
		return i.checkBedrock(rawBody)
	default:
		return rawBody, true
	}
}

func (i *PromptCachingInterceptor) checkAnthropic(rawBody []byte) ([]byte, bool) {
	var req map[string]interface{}
	if err := json.Unmarshal(rawBody, &req); err != nil {
		return rawBody, true
	}

	if i.hasExistingCacheControl(req) {
		return rawBody, true
	}

	modified := false

	if system, exists := req["system"]; exists {
		switch s := system.(type) {
		case string:
			if s != "" {
				req["system"] = []interface{}{
					map[string]interface{}{
						"type":          "text",
						"text":          s,
						"cache_control": i.buildCacheControl(),
					},
				}
				modified = true
			}
		case []interface{}:
			if len(s) > 0 {
				lastIdx := len(s) - 1
				if block, ok := s[lastIdx].(map[string]interface{}); ok {
					if _, hasCC := block["cache_control"]; !hasCC {
						block["cache_control"] = i.buildCacheControl()
						s[lastIdx] = block
						modified = true
					}
				}
			}
		}
	}

	if messages, exists := req["messages"]; exists {
		if msgSlice, ok := messages.([]interface{}); ok && len(msgSlice) > 0 {
			for idx := len(msgSlice) - 1; idx >= 0; idx-- {
				if msg, ok := msgSlice[idx].(map[string]interface{}); ok {
					if role, ok := msg["role"].(string); ok && (role == "user" || role == "assistant") {
						if content := msg["content"]; content != nil {
							switch c := content.(type) {
							case string:
								if c != "" {
									msg["content"] = []interface{}{
										map[string]interface{}{
											"type":          "text",
											"text":          c,
											"cache_control": i.buildCacheControl(),
										},
									}
									modified = true
								}
							case []interface{}:
								if len(c) > 0 {
									lastBlock, ok := c[len(c)-1].(map[string]interface{})
									if ok {
										if _, hasCC := lastBlock["cache_control"]; !hasCC {
											lastBlock["cache_control"] = i.buildCacheControl()
											c[len(c)-1] = lastBlock
											modified = true
										}
									}
								}
							}
						}
						break
					}
				}
			}
		}
	}

	if !modified {
		return rawBody, true
	}

	result, err := json.Marshal(req)
	if err != nil {
		return rawBody, true
	}

	return result, false
}

func (i *PromptCachingInterceptor) hasExistingCacheControl(req map[string]interface{}) bool {
	if system, exists := req["system"]; exists {
		if blocks, ok := system.([]interface{}); ok {
			for _, b := range blocks {
				if block, ok := b.(map[string]interface{}); ok {
					if _, has := block["cache_control"]; has {
						return true
					}
				}
			}
		}
	}

	if messages, exists := req["messages"]; exists {
		if msgSlice, ok := messages.([]interface{}); ok {
			for _, m := range msgSlice {
				if msg, ok := m.(map[string]interface{}); ok {
					if content, ok := msg["content"].([]interface{}); ok {
						for _, c := range content {
							if block, ok := c.(map[string]interface{}); ok {
								if _, has := block["cache_control"]; has {
									return true
								}
							}
						}
					}
				}
			}
		}
	}

	return false
}

func (i *PromptCachingInterceptor) buildCacheControl() map[string]interface{} {
	cc := map[string]interface{}{
		"type": "ephemeral",
	}
	if i.config.Retention == CacheRetention1h {
		cc["ttl"] = "1h"
	}
	return cc
}

func (i *PromptCachingInterceptor) checkOpenAI(req *http.Request, meta llmproxy.BodyMetadata, rawBody []byte) ([]byte, bool) {
	var body map[string]interface{}
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return rawBody, true
	}

	if _, exists := body["prompt_cache_key"]; exists {
		return rawBody, true
	}

	modified := false

	cacheKey := i.resolveCacheKey(req, meta, rawBody, body)
	if cacheKey != "" {
		body["prompt_cache_key"] = cacheKey
		modified = true
	}

	if i.config.Retention != "" {
		if _, exists := body["prompt_cache_retention"]; !exists {
			body["prompt_cache_retention"] = string(i.config.Retention)
			modified = true
		}
	}

	if !modified {
		return rawBody, true
	}

	result, err := json.Marshal(body)
	if err != nil {
		return rawBody, true
	}

	return result, false
}

func (i *PromptCachingInterceptor) checkBedrock(rawBody []byte) ([]byte, bool) {
	var req map[string]interface{}
	if err := json.Unmarshal(rawBody, &req); err != nil {
		return rawBody, true
	}

	if i.hasExistingCachePoint(req) {
		return rawBody, true
	}

	modified := false

	if system, exists := req["system"]; exists {
		if sysSlice, ok := system.([]interface{}); ok && len(sysSlice) > 0 {
			lastIdx := len(sysSlice) - 1
			if block, ok := sysSlice[lastIdx].(map[string]interface{}); ok {
				if _, hasCP := block["cachePoint"]; !hasCP {
					sysSlice = append(sysSlice, map[string]interface{}{
						"cachePoint": i.buildCachePoint(),
					})
					req["system"] = sysSlice
					modified = true
				}
			}
		}
	}

	if messages, exists := req["messages"]; exists {
		if msgSlice, ok := messages.([]interface{}); ok && len(msgSlice) > 0 {
			for idx := len(msgSlice) - 1; idx >= 0; idx-- {
				if msg, ok := msgSlice[idx].(map[string]interface{}); ok {
					if role, ok := msg["role"].(string); ok && (role == "user" || role == "assistant") {
						if content := msg["content"]; content != nil {
							if contentSlice, ok := content.([]interface{}); ok && len(contentSlice) > 0 {
								lastBlock, ok := contentSlice[len(contentSlice)-1].(map[string]interface{})
								if ok {
									if _, hasCP := lastBlock["cachePoint"]; !hasCP {
										contentSlice = append(contentSlice, map[string]interface{}{
											"cachePoint": i.buildCachePoint(),
										})
										msg["content"] = contentSlice
										modified = true
									}
								}
							}
						}
						break
					}
				}
			}
		}
	}

	if toolConfig, exists := req["toolConfig"]; exists {
		if tc, ok := toolConfig.(map[string]interface{}); ok {
			if tools, exists := tc["tools"]; exists {
				if toolSlice, ok := tools.([]interface{}); ok && len(toolSlice) > 0 {
					lastBlock, ok := toolSlice[len(toolSlice)-1].(map[string]interface{})
					if ok {
						if _, hasCP := lastBlock["cachePoint"]; !hasCP {
							toolSlice = append(toolSlice, map[string]interface{}{
								"cachePoint": i.buildCachePoint(),
							})
							tc["tools"] = toolSlice
							modified = true
						}
					}
				}
			}
		}
	}

	if !modified {
		return rawBody, true
	}

	result, err := json.Marshal(req)
	if err != nil {
		return rawBody, true
	}

	return result, false
}

func (i *PromptCachingInterceptor) hasExistingCachePoint(req map[string]interface{}) bool {
	if system, exists := req["system"]; exists {
		if blocks, ok := system.([]interface{}); ok {
			for _, b := range blocks {
				if block, ok := b.(map[string]interface{}); ok {
					if _, has := block["cachePoint"]; has {
						return true
					}
				}
			}
		}
	}

	if messages, exists := req["messages"]; exists {
		if msgSlice, ok := messages.([]interface{}); ok {
			for _, m := range msgSlice {
				if msg, ok := m.(map[string]interface{}); ok {
					if content, ok := msg["content"].([]interface{}); ok {
						for _, c := range content {
							if block, ok := c.(map[string]interface{}); ok {
								if _, has := block["cachePoint"]; has {
									return true
								}
							}
						}
					}
				}
			}
		}
	}

	if toolConfig, exists := req["toolConfig"]; exists {
		if tc, ok := toolConfig.(map[string]interface{}); ok {
			if tools, ok := tc["tools"].([]interface{}); ok {
				for _, t := range tools {
					if tool, ok := t.(map[string]interface{}); ok {
						if _, has := tool["cachePoint"]; has {
							return true
						}
					}
				}
			}
		}
	}

	return false
}

func (i *PromptCachingInterceptor) buildCachePoint() map[string]interface{} {
	cp := map[string]interface{}{
		"type": "default",
	}
	if i.config.Retention == CacheRetention1h {
		cp["ttl"] = "1h"
	}
	return cp
}

func (i *PromptCachingInterceptor) resolveCacheKey(req *http.Request, meta llmproxy.BodyMetadata, rawBody []byte, body map[string]interface{}) string {
	orgID := i.extractOrgID(req, meta)

	if headerKey := req.Header.Get(HeaderCacheKey); headerKey != "" {
		return i.buildNamespacedKey(orgID, headerKey)
	}

	if i.config.CacheKey != "" {
		return i.buildNamespacedKey(orgID, i.config.CacheKey)
	}

	if i.config.CacheKeyFn != nil {
		derived := i.config.CacheKeyFn(meta, rawBody)
		if derived != "" {
			return i.buildNamespacedKey(orgID, derived)
		}
	}

	return ""
}

func (i *PromptCachingInterceptor) extractOrgID(req *http.Request, meta llmproxy.BodyMetadata) string {
	if i.config.OrgIDExtractor != nil {
		if orgID := i.config.OrgIDExtractor(req.Context(), req, meta); orgID != "" {
			return orgID
		}
	}

	if metaCtx := llmproxy.GetMetaFromContext(req.Context()); metaCtx.OrgID != "" {
		return metaCtx.OrgID
	}

	if orgID := req.Header.Get(HeaderOrgID); orgID != "" {
		return orgID
	}

	if orgID, ok := meta.Custom["org_id"].(string); ok && orgID != "" {
		return orgID
	}

	return i.config.Namespace
}

func (i *PromptCachingInterceptor) buildNamespacedKey(orgID, key string) string {
	if orgID != "" {
		return orgID + ":" + key
	}
	if i.config.Namespace != "" {
		return i.config.Namespace + ":" + key
	}
	return key
}

func (i *PromptCachingInterceptor) resolveDynamicCacheKey(req *http.Request, meta llmproxy.BodyMetadata, rawBody []byte) string {
	if headerKey := req.Header.Get(HeaderCacheKey); headerKey != "" {
		return headerKey
	}

	if i.config.CacheKeyExtractor != nil {
		if key := i.config.CacheKeyExtractor(req.Context(), req, meta, rawBody); key != "" {
			return key
		}
	}

	if i.config.CacheKeyFn != nil {
		if key := i.config.CacheKeyFn(meta, rawBody); key != "" {
			return key
		}
	}

	return i.config.CacheKey
}

func DeriveCacheKeyFromPrefix(meta llmproxy.BodyMetadata, rawBody []byte) string {
	var body struct {
		System   interface{} `json:"system"`
		Messages []struct {
			Role    string      `json:"role"`
			Content interface{} `json:"content"`
		} `json:"messages"`
		Tools interface{} `json:"tools"`
	}
	json.Unmarshal(rawBody, &body)

	var prefix bytes.Buffer
	if body.System != nil {
		sysBytes, _ := json.Marshal(body.System)
		prefix.Write(sysBytes)
	}
	if body.Tools != nil {
		toolsBytes, _ := json.Marshal(body.Tools)
		prefix.Write(toolsBytes)
	}
	for i, msg := range body.Messages {
		if i < len(body.Messages)-1 {
			msgBytes, _ := json.Marshal(msg)
			prefix.Write(msgBytes)
		}
	}

	if prefix.Len() == 0 {
		return ""
	}

	hash := sha256.Sum256(prefix.Bytes())
	return hex.EncodeToString(hash[:16])
}

func isOpenAIModel(modelLower string) bool {
	return strings.Contains(modelLower, "gpt-") ||
		strings.Contains(modelLower, "o1-") ||
		strings.Contains(modelLower, "o3-") ||
		strings.Contains(modelLower, "o4-") ||
		strings.Contains(modelLower, "chatgpt")
}

func isXAIModel(modelLower string) bool {
	return strings.Contains(modelLower, "grok")
}

func isFireworksModel(modelLower string) bool {
	return strings.Contains(modelLower, "fireworks") ||
		strings.Contains(modelLower, "accounts/fireworks")
}

func isBedrockModel(modelLower string) bool {
	return strings.Contains(modelLower, "anthropic.claude") ||
		strings.Contains(modelLower, "amazon.nova") ||
		strings.Contains(modelLower, "amazon.titan")
}

func cloneRequestWithBody(req *http.Request, body []byte) *http.Request {
	cloned := req.Clone(req.Context())
	cloned.Body = io.NopCloser(bytes.NewReader(body))
	cloned.ContentLength = int64(len(body))
	return cloned
}

func NewPromptCaching(provider string, config PromptCachingConfig) *PromptCachingInterceptor {
	return &PromptCachingInterceptor{
		provider: provider,
		config:   config,
	}
}

func NewPromptCachingWithResult(provider string, config PromptCachingConfig, onResult func(llmproxy.CacheUsage)) *PromptCachingInterceptor {
	return &PromptCachingInterceptor{
		provider: provider,
		config:   config,
		onResult: onResult,
	}
}

func NewAnthropicPromptCaching(retention CacheRetention) *PromptCachingInterceptor {
	return NewPromptCaching("anthropic", PromptCachingConfig{
		Enabled:   true,
		Retention: retention,
	})
}

func NewAnthropicPromptCachingWithResult(retention CacheRetention, onResult func(llmproxy.CacheUsage)) *PromptCachingInterceptor {
	return NewPromptCachingWithResult("anthropic", PromptCachingConfig{
		Enabled:   true,
		Retention: retention,
	}, onResult)
}

func NewOpenAIPromptCaching(retention CacheRetention, cacheKey string) *PromptCachingInterceptor {
	return NewPromptCaching("openai", PromptCachingConfig{
		Enabled:   true,
		Retention: retention,
		CacheKey:  cacheKey,
	})
}

func NewOpenAIPromptCachingWithResult(retention CacheRetention, cacheKey string, onResult func(llmproxy.CacheUsage)) *PromptCachingInterceptor {
	return NewPromptCachingWithResult("openai", PromptCachingConfig{
		Enabled:   true,
		Retention: retention,
		CacheKey:  cacheKey,
	}, onResult)
}

func NewOpenAIPromptCachingWithNamespace(namespace string, retention CacheRetention, cacheKey string) *PromptCachingInterceptor {
	return NewPromptCaching("openai", PromptCachingConfig{
		Enabled:   true,
		Retention: retention,
		CacheKey:  cacheKey,
		Namespace: namespace,
	})
}

func NewOpenAIPromptCachingAuto(namespace string, retention CacheRetention) *PromptCachingInterceptor {
	return NewPromptCaching("openai", PromptCachingConfig{
		Enabled:    true,
		Retention:  retention,
		Namespace:  namespace,
		CacheKeyFn: DeriveCacheKeyFromPrefix,
	})
}

func NewOpenAIPromptCachingAutoWithResult(namespace string, retention CacheRetention, onResult func(llmproxy.CacheUsage)) *PromptCachingInterceptor {
	return NewPromptCachingWithResult("openai", PromptCachingConfig{
		Enabled:    true,
		Retention:  retention,
		Namespace:  namespace,
		CacheKeyFn: DeriveCacheKeyFromPrefix,
	}, onResult)
}

func NewXAIPromptCaching(convID string) *PromptCachingInterceptor {
	return NewPromptCaching("xai", PromptCachingConfig{
		Enabled:  true,
		CacheKey: convID,
	})
}

func NewXAIPromptCachingWithResult(convID string, onResult func(llmproxy.CacheUsage)) *PromptCachingInterceptor {
	return NewPromptCachingWithResult("xai", PromptCachingConfig{
		Enabled:  true,
		CacheKey: convID,
	}, onResult)
}

func NewXAIPromptCachingAuto() *PromptCachingInterceptor {
	return NewPromptCaching("xai", PromptCachingConfig{
		Enabled:    true,
		CacheKeyFn: DeriveCacheKeyFromPrefix,
	})
}

func NewXAIPromptCachingAutoWithResult(onResult func(llmproxy.CacheUsage)) *PromptCachingInterceptor {
	return NewPromptCachingWithResult("xai", PromptCachingConfig{
		Enabled:    true,
		CacheKeyFn: DeriveCacheKeyFromPrefix,
	}, onResult)
}

func NewXAIPromptCachingWithExtractor(extractor CacheKeyExtractor) *PromptCachingInterceptor {
	return NewPromptCaching("xai", PromptCachingConfig{
		Enabled:           true,
		CacheKeyExtractor: extractor,
	})
}

func NewXAIPromptCachingWithExtractorAndResult(extractor CacheKeyExtractor, onResult func(llmproxy.CacheUsage)) *PromptCachingInterceptor {
	return NewPromptCachingWithResult("xai", PromptCachingConfig{
		Enabled:           true,
		CacheKeyExtractor: extractor,
	}, onResult)
}

func NewXAIPromptCachingWithTraceID(traceExtractor TraceExtractor) *PromptCachingInterceptor {
	return NewPromptCaching("xai", PromptCachingConfig{
		Enabled:           true,
		CacheKeyExtractor: TraceIDCacheKeyExtractor(traceExtractor),
	})
}

func NewXAIPromptCachingWithTraceIDAndResult(traceExtractor TraceExtractor, onResult func(llmproxy.CacheUsage)) *PromptCachingInterceptor {
	return NewPromptCachingWithResult("xai", PromptCachingConfig{
		Enabled:           true,
		CacheKeyExtractor: TraceIDCacheKeyExtractor(traceExtractor),
	}, onResult)
}

func NewFireworksPromptCaching(sessionID string) *PromptCachingInterceptor {
	return NewPromptCaching("fireworks", PromptCachingConfig{
		Enabled:  true,
		CacheKey: sessionID,
	})
}

func NewFireworksPromptCachingWithResult(sessionID string, onResult func(llmproxy.CacheUsage)) *PromptCachingInterceptor {
	return NewPromptCachingWithResult("fireworks", PromptCachingConfig{
		Enabled:  true,
		CacheKey: sessionID,
	}, onResult)
}

func NewFireworksPromptCachingAuto() *PromptCachingInterceptor {
	return NewPromptCaching("fireworks", PromptCachingConfig{
		Enabled:    true,
		CacheKeyFn: DeriveCacheKeyFromPrefix,
	})
}

func NewFireworksPromptCachingAutoWithResult(onResult func(llmproxy.CacheUsage)) *PromptCachingInterceptor {
	return NewPromptCachingWithResult("fireworks", PromptCachingConfig{
		Enabled:    true,
		CacheKeyFn: DeriveCacheKeyFromPrefix,
	}, onResult)
}

func NewFireworksPromptCachingWithExtractor(extractor CacheKeyExtractor) *PromptCachingInterceptor {
	return NewPromptCaching("fireworks", PromptCachingConfig{
		Enabled:           true,
		CacheKeyExtractor: extractor,
	})
}

func NewFireworksPromptCachingWithExtractorAndResult(extractor CacheKeyExtractor, onResult func(llmproxy.CacheUsage)) *PromptCachingInterceptor {
	return NewPromptCachingWithResult("fireworks", PromptCachingConfig{
		Enabled:           true,
		CacheKeyExtractor: extractor,
	}, onResult)
}

func NewFireworksPromptCachingWithTraceID(traceExtractor TraceExtractor) *PromptCachingInterceptor {
	return NewPromptCaching("fireworks", PromptCachingConfig{
		Enabled:           true,
		CacheKeyExtractor: TraceIDCacheKeyExtractor(traceExtractor),
	})
}

func NewFireworksPromptCachingWithTraceIDAndResult(traceExtractor TraceExtractor, onResult func(llmproxy.CacheUsage)) *PromptCachingInterceptor {
	return NewPromptCachingWithResult("fireworks", PromptCachingConfig{
		Enabled:           true,
		CacheKeyExtractor: TraceIDCacheKeyExtractor(traceExtractor),
	}, onResult)
}

func NewFireworksPromptCachingWithOrgExtractor(sessionID string, orgExtractor OrgIDExtractor) *PromptCachingInterceptor {
	return NewPromptCaching("fireworks", PromptCachingConfig{
		Enabled:        true,
		CacheKey:       sessionID,
		OrgIDExtractor: orgExtractor,
	})
}

func NewBedrockPromptCaching(retention CacheRetention) *PromptCachingInterceptor {
	return NewPromptCaching("bedrock", PromptCachingConfig{
		Enabled:   true,
		Retention: retention,
	})
}

func NewBedrockPromptCachingWithResult(retention CacheRetention, onResult func(llmproxy.CacheUsage)) *PromptCachingInterceptor {
	return NewPromptCachingWithResult("bedrock", PromptCachingConfig{
		Enabled:   true,
		Retention: retention,
	}, onResult)
}

func NewOpenAIPromptCachingWithOrgExtractor(retention CacheRetention, cacheKey string, orgExtractor OrgIDExtractor) *PromptCachingInterceptor {
	return NewPromptCaching("openai", PromptCachingConfig{
		Enabled:        true,
		Retention:      retention,
		CacheKey:       cacheKey,
		OrgIDExtractor: orgExtractor,
	})
}

func NewOpenAIPromptCachingAutoWithOrgExtractor(retention CacheRetention, orgExtractor OrgIDExtractor) *PromptCachingInterceptor {
	return NewPromptCaching("openai", PromptCachingConfig{
		Enabled:        true,
		Retention:      retention,
		CacheKeyFn:     DeriveCacheKeyFromPrefix,
		OrgIDExtractor: orgExtractor,
	})
}

func DefaultOrgIDExtractor(ctx context.Context, req *http.Request, meta llmproxy.BodyMetadata) string {
	if metaCtx := llmproxy.GetMetaFromContext(ctx); metaCtx.OrgID != "" {
		return metaCtx.OrgID
	}
	if orgID := req.Header.Get(HeaderOrgID); orgID != "" {
		return orgID
	}
	if orgID, ok := meta.Custom["org_id"].(string); ok {
		return orgID
	}
	return ""
}

func TraceIDCacheKeyExtractor(traceExtractor TraceExtractor) CacheKeyExtractor {
	return func(ctx context.Context, req *http.Request, meta llmproxy.BodyMetadata, rawBody []byte) string {
		if traceExtractor == nil {
			return ""
		}
		traceInfo := traceExtractor(ctx)
		if traceInfo.TraceID != [16]byte{} {
			return hex.EncodeToString(traceInfo.TraceID[:])
		}
		return ""
	}
}
