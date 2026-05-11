package llmproxy

import (
	"encoding/json"
)

type APIType string

const (
	APITypeChatCompletions       APIType = "chat_completions"
	APITypeResponses             APIType = "responses"
	APITypeCompletions           APIType = "completions"
	APITypeMessages              APIType = "messages"
	APITypeGenerateContent       APIType = "generate_content"
	APITypeStreamGenerateContent APIType = "stream_generate_content"
	APITypePredictLongRunning    APIType = "predict_long_running"
	APITypeEmbeddings            APIType = "embeddings"
	APITypeImagesGenerations     APIType = "images_generations"
	APITypeAudioSpeech           APIType = "audio_speech"
	APITypeAudioTranscriptions   APIType = "audio_transcriptions"
	APITypeConverse              APIType = "converse"
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
	case containsPath(path, "/v1/embeddings"):
		return APITypeEmbeddings
	case containsPath(path, "/v1/images/generations"):
		return APITypeImagesGenerations
	case containsPath(path, "/v1/audio/speech"):
		return APITypeAudioSpeech
	case containsPath(path, "/v1/audio/transcriptions"):
		return APITypeAudioTranscriptions
	case containsPath(path, "/v1/messages"):
		return APITypeMessages
	case containsPath(path, ":streamGenerateContent"):
		return APITypeStreamGenerateContent
	case containsPath(path, ":predictLongRunning"):
		return APITypePredictLongRunning
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
