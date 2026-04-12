package llmproxy

import (
	"net/http"
	"sync"
)

// Registry manages a collection of providers and supports routing requests
// to the appropriate provider based on request characteristics.
type Registry interface {
	// Register adds a provider to the registry.
	Register(p Provider)

	// Get retrieves a provider by name.
	// Returns the provider and true if found, nil and false otherwise.
	Get(name string) (Provider, bool)

	// Match selects a provider for the given request.
	// Implementations may parse the request body to determine routing.
	// Returns the matched provider, or an error if no match is found.
	Match(req *http.Request) (Provider, error)
}

// MapRegistry is a simple registry that stores providers by name.
// It provides thread-safe registration and lookup.
type MapRegistry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry creates a new empty registry.
func NewRegistry() *MapRegistry {
	return &MapRegistry{
		providers: make(map[string]Provider),
	}
}

// Register adds a provider to the registry under its name.
// If a provider with the same name exists, it is replaced.
func (r *MapRegistry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Name()] = p
}

// Get retrieves a provider by name.
func (r *MapRegistry) Get(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	return p, ok
}

// Match is not implemented for MapRegistry and returns nil.
// Use a more sophisticated implementation for request-based routing.
func (r *MapRegistry) Match(req *http.Request) (Provider, error) {
	return nil, nil
}
