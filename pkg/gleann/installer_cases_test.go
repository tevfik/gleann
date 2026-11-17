package gleann

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// noHome makes os.UserHomeDir fail on every platform.
func noHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
}

// fileAt creates a regular file at path (making parents) so that any
// directory created below it fails.
func fileAt(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fxProgress(t *testing.T) (chan string, func() string) {
	t.Helper()
	ch := make(chan string, 64)
	stop := drain(ch)
	return ch, func() string { return strings.Join(stop(), "\n") }
}

func TestInstallPlugin_HomeAndPluginsDirErrors(t *testing.T) {
	t.Run("no home", func(t *testing.T) {
		noHome(t)
		ch, _ := fxProgress(t)
		_, err := InstallPlugin(PluginMeta{Name: "p"}, ch)
		if err == nil || !strings.Contains(err.Error(), "user home dir") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("plugins dir cannot be created", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		fileAt(t, filepath.Join(home, ".gleann")) // a file where the dir must go
		ch, _ := fxProgress(t)
		_, err := InstallPlugin(PluginMeta{Name: "p"}, ch)
		if err == nil || !strings.Contains(err.Error(), "create plugins dir") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("uninstall without home", func(t *testing.T) {
		noHome(t)
		if _, err := UninstallPlugin("p"); err == nil || !strings.Contains(err.Error(), "user home dir") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("uninstall rejects unsafe names", func(t *testing.T) {
		if _, err := UninstallPlugin("../evil"); err == nil {
			t.Fatal("path traversal name must be rejected")
		}
	})
}

func TestInstallPlugin_ClonesLocalRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	_, plugins, _ := installEnv(t)
	upstream := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = upstream
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
			"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(upstream, "README"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")

	url := "file://" + filepath.ToSlash(upstream)
	if runtime.GOOS == "windows" {
		url = "file:///" + filepath.ToSlash(upstream)
	}
	out, msgs, err := runInstall(t, PluginMeta{Name: "covc-clone", RepoURL: url, Language: "text"})
	if err != nil || out != "Installed covc-clone" {
		t.Fatalf("install = %q, %v (%v)", out, err, msgs)
	}
	if !strings.Contains(strings.Join(msgs, "\n"), "Repository cloned successfully") {
		t.Errorf("progress: %v", msgs)
	}
	if _, err := os.Stat(filepath.Join(plugins, "covc-clone", "README")); err != nil {
		t.Errorf("plugin dir does not expose the clone: %v", err)
	}
}

func TestInstallPlugin_NameWithoutPrefixMapsToPluginRepo(t *testing.T) {
	home, _, _ := installEnv(t)
	g := newInstallGitHub(t)
	// Go plugin, empty directory, nothing in the releases: the repo name is
	// derived from the bare plugin name and the install ends without a binary.
	if err := os.MkdirAll(filepath.Join(home, ".gleann", "plugins", "covcbare"), 0o755); err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	g.apiStatus = http.StatusNotFound
	g.mu.Unlock()
	_, msgs, err := runInstall(t, PluginMeta{Name: "covcbare", Language: "go"})
	if err == nil || !strings.Contains(err.Error(), "no binary in releases") {
		t.Fatalf("err = %v (%v)", err, msgs)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.hits) == 0 || !strings.Contains(g.hits[0], "/repos/acme/gleann-plugin-covcbare/") {
		t.Errorf("release lookup went to %v", g.hits)
	}
}

func TestInstallPlugin_DownloadedDocsAndSoundBinaries(t *testing.T) {
	requireShell(t)
	pattern := platformOrSkip(t)
	for _, tc := range []struct {
		name string
		want string
	}{
		{"gleann-plugin-docs", "models download"},
		{"gleann-plugin-sound", "models download --default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, plugins, logPath := installEnv(t)
			if err := os.MkdirAll(filepath.Join(plugins, tc.name), 0o755); err != nil {
				t.Fatal(err)
			}
			g := newInstallGitHub(t)
			script := "#!/bin/sh\necho \"$(basename \"$0\") $*\" >> '" + logPath + "'\nexit 0\n"
			g.mu.Lock()
			g.assets = map[string][]byte{tc.name + "-" + pattern: []byte(script)}
			g.mu.Unlock()
			out, msgs, err := runInstall(t, PluginMeta{Name: tc.name, Language: "go"})
			if err != nil || !strings.Contains(out, "Go binary") {
				t.Fatalf("install = %q, %v (%v)", out, err, msgs)
			}
			if log := readLog(t, logPath); !strings.Contains(log, tc.want) {
				t.Errorf("binary was not asked to %q:\n%s", tc.want, log)
			}
		})
	}
}

func TestPrepareSoundModelsFailureIsNotFatal(t *testing.T) {
	requireShell(t)
	bin := filepath.Join(t.TempDir(), "snd")
	writeScript(t, bin, filepath.Join(t.TempDir(), "log"), "exit 3")
	ch, stop := fxProgress(t)
	prepareSoundModels(bin, ch)
	if msgs := stop(); !strings.Contains(msgs, "Sound models setup skipped") {
		t.Errorf("progress: %s", msgs)
	}
}

func TestEnsureDoclingModel_PresentAndNoVLMInRelease(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GLEANN_CONFIG_DIR", filepath.Join(home, ".gleann"))
	dir := filepath.Join(home, ".gleann", "models", "granite-docling")

	// Only a release without the VLM model: informative message, no download.
	fr := &fakeRelease{order: []string{"readme.txt"}, assets: map[string]http.HandlerFunc{}}
	newFakeGitHub(t, fr)
	ch, stop := fxProgress(t)
	ensureDoclingModel("o", "r", ch)
	if msgs := stop(); !strings.Contains(msgs, "no Docling VLM model") {
		t.Errorf("progress: %s", msgs)
	}

	// A versioned model file already on disk is recognised without any network.
	if err := os.WriteFile(filepath.Join(dir, "granite_docling_bitnet_dense-q8.eifm"), []byte("m"), 0o644); err != nil {
		t.Fatal(err)
	}
	ch, stop = fxProgress(t)
	ensureDoclingModel("o", "r", ch)
	if msgs := stop(); !strings.Contains(msgs, "present (granite_docling_bitnet_dense-q8.eifm)") {
		t.Errorf("progress: %s", msgs)
	}
}

func TestFindPython3FallsBackToPlainPython(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // neither python nor python3 resolvable
	if got := findPython3(); got != "python" {
		t.Errorf("findPython3 = %q, want the plain fallback", got)
	}
}

func TestCopyDirErrors(t *testing.T) {
	if err := copyDir(filepath.Join(t.TempDir(), "missing"), t.TempDir()); err == nil {
		t.Error("copying a missing source must fail")
	}
	src := t.TempDir()
	if err := os.Symlink(filepath.Join(src, "nowhere"), filepath.Join(src, "dangling")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := copyDir(src, t.TempDir()); err == nil {
		t.Error("an unreadable (dangling) entry must abort the copy")
	}
}

func TestLinkOrCopyFallsBackToCopy(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "f"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir() // already exists: symlink/hardlink refuses, copy merges
	if err := linkOrCopy(src, dst); err != nil {
		t.Fatalf("linkOrCopy: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "f")); string(b) != "data" {
		t.Errorf("copy fallback did not copy: %q", b)
	}
}

func TestExtractFileWithOpenError(t *testing.T) {
	called := false
	err := extractFileWith(filepath.Join(t.TempDir(), "nope"), func(io.Reader) error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Fatalf("err = %v, called = %v", err, called)
	}
}

// fxTarGz builds a gzip'd tar from entries; truncate leaves the last
// entry's body short (a dropped transfer).
func fxTarGz(t *testing.T, truncate bool, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for i, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o644}
		if e.dir {
			h.Typeflag = tar.TypeDir
			h.Mode = 0o755
		} else {
			h.Typeflag = tar.TypeReg
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.dir {
			continue
		}
		if truncate && i == len(entries)-1 {
			_, _ = tw.Write([]byte(e.body[:len(e.body)/2]))
			_ = tw.Flush()
			break
		}
		_, _ = tw.Write([]byte(e.body))
	}
	if !truncate {
		_ = tw.Close()
	}
	_ = gz.Close()
	return buf.Bytes()
}

func TestExtractTarballToDirFailures(t *testing.T) {
	t.Run("dir entry blocked by a file", func(t *testing.T) {
		dest := t.TempDir()
		fileAt(t, filepath.Join(dest, "sub"))
		data := fxTarGz(t, false, tarEntry{name: "top/", dir: true}, tarEntry{name: "top/sub", dir: true})
		err := extractTarballToDir(bytes.NewReader(data), dest)
		if err == nil || !strings.Contains(err.Error(), "mkdir") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("parent of a file blocked by a file", func(t *testing.T) {
		dest := t.TempDir()
		fileAt(t, filepath.Join(dest, "sub"))
		data := fxTarGz(t, false, tarEntry{name: "top/sub/f.txt", body: "x"})
		err := extractTarballToDir(bytes.NewReader(data), dest)
		if err == nil || !strings.Contains(err.Error(), "mkdir parent") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("target is a directory", func(t *testing.T) {
		dest := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dest, "f.txt"), 0o755); err != nil {
			t.Fatal(err)
		}
		data := fxTarGz(t, false, tarEntry{name: "top/f.txt", body: "x"})
		err := extractTarballToDir(bytes.NewReader(data), dest)
		if err == nil || !strings.Contains(err.Error(), "create file") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("truncated entry body", func(t *testing.T) {
		data := fxTarGz(t, true, tarEntry{name: "top/f.txt", body: strings.Repeat("abcd", 64)})
		err := extractTarballToDir(bytes.NewReader(data), t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "write file") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestExtractBinaryFromTarGzFailures(t *testing.T) {
	good := fxTarGz(t, false, tarEntry{name: "pkg/tool", body: "BIN"})
	t.Run("not gzip", func(t *testing.T) {
		err := extractBinaryFromTarGz(strings.NewReader("junk"), filepath.Join(t.TempDir(), "o"), "tool")
		if err == nil || !strings.Contains(err.Error(), "gzip reader") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("binary missing", func(t *testing.T) {
		err := extractBinaryFromTarGz(bytes.NewReader(good), filepath.Join(t.TempDir(), "o"), "other")
		if err == nil || !strings.Contains(err.Error(), "not found in archive") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("corrupt tar stream", func(t *testing.T) {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		_, _ = gz.Write(bytes.Repeat([]byte{0xff}, 1024))
		_ = gz.Close()
		err := extractBinaryFromTarGz(&buf, filepath.Join(t.TempDir(), "o"), "tool")
		if err == nil || !strings.Contains(err.Error(), "tar read") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("destination cannot be created", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "no-such-dir", "o")
		err := extractBinaryFromTarGz(bytes.NewReader(good), dest, "tool")
		if err == nil || !strings.Contains(err.Error(), "create file") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("truncated body", func(t *testing.T) {
		data := fxTarGz(t, true, tarEntry{name: "pkg/tool", body: strings.Repeat("abcd", 64)})
		err := extractBinaryFromTarGz(bytes.NewReader(data), filepath.Join(t.TempDir(), "o"), "tool")
		if err == nil || !strings.Contains(err.Error(), "write file") {
			t.Fatalf("err = %v", err)
		}
	})
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestExtractBinaryFromZipFailures(t *testing.T) {
	t.Run("reader fails", func(t *testing.T) {
		err := extractBinaryFromZip(errReader{}, filepath.Join(t.TempDir(), "o"), "tool")
		if err == nil || !strings.Contains(err.Error(), "read zip data") {
			t.Fatalf("err = %v", err)
		}
	})
	rawZip := func(method uint16, payload []byte) []byte {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, err := zw.CreateRaw(&zip.FileHeader{Name: "tool", Method: method, CompressedSize64: uint64(len(payload)), UncompressedSize64: uint64(len(payload) * 2)})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(payload)
		_ = zw.Close()
		return buf.Bytes()
	}
	t.Run("unsupported compression method", func(t *testing.T) {
		err := extractBinaryFromZip(bytes.NewReader(rawZip(99, []byte("zz"))), filepath.Join(t.TempDir(), "o"), "tool")
		if err == nil || !strings.Contains(err.Error(), "open file in zip") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("corrupt deflate stream", func(t *testing.T) {
		err := extractBinaryFromZip(bytes.NewReader(rawZip(zip.Deflate, bytes.Repeat([]byte{0xff}, 64))), filepath.Join(t.TempDir(), "o"), "tool")
		if err == nil || !strings.Contains(err.Error(), "write file") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("destination cannot be created", func(t *testing.T) {
		data := makeZip(t, map[string]string{"tool": "BIN"})
		err := extractBinaryFromZip(bytes.NewReader(data), filepath.Join(t.TempDir(), "gone", "o"), "tool")
		if err == nil || !strings.Contains(err.Error(), "create file") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestDownloadBinaryFromGitHub_Failures(t *testing.T) {
	pattern := platformOrSkip(t)
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	run := func(t *testing.T, fr *fakeRelease, dest string) error {
		t.Helper()
		newFakeGitHub(t, fr)
		ch := make(chan string, 64)
		stop := drain(ch)
		err := downloadBinaryFromGitHub("o", "tool", dest, ch)
		stop()
		return err
	}
	t.Run("no asset for this platform", func(t *testing.T) {
		fr := &fakeRelease{order: []string{"tool-plan9-arm.tar.gz"}, assets: map[string]http.HandlerFunc{}}
		err := run(t, fr, filepath.Join(t.TempDir(), "tool"+ext))
		if err == nil || !strings.Contains(err.Error(), "no binary found") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("archive download fails", func(t *testing.T) {
		name := "tool-" + pattern + ".tar.gz"
		fr := &fakeRelease{order: []string{name}, assets: map[string]http.HandlerFunc{
			name: func(w http.ResponseWriter, r *http.Request) { http.Error(w, "gone", http.StatusInternalServerError) },
		}}
		err := run(t, fr, filepath.Join(t.TempDir(), "tool"+ext))
		if err == nil || !strings.Contains(err.Error(), "download archive") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("tar.gz without the binary", func(t *testing.T) {
		name := "tool-" + pattern + ".tar.gz"
		fr := &fakeRelease{order: []string{name}, assets: map[string]http.HandlerFunc{
			name: serveBytes(fxTarGz(t, false, tarEntry{name: "README.md", body: "x"})),
		}}
		err := run(t, fr, filepath.Join(t.TempDir(), "tool"+ext))
		if err == nil || !strings.Contains(err.Error(), "extract tar.gz") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("zip without the binary", func(t *testing.T) {
		name := "tool-" + pattern + ".zip"
		fr := &fakeRelease{order: []string{name}, assets: map[string]http.HandlerFunc{
			name: serveBytes(makeZip(t, map[string]string{"README.md": "x"})),
		}}
		err := run(t, fr, filepath.Join(t.TempDir(), "tool"+ext))
		if err == nil || !strings.Contains(err.Error(), "extract zip") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("destination is a non-empty directory", func(t *testing.T) {
		name := "tool-" + pattern
		fr := &fakeRelease{order: []string{name}, assets: map[string]http.HandlerFunc{name: serveBytes([]byte("BIN"))}}
		dest := filepath.Join(t.TempDir(), "tool"+ext)
		if err := os.MkdirAll(filepath.Join(dest, "keep"), 0o755); err != nil {
			t.Fatal(err)
		}
		err := run(t, fr, dest)
		if err == nil || !strings.Contains(err.Error(), "install binary") {
			t.Fatalf("err = %v", err)
		}
		if _, statErr := os.Stat(dest + ".part"); !os.IsNotExist(statErr) {
			t.Errorf("temporary download left behind: %v", statErr)
		}
	})
}
