package googleai

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/agentuity/llmproxy"
)

// Extractor implements llmproxy.ResponseExtractor for Google AI responses.
type Extractor struct{}

// Extract parses a Google AI response and returns unified metadata.
//
// Google AI responses use:
//   - candidates array instead of choices
//   - usageMetadata with promptTokenCount/candidatesTokenCount
//
// Returns metadata, raw body bytes, and any error.
func (e *Extractor) Extract(resp *http.Response) (llmproxy.ResponseMetadata, []byte, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return llmproxy.ResponseMetadata{}, nil, err
	}

	var googleResp Response
	if err := json.Unmarshal(body, &googleResp); err != nil {
		return llmproxy.ResponseMetadata{}, nil, err
	}

	meta := llmproxy.ResponseMetadata{
		Model: googleResp.ModelName,
		Usage: llmproxy.Usage{
			PromptTokens:     googleResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: googleResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      googleResp.UsageMetadata.TotalTokenCount,
		},
		Choices: make([]llmproxy.Choice, 0, len(googleResp.Candidates)),
		Custom:  make(map[string]any),
	}

	for i, candidate := range googleResp.Candidates {
		text := extractTextFromContent(candidate.Content)
		meta.Choices = append(meta.Choices, llmproxy.Choice{
			Index: i,
			Message: &llmproxy.Message{
				Role:    "assistant",
				Content: text,
			},
			FinishReason: mapFinishReason(candidate.FinishReason),
		})
	}

	if googleResp.PromptFeedback != nil {
		meta.Custom["prompt_feedback"] = googleResp.PromptFeedback
	}

	return meta, body, nil
}

func extractTextFromContent(content *Content) string {
	if content == nil {
		return ""
	}
	return extractTextFromParts(content.Parts)
}

func mapFinishReason(reason string) string {
	switch reason {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY":
		return "content_filter"
	case "RECITATION":
		return "content_filter"
	default:
		return reason
	}
}

// Response represents a Google AI generateContent response.
type Response struct {
	Candidates     []Candidate     `json:"candidates,omitempty"`
	PromptFeedback *PromptFeedback `json:"promptFeedback,omitempty"`
	UsageMetadata  UsageMetadata   `json:"usageMetadata,omitempty"`
	ModelName      string          `json:"model,omitempty"`
}

// Candidate represents a single completion candidate.
type Candidate struct {
	Content       *Content       `json:"content,omitempty"`
	FinishReason  string         `json:"finishReason,omitempty"`
	SafetyRatings []SafetyRating `json:"safetyRatings,omitempty"`
}

// SafetyRating represents safety assessment for a candidate.
type SafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
}

// PromptFeedback contains feedback about the prompt.
type PromptFeedback struct {
	BlockReason   string         `json:"blockReason,omitempty"`
	SafetyRatings []SafetyRating `json:"safetyRatings,omitempty"`
}

// UsageMetadata tracks token usage in a Google AI response.
type UsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// NewExtractor creates a new Google AI response extractor.
func NewExtractor() *Extractor {
	return &Extractor{}
}
