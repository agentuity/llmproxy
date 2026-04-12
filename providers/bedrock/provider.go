package bedrock

import (
	"github.com/agentuity/llmproxy"
)

// Provider is an AWS Bedrock provider implementation.
type Provider struct {
	*llmproxy.BaseProvider
}

// New creates a new Bedrock provider with AWS credentials.
// Uses the Converse API which provides a unified format across models.
//
// Parameters:
//   - region: AWS region (e.g., "us-east-1", "us-west-2", "eu-west-1")
//   - accessKeyID: AWS Access Key ID
//   - secretAccessKey: AWS Secret Access Key
//   - sessionToken: AWS Session Token (optional, pass "" for long-term credentials)
//
// Example:
//
//	// Long-term credentials
//	provider, _ := bedrock.New("us-east-1", "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "")
//
//	// Temporary credentials (from AssumeRole, etc.)
//	provider, _ := bedrock.New("us-east-1", "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "AQoDYXdzEJr...")
func New(region, accessKeyID, secretAccessKey, sessionToken string) (*Provider, error) {
	return &Provider{
		BaseProvider: llmproxy.NewBaseProvider("bedrock",
			llmproxy.WithBodyParser(&Parser{}),
			llmproxy.WithRequestEnricher(NewEnricher(region, accessKeyID, secretAccessKey, sessionToken)),
			llmproxy.WithResponseExtractor(NewExtractor()),
			llmproxy.WithURLResolver(NewResolver(region)),
		),
	}, nil
}

// NewWithConfig creates a Bedrock provider with full configuration.
func NewWithConfig(region, accessKeyID, secretAccessKey, sessionToken string, useConverseAPI bool) (*Provider, error) {
	var resolver llmproxy.URLResolver
	if useConverseAPI {
		resolver = NewResolver(region)
	} else {
		resolver = NewInvokeResolver(region)
	}

	return &Provider{
		BaseProvider: llmproxy.NewBaseProvider("bedrock",
			llmproxy.WithBodyParser(&Parser{}),
			llmproxy.WithRequestEnricher(NewEnricher(region, accessKeyID, secretAccessKey, sessionToken)),
			llmproxy.WithResponseExtractor(NewExtractor()),
			llmproxy.WithURLResolver(resolver),
		),
	}, nil
}
