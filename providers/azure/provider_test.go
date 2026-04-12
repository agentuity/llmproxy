package azure

import (
	"context"
	"net/http"
	"testing"

	"github.com/agentuity/llmproxy"
)

func TestResolver_FixedDeployment(t *testing.T) {
	resolver := NewResolver("myresource", "my-deployment", "2024-02-15-preview")

	meta := llmproxy.BodyMetadata{Model: "gpt-4"}
	u, err := resolver.Resolve(meta)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	expected := "https://myresource.openai.azure.com/openai/deployments/my-deployment/chat/completions?api-version=2024-02-15-preview"
	if u.String() != expected {
		t.Errorf("URL = %q, want %q", u.String(), expected)
	}
}

func TestResolver_DynamicDeployment(t *testing.T) {
	resolver := NewResolver("myresource", "", "2024-02-15-preview")

	meta := llmproxy.BodyMetadata{Model: "gpt-4-deployment"}
	u, err := resolver.Resolve(meta)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	expected := "https://myresource.openai.azure.com/openai/deployments/gpt-4-deployment/chat/completions?api-version=2024-02-15-preview"
	if u.String() != expected {
		t.Errorf("URL = %q, want %q", u.String(), expected)
	}
}

func TestEnricher_APIKey(t *testing.T) {
	cfg := &config{
		authMethod: AuthMethodAPIKey,
		apiKey:     "my-api-key",
	}
	enricher := NewEnricher(cfg)

	req, _ := newTestRequest()
	err := enricher.Enrich(req, llmproxy.BodyMetadata{}, nil)
	if err != nil {
		t.Fatalf("Enrich returned error: %v", err)
	}

	if got := req.Header.Get("api-key"); got != "my-api-key" {
		t.Errorf("api-key header = %q, want %q", got, "my-api-key")
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization header should be empty, got %q", got)
	}
}

func TestEnricher_AzureADToken(t *testing.T) {
	cfg := &config{
		authMethod:   AuthMethodAzureAD,
		azureADToken: "my-azure-token",
	}
	enricher := NewEnricher(cfg)

	req, _ := newTestRequest()
	err := enricher.Enrich(req, llmproxy.BodyMetadata{}, nil)
	if err != nil {
		t.Fatalf("Enrich returned error: %v", err)
	}

	if got := req.Header.Get("Authorization"); got != "Bearer my-azure-token" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer my-azure-token")
	}
	if got := req.Header.Get("api-key"); got != "" {
		t.Errorf("api-key header should be empty, got %q", got)
	}
}

func TestEnricher_TokenRefresher(t *testing.T) {
	callCount := 0
	cfg := &config{
		authMethod: AuthMethodAzureAD,
		tokenRefresher: func(ctx context.Context) (string, error) {
			callCount++
			return "refreshed-token", nil
		},
	}
	enricher := NewEnricher(cfg)

	req, _ := newTestRequest()
	err := enricher.Enrich(req, llmproxy.BodyMetadata{}, nil)
	if err != nil {
		t.Fatalf("Enrich returned error: %v", err)
	}

	if callCount != 1 {
		t.Errorf("tokenRefresher called %d times, want 1", callCount)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer refreshed-token" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer refreshed-token")
	}
}

func TestNew_WithAPIKey(t *testing.T) {
	provider, err := New("myresource", "my-deployment", "2024-02-15-preview", WithAPIKey("my-key"))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	if provider.Name() != "azure" {
		t.Errorf("Name = %q, want %q", provider.Name(), "azure")
	}
	if provider.resourceName != "myresource" {
		t.Errorf("resourceName = %q, want %q", provider.resourceName, "myresource")
	}
}

func TestNewWithDynamicDeployment(t *testing.T) {
	provider, err := NewWithDynamicDeployment("myresource", "2024-02-15-preview", WithAPIKey("my-key"))
	if err != nil {
		t.Fatalf("NewWithDynamicDeployment returned error: %v", err)
	}

	if provider.deploymentID != "" {
		t.Errorf("deploymentID should be empty for dynamic deployment, got %q", provider.deploymentID)
	}
}

func TestDefaultAPIVersion(t *testing.T) {
	if v := DefaultAPIVersion(); v != "2024-02-15-preview" {
		t.Errorf("DefaultAPIVersion() = %q, want %q", v, "2024-02-15-preview")
	}
}

func newTestRequest() (*http.Request, error) {
	return http.NewRequest("POST", "https://example.com", nil)
}
