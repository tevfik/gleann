package llamacpp

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// Runner manages the lifecycle of the embedded llama-server process.
type Runner struct {
	cmd       *exec.Cmd
	exited    chan struct{} // closed when cmd has exited
	waitErr   error         // cmd.Wait result, valid after exited is closed
	port      int
	modelPath string
	embedding bool
}

// NewRunner creates a new Runner instance. A bare file name (what the setup
// wizard and the web UI store) is looked up in ~/.gleann/models.
func NewRunner(modelPath string) *Runner {
	return &Runner{
		modelPath: ResolveModelPath(modelPath),
	}
}

// ResolveModelPath returns p when it names an existing file, else the file of
// that name in ~/.gleann/models when there is one, else p unchanged.
func ResolveModelPath(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		if c := filepath.Join(home, ".gleann", "models", p); fileExists(c) {
			return c
		}
	}
	return p
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// SetEmbedding configures whether the server should be launched in embedding mode.
func (r *Runner) SetEmbedding(embedding bool) {
	r.embedding = embedding
}

// Start extracts the embedded binary, finds a free port, and starts the server.
func (r *Runner) Start(ctx context.Context) error {
	// llama-server aborts the whole process on a missing model; say why instead.
	if !fileExists(r.modelPath) {
		return fmt.Errorf("model file %q not found (put it in ~/.gleann/models, e.g. with: gleann models download <id>)", r.modelPath)
	}
	// 1. Determine platform and binary name
	binName := fmt.Sprintf("llama-server-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		binName += ".exe"
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
	args := []string{
		"--model", r.modelPath,
		"--host", "127.0.0.1",
		"--port", fmt.Sprintf("%d", port),
		"--log-disable", // Keep stdout clean
	}
	if r.embedding {
		args = append(args, "--embedding")
	}

	r.cmd = exec.CommandContext(ctx, exePath, args...)

	// Inject LD_LIBRARY_PATH so dynamic linker finds .so libraries in ~/.gleann/bin
	destDir := filepath.Dir(exePath)
	r.cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+destDir)

	// stderr, not stdout: callers such as `gleann search --json` own stdout.
	r.cmd.Stdout = os.Stderr
	r.cmd.Stderr = os.Stderr

	if err := r.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start llama-server: %w", err)
	}
	r.exited = make(chan struct{})
	go func(cmd *exec.Cmd, exited chan struct{}) {
		r.waitErr = cmd.Wait()
		close(exited)
	}(r.cmd, r.exited)

	// 5. Wait for server to be ready (simple ping loop)
	if err := r.waitForReady(ctx); err != nil {
		r.Stop()
		return err
	}

	return nil
}

// Stop gracefully shuts down the server process.
func (r *Runner) Stop() error {
	if r.cmd == nil || r.cmd.Process == nil || r.exited == nil {
		return nil
	}
	select {
	case <-r.exited:
		return nil
	default:
	}
	// Send SIGTERM for graceful shutdown; force-kill if still running after 5s.
	_ = r.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-r.exited:
		return nil
	case <-time.After(5 * time.Second):
		return r.cmd.Process.Kill()
	}
}

// BaseURL returns the URL to access the OpenAI compatible API.
func (r *Runner) BaseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", r.port)
}

// readyTimeout bounds how long Start waits for the model to load.
var readyTimeout = 120 * time.Second

// waitForReady polls /health until it answers 200. llama-server opens its port
// before the model is loaded and answers 503 meanwhile, so an open port is not
// enough. If the process exits first (bad flag, unsupported model, missing
// library) it fails at once instead of waiting for the timeout.
func (r *Runner) waitForReady(ctx context.Context) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/health", r.port)
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(readyTimeout)
	for time.Now().Before(deadline) {
		if resp, err := client.Get(url); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.exited:
			return fmt.Errorf("llama-server exited before it was ready: %v (see its output above)", r.waitErr)
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("timeout waiting for llama-server on 127.0.0.1:%d", r.port)
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

	entries, err := embeddedBinaries.ReadDir(embeddedDir)
	if err != nil || len(entries) == 0 {
		return "", fmt.Errorf("no embedded llama-server for %s/%s: use an external server (--rerank-url / openai_base_url) instead", runtime.GOOS, runtime.GOARCH)
	}

	for _, name := range staleBinaries {
		_ = os.Remove(filepath.Join(destDir, name))
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := embeddedBinaries.ReadFile(embeddedDir + "/" + entry.Name())
		if err != nil {
			return "", fmt.Errorf("failed to read embedded asset %s: %w", entry.Name(), err)
		}
		if err := writeIfChanged(filepath.Join(destDir, entry.Name()), data); err != nil {
			return "", fmt.Errorf("failed to extract %s: %w", entry.Name(), err)
		}
	}

	return filepath.Join(destDir, mainBinName), nil
}

// staleBinaries are files an older gleann extracted to ~/.gleann/bin that the
// current llama.cpp build no longer ships. They are removed because ggml loads
// CPU backends from the executable's directory, and an old libggml-cpu.so
// built against a different libggml-base would be ABI-incompatible.
var staleBinaries = []string{
	"libllava_shared.so", "libggml-cpu.so", "libggml.so", "libggml-base.so",
	"libggml-rpc.so", "libllama.so",
}

// writeIfChanged writes data to path unless the file already holds exactly
// that content. An upgraded gleann therefore replaces the llama-server it
// extracted earlier, instead of keeping the old one forever. The write goes
// to a temporary file that is renamed into place, so a llama-server that is
// still running from the old file is not affected ("text file busy").
func writeIfChanged(path string, data []byte) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
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
