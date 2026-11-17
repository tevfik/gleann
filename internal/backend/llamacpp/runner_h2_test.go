package llamacpp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveModelPathKeepsExistingRelativePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GLEANN_CONFIG_DIR", "")
	// The same name exists in the models dir and in the working directory:
	// the file the caller can already see wins.
	models := filepath.Join(home, ".gleann", "models")
	if err := os.MkdirAll(models, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(models, "m.gguf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "m.gguf"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)
	if got := ResolveModelPath("m.gguf"); got != "m.gguf" {
		t.Fatalf("ResolveModelPath = %q, want the relative path unchanged", got)
	}
}

func TestWaitForReadyGivesUpAfterReadyTimeout(t *testing.T) {
	old := readyTimeout
	readyTimeout = 300 * time.Millisecond
	t.Cleanup(func() { readyTimeout = old })

	r := NewRunner("/m")
	port, err := getFreePort()
	if err != nil {
		t.Fatal(err)
	}
	r.port = port // nothing listens there
	start := time.Now()
	err = r.waitForReady(context.Background())
	if err == nil || !strings.Contains(err.Error(), "timeout waiting for llama-server") {
		t.Fatalf("err = %v, want the ready timeout", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("waitForReady took %v with a %v timeout", time.Since(start), readyTimeout)
	}
}

// Start without an extract seam uses the binaries embedded for this
// platform. A model file that is not a GGUF must end in a clean error: either
// no embedded server exists for the platform, or the real llama-server exits
// before it is ready.
func TestStartWithEmbeddedRuntimeRejectsBadModel(t *testing.T) {
	if testing.Short() {
		t.Skip("extracts and runs the embedded llama-server")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	model := filepath.Join(t.TempDir(), "bad.gguf")
	if err := os.WriteFile(model, []byte("not a gguf file"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r := NewRunner(model)
	err := r.Start(ctx)
	if err == nil {
		r.Stop()
		t.Fatal("Start accepted a model that is not a GGUF file")
	}
	msg := err.Error()
	if !strings.Contains(msg, "no embedded llama-server") &&
		!strings.Contains(msg, "exited before it was ready") &&
		!strings.Contains(msg, "failed to start llama-server") {
		t.Fatalf("unexpected error: %v", err)
	}
}
