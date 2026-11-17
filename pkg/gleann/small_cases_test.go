package gleann

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// unreadableDir creates a subdirectory the walker cannot enter, or skips
// the test where permission bits are not enforced.
func unreadableDir(t *testing.T, parent, name string) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits are not enforced here")
	}
	d := filepath.Join(parent, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "f"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(d, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(d, 0o755) })
}

func TestMeasureDirFaults(t *testing.T) {
	t.Run("missing directory is empty", func(t *testing.T) {
		got, err := MeasureDir(filepath.Join(t.TempDir(), "absent"))
		if err != nil || got.Bytes != 0 || len(got.Folders) != 0 {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("unreadable subdirectory is skipped", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "top.txt"), []byte("12345"), 0o644); err != nil {
			t.Fatal(err)
		}
		unreadableDir(t, dir, "locked")
		got, err := MeasureDir(dir)
		if err != nil || got.Bytes != 5 || got.Folders["top.txt"] != 5 {
			t.Fatalf("got %+v, %v", got, err)
		}
		if _, ok := got.Folders["locked"]; ok {
			t.Errorf("a directory that cannot be read has no usage: %+v", got.Folders)
		}
	})
	t.Run("a path below a regular file is an error", func(t *testing.T) {
		dir := t.TempDir()
		fileAt(t, filepath.Join(dir, "file"))
		_, err := MeasureDir(filepath.Join(dir, "file", "sub"))
		if runtime.GOOS == "windows" {
			// Windows reports ERROR_PATH_NOT_FOUND, which counts as "missing".
			if err != nil {
				t.Errorf("err = %v, want the missing-directory result", err)
			}
		} else if err == nil {
			t.Error("ENOTDIR is a real failure, not an empty directory")
		}
	})
}

func TestMeasureIndexUsageErrors(t *testing.T) {
	t.Run("index dir is a file", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "notadir")
		fileAt(t, f)
		if _, err := MeasureIndexUsage(f); err == nil {
			t.Fatal("a regular file is not an index directory")
		}
	})
	t.Run("unreadable subdirectory aborts the measurement", func(t *testing.T) {
		dir := t.TempDir()
		unreadableDir(t, dir, "locked")
		if _, err := MeasureIndexUsage(dir); err == nil {
			t.Fatal("an unreadable entry must be reported, not silently under-counted")
		}
	})
}

func TestOllamaRequestHelpers(t *testing.T) {
	var gotAuth, gotType, gotBody, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody, gotMethod = string(b), r.Method
	}))
	defer srv.Close()

	if _, err := OllamaGet(nil, "http://host\x7f/api", "k"); err == nil {
		t.Error("OllamaGet must reject an invalid URL")
	}
	if _, err := OllamaPost(nil, "http://host\x7f/api", "application/json", strings.NewReader("{}"), "k"); err == nil {
		t.Error("OllamaPost must reject an invalid URL")
	}

	// A nil client falls back to http.DefaultClient.
	resp, err := OllamaPost(nil, srv.URL, "application/json", strings.NewReader(`{"a":1}`), "secret")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotMethod != http.MethodPost || gotAuth != "Bearer secret" || gotType != "application/json" || gotBody != `{"a":1}` {
		t.Errorf("POST: method=%q auth=%q type=%q body=%q", gotMethod, gotAuth, gotType, gotBody)
	}
	resp, err = OllamaGet(nil, srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotMethod != http.MethodGet || gotAuth != "" {
		t.Errorf("GET without a key: method=%q auth=%q", gotMethod, gotAuth)
	}
}

func TestEstimateJobSkipsUnreadableSamples(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte(strings.Repeat("x", 4096)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, "dangling.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	est, err := EstimateJob(dir, 0) // 0 workers = one per CPU
	if err != nil {
		t.Fatalf("EstimateJob: %v", err)
	}
	if est.FileCount != 2 || est.TotalBytes < 4096 {
		t.Errorf("estimate = %+v", est)
	}
	if est.SampledRate <= 0 {
		t.Errorf("the rate must come from the readable sample or the fallback, got %v", est.SampledRate)
	}
}

func TestInstalledPluginVersionWithoutNumber(t *testing.T) {
	requireShell(t)
	bin := filepath.Join(t.TempDir(), "plug")
	writeScript(t, bin, filepath.Join(t.TempDir(), "log"), "echo 'plugin dev build'")
	if got := installedPluginVersion(bin); got != "" {
		t.Errorf("version = %q, want empty for a build that reports no number", got)
	}
	writeScript(t, bin, filepath.Join(t.TempDir(), "log"), "echo 'plugin 1.4.2-rc1'")
	if got := installedPluginVersion(bin); got != "v1.4.2" {
		t.Errorf("version = %q, want v1.4.2", got)
	}
}

func TestEstimateJobUnreadableDirIsSkipped(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	unreadableDir(t, dir, "locked")
	est, err := EstimateJob(dir, 2)
	if err != nil || est.FileCount != 1 || est.TotalBytes != 3 {
		t.Fatalf("estimate = %+v, %v", est, err)
	}
}
