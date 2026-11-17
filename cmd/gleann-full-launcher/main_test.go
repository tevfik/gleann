package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// childEnv makes the test binary act as the launched gleann-full-bin: it
// echoes its arguments and one variable, then exits with the given code.
const childEnv = "GLEANN_LAUNCHER_TEST_CHILD_EXIT"

func TestMain(m *testing.M) {
	if code := os.Getenv(childEnv); code != "" {
		fmt.Printf("args=%s lib=%s\n", strings.Join(os.Args[1:], ","), os.Getenv("LD_LIBRARY_PATH"))
		n, _ := strconv.Atoi(code)
		os.Exit(n)
	}
	os.Exit(m.Run())
}

func TestLaunchUnixReplacesProcess(t *testing.T) {
	var gotArgv0 string
	var gotArgv, gotEnv []string
	replace := func(argv0 string, argv, envv []string) error {
		gotArgv0, gotArgv, gotEnv = argv0, argv, envv
		return nil
	}
	var stderr bytes.Buffer
	code := launch("/rt/gleann-full-bin", []string{"search", "idx", "q"}, []string{"A=1"}, "linux", replace, nil, nil, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if gotArgv0 != "/rt/gleann-full-bin" {
		t.Errorf("argv0 = %q", gotArgv0)
	}
	if want := []string{"/rt/gleann-full-bin", "search", "idx", "q"}; !slices.Equal(gotArgv, want) {
		t.Errorf("argv = %q, want %q", gotArgv, want)
	}
	if !slices.Equal(gotEnv, []string{"A=1"}) {
		t.Errorf("env = %q", gotEnv)
	}
	if stderr.Len() != 0 {
		t.Errorf("unexpected stderr %q", stderr.String())
	}
}

func TestLaunchUnixExecFailure(t *testing.T) {
	replace := func(string, []string, []string) error { return errors.New("permission denied") }
	var stderr bytes.Buffer
	if code := launch("/rt/gleann-full-bin", nil, nil, "darwin", replace, nil, nil, &stderr); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "exec failed: permission denied") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// The Windows path runs the binary as a child; it is exercised on every OS
// with the test binary standing in for gleann-full-bin.
func TestLaunchChildProcess(t *testing.T) {
	noReplace := func(string, []string, []string) error {
		t.Fatal("the child-process path must not replace the process")
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	for _, code := range []int{0, 3} {
		t.Run(fmt.Sprintf("exit %d", code), func(t *testing.T) {
			env := []string{fmt.Sprintf("%s=%d", childEnv, code), "LD_LIBRARY_PATH=/rt"}
			var stdout, stderr bytes.Buffer
			got := launch(self, []string{"a", "b"}, env, "windows", noReplace, strings.NewReader(""), &stdout, &stderr)
			if got != code {
				t.Errorf("exit code = %d, want the child's %d (stderr %q)", got, code, stderr.String())
			}
			if want := "args=a,b lib=/rt\n"; stdout.String() != want {
				t.Errorf("child stdout = %q, want %q", stdout.String(), want)
			}
		})
	}

	t.Run("missing binary", func(t *testing.T) {
		var stderr bytes.Buffer
		missing := filepath.Join(t.TempDir(), "gleann-full-bin.exe")
		if got := launch(missing, nil, nil, "windows", noReplace, nil, nil, &stderr); got != 1 {
			t.Errorf("exit code = %d, want 1", got)
		}
		if !strings.Contains(stderr.String(), "gleann-full:") {
			t.Errorf("stderr = %q, want the start error", stderr.String())
		}
	})
}

type tarEntry struct {
	name     string
	typeflag byte
	mode     int64
	body     string
}

// buildPayload returns a gzipped tar like the one `make full` embeds.
func buildPayload(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Typeflag: e.typeflag, Mode: e.mode, Size: int64(len(e.body))}
		if e.typeflag == tar.TypeSymlink {
			hdr.Linkname = e.body
			hdr.Size = 0
		}
		if e.typeflag == tar.TypeDir {
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Size > 0 {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestExtractPayloadWritesTreeAndMarker(t *testing.T) {
	payload := buildPayload(t, []tarEntry{
		{name: "./", typeflag: tar.TypeDir, mode: 0o755},
		{name: "./gleann-full-bin", typeflag: tar.TypeReg, mode: 0o755, body: "#!/bin/sh\n"},
		{name: "./libfaiss.so", typeflag: tar.TypeReg, mode: 0o644, body: "lib"},
		// A file whose directory has no entry of its own.
		{name: "./share/models/readme.txt", typeflag: tar.TypeReg, mode: 0, body: "hi"},
		// Entries the launcher must not follow or create.
		{name: "../escape.txt", typeflag: tar.TypeReg, mode: 0o644, body: "evil"},
		{name: "./link", typeflag: tar.TypeSymlink, body: "/etc/passwd"},
	})

	root := t.TempDir()
	target := filepath.Join(root, "dev-amd64-abc")
	marker := filepath.Join(target, ".extracted")
	if err := extractPayload(payload, target, marker); err != nil {
		t.Fatalf("extractPayload: %v", err)
	}

	if got := readFile(t, filepath.Join(target, "gleann-full-bin")); got != "#!/bin/sh\n" {
		t.Errorf("binary content = %q", got)
	}
	if got := readFile(t, filepath.Join(target, "share", "models", "readme.txt")); got != "hi" {
		t.Errorf("nested file content = %q", got)
	}
	if got := readFile(t, marker); got != "ok" {
		t.Errorf("marker content = %q", got)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(filepath.Join(target, "gleann-full-bin"))
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm()&0o100 == 0 {
			t.Errorf("binary lost its executable bit: %v", st.Mode())
		}
		st, err = os.Stat(filepath.Join(target, "share", "models", "readme.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o644 {
			t.Errorf("mode-0 entry mode = %v, want 0644", st.Mode().Perm())
		}
	}
	if _, err := os.Stat(filepath.Join(root, "escape.txt")); !os.IsNotExist(err) {
		t.Errorf("path-traversal entry was written outside the target (stat err %v)", err)
	}
	if _, err := os.Lstat(filepath.Join(target, "link")); !os.IsNotExist(err) {
		t.Errorf("symlink entry was created (lstat err %v)", err)
	}
	if _, err := os.Stat(target + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temporary directory left behind (stat err %v)", err)
	}
}

func TestExtractPayloadReplacesPartialTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "rt")
	// A previous run died after creating the target but before the marker,
	// and left a stale .tmp as well.
	for _, dir := range []string{target, target + ".tmp"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "stale"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	payload := buildPayload(t, []tarEntry{{name: "gleann-full-bin", typeflag: tar.TypeReg, mode: 0o755, body: "new"}})
	if err := extractPayload(payload, target, filepath.Join(target, ".extracted")); err != nil {
		t.Fatalf("extractPayload: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "stale")); !os.IsNotExist(err) {
		t.Errorf("stale file from the partial extraction survived (stat err %v)", err)
	}
	if got := readFile(t, filepath.Join(target, "gleann-full-bin")); got != "new" {
		t.Errorf("binary content = %q", got)
	}
}

func TestExtractPayloadRejectsCorruptPayload(t *testing.T) {
	good := buildPayload(t, []tarEntry{{name: "gleann-full-bin", typeflag: tar.TypeReg, mode: 0o755, body: strings.Repeat("x", 4096)}})

	// A valid gzip stream whose content is not a tar archive.
	var notTar bytes.Buffer
	gz := gzip.NewWriter(&notTar)
	if _, err := gz.Write(bytes.Repeat([]byte{0xff}, 1024)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		payload []byte
		wantErr string
	}{
		{"not gzip", []byte("plain text"), "gzip reader"},
		{"not tar", notTar.Bytes(), "tar reader"},
		{"truncated", good[:len(good)/2], ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "rt")
			marker := filepath.Join(target, ".extracted")
			err := extractPayload(tc.payload, target, marker)
			if err == nil {
				t.Fatal("extractPayload succeeded on a corrupt payload")
			}
			if tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
			// Without the marker the next start retries the extraction.
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Errorf("marker written for a failed extraction (stat err %v)", err)
			}
		})
	}
}

// A malformed archive whose entries collide must fail the extraction, not
// leave a half-written runtime directory with a marker.
func TestExtractPayloadConflictingEntries(t *testing.T) {
	tests := []struct {
		name    string
		entries []tarEntry
	}{
		{"directory over a file", []tarEntry{
			{name: "x", typeflag: tar.TypeReg, mode: 0o644, body: "f"},
			{name: "x", typeflag: tar.TypeDir, mode: 0o755},
		}},
		{"file below a file", []tarEntry{
			{name: "x", typeflag: tar.TypeReg, mode: 0o644, body: "f"},
			{name: "x/y", typeflag: tar.TypeReg, mode: 0o644, body: "g"},
		}},
		{"file over a directory", []tarEntry{
			{name: "x", typeflag: tar.TypeDir, mode: 0o755},
			{name: "x", typeflag: tar.TypeReg, mode: 0o644, body: "f"},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "rt")
			marker := filepath.Join(target, ".extracted")
			if err := extractPayload(buildPayload(t, tc.entries), target, marker); err == nil {
				t.Fatal("extractPayload succeeded")
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Errorf("marker written for a failed extraction (stat err %v)", err)
			}
		})
	}
}

func TestExtractPayloadUnwritableParent(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(parent, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	payload := buildPayload(t, []tarEntry{{name: "a", typeflag: tar.TypeReg, mode: 0o644, body: "a"}})
	target := filepath.Join(parent, "rt") // parent is a regular file
	if err := extractPayload(payload, target, filepath.Join(target, ".extracted")); err == nil {
		t.Fatal("extractPayload succeeded below a regular file")
	}
}

func TestRuntimeDirFor(t *testing.T) {
	home := filepath.Join("home", "u")
	a := runtimeDirFor([]byte("payload-a"), "v1.9.0", "amd64", home, "tmp", 1000)
	if filepath.Dir(a) != filepath.Join(home, ".gleann", "runtime") {
		t.Errorf("runtime dir %q is not under the home runtime directory", a)
	}
	base := filepath.Base(a)
	if !strings.HasPrefix(base, "v1.9.0-amd64-") || len(base) != len("v1.9.0-amd64-")+16 {
		t.Errorf("runtime dir name %q, want v1.9.0-amd64-<16 hex digits>", base)
	}
	if again := runtimeDirFor([]byte("payload-a"), "v1.9.0", "amd64", home, "tmp", 1000); again != a {
		t.Errorf("same payload gave %q then %q", a, again)
	}
	if b := runtimeDirFor([]byte("payload-b"), "v1.9.0", "amd64", home, "tmp", 1000); b == a {
		t.Error("a different payload of the same version reuses the runtime directory")
	}

	noHome := runtimeDirFor([]byte("payload-a"), "dev", "arm64", "", "tmp", 42)
	if filepath.Dir(noHome) != filepath.Join("tmp", "gleann-runtime-42") {
		t.Errorf("without a home directory got %q, want it under tmp/gleann-runtime-42", noHome)
	}
}

func TestBinaryNameFor(t *testing.T) {
	if got := binaryNameFor("windows"); got != "gleann-full-bin.exe" {
		t.Errorf("windows: %q", got)
	}
	for _, goos := range []string{"linux", "darwin"} {
		if got := binaryNameFor(goos); got != "gleann-full-bin" {
			t.Errorf("%s: %q", goos, got)
		}
	}
}

func TestLaunchEnv(t *testing.T) {
	sep := string(os.PathListSeparator)
	tests := []struct {
		name string
		env  []string
		goos string
		self string
		want []string
	}{
		{
			name: "no library path yet",
			env:  []string{"HOME=/h", "PATH=/bin"},
			goos: "linux",
			self: "/opt/gleann-full",
			want: []string{"HOME=/h", "PATH=/bin", "LD_LIBRARY_PATH=/rt", "GLEANN_LAUNCHER=/opt/gleann-full"},
		},
		{
			name: "existing path is kept after the runtime dir",
			env:  []string{"LD_LIBRARY_PATH=/usr/lib", "PATH=/bin"},
			goos: "linux",
			want: []string{"PATH=/bin", "LD_LIBRARY_PATH=/rt" + sep + "/usr/lib"},
		},
		{
			name: "empty variable is replaced, not duplicated",
			env:  []string{"LD_LIBRARY_PATH=", "PATH=/bin"},
			goos: "linux",
			want: []string{"PATH=/bin", "LD_LIBRARY_PATH=/rt"},
		},
		{
			name: "darwin uses DYLD_LIBRARY_PATH and leaves LD_LIBRARY_PATH alone",
			env:  []string{"LD_LIBRARY_PATH=/x", "DYLD_LIBRARY_PATH=/y"},
			goos: "darwin",
			want: []string{"LD_LIBRARY_PATH=/x", "DYLD_LIBRARY_PATH=/rt" + sep + "/y"},
		},
		{
			name: "a variable that only starts with the key is not touched",
			env:  []string{"LD_LIBRARY_PATH_EXTRA=/z"},
			goos: "linux",
			want: []string{"LD_LIBRARY_PATH_EXTRA=/z", "LD_LIBRARY_PATH=/rt"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := launchEnv(tc.env, "/rt", tc.goos, tc.self)
			if !slices.Equal(got, tc.want) {
				t.Errorf("launchEnv =\n %q\nwant\n %q", got, tc.want)
			}
		})
	}
}

func TestEnsureExtracted(t *testing.T) {
	t.Run("empty payload", func(t *testing.T) {
		if _, err := ensureExtracted(nil, filepath.Join(t.TempDir(), "rt"), "linux"); err == nil || !strings.Contains(err.Error(), "empty embedded payload") {
			t.Fatalf("err = %v, want the empty-payload error", err)
		}
	})

	t.Run("extracts once, then reuses the runtime dir", func(t *testing.T) {
		runtimeDir := filepath.Join(t.TempDir(), "rt")
		payload := buildPayload(t, []tarEntry{{name: "gleann-full-bin.exe", typeflag: tar.TypeReg, mode: 0o755, body: "v1"}})

		bin, err := ensureExtracted(payload, runtimeDir, "windows")
		if err != nil {
			t.Fatal(err)
		}
		if bin != filepath.Join(runtimeDir, "gleann-full-bin.exe") {
			t.Errorf("binary path = %q", bin)
		}
		// Mark the extracted copy; a second start must not extract again.
		if err := os.WriteFile(bin, []byte("touched"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := ensureExtracted(payload, runtimeDir, "windows"); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, bin); got != "touched" {
			t.Errorf("second start re-extracted the payload (content %q)", got)
		}
	})

	t.Run("missing marker re-extracts", func(t *testing.T) {
		runtimeDir := filepath.Join(t.TempDir(), "rt")
		payload := buildPayload(t, []tarEntry{{name: "gleann-full-bin", typeflag: tar.TypeReg, mode: 0o755, body: "v1"}})
		bin, err := ensureExtracted(payload, runtimeDir, "linux")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(runtimeDir, ".extracted")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bin, []byte("half-written"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := ensureExtracted(payload, runtimeDir, "linux"); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, bin); got != "v1" {
			t.Errorf("binary content after re-extraction = %q, want v1", got)
		}
	})

	t.Run("corrupt payload", func(t *testing.T) {
		_, err := ensureExtracted([]byte("garbage"), filepath.Join(t.TempDir(), "rt"), "linux")
		if err == nil || !strings.Contains(err.Error(), "extraction failed") {
			t.Fatalf("err = %v, want an extraction error", err)
		}
	})
}
