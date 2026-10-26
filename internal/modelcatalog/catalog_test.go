package modelcatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestModelsJSONIsComplete(t *testing.T) {
	ids := map[string]bool{}
	for _, e := range All() {
		if e.ID == "" || e.Name == "" || e.File == "" || e.Bytes <= 0 || len(e.SHA256) != 64 ||
			!strings.HasPrefix(e.URL, "https://") || !strings.HasSuffix(e.URL, "/"+e.File) {
			t.Errorf("incomplete entry: %+v", e)
		}
		if e.Kind != Embedding && e.Kind != LLM && e.Kind != Reranker {
			t.Errorf("%s: unknown kind %q", e.ID, e.Kind)
		}
		if ids[e.ID] {
			t.Errorf("duplicate id %s", e.ID)
		}
		ids[e.ID] = true
	}
	for _, k := range []string{Embedding, LLM, Reranker} {
		if len(OfKind(k)) == 0 {
			t.Errorf("no %s models", k)
		}
	}
	if e, ok := Find("/somewhere/BGE-M3-Q4_K_M.gguf"); !ok || e.ID != "bge-m3" {
		t.Errorf("Find by path: %+v %v", e, ok)
	}
}

func TestDownloadChecksSizeAndChecksum(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	body := []byte("pretend this is a gguf model")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	sum := sha256.Sum256(body)
	e := Entry{ID: "t", Name: "test", File: "t.gguf", URL: srv.URL, Bytes: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}

	var last int64
	if err := Download(context.Background(), e, func(done, _ int64) { last = done }); err != nil {
		t.Fatal(err)
	}
	if !e.Installed() || last != int64(len(body)) {
		t.Errorf("installed=%v last progress=%d", e.Installed(), last)
	}

	bad := e
	bad.File, bad.SHA256 = "bad.gguf", strings.Repeat("0", 64)
	if err := Download(context.Background(), bad, nil); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Errorf("checksum mismatch not caught: %v", err)
	}
	if _, err := os.Stat(bad.Path()); err == nil {
		t.Error("a corrupt download was kept under the real name")
	}
	if _, err := os.Stat(bad.Path() + ".part"); err == nil {
		t.Error("the .part file was left behind")
	}
}
