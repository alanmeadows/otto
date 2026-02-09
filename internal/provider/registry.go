package provider

import "fmt"

// Registry manages registered PRBackend implementations and provides
// lookup by name or URL-based auto-detection.
type Registry struct {
	backends []PRBackend
}

// NewRegistry creates an empty backend registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a PRBackend implementation to the registry.
func (r *Registry) Register(b PRBackend) {
	r.backends = append(r.backends, b)
}

// Detect iterates registered backends and returns the first one whose
// MatchesURL method returns true for the given URL.
func (r *Registry) Detect(url string) (PRBackend, error) {
	for _, b := range r.backends {
		if b.MatchesURL(url) {
			return b, nil
		}
	}
	return nil, fmt.Errorf("no registered backend matches URL: %s", url)
}

// Get looks up a registered backend by its Name().
func (r *Registry) Get(name string) (PRBackend, error) {
	for _, b := range r.backends {
		if b.Name() == name {
			return b, nil
		}
	}
	return nil, fmt.Errorf("no registered backend with name: %s", name)
}
