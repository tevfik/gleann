package gleann

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installScript is what a successful pipx leaves behind. It only uses
// absolute tool paths because the tests run with an otherwise empty PATH.
const installScript = `/bin/mkdir -p "$HOME/.local/bin" && printf '#!/bin/sh\n' > "$HOME/.local/bin/markitdown" && /bin/chmod +x "$HOME/.local/bin/markitdown"`

// fxFakeBin writes an executable shell script at dir/name.
func fxFakeBin(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// isolatedPath sets HOME and an empty PATH so only what the test creates
// can be found.
func isolatedPath(t *testing.T) (home, bin string) {
	t.Helper()
	requireShell(t)
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	bin = t.TempDir()
	t.Setenv("PATH", bin)
	return home, bin
}

func TestFindAnyDocLocations(t *testing.T) {
	home, _ := isolatedPath(t)
	if _, _, err := FindAnyDoc(); err == nil {
		t.Fatal("nothing installed: expected an error")
	}
	if NewAnyDocExtractor() != nil {
		t.Error("no CLI available: extractor must be nil")
	}

	// markitdown in ~/.local/bin is the fallback and is flagged as such.
	md := fxFakeBin(t, filepath.Join(home, ".local", "bin"), "markitdown", "echo md")
	path, isAnyDoc, err := FindAnyDoc()
	if err != nil || isAnyDoc || path != md {
		t.Fatalf("FindAnyDoc = %q, %v, %v; want the markitdown fallback", path, isAnyDoc, err)
	}
	if ex := NewMarkItDownExtractor(); ex == nil || ex.IsAnyDoc() || !ex.Available() || ex.BinaryPath() != md {
		t.Errorf("extractor = %+v", ex)
	}

	// anydoc in ~/.cargo/bin wins over it.
	ad := fxFakeBin(t, filepath.Join(home, ".cargo", "bin"), "anydoc", "echo ad")
	path, isAnyDoc, err = FindAnyDoc()
	if err != nil || !isAnyDoc || path != ad {
		t.Fatalf("FindAnyDoc = %q, %v, %v; want anydoc", path, isAnyDoc, err)
	}
}

func TestFindMarkItDownPipxVenv(t *testing.T) {
	home, _ := isolatedPath(t)
	want := fxFakeBin(t, filepath.Join(home, ".local", "pipx", "venvs", "markitdown", "bin"), "markitdown", "true")
	got, err := FindMarkItDown()
	if err != nil || got != want {
		t.Fatalf("FindMarkItDown = %q, %v", got, err)
	}
}

func TestInstallMarkItDown(t *testing.T) {
	t.Run("pipx installs it", func(t *testing.T) {
		home, bin := isolatedPath(t)
		fxFakeBin(t, bin, "pipx", installScript)
		got, err := InstallMarkItDown()
		if err != nil || got != filepath.Join(home, ".local", "bin", "markitdown") {
			t.Fatalf("InstallMarkItDown = %q, %v", got, err)
		}
	})
	t.Run("pipx fails", func(t *testing.T) {
		_, bin := isolatedPath(t)
		fxFakeBin(t, bin, "pipx", "exit 1")
		if _, err := InstallMarkItDown(); err == nil || !strings.Contains(err.Error(), "pipx install markitdown") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("pip needs the PEP 668 override", func(t *testing.T) {
		home, bin := isolatedPath(t)
		fxFakeBin(t, bin, "pip3", `case "$*" in *--break-system-packages*) /bin/mkdir -p "$HOME/.local/bin" && printf '#!/bin/sh\n' > "$HOME/.local/bin/markitdown" && /bin/chmod +x "$HOME/.local/bin/markitdown";; *) exit 1;; esac`)
		got, err := InstallMarkItDown()
		if err != nil || got != filepath.Join(home, ".local", "bin", "markitdown") {
			t.Fatalf("InstallMarkItDown = %q, %v", got, err)
		}
	})
	t.Run("every pip fails", func(t *testing.T) {
		_, bin := isolatedPath(t)
		fxFakeBin(t, bin, "pip3", "exit 1")
		fxFakeBin(t, bin, "pip", "exit 1")
		if _, err := InstallMarkItDown(); err == nil || !strings.Contains(err.Error(), "cannot auto-install") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("no installer at all", func(t *testing.T) {
		isolatedPath(t)
		if _, err := InstallMarkItDown(); err == nil || !strings.Contains(err.Error(), "neither pipx nor pip") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestExtractRunsTheConverter(t *testing.T) {
	_, bin := isolatedPath(t)
	good := &AnyDocExtractor{binaryPath: fxFakeBin(t, bin, "good", `echo "# converted $1"`)}
	out, err := good.Extract("doc.pdf")
	if err != nil || strings.TrimSpace(out) != "# converted doc.pdf" {
		t.Fatalf("Extract = %q, %v", out, err)
	}

	bad := &AnyDocExtractor{binaryPath: fxFakeBin(t, bin, "bad", `echo "corrupt input" >&2; exit 3`)}
	if _, err := bad.Extract("doc.pdf"); err == nil || !strings.Contains(err.Error(), "corrupt input") {
		t.Errorf("a failing converter must surface its stderr, got %v", err)
	}

	missing := &AnyDocExtractor{binaryPath: filepath.Join(bin, "not-there")}
	if _, err := missing.Extract("doc.pdf"); err == nil || !strings.Contains(err.Error(), "document extraction exec") {
		t.Errorf("a converter that cannot be started: %v", err)
	}
}

func TestExtractorNilAndCapabilities(t *testing.T) {
	var nilEx *AnyDocExtractor
	if nilEx.Available() || nilEx.BinaryPath() != "" || nilEx.IsAnyDoc() {
		t.Error("nil extractor must report nothing")
	}
	if _, err := nilEx.Extract("x"); err == nil {
		t.Error("nil extractor cannot extract")
	}
	if nilEx.CanHandle(".html") {
		t.Error("the markitdown set does not include html")
	}
	ad := &AnyDocExtractor{binaryPath: "x", isAnyDoc: true}
	if !ad.CanHandle(".HTML") || len(ad.SupportedExtensions()) <= len(nilEx.SupportedExtensions()) {
		t.Error("anydoc handles more formats than markitdown, case-insensitively")
	}
}
