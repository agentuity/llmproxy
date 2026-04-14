// Package modelsdev provides an adapter for loading model pricing data
// from models.dev (https://models.dev/api.json).
//
// The adapter supports loading from a local file, fetching from the URL,
// or refreshing the data on-demand.
package modelsdev

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/agentuity/llmproxy"
)

// Adapter loads and caches model pricing data from models.dev.
type Adapter struct {
	mu       sync.RWMutex
	data     ModelsDevJSON
	url      string
	filePath string
	client   *http.Client
	expires  time.Time
	ttl      time.Duration
	markup   float64
}

// Option configures the Adapter.
type Option func(*Adapter)

// WithURL sets a custom URL for fetching the models.dev JSON.
func WithURL(url string) Option {
	return func(a *Adapter) { a.url = url }
}

// WithFile sets a local file path to load the JSON from.
func WithFile(path string) Option {
	return func(a *Adapter) { a.filePath = path }
}

// WithHTTPClient sets a custom HTTP client for fetching.
func WithHTTPClient(client *http.Client) Option {
	return func(a *Adapter) { a.client = client }
}

// WithTTL sets the cache TTL for auto-refresh when fetching from URL.
// Default is 1 hour. Set to 0 to disable auto-refresh.
func WithTTL(ttl time.Duration) Option {
	return func(a *Adapter) { a.ttl = ttl }
}

// WithMarkup sets a markup multiplier applied to all prices.
// For example:
//   - 1.0 = no markup (default)
//   - 1.2 = 20% markup (price * 1.2)
//   - 1.5 = 50% markup (price * 1.5)
//
// This is useful for reselling API access with a profit margin.
func WithMarkup(multiplier float64) Option {
	return func(a *Adapter) { a.markup = multiplier }
}

// New creates a new models.dev adapter.
// Data is not loaded until Load() or Lookup() is called.
//
// Example - fetch from URL:
//
//	adapter := modelsdev.New()
//	adapter.Load(context.Background()) // optional, loads on first lookup
//
// Example - load from file:
//
//	adapter := modelsdev.New(modelsdev.WithFile("models.json"))
//
// Example - custom URL, TTL, and markup:
//
//	adapter := modelsdev.New(
//	    modelsdev.WithURL("https://internal.example.com/models.json"),
//	    modelsdev.WithTTL(30*time.Minute),
//	    modelsdev.WithMarkup(1.2), // 20% markup
//	)
func New(opts ...Option) *Adapter {
	a := &Adapter{
		url:    "https://models.dev/api.json",
		client: http.DefaultClient,
		ttl:    time.Hour,
		markup: 1.0,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Load fetches or reads the models.dev JSON data.
// If a file path is set, it reads from the file.
// Otherwise, it fetches from the URL.
func (a *Adapter) Load(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	var data []byte
	var err error

	if a.filePath != "" {
		data, err = os.ReadFile(a.filePath)
	} else {
		data, err = a.fetch(ctx)
	}

	if err != nil {
		return err
	}

	if err := json.Unmarshal(data, &a.data); err != nil {
		return err
	}

	if a.ttl > 0 {
		a.expires = time.Now().Add(a.ttl)
	}
	return nil
}

func (a *Adapter) fetch(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", a.url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

// Refresh forces a reload of the data, bypassing TTL.
func (a *Adapter) Refresh(ctx context.Context) error {
	a.mu.Lock()
	a.expires = time.Time{}
	a.mu.Unlock()
	return a.Load(ctx)
}

// Lookup returns pricing for a provider/model.
// It implements llmproxy.CostLookup interface.
//
// If data is not loaded or TTL expired, it loads automatically.
// Note: Auto-load uses context.Background() - call Load() explicitly
// to control the context for initial load.
func (a *Adapter) Lookup(provider string, model string) (llmproxy.CostInfo, bool) {
	a.mu.RLock()
	needLoad := len(a.data) == 0 || (!a.expires.IsZero() && time.Now().After(a.expires))
	a.mu.RUnlock()

	if needLoad {
		_ = a.Load(context.Background())
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	// Try with provider first
	if provider != "" {
		if p, ok := a.data[provider]; ok {
			if m, ok := p.Models[model]; ok && m.Cost != nil {
				return costToInfo(*m.Cost, a.markup), true
			}
		}
	}

	// Search all providers
	for _, p := range a.data {
		if m, ok := p.Models[model]; ok && m.Cost != nil {
			return costToInfo(*m.Cost, a.markup), true
		}
	}

	return llmproxy.CostInfo{}, false
}

// FindProviderForModel searches all providers to find which one has the given model.
// Returns the provider ID if found, or empty string if not found.
// This is useful for provider detection when the model name is known but provider is not.
func (a *Adapter) FindProviderForModel(model string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.data == nil {
		return ""
	}

	for providerID, provider := range a.data {
		if _, exists := provider.Models[model]; exists {
			return providerID
		}
	}
	return ""
}

// GetCostLookup returns a CostLookup function for use with interceptors.
//
// Example:
//
//	adapter := modelsdev.New(modelsdev.WithFile("models.json"))
//	billing := interceptors.NewBilling(adapter.GetCostLookup(), func(r llmproxy.BillingResult) {
//	    log.Printf("Cost: $%.6f", r.TotalCost)
//	})
func (a *Adapter) GetCostLookup() llmproxy.CostLookup {
	return a.Lookup
}

// GetModel returns model information for a provider/model.
// Returns nil if not found.
func (a *Adapter) GetModel(provider string, model string) *Model {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if p, ok := a.data[provider]; ok {
		if m, ok := p.Models[model]; ok {
			return &m
		}
	}
	return nil
}

// GetProvider returns provider information.
// Returns nil if not found.
func (a *Adapter) GetProvider(provider string) *Provider {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if p, ok := a.data[provider]; ok {
		return &p
	}
	return nil
}

// ListProviders returns all provider IDs.
func (a *Adapter) ListProviders() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	ids := make([]string, 0, len(a.data))
	for id := range a.data {
		ids = append(ids, id)
	}
	return ids
}

// ListModels returns all model IDs for a provider.
func (a *Adapter) ListModels(provider string) []string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	p, ok := a.data[provider]
	if !ok {
		return nil
	}

	models := make([]string, 0, len(p.Models))
	for id := range p.Models {
		models = append(models, id)
	}
	return models
}

// ModelsDevJSON is the root structure of models.dev/api.json.
type ModelsDevJSON map[string]Provider

// Provider represents a provider in models.dev.
type Provider struct {
	ID     string           `json:"id"`
	Name   string           `json:"name"`
	Env    []string         `json:"env"`
	NPM    string           `json:"npm"`
	Doc    string           `json:"doc"`
	API    string           `json:"api,omitempty"`
	Models map[string]Model `json:"models"`
}

// Model represents a model in models.dev.
type Model struct {
	ID               string      `json:"id"`
	Name             string      `json:"name"`
	Family           string      `json:"family,omitempty"`
	Attachment       bool        `json:"attachment"`
	Reasoning        bool        `json:"reasoning"`
	ToolCall         bool        `json:"tool_call"`
	Temperature      bool        `json:"temperature,omitempty"`
	Knowledge        string      `json:"knowledge,omitempty"`
	ReleaseDate      string      `json:"release_date"`
	LastUpdated      string      `json:"last_updated"`
	OpenWeights      bool        `json:"open_weights"`
	StructuredOutput bool        `json:"structured_output,omitempty"`
	Status           string      `json:"status,omitempty"`
	Cost             *Cost       `json:"cost,omitempty"`
	Limit            *Limit      `json:"limit,omitempty"`
	Modalities       *Modalities `json:"modalities,omitempty"`
}

// Cost represents pricing in USD per 1M tokens.
type Cost struct {
	Input       float64 `json:"input,omitempty"`
	Output      float64 `json:"output,omitempty"`
	Reasoning   float64 `json:"reasoning,omitempty"`
	CacheRead   float64 `json:"cache_read,omitempty"`
	CacheWrite  float64 `json:"cache_write,omitempty"`
	InputAudio  float64 `json:"input_audio,omitempty"`
	OutputAudio float64 `json:"output_audio,omitempty"`
}

// Limit represents token limits.
type Limit struct {
	Context int `json:"context,omitempty"`
	Input   int `json:"input,omitempty"`
	Output  int `json:"output,omitempty"`
}

// Modalities represents supported input/output types.
type Modalities struct {
	Input  []string `json:"input,omitempty"`
	Output []string `json:"output,omitempty"`
}

func costToInfo(c Cost, markup float64) llmproxy.CostInfo {
	info := llmproxy.CostInfo{
		Input:      c.Input,
		Output:     c.Output,
		CacheRead:  c.CacheRead,
		CacheWrite: c.CacheWrite,
	}
	if markup > 0 && markup != 1.0 {
		info.Input *= markup
		info.Output *= markup
		info.CacheRead *= markup
		info.CacheWrite *= markup
	}
	return info
}

// LoadFromFile is a convenience function that creates an adapter
// and loads data from a file in one step.
func LoadFromFile(path string) (*Adapter, error) {
	a := New(WithFile(path))
	if err := a.Load(context.Background()); err != nil {
		return nil, err
	}
	return a, nil
}

// LoadFromURL is a convenience function that creates an adapter
// and fetches data from the URL in one step.
func LoadFromURL() (*Adapter, error) {
	a := New()
	if err := a.Load(context.Background()); err != nil {
		return nil, err
	}
	return a, nil
}

// NewLookup is a convenience function that creates an adapter
// and returns just the CostLookup function.
//
// Example:
//
//	lookup, _ := modelsdev.NewLookup(modelsdev.WithFile("models.json"))
//	billing := interceptors.NewBilling(lookup, onResult)
func NewLookup(opts ...Option) (llmproxy.CostLookup, error) {
	a := New(opts...)
	if err := a.Load(context.Background()); err != nil {
		return nil, err
	}
	return a.GetCostLookup(), nil
}
