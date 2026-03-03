package llamacpp

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// Runner manages the lifecycle of the embedded llama-server process.
type Runner struct {
	cmd       *exec.Cmd
	port      int
	modelPath string
}

// NewRunner creates a new Runner instance.
func NewRunner(modelPath string) *Runner {
	return &Runner{
		modelPath: modelPath,
	}
}

// Start extracts the embedded binary, finds a free port, and starts the server.
func (r *Runner) Start(ctx context.Context) error {
	// 1. Determine platform and binary name
	var binName string
	switch runtime.GOOS {
	case "windows":
		binName = "llama-server-windows-amd64.exe"
	case "darwin":
		binName = "llama-server-macos-amd64" // Hypothetical, adjust to your needs
	case "linux":
		binName = "llama-server-linux-amd64"
	default:
		return fmt.Errorf("unsupported OS for embedded llama-server: %s", runtime.GOOS)
	}

	// 2. Extract binaries and libraries
	exePath, err := extractAllBinaries(binName)
	if err != nil {
		return fmt.Errorf("failed to extract embedded runner and libraries: %w", err)
	}

	// 3. Find open port
	port, err := getFreePort()
	if err != nil {
		return fmt.Errorf("failed to find free port: %w", err)
	}
	r.port = port

	// 4. Start the process
	r.cmd = exec.CommandContext(ctx, exePath,
		"--model", r.modelPath,
		"--host", "127.0.0.1",
		"--port", fmt.Sprintf("%d", port),
		"--mlock",       // Optional: prevent swapping
		"--log-disable", // Keep stdout clean
	)

	// Inject LD_LIBRARY_PATH so dynamic linker finds .so libraries in ~/.gleann/bin
	destDir := filepath.Dir(exePath)
	r.cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+destDir)

	// We can bind stdout/stderr here for debugging if needed
	r.cmd.Stdout = os.Stdout
	r.cmd.Stderr = os.Stderr

	if err := r.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start llama-server: %w", err)
	}

	// 5. Wait for server to be ready (simple ping loop)
	if err := r.waitForReady(ctx); err != nil {
		r.Stop()
		return err
	}

	return nil
}

// Stop gracefully shuts down the server process.
func (r *Runner) Stop() error {
	if r.cmd != nil && r.cmd.Process != nil {
		return r.cmd.Process.Kill()
	}
	return nil
}

// BaseURL returns the URL to access the OpenAI compatible API.
func (r *Runner) BaseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", r.port)
}

func (r *Runner) waitForReady(ctx context.Context) error {
	// Simple polling to check if the port is listening
	addr := fmt.Sprintf("127.0.0.1:%d", r.port)
	for i := 0; i < 30; i++ {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
			// retry
		}
	}
	return fmt.Errorf("timeout waiting for llama-server to start on %s", addr)
}

func extractAllBinaries(mainBinName string) (string, error) {
	// Destination extraction path (e.g. ~/.gleann/bin/)
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	destDir := filepath.Join(home, ".gleann", "bin")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", err
	}

	entries, err := embeddedBinaries.ReadDir("bin")
	if err != nil {
		return "", fmt.Errorf("failed to read embedded bin directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		destPath := filepath.Join(destDir, entry.Name())

		// Check if already extracted
		if info, err := os.Stat(destPath); err == nil && info.Size() > 0 {
			continue
		}

		fileData, err := embeddedBinaries.Open("bin/" + entry.Name())
		if err != nil {
			return "", fmt.Errorf("failed to open embedded asset %s: %w", entry.Name(), err)
		}

		out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			fileData.Close()
			return "", err
		}

		_, err = io.Copy(out, fileData)
		fileData.Close()
		out.Close()
		if err != nil {
			return "", err
		}
	}

	return filepath.Join(destDir, mainBinName), nil
}

func getFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
