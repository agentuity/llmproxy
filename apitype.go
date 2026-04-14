package llmproxy

import (
	"encoding/json"
)

// APIType represents the type of LLM API being used.
type APIType string

const (
	// APITypeChatCompletions is the OpenAI chat completions API (/v1/chat/completions).
	APITypeChatCompletions APIType = "chat_completions"
	// APITypeResponses is the OpenAI responses API (/v1/responses).
	APITypeResponses APIType = "responses"
	// APITypeCompletions is the legacy OpenAI completions API (/v1/completions).
	APITypeCompletions APIType = "completions"
	// APITypeMessages is the Anthropic messages API (/v1/messages).
	APITypeMessages APIType = "messages"
	// APITypeGenerateContent is the Google AI generateContent API.
	APITypeGenerateContent APIType = "generate_content"
	// APITypeConverse is the AWS Bedrock converse API.
	APITypeConverse APIType = "converse"
)

// DetectAPIType examines the request body to determine the API type.
// It looks for characteristic fields to identify the API format.
func DetectAPIType(body []byte) APIType {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return APITypeChatCompletions
	}

	if _, hasInput := raw["input"]; hasInput {
		if _, hasMessages := raw["messages"]; !hasMessages {
			return APITypeResponses
		}
	}

	if _, hasPrompt := raw["prompt"]; hasPrompt {
		if _, hasMessages := raw["messages"]; !hasMessages {
			return APITypeCompletions
		}
	}

	return APITypeChatCompletions
}

// DetectAPITypeFromPath examines the request path to determine the API type.
func DetectAPITypeFromPath(path string) APIType {
	switch {
	case containsPath(path, "/v1/chat/completions"):
		return APITypeChatCompletions
	case containsPath(path, "/v1/responses"):
		return APITypeResponses
	case containsPath(path, "/v1/completions"):
		return APITypeCompletions
	case containsPath(path, "/v1/messages"):
		return APITypeMessages
	case containsPath(path, ":generateContent"):
		return APITypeGenerateContent
	case containsPath(path, "/converse"):
		return APITypeConverse
	default:
		return APITypeChatCompletions
	}
}

func containsPath(path, substr string) bool {
	return len(path) >= len(substr) && path[len(path)-len(substr):] == substr
}
