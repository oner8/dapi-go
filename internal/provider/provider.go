package provider

import "net/http"

// Provider is the interface that all upstream providers must implement.
type Provider interface {
	// Name returns the provider name (e.g., "ykt").
	Name() string

	// Prefix returns the URL path prefix (e.g., "/ykt").
	Prefix() string

	// Handler returns the HTTP handler for this provider.
	// It is responsible for handling all requests under Prefix().
	Handler() http.Handler

	// StatusSnapshot returns a JSON-serializable status summary for the
	// gateway /health aggregate. It must not include tokens or credentials.
	StatusSnapshot() map[string]interface{}
}

// Registry holds registered providers by name.
type Registry struct {
	providers map[string]Provider
}

// NewRegistry creates a new provider registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
	}
}

// Register registers a provider.
func (r *Registry) Register(provider Provider) {
	r.providers[provider.Name()] = provider
}

// Get retrieves a provider by name.
func (r *Registry) Get(name string) Provider {
	return r.providers[name]
}

// Handlers returns all registered providers' handlers as a map of prefix -> handler.
func (r *Registry) Handlers() map[string]http.Handler {
	handlers := make(map[string]http.Handler)
	for _, p := range r.providers {
		handlers[p.Prefix()] = p.Handler()
	}
	return handlers
}

// StatusSnapshots returns status summaries from all registered providers.
func (r *Registry) StatusSnapshots() []map[string]interface{} {
	snapshots := make([]map[string]interface{}, 0, len(r.providers))
	for _, p := range r.providers {
		snapshots = append(snapshots, p.StatusSnapshot())
	}
	return snapshots
}
