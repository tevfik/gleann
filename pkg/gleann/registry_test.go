package gleann

import (
	"testing"
)

func TestRegisterBackend(t *testing.T) {
	// HNSW should be registered via its init() function
	// when the hnsw package is imported.
	// Here we test the registry API directly.

	// Create a mock backend.
	mockFactory := &mockBackendFactory{name: "test-backend"}
	RegisterBackend(mockFactory)

	factory, err := GetBackend("test-backend")
	if err != nil {
		t.Fatalf("GetBackend: %v", err)
	}
	if factory.Name() != "test-backend" {
		t.Errorf("expected name 'test-backend', got %q", factory.Name())
	}
}

func TestGetBackendNotFound(t *testing.T) {
	_, err := GetBackend("nonexistent-backend-xyz")
	if err == nil {
		t.Error("expected error for nonexistent backend")
	}
}

func TestListBackends(t *testing.T) {
	RegisterBackend(&mockBackendFactory{name: "list-test"})
	backends := ListBackends()
	found := false
	for _, name := range backends {
		if name == "list-test" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'list-test' in backend list")
	}
}

func TestMustGetBackendPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nonexistent backend")
		}
	}()
	MustGetBackend("definitely-not-registered")
}

type mockBackendFactory struct {
	name string
}

func (f *mockBackendFactory) Name() string                              { return f.name }
func (f *mockBackendFactory) NewBuilder(config Config) BackendBuilder   { return nil }
func (f *mockBackendFactory) NewSearcher(config Config) BackendSearcher { return nil }
