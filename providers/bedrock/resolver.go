package bedrock

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"

	"github.com/agentuity/llmproxy"
)

// Resolver implements llmproxy.URLResolver for AWS Bedrock.
type Resolver struct {
	Region      string
	UseConverse bool
}

// Resolve returns the Bedrock endpoint URL for the given model.
// The URL format depends on whether we use the Converse or Invoke API:
//   - Converse: https://bedrock-runtime.{region}.amazonaws.com/model/{modelId}/converse
//   - Invoke: https://bedrock-runtime.{region}.amazonaws.com/model/{modelId}/invoke
func (r *Resolver) Resolve(meta llmproxy.BodyMetadata) (*url.URL, error) {
	modelID := meta.Model
	if modelID == "" {
		return nil, fmt.Errorf("model ID is required for Bedrock")
	}

	// URL encode the model ID (it contains dots and colons)
	encodedModelID := url.PathEscape(modelID)

	var endpoint string
	if r.UseConverse {
		endpoint = fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/converse", r.Region, encodedModelID)
	} else {
		endpoint = fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/invoke", r.Region, encodedModelID)
	}

	return url.Parse(endpoint)
}

// NewResolver creates a new Bedrock resolver for the Converse API.
func NewResolver(region string) *Resolver {
	return &Resolver{
		Region:      region,
		UseConverse: true,
	}
}

// NewInvokeResolver creates a new Bedrock resolver for the Invoke API.
func NewInvokeResolver(region string) *Resolver {
	return &Resolver{
		Region:      region,
		UseConverse: false,
	}
}

// ModelIDExtractor extracts the model ID from a request body.
// This is useful when the model ID is in the body and needs to be
// used in the URL path.
type ModelIDExtractor struct{}

// Extract reads the body and returns the model ID.
func (e *ModelIDExtractor) Extract(body io.ReadCloser) (string, []byte, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return "", nil, err
	}
	body.Close()

	var req struct {
		ModelID string `json:"modelId"`
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return "", nil, err
	}

	return req.ModelID, data, nil
}
