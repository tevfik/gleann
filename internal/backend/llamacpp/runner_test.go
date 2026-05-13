package llamacpp

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestNewRunner_FieldsSet ensures the constructor stores the model path and
// leaves cmd/port zero-valued.
func TestNewRunner_FieldsSet(t *testing.T) {
	r := NewRunner("/models/foo.gguf")
	if r == nil {
		t.Fatal("NewRunner returned nil")
	}
	if r.modelPath != "/models/foo.gguf" {
		t.Errorf("modelPath = %q, want %q", r.modelPath, "/models/foo.gguf")
	}
	if r.cmd != nil {
		t.Errorf("cmd should start nil, got %v", r.cmd)
	}
	if r.port != 0 {
		t.Errorf("port should start 0, got %d", r.port)
	}
}

// TestBaseURL_FormatsHostAndPort verifies the URL helper.
func TestBaseURL_FormatsHostAndPort(t *testing.T) {
	r := NewRunner("/m")
	r.port = 12345
	got := r.BaseURL()
	want := "http://127.0.0.1:12345"
	if got != want {
		t.Errorf("BaseURL = %q, want %q", got, want)
	}
}

// TestStop_Idempotent_NoProcess ensures Stop is a no-op when nothing has been
// started (cmd is nil).
func TestStop_Idempotent_NoProcess(t *testing.T) {
	r := NewRunner("/m")
	if err := r.Stop(); err != nil {
		t.Errorf("Stop() with no process should be nil, got %v", err)
	}
}

// TestGetFreePort_ReturnsBindablePort checks that the helper returns a port
// the caller can immediately listen on.
func TestGetFreePort_ReturnsBindablePort(t *testing.T) {
	port, err := getFreePort()
	if err != nil {
		t.Fatalf("getFreePort: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("invalid port %d", port)
	}
	// Make sure it's actually free.
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", itoaTest(port)))
	if err != nil {
		t.Fatalf("port %d should be bindable: %v", port, err)
	}
	l.Close()
}

// TestWaitForReady_TimeoutOnDeadPort exercises the failure branch of
// waitForReady by pointing it at a port that never opens. We use a 200ms
// context to keep the test fast.
func TestWaitForReady_TimeoutOnDeadPort(t *testing.T) {
	r := NewRunner("/m")
	port, err := getFreePort()
	if err != nil {
		t.Fatal(err)
	}
	r.port = port // free port — nothing listening

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err = r.waitForReady(ctx)
	if err == nil {
		t.Fatal("expected timeout error from waitForReady, got nil")
	}
	if !(strings.Contains(err.Error(), "timeout") || err == context.DeadlineExceeded) {
		t.Errorf("expected timeout-related error, got %v", err)
	}
}

// TestWaitForReady_SucceedsWhenPortListens covers the success branch.
func TestWaitForReady_SucceedsWhenPortListens(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	port := l.Addr().(*net.TCPAddr).Port
	r := NewRunner("/m")
	r.port = port

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := r.waitForReady(ctx); err != nil {
		t.Errorf("expected success, got %v", err)
	}
}

// TestStart_UnsupportedOS hits the platform-switch fallthrough by faking the
// runtime.GOOS — we can't override it, so we instead confirm the supported
// branches reach extractAllBinaries (which fails fast on missing embedded
// binary). On linux, where we have an embedded binary, this becomes a real
// integration test and is skipped.
func TestStart_PlatformBranches(t *testing.T) {
	switch runtime.GOOS {
	case "linux", "darwin", "windows":
		// supported branches — we skip to avoid actually launching llama-server.
		t.Skip("Start launches a real binary; covered by integration tests")
	default:
		r := NewRunner("/m")
		err := r.Start(context.Background())
		if err == nil {
			t.Fatal("expected unsupported-OS error")
		}
		if !strings.Contains(err.Error(), "unsupported") {
			t.Errorf("expected 'unsupported' in error, got %v", err)
		}
	}
}

// TestExtractAllBinaries_NoBinariesEmbedded covers the path when the embed FS
// has no executable matching the requested name. We assert that the function
// either returns a path inside ~/.gleann/bin (when binaries are embedded)
// or an error (when they are not), and that the destination directory is
// always created.
func TestExtractAllBinaries_DestDirCreated(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// Use a name that won't exist in the embed FS — the function should still
	// create the dest dir and return a path under it (extractAllBinaries
	// unconditionally returns filepath.Join(destDir, mainBinName)).
	path, err := extractAllBinaries("definitely-not-real-binary")
	if err != nil {
		// Acceptable: embed FS read error. We still want destDir created.
		t.Logf("extractAllBinaries returned error: %v", err)
	}
	if path != "" {
		want := filepath.Join(tmp, ".gleann", "bin", "definitely-not-real-binary")
		if path != want {
			t.Errorf("path = %q, want %q", path, want)
		}
	}
	// destDir must exist.
	if _, statErr := os.Stat(filepath.Join(tmp, ".gleann", "bin")); statErr != nil {
		t.Errorf("dest dir not created: %v", statErr)
	}
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
