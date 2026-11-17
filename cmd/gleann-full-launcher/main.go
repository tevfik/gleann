package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

//go:embed payload.tar.gz
var payloadBytes []byte

var (
	version = "dev"
	commit  = "none"
	_       = commit
)

func main() {
	os.Exit(run(payloadBytes, version, os.Args[1:], runtime.GOOS, syscall.Exec, os.Stdin, os.Stdout, os.Stderr))
}

// run extracts payload (built as ver) unless already extracted, starts the
// real binary with args and returns the launcher's exit code. main passes the
// embedded payload and syscall.Exec; tests pass their own.
func run(payload []byte, ver string, args []string, goos string,
	replaceProcess func(argv0 string, argv, envv []string) error,
	stdin io.Reader, stdout, stderr io.Writer) int {
	homeDir, _ := os.UserHomeDir()
	runtimeDir := runtimeDirFor(payload, ver, runtime.GOARCH, homeDir, os.TempDir(), os.Getuid())
	binPath, err := ensureExtracted(payload, runtimeDir, goos)
	if err != nil {
		fmt.Fprintf(stderr, "gleann-full: %v\n", err)
		return 1
	}

	// Tell the real binary which self-contained launcher started it, so that
	// `gleann install` installs the launcher (which embeds every shared
	// library) instead of the extracted gleann-full-bin, which cannot run
	// without the libraries sitting next to it.
	self := ""
	if exe, err := os.Executable(); err == nil {
		self = exe
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			self = resolved
		}
	}
	newEnv := launchEnv(os.Environ(), runtimeDir, goos, self)

	return launch(binPath, args, newEnv, goos, replaceProcess, stdin, stdout, stderr)
}

// launch runs the real binary and returns the launcher's exit code. On Unix
// replaceProcess (syscall.Exec) replaces the launcher with zero overhead and
// returns only on failure; on Windows the binary runs as a child process and
// its exit code is passed on.
func launch(binPath string, args, env []string, goos string,
	replaceProcess func(argv0 string, argv, envv []string) error,
	stdin io.Reader, stdout, stderr io.Writer) int {
	if goos != "windows" {
		execArgs := append([]string{binPath}, args...)
		if err := replaceProcess(binPath, execArgs, env); err != nil {
			fmt.Fprintf(stderr, "gleann-full: exec failed: %v\n", err)
			return 1
		}
		return 0
	}
	cmd := exec.Command(binPath, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = env
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "gleann-full: %v\n", err)
		return 1
	}
	return 0
}

// ensureExtracted extracts payload into runtimeDir unless an earlier run
// finished doing so (its marker file exists) and returns the path of the real
// binary inside it.
func ensureExtracted(payload []byte, runtimeDir, goos string) (string, error) {
	if len(payload) == 0 {
		return "", fmt.Errorf("internal error: empty embedded payload")
	}
	markerFile := filepath.Join(runtimeDir, ".extracted")
	if _, err := os.Stat(markerFile); os.IsNotExist(err) {
		if err := extractPayload(payload, runtimeDir, markerFile); err != nil {
			return "", fmt.Errorf("extraction failed: %w", err)
		}
	}
	return filepath.Join(runtimeDir, binaryNameFor(goos)), nil
}

// runtimeDirFor returns the directory the payload is extracted into:
// <home>/.gleann/runtime/<version>-<arch>-<payload hash prefix>, or a
// per-user directory under tmpDir when there is no home directory. The hash
// gives every build its own directory, so an upgrade never runs stale
// libraries.
func runtimeDirFor(payload []byte, version, arch, homeDir, tmpDir string, uid int) string {
	h := sha256.Sum256(payload)
	hashPrefix := hex.EncodeToString(h[:8])
	var baseDir string
	if homeDir != "" {
		baseDir = filepath.Join(homeDir, ".gleann", "runtime")
	} else {
		baseDir = filepath.Join(tmpDir, fmt.Sprintf("gleann-runtime-%d", uid))
	}
	return filepath.Join(baseDir, fmt.Sprintf("%s-%s-%s", version, arch, hashPrefix))
}

// binaryNameFor is the name of the real binary inside the payload.
func binaryNameFor(goos string) string {
	if goos == "windows" {
		return "gleann-full-bin.exe"
	}
	return "gleann-full-bin"
}

// launchEnv returns env with runtimeDir prepended to the shared-library
// search path (LD_LIBRARY_PATH, DYLD_LIBRARY_PATH on darwin) and, when self
// is known, GLEANN_LAUNCHER set to it.
func launchEnv(env []string, runtimeDir, goos, self string) []string {
	libPathKey := "LD_LIBRARY_PATH"
	if goos == "darwin" {
		libPathKey = "DYLD_LIBRARY_PATH"
	}
	prefix := libPathKey + "="

	existingLibPath := ""
	newEnv := make([]string, 0, len(env)+2)
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			existingLibPath = e[len(prefix):]
			continue
		}
		newEnv = append(newEnv, e)
	}
	newLibPath := runtimeDir
	if existingLibPath != "" {
		newLibPath = runtimeDir + string(os.PathListSeparator) + existingLibPath
	}
	newEnv = append(newEnv, prefix+newLibPath)
	if self != "" {
		newEnv = append(newEnv, "GLEANN_LAUNCHER="+self)
	}
	return newEnv
}

// extractPayload unpacks the gzipped tar payload into targetDir via a
// sibling .tmp directory renamed into place, so a crash mid-extraction never
// leaves a directory with the marker file but missing libraries.
func extractPayload(payload []byte, targetDir, markerFile string) error {
	tmpDir := targetDir + ".tmp"
	_ = os.RemoveAll(tmpDir)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return err
	}

	gzReader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar reader: %w", err)
		}

		targetPath := filepath.Join(tmpDir, header.Name)
		// Prevent Zip Slip vulnerability
		rel, err := filepath.Rel(tmpDir, targetPath)
		if err != nil || filepath.IsAbs(rel) || (len(rel) >= 2 && rel[:2] == "..") {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(header.Mode)
			if mode == 0 {
				mode = 0o644
			}
			outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()
		}
	}

	// Create marker file inside tmpDir before renaming
	if err := os.WriteFile(filepath.Join(tmpDir, ".extracted"), []byte("ok"), 0o644); err != nil {
		return err
	}

	// Remove any existing partial targetDir and atomic rename
	_ = os.RemoveAll(targetDir)
	if err := os.Rename(tmpDir, targetDir); err != nil {
		if _, statErr := os.Stat(markerFile); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}
