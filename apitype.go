package llmproxy

import (
	"encoding/json"
)

type APIType string

const (
	APITypeChatCompletions APIType = "chat_completions"
	APITypeResponses       APIType = "responses"
	APITypeCompletions     APIType = "completions"
	APITypeMessages        APIType = "messages"
	APITypeGenerateContent APIType = "generate_content"
	APITypeConverse        APIType = "converse"
)

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
		return ""
	}
}

func DetectAPITypeFromBodyAndProvider(body []byte, provider string) APIType {
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

	if _, hasContents := raw["contents"]; hasContents {
		return APITypeGenerateContent
	}

	if _, hasMessages := raw["messages"]; hasMessages {
		switch provider {
		case "anthropic":
			return APITypeMessages
		case "googleai":
			if _, hasContents := raw["contents"]; hasContents {
				return APITypeGenerateContent
			}
			return APITypeMessages
		case "bedrock":
			return APITypeConverse
		}
	}

	if _, hasSystem := raw["system"]; hasSystem {
		if _, hasMessages := raw["messages"]; hasMessages {
			return APITypeMessages
		}
	}

	return APITypeChatCompletions
}

func containsPath(path, substr string) bool {
	return len(path) >= len(substr) && path[len(path)-len(substr):] == substr
}
