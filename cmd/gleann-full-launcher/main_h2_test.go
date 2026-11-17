package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunExtractsAndLaunches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LD_LIBRARY_PATH", "")
	payload := buildPayload(t, []tarEntry{{name: "gleann-full-bin", typeflag: tar.TypeReg, mode: 0o755, body: "bin"}})

	var argv0 string
	var argv, env []string
	replace := func(a0 string, av, ev []string) error {
		argv0, argv, env = a0, av, ev
		return nil
	}
	var stderr bytes.Buffer
	if code := run(payload, "v9", []string{"search", "q"}, "linux", replace, nil, nil, &stderr); code != 0 {
		t.Fatalf("run = %d, stderr %q", code, stderr.String())
	}
	runtimeRoot := filepath.Join(home, ".gleann", "runtime")
	if filepath.Dir(filepath.Dir(argv0)) != runtimeRoot || filepath.Base(argv0) != "gleann-full-bin" {
		t.Fatalf("argv0 %q is not the extracted binary under %s", argv0, runtimeRoot)
	}
	if !strings.HasPrefix(filepath.Base(filepath.Dir(argv0)), "v9-") {
		t.Errorf("runtime dir %q does not carry the version", filepath.Dir(argv0))
	}
	if got := readFile(t, argv0); got != "bin" {
		t.Errorf("extracted binary = %q", got)
	}
	if len(argv) != 3 || argv[1] != "search" || argv[2] != "q" {
		t.Errorf("argv = %q", argv)
	}
	var lib, launcher string
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "LD_LIBRARY_PATH="); ok {
			lib = v
		}
		if v, ok := strings.CutPrefix(e, "GLEANN_LAUNCHER="); ok {
			launcher = v
		}
	}
	if lib != filepath.Dir(argv0) {
		t.Errorf("LD_LIBRARY_PATH = %q, want the runtime dir", lib)
	}
	if launcher == "" {
		t.Error("GLEANN_LAUNCHER not set to the running executable")
	}
}

func TestRunExtractionFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	replace := func(string, []string, []string) error {
		t.Fatal("nothing must be launched when extraction fails")
		return nil
	}
	var stderr bytes.Buffer
	if code := run(nil, "v9", nil, "linux", replace, nil, nil, &stderr); code != 1 {
		t.Fatalf("run = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "gleann-full: internal error: empty embedded payload") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestExtractPayloadTruncatedFile(t *testing.T) {
	// A tar whose file body is cut short: the header promises more bytes
	// than the stream holds, so copying the body fails.
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	body := strings.Repeat("x", 4096)
	if err := tw.WriteHeader(&tar.Header{Name: "gleann-full-bin", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	var gzBuf bytes.Buffer
	gz := gzip.NewWriter(&gzBuf)
	if _, err := gz.Write(tarBuf.Bytes()[:512+100]); err != nil { // header + 100 body bytes
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "rt")
	marker := filepath.Join(target, ".extracted")
	if err := extractPayload(gzBuf.Bytes(), target, marker); err == nil {
		t.Fatal("a truncated file body must fail the extraction")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("marker written for a failed extraction (stat err %v)", err)
	}
}

func TestExtractPayloadMarkerNotWritable(t *testing.T) {
	// A payload directory named like the marker file: writing the marker
	// fails, and the extraction must report it instead of renaming.
	payload := buildPayload(t, []tarEntry{
		{name: ".extracted/", typeflag: tar.TypeDir, mode: 0o755},
		{name: ".extracted/x", typeflag: tar.TypeReg, mode: 0o644, body: "x"},
	})
	target := filepath.Join(t.TempDir(), "rt")
	if err := extractPayload(payload, target, filepath.Join(target, ".extracted")); err == nil {
		t.Fatal("extractPayload succeeded although the marker could not be written")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("target dir created for a failed extraction (stat err %v)", err)
	}
}
