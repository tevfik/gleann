package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// Under the gleann-full launcher the running binary is the extracted
// gleann-full-bin, which cannot start without the libraries beside it. The
// launcher announces itself via GLEANN_LAUNCHER and is what must be installed.
func TestInstallSource_PrefersLauncher(t *testing.T) {
	dir := t.TempDir()
	launcher := filepath.Join(dir, "gleann-full")
	if err := os.WriteFile(launcher, []byte("launcher"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLEANN_LAUNCHER", launcher)

	path, needsLibs, err := installSource()
	if err != nil {
		t.Fatal(err)
	}
	if path != launcher || needsLibs {
		t.Errorf("installSource = (%q, %v), want (%q, false)", path, needsLibs, launcher)
	}
}

func TestInstallSource_FallsBackToExecutable(t *testing.T) {
	for _, env := range []string{"", filepath.Join(t.TempDir(), "missing")} {
		t.Setenv("GLEANN_LAUNCHER", env)
		path, needsLibs, err := installSource()
		if err != nil {
			t.Fatal(err)
		}
		if path == "" || !needsLibs {
			t.Errorf("GLEANN_LAUNCHER=%q: installSource = (%q, %v), want the running binary with libs", env, path, needsLibs)
		}
	}
}

func TestInstallBinary_InstallsLauncherWithoutLibs(t *testing.T) {
	src := t.TempDir()
	launcher := filepath.Join(src, "gleann-full")
	if err := os.WriteFile(launcher, []byte("self-contained launcher"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A stray library beside the launcher must not be copied: the launcher
	// embeds what it needs.
	if err := os.WriteFile(filepath.Join(src, "libstray.so.1"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLEANN_LAUNCHER", launcher)

	target := filepath.Join(t.TempDir(), "bin")
	if err := InstallBinary(target); err != nil {
		t.Fatal(err)
	}
	name := "gleann"
	if got := mustRead(t, filepath.Join(target, name+exeSuffix())); got != "self-contained launcher" {
		t.Errorf("installed file = %q, want the launcher", got)
	}
	if _, err := os.Stat(filepath.Join(target, "libstray.so.1")); err == nil {
		t.Error("libraries were copied although the launcher is self-contained")
	}
}

func TestBundledLibs_IncludesEverySharedLibNextToExe(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"libgleann_core_rs.so", "libkuzu.so", "libopenblas.so.0", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := map[string]bool{}
	for _, l := range bundledLibs(dir) {
		if got[l] {
			t.Errorf("%s listed twice", l)
		}
		got[l] = true
	}
	for _, want := range []string{"libgleann_core_rs.so", "libkuzu.so", "libopenblas.so.0"} {
		if !got[want] {
			t.Errorf("bundledLibs missed %s", want)
		}
	}
	if got["notes.txt"] {
		t.Error("bundledLibs picked up a non-library")
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exeSuffix() string {
	if os.PathSeparator == '\\' {
		return ".exe"
	}
	return ""
}
