package roles

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadMessageHTTPTruncatedBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Promise more bytes than are sent: reading the body fails.
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("short"))
	}))
	defer ts.Close()
	_, err := LoadMessage(ts.URL)
	if err == nil || !strings.Contains(err.Error(), "read "+ts.URL) {
		t.Fatalf("err = %v, want a read error", err)
	}
}

func TestLoadMessageRelativeRolesDirWithoutWorkingDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the working directory cannot be removed on windows")
	}
	old := RolesDir
	RolesDir = "roles" // relative: resolving it needs the working directory
	t.Cleanup(func() { RolesDir = old })

	dir := filepath.Join(t.TempDir(), "gone")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("PWD", "")
	if err := os.Remove(dir); err != nil {
		t.Skipf("cannot remove the working directory: %v", err)
	}
	if _, err := os.Getwd(); err == nil {
		t.Skip("this OS still reports the removed working directory (darwin)")
	}
	_, err := LoadMessage("file://role.md")
	if err == nil || !strings.Contains(err.Error(), "abs base") {
		t.Fatalf("err = %v, want the abs base error", err)
	}
}
