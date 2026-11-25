package llamacpp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/modelcatalog"
)

func resolveEnv(t *testing.T) (modelcatalog.Entry, ModelOptions, *int) {
	t.Helper()
	t.Setenv("GLEANN_CONFIG_DIR", t.TempDir())
	e := modelcatalog.Entry{ID: "tiny", Name: "Tiny", File: "tiny.gguf", Bytes: 4}
	calls := 0
	return e, ModelOptions{
		Lookup: func(n string) (modelcatalog.Entry, bool) { return e, n == "tiny" || n == "tiny.gguf" },
		Download: func(_ context.Context, en modelcatalog.Entry, _ func(int64, int64)) error {
			calls++
			_ = os.MkdirAll(filepath.Dir(en.Path()), 0o755)
			return os.WriteFile(en.Path(), []byte("abcd"), 0o644)
		},
	}, &calls
}

func TestResolveModel_OptInDownloadsOnceThenResolves(t *testing.T) {
	e, o, calls := resolveEnv(t)
	if _, err := ResolveModel(context.Background(), "tiny:latest", o); err == nil || *calls != 0 {
		t.Fatalf("without the flag: err=%v downloads=%d, want an error and no download", err, *calls)
	}
	o.AutoDownload = true
	for i := 0; i < 2; i++ {
		got, err := ResolveModel(context.Background(), "tiny:latest", o)
		if err != nil || got != e.Path() {
			t.Fatalf("got %q, %v; want %q", got, err, e.Path())
		}
	}
	if *calls != 1 {
		t.Errorf("downloads = %d, want 1", *calls)
	}
}

func TestResolveModel_PathsUnknownAndEmpty(t *testing.T) {
	_, o, calls := resolveEnv(t)
	o.AutoDownload = true
	f := filepath.Join(t.TempDir(), "x.gguf")
	_ = os.WriteFile(f, []byte("z"), 0o644)
	if got, err := ResolveModel(context.Background(), f, o); err != nil || got != f {
		t.Errorf("path: %q %v", got, err)
	}
	if got, err := ResolveModel(context.Background(), "absent.gguf", o); err != nil || got != "absent.gguf" {
		t.Errorf("missing .gguf reference passes through: %q %v", got, err)
	}
	if _, err := ResolveModel(context.Background(), "nope", o); err == nil || !strings.Contains(err.Error(), "catalogue id") {
		t.Errorf("unknown: %v", err)
	}
	if _, err := ResolveModel(context.Background(), "", o); err == nil {
		t.Error("empty must fail")
	}
	if *calls != 0 {
		t.Errorf("downloads = %d, want 0", *calls)
	}
}

func TestResolveModel_RejectsEmbeddingGemma(t *testing.T) {
	_, o, calls := resolveEnv(t)
	o.AutoDownload = true
	for _, name := range []string{"embeddinggemma-2", "/m/embeddinggemma-2-Q8_0.gguf", "EmbeddingGemma-2-Q8_0.gguf"} {
		_, err := ResolveModel(context.Background(), name, o)
		if err == nil || !strings.Contains(err.Error(), `needs embedding_provider "eif"`) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if *calls != 0 {
		t.Errorf("downloads = %d, want 0", *calls)
	}
}

func TestIsEmbeddingGemma2(t *testing.T) {
	cases := map[string]bool{
		"embeddinggemma-2":                     true,
		"embeddinggemma-2:740m":                true,
		"/m/embeddinggemma-2-Q8_0.gguf":        true,
		"EmbeddingGemma-2-Q8_0.gguf":           true,
		"embeddinggemma-300M-Q8_0.gguf":        false,
		"/m/embeddinggemma-300M-Q8_0.gguf":     false,
		"embeddinggemma-300m":                  false,
		"embeddinggemma:300m":                  false,
		"embeddinggemma":                       false,
		"/x/embeddinggemma-2/other-model.gguf": false,
		"bge-m3":                               false,
	}
	for name, want := range cases {
		if got := isEmbeddingGemma2(name); got != want {
			t.Errorf("isEmbeddingGemma2(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestResolveModel_AllowsEmbeddingGemmaV1(t *testing.T) {
	_, o, _ := resolveEnv(t)
	f := filepath.Join(t.TempDir(), "embeddinggemma-300M-Q8_0.gguf")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveModel(context.Background(), f, o)
	if err != nil || got != f {
		t.Fatalf("got %q, %v", got, err)
	}
}
