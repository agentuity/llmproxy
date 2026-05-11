package llmproxy

import (
	"fmt"
	"strings"
)

// ModelMetadata describes catalog capabilities for a model.
type ModelMetadata struct {
	APICompatibility string
	InputModalities  []string
	OutputModalities []string
}

// ModelMetadataLookup resolves catalog metadata for a provider/model pair.
type ModelMetadataLookup func(provider string, model string) (ModelMetadata, bool)

func validateAPITypeAgainstModel(apiType APIType, provider string, model string, metadata ModelMetadata) error {
	if apiType == "" {
		return nil
	}

	switch apiType {
	case APITypeChatCompletions, APITypeResponses, APITypeCompletions, APITypeMessages, APITypeGenerateContent, APITypeStreamGenerateContent, APITypeConverse:
		if !hasModality(metadata.OutputModalities, "text") {
			return unsupportedModelSurfaceError(provider, model, apiType, "text output")
		}
	case APITypeEmbeddings:
		if !hasModality(metadata.OutputModalities, "embedding") {
			return unsupportedModelSurfaceError(provider, model, apiType, "embedding output")
		}
	case APITypeImagesGenerations:
		if !hasModality(metadata.OutputModalities, "image") {
			return unsupportedModelSurfaceError(provider, model, apiType, "image output")
		}
	case APITypeAudioSpeech:
		if !hasModality(metadata.OutputModalities, "audio") {
			return unsupportedModelSurfaceError(provider, model, apiType, "audio output")
		}
	case APITypeAudioTranscriptions:
		if !hasModality(metadata.InputModalities, "audio") || !hasModality(metadata.OutputModalities, "text") {
			return unsupportedModelSurfaceError(provider, model, apiType, "audio input and text output")
		}
	case APITypePredictLongRunning:
		if !hasModality(metadata.OutputModalities, "video") {
			return unsupportedModelSurfaceError(provider, model, apiType, "video output")
		}
	}

	return nil
}

func hasModality(values []string, wanted string) bool {
	wanted = strings.ToLower(strings.TrimSpace(wanted))
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value)) == wanted {
			return true
		}
	}
	return false
}

func unsupportedModelSurfaceError(provider string, model string, apiType APIType, required string) error {
	id := strings.Trim(strings.TrimSpace(provider)+"/"+strings.TrimSpace(model), "/")
	return &ProviderError{
		Message: fmt.Sprintf("model %s does not support %s; %s requires %s", id, apiType, apiType, required),
	}
}
