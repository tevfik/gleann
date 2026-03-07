package gleann

import (
	"fmt"
	"sync"
)

// registry holds all registered backend factories.
var (
	registryMu sync.RWMutex
	registry   = make(map[string]BackendFactory)

	// GraphDBOpener is injected by main if Cgo graph support is enabled.
	GraphDBOpener func(dir string) (GraphDB, error)
)

// RegisterBackend registers a backend factory.
// This is called by backend packages in their init() functions.
func RegisterBackend(factory BackendFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[factory.Name()] = factory
}

// GetBackend returns the backend factory for the given name.
func GetBackend(name string) (BackendFactory, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	factory, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("backend %q not registered; available: %v", name, ListBackends())
	}
	return factory, nil
}

// ListBackends returns the names of all registered backends.
func ListBackends() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

// MustGetBackend returns the backend factory or panics.
func MustGetBackend(name string) BackendFactory {
	factory, err := GetBackend(name)
	if err != nil {
		panic(err)
	}
	return factory
}
