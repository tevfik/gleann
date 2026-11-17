package llamacpp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeLlamaEnv selects the behaviour of the test binary when it is re-executed
// as a stand-in for llama-server (the os.Args[0] helper-process pattern).
const fakeLlamaEnv = "GLEANN_FAKE_LLAMA_SERVER"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeLlamaEnv); mode != "" {
		runFakeLlama(mode)
		return
	}
	os.Exit(m.Run())
}

// runFakeLlama imitates the parts of llama-server that Runner depends on.
//
//	serve:  answers /health 200 and echoes its argv and LD_LIBRARY_PATH on /args
//	exit:   dies immediately with status 3 (bad flag / missing library)
//	hang:   never opens its port
//	ignore: like serve, but ignores SIGTERM so only a kill stops it
func runFakeLlama(mode string) {
	switch mode {
	case "exit":
		os.Exit(3)
	case "hang":
		select {}
	}
	port := ""
	for i, a := range os.Args {
		if a == "--port" && i+1 < len(os.Args) {
			port = os.Args[i+1]
		}
	}
	if mode == "ignore" {
		signal.Ignore(syscall.SIGTERM)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/args", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s\nLD=%s", strings.Join(os.Args[1:], " "), os.Getenv("LD_LIBRARY_PATH"))
	})
	if err := http.ListenAndServe("127.0.0.1:"+port, mux); err != nil {
		fmt.Fprintln(os.Stderr, "fake llama:", err)
		os.Exit(2)
	}
}

// newFakeRunner returns a Runner whose "extracted binary" is this test binary.
func newFakeRunner(t *testing.T, mode string) *Runner {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake server relies on POSIX signals")
	}
	t.Setenv(fakeLlamaEnv, mode)
	model := filepath.Join(t.TempDir(), "m.gguf")
	if err := os.WriteFile(model, []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(model)
	r.extract = func(string) (string, error) { return os.Args[0], nil }
	t.Cleanup(func() { _ = r.Stop() })
	return r
}

func httpBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestStartLaunchesServerWithArguments(t *testing.T) {
	r := newFakeRunner(t, "serve")
	r.SetEmbedding(true)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	body := httpBody(t, r.BaseURL()+"/args")
	for _, want := range []string{
		"--model " + r.modelPath,
		"--host 127.0.0.1",
		fmt.Sprintf("--port %d", r.port),
		"--log-disable",
		"--embedding",
		"LD=" + filepath.Dir(os.Args[0]),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("server args missing %q in:\n%s", want, body)
		}
	}

	if err := r.Stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
	if _, err := http.Get(r.BaseURL() + "/health"); err == nil {
		t.Error("server still answering after Stop")
	}
	// Stop after the process is gone is a no-op.
	if err := r.Stop(); err != nil {
		t.Errorf("second Stop: %v", err)
	}
}

func TestStartOmitsEmbeddingFlagByDefault(t *testing.T) {
	r := newFakeRunner(t, "serve")
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if body := httpBody(t, r.BaseURL()+"/args"); strings.Contains(body, "--embedding") {
		t.Errorf("--embedding passed without SetEmbedding: %s", body)
	}
}

func TestStartReportsServerThatDiesAtStartup(t *testing.T) {
	r := newFakeRunner(t, "exit")
	start := time.Now()
	err := r.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exited before it was ready") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "exit status 3") {
		t.Errorf("exit status not surfaced: %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("took %v: Start waited for the timeout", time.Since(start))
	}
}

func TestStartStopsServerWhenContextEnds(t *testing.T) {
	r := newFakeRunner(t, "hang")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := r.Start(ctx)
	if err == nil {
		t.Fatal("Start succeeded for a server that never listens")
	}
	// The context ends the child (CommandContext) and the ready loop; either
	// observation may win the race, both are correct.
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "exited before") {
		t.Errorf("err = %v", err)
	}
	select {
	case <-r.exited:
	case <-time.After(5 * time.Second):
		t.Error("server process still running after Start gave up")
	}
}

func TestStartExtractFailureIsWrapped(t *testing.T) {
	r := NewRunner(writeModel(t))
	boom := errors.New("no space left")
	r.extract = func(string) (string, error) { return "", boom }
	err := r.Start(context.Background())
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "failed to extract embedded runner") {
		t.Fatalf("err = %v", err)
	}
}

func TestStartBinaryCannotBeExecuted(t *testing.T) {
	r := NewRunner(writeModel(t))
	r.extract = func(string) (string, error) { return filepath.Join(t.TempDir(), "absent"), nil }
	err := r.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed to start llama-server") {
		t.Fatalf("err = %v", err)
	}
	if err := r.Stop(); err != nil {
		t.Errorf("Stop after failed start: %v", err)
	}
}

func TestStartMissingModelNamesTheFile(t *testing.T) {
	t.Setenv("GLEANN_CONFIG_DIR", t.TempDir())
	r := NewRunner("nope.gguf")
	r.extract = func(string) (string, error) {
		t.Error("extraction must not run for a missing model")
		return "", nil
	}
	err := r.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), `model file "nope.gguf" not found`) {
		t.Fatalf("err = %v", err)
	}
}

func TestStopKillsServerThatIgnoresSIGTERM(t *testing.T) {
	r := newFakeRunner(t, "ignore")
	old := stopGrace
	stopGrace = 200 * time.Millisecond
	t.Cleanup(func() { stopGrace = old })

	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case <-r.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("server survived Stop")
	}
	var ee interface{ ExitCode() int }
	if !errors.As(r.waitErr, &ee) || ee.ExitCode() != -1 {
		t.Errorf("expected a signal kill, waitErr = %v", r.waitErr)
	}
}

func TestSetEmbedding(t *testing.T) {
	r := NewRunner("/m")
	if r.embedding {
		t.Fatal("embedding must default to off")
	}
	r.SetEmbedding(true)
	if !r.embedding {
		t.Error("SetEmbedding(true) not stored")
	}
	r.SetEmbedding(false)
	if r.embedding {
		t.Error("SetEmbedding(false) not stored")
	}
}

func writeModel(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "m.gguf")
	if err := os.WriteFile(p, []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
