package bedrock

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/agentuity/llmproxy"
)

// Extractor implements llmproxy.ResponseExtractor for Bedrock responses.
type Extractor struct{}

// Extract parses a Bedrock Converse response and returns unified metadata.
func (e *Extractor) Extract(resp *http.Response) (llmproxy.ResponseMetadata, []byte, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return llmproxy.ResponseMetadata{}, nil, err
	}

	var bedrockResp ConverseResponse
	if err := json.Unmarshal(body, &bedrockResp); err != nil {
		return llmproxy.ResponseMetadata{}, nil, err
	}

	meta := llmproxy.ResponseMetadata{
		ID:    bedrockResp.RequestID,
		Model: bedrockResp.ModelID,
		Usage: llmproxy.Usage{
			PromptTokens:     bedrockResp.Usage.InputTokens,
			CompletionTokens: bedrockResp.Usage.OutputTokens,
			TotalTokens:      bedrockResp.Usage.TotalTokens,
		},
		Choices: make([]llmproxy.Choice, 0, 1),
		Custom:  make(map[string]any),
	}

	// Extract output content
	if bedrockResp.Output != nil && bedrockResp.Output.Message != nil {
		text := extractOutputText(bedrockResp.Output.Message.Content)
		role := bedrockResp.Output.Message.Role
		if role == "" {
			role = "assistant"
		}
		meta.Choices = append(meta.Choices, llmproxy.Choice{
			Index: 0,
			Message: &llmproxy.Message{
				Role:    role,
				Content: text,
			},
			FinishReason: bedrockResp.StopReason,
		})
	}

	if bedrockResp.Metrics != nil {
		meta.Custom["latency_ms"] = bedrockResp.Metrics.LatencyMs
	}

	if bedrockResp.Usage.CacheReadInputTokens > 0 || bedrockResp.Usage.CacheWriteInputTokens > 0 || len(bedrockResp.Usage.CacheDetails) > 0 {
		meta.Custom["cache_usage"] = llmproxy.CacheUsage{
			CachedTokens:     bedrockResp.Usage.CacheReadInputTokens,
			CacheWriteTokens: bedrockResp.Usage.CacheWriteInputTokens,
			CacheDetails:     extractCacheDetails(bedrockResp.Usage.CacheDetails),
		}
	}

	return meta, body, nil
}

func extractCacheDetails(details []CacheDetail) []llmproxy.CacheDetail {
	if len(details) == 0 {
		return nil
	}
	result := make([]llmproxy.CacheDetail, len(details))
	for i, d := range details {
		result[i] = llmproxy.CacheDetail{
			TTL:              d.TTL,
			CacheWriteTokens: d.CacheWriteInputTokens,
		}
	}
	return result
}

func extractOutputText(content []ContentBlock) string {
	var text string
	for _, block := range content {
		if block.Text != "" {
			text += block.Text
		}
	}
	return text
}

// ConverseResponse represents a Bedrock Converse API response.
type ConverseResponse struct {
	RequestID  string           `json:"requestId,omitempty"`
	ModelID    string           `json:"modelId,omitempty"`
	Output     *Output          `json:"output,omitempty"`
	Usage      ResponseUsage    `json:"usage"`
	StopReason string           `json:"stopReason,omitempty"`
	Metrics    *ResponseMetrics `json:"metrics,omitempty"`
}

// Output contains the model's response.
type Output struct {
	Message *OutputMessage `json:"message,omitempty"`
}

// OutputMessage represents the assistant's response message.
type OutputMessage struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

// ResponseUsage contains token usage information.
type ResponseUsage struct {
	InputTokens           int           `json:"inputTokens"`
	OutputTokens          int           `json:"outputTokens"`
	TotalTokens           int           `json:"totalTokens"`
	CacheReadInputTokens  int           `json:"cacheReadInputTokens,omitempty"`
	CacheWriteInputTokens int           `json:"cacheWriteInputTokens,omitempty"`
	CacheDetails          []CacheDetail `json:"cacheDetails,omitempty"`
}

// CacheDetail contains cache details for a checkpoint.
type CacheDetail struct {
	TTL                   string `json:"ttl"`
	CacheWriteInputTokens int    `json:"cacheWriteInputTokens"`
}

// ResponseMetrics contains performance metrics.
type ResponseMetrics struct {
	LatencyMs int64 `json:"latencyMs"`
}

// NewExtractor creates a new Bedrock response extractor.
func NewExtractor() *Extractor {
	return &Extractor{}
}
