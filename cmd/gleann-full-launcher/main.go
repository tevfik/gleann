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
	if len(payloadBytes) == 0 {
		fmt.Fprintln(os.Stderr, "gleann-full: internal error: empty embedded payload")
		os.Exit(1)
	}

	// Compute payload hash to identify this specific build
	h := sha256.Sum256(payloadBytes)
	hashPrefix := hex.EncodeToString(h[:8])

	// Determine runtime extraction directory
	homeDir, err := os.UserHomeDir()
	var baseDir string
	if err == nil && homeDir != "" {
		baseDir = filepath.Join(homeDir, ".gleann", "runtime")
	} else {
		baseDir = filepath.Join(os.TempDir(), fmt.Sprintf("gleann-runtime-%d", os.Getuid()))
	}

	runtimeDir := filepath.Join(baseDir, fmt.Sprintf("%s-%s-%s", version, runtime.GOARCH, hashPrefix))
	markerFile := filepath.Join(runtimeDir, ".extracted")

	// Check if already extracted
	if _, err := os.Stat(markerFile); os.IsNotExist(err) {
		if err := extractPayload(runtimeDir, markerFile); err != nil {
			fmt.Fprintf(os.Stderr, "gleann-full: extraction failed: %v\n", err)
			os.Exit(1)
		}
	}

	// Executable to run inside runtimeDir
	binaryName := "gleann-full-bin"
	if runtime.GOOS == "windows" {
		binaryName = "gleann-full-bin.exe"
	}
	binPath := filepath.Join(runtimeDir, binaryName)

	// Prepare environment with library paths
	env := os.Environ()
	var newEnv []string
	libPathKey := "LD_LIBRARY_PATH"
	if runtime.GOOS == "darwin" {
		libPathKey = "DYLD_LIBRARY_PATH"
	}

	existingLibPath := os.Getenv(libPathKey)
	var newLibPath string
	if existingLibPath != "" {
		newLibPath = runtimeDir + string(os.PathListSeparator) + existingLibPath
	} else {
		newLibPath = runtimeDir
	}

	for _, e := range env {
		if len(e) > len(libPathKey)+1 && e[:len(libPathKey)+1] == libPathKey+"=" {
			continue
		}
		newEnv = append(newEnv, e)
	}
	newEnv = append(newEnv, libPathKey+"="+newLibPath)

	// On Unix, use syscall.Exec for zero overhead process replacement
	if runtime.GOOS != "windows" {
		execArgs := append([]string{binPath}, os.Args[1:]...)
		err := syscall.Exec(binPath, execArgs, newEnv)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gleann-full: exec failed: %v\n", err)
			os.Exit(1)
		}
	} else {
		// On Windows, use exec.Command
		cmd := exec.Command(binPath, os.Args[1:]...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = newEnv
		if err := cmd.Run(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				os.Exit(exitErr.ExitCode())
			}
			os.Exit(1)
		}
	}
}

func extractPayload(targetDir, markerFile string) error {
	tmpDir := targetDir + ".tmp"
	_ = os.RemoveAll(tmpDir)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return err
	}

	gzReader, err := gzip.NewReader(bytes.NewReader(payloadBytes))
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
