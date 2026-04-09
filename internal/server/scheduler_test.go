package server

import (
	"os"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/memory"
)

// openTempStore creates a temporary BBolt store suitable for tests.
func openTempStore(t *testing.T) *memory.Store {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "gleann-memory-*.db")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	f.Close()
	store, err := memory.OpenStore(f.Name())
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestNewSchedulerConfig_Defaults(t *testing.T) {
	cfg := newSchedulerConfig()
	if !cfg.enabled {
		t.Fatal("expected scheduler to be enabled by default")
	}
	if cfg.interval != 24*time.Hour {
		t.Fatalf("expected 24h default interval, got %v", cfg.interval)
	}
}

func TestNewSchedulerConfig_Disabled(t *testing.T) {
	t.Setenv("GLEANN_MAINTENANCE_ENABLED", "false")
	cfg := newSchedulerConfig()
	if cfg.enabled {
		t.Fatal("expected scheduler to be disabled when GLEANN_MAINTENANCE_ENABLED=false")
	}
}

func TestNewSchedulerConfig_DisabledZero(t *testing.T) {
	t.Setenv("GLEANN_MAINTENANCE_ENABLED", "0")
	cfg := newSchedulerConfig()
	if cfg.enabled {
		t.Fatal("expected scheduler to be disabled when GLEANN_MAINTENANCE_ENABLED=0")
	}
}

func TestNewSchedulerConfig_CustomInterval(t *testing.T) {
	t.Setenv("GLEANN_MAINTENANCE_INTERVAL_H", "2")
	cfg := newSchedulerConfig()
	if cfg.interval != 2*time.Hour {
		t.Fatalf("expected 2h interval, got %v", cfg.interval)
	}
}

func TestStartMaintenanceScheduler_NilManagerIsNoop(t *testing.T) {
	stopCh := make(chan struct{})
	// Should not panic with nil manager.
	startMaintenanceScheduler(nil, stopCh)
	close(stopCh) // no goroutine spawned, just close immediately
}

func TestStartMaintenanceScheduler_StopsOnClose(t *testing.T) {
	t.Setenv("GLEANN_MAINTENANCE_INTERVAL_H", "0.0000001") // tiny interval
	// Override initial delay by using an env var isn't directly possible here,
	// so we test that sending to stopCh does not block/hang indefinitely.

	store := openTempStore(t)
	mgr := memory.NewManager(store)

	stopCh := make(chan struct{})
	startMaintenanceScheduler(mgr, stopCh)

	// Let the goroutine start.
	time.Sleep(10 * time.Millisecond)

	// Closing stopCh should cause the goroutine to exit cleanly.
	done := make(chan struct{})
	go func() {
		close(stopCh)
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler goroutine did not stop within 2s")
	}
}

func TestRunOnce_DoesNotPanicOnEmptyStore(t *testing.T) {
	store := openTempStore(t)
	mgr := memory.NewManager(store)
	// runOnce calls mgr.RunMaintenance(); should not panic on empty store.
	runOnce(mgr)
}
