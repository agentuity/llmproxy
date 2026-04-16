package openai_compatible

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/agentuity/llmproxy"
)

type ResponsesExtractor struct{}

func (e *ResponsesExtractor) Extract(resp *http.Response) (llmproxy.ResponseMetadata, []byte, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return llmproxy.ResponseMetadata{}, nil, err
	}

	var responsesResp ResponsesResponse
	if err := json.Unmarshal(body, &responsesResp); err != nil {
		if isArray := len(body) > 0 && body[0] == '['; isArray {
			var outputItems []ResponsesOutputItem
			if err := json.Unmarshal(body, &outputItems); err != nil {
				return llmproxy.ResponseMetadata{}, nil, err
			}
			responsesResp.Output = outputItems
			responsesResp.Status = "completed"
		} else {
			return llmproxy.ResponseMetadata{}, nil, err
		}
	}

	meta := llmproxy.ResponseMetadata{
		ID:     responsesResp.ID,
		Object: responsesResp.Object,
		Model:  responsesResp.Model,
		Usage: llmproxy.Usage{
			PromptTokens:     responsesResp.Usage.InputTokens,
			CompletionTokens: responsesResp.Usage.OutputTokens,
			TotalTokens:      responsesResp.Usage.TotalTokens,
		},
		Custom: make(map[string]any),
	}

	if responsesResp.Usage.InputTokensDetails != nil && responsesResp.Usage.InputTokensDetails.CachedTokens > 0 {
		meta.Custom["cache_usage"] = llmproxy.CacheUsage{
			CachedTokens: responsesResp.Usage.InputTokensDetails.CachedTokens,
		}
	}

	if len(responsesResp.Output) > 0 {
		content := extractResponsesContent(responsesResp.Output)
		meta.Choices = []llmproxy.Choice{
			{
				Index:        0,
				Message:      &llmproxy.Message{Role: "assistant", Content: content},
				FinishReason: responsesResp.Status,
			},
		}
	}

	meta.Custom["status"] = responsesResp.Status
	meta.Custom["api_type"] = llmproxy.APITypeResponses
	if responsesResp.Error != nil {
		meta.Custom["error"] = responsesResp.Error
	}
	if len(responsesResp.Output) > 0 {
		meta.Custom["output"] = responsesResp.Output
	}

	return meta, body, nil
}

func extractResponsesContent(output []ResponsesOutputItem) string {
	var texts []string
	for _, item := range output {
		if item.Type == "message" {
			for _, c := range item.Content {
				if c.Type == "output_text" && c.Text != "" {
					texts = append(texts, c.Text)
				}
			}
		}
	}
	if len(texts) == 0 {
		return ""
	}
	if len(texts) == 1 {
		return texts[0]
	}
	// Join multiple text segments with newline
	result := texts[0]
	for _, t := range texts[1:] {
		result += "\n" + t
	}
	return result
}

type ResponsesResponse struct {
	ID      string                `json:"id"`
	Object  string                `json:"object"`
	Created int64                 `json:"created"`
	Model   string                `json:"model"`
	Status  string                `json:"status"`
	Output  []ResponsesOutputItem `json:"output"`
	Usage   ResponsesUsage        `json:"usage"`
	Error   *ResponsesError       `json:"error,omitempty"`
}

type ResponsesOutputItem struct {
	ID        string                   `json:"id"`
	Type      string                   `json:"type"`
	Status    string                   `json:"status"`
	Role      string                   `json:"role,omitempty"`
	Content   []ResponsesOutputContent `json:"content,omitempty"`
	Name      string                   `json:"name,omitempty"`
	Arguments string                   `json:"arguments,omitempty"`
	Summary   []ResponsesOutputSummary `json:"summary,omitempty"`
}

type ResponsesOutputSummary struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type ResponsesOutputContent struct {
	Type        string                      `json:"type"`
	Text        string                      `json:"text,omitempty"`
	Annotations []ResponsesOutputAnnotation `json:"annotations,omitempty"`
	Logprobs    interface{}                 `json:"logprobs,omitempty"`
}

type ResponsesOutputAnnotation struct {
	Type  string `json:"type"`
	Title string `json:"title,omitempty"`
	URL   string `json:"url,omitempty"`
	Index *int   `json:"index,omitempty"`
}

type ResponsesUsage struct {
	InputTokens         int                     `json:"input_tokens"`
	OutputTokens        int                     `json:"output_tokens"`
	TotalTokens         int                     `json:"total_tokens"`
	InputTokensDetails  *ResponsesInputDetails  `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *ResponsesOutputDetails `json:"output_tokens_details,omitempty"`
}

type ResponsesInputDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

type ResponsesOutputDetails struct {
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

type ResponsesError struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewResponsesExtractor() *ResponsesExtractor {
	return &ResponsesExtractor{}
}
