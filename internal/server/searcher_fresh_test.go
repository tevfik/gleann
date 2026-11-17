package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
	"github.com/tevfik/gleann/pkg/gleann"
)

// writeGraphRows plays another process writing the code graph into the
// index database through its own handle.
func writeGraphRows(t *testing.T, dir, name string) {
	t.Helper()
	st, err := store.Open(filepath.Join(dir, name, name+".db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	err = graphstore.New(st).ApplyFileDelta(context.Background(), graphstore.FileDelta{
		File: "a.go", GraphHash: "h1",
		Symbols: []graphstore.Symbol{
			{FQN: "m.A", Kind: "function", Name: "A", StartLine: 1, EndLine: 3},
			{FQN: "m.B", Kind: "function", Name: "B", StartLine: 5, EndLine: 7},
		},
		Edges: []graphstore.Edge{{Src: "m.A", Dst: "m.B", Kind: "CALLS"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

// newLenOllama is a fake Ollama whose vectors depend only on the text length,
// so passages of distinct lengths have distinct vectors and a query equal to
// a passage text retrieves exactly that passage.
func newLenOllama(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input any `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var texts []string
		switch v := req.Input.(type) {
		case []any:
			for _, x := range v {
				str, _ := x.(string)
				texts = append(texts, str)
			}
		case string:
			texts = []string{v}
		}
		embs := make([][]float32, len(texts))
		for i, txt := range texts {
			e := make([]float32, 8)
			for j := range e {
				e[j] = float32(len(txt)+j) * 0.01
			}
			embs[i] = e
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": embs})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newFreshServer builds an index "idx" (format 2) and returns a server with a
// cached-searcher cache over it, the config and the embedder a second builder
// (the "CLI") uses.
func newFreshServer(t *testing.T) (*Server, http.Handler, gleann.Config, gleann.EmbeddingComputer) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ollama := newLenOllama(t)
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = t.TempDir()
	cfg.Backend = "hnsw"
	cfg.EmbeddingProvider = "ollama"
	cfg.EmbeddingModel = fmt.Sprintf("len-model-%d", time.Now().UnixNano())
	cfg.OllamaHost = ollama.URL
	emb := embedding.NewComputer(embedding.Options{Provider: embedding.ProviderOllama, Model: cfg.EmbeddingModel, BaseURL: ollama.URL})
	b, err := gleann.NewBuilder(cfg, emb)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Build(context.Background(), "idx", freshItems()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	s := &Server{config: cfg, embedder: emb, searchers: make(map[string]*gleann.LeannSearcher)}
	// Before the TempDirs go: Windows cannot delete files a cached searcher holds open.
	t.Cleanup(s.invalidateAllSearchers)
	return s, s.buildMux(), cfg, emb
}

func freshItems() []gleann.Item {
	return []gleann.Item{
		{Text: "apple pie recipe", Metadata: map[string]any{"source": "a.md", "chunk_index": 0}},
		{Text: "banana bread", Metadata: map[string]any{"source": "b.md", "chunk_index": 0}},
		{Text: "cherry tart dessert", Metadata: map[string]any{"source": "c.md", "chunk_index": 0}},
	}
}

func searchTop(t *testing.T, h http.Handler, name, query string) []gleann.SearchResult {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"query": query, "top_k": 1, "hybrid_alpha": 1.0})
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/"+name+"/search", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("search %q: %d %s", query, w.Code, w.Body.String())
	}
	var resp searchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Results
}

// A running server keeps a cached searcher. Another process (here a second
// builder over the same directory, as a CLI `gleann index sync` would be)
// removes a file: the vector slots of the remaining passages are renumbered
// and the vector file is replaced. The next search through the server must
// return the right text, not resolve new slots against the old vector index.
func TestSearcherCache_SeesOtherProcessSync(t *testing.T) {
	s, h, cfg, emb := newFreshServer(t)
	ctx := context.Background()

	if r := searchTop(t, h, "idx", "cherry tart dessert"); len(r) != 1 || r[0].Text != "cherry tart dessert" {
		t.Fatalf("first search = %+v", r)
	}
	if s.cachedSearcherCount() != 1 {
		t.Fatalf("searcher not cached: %d", s.cachedSearcherCount())
	}

	// The "CLI": a different builder (different handles), same index.
	b2, err := gleann.NewBuilder(cfg, emb)
	if err != nil {
		t.Fatal(err)
	}
	if err := b2.UpdateIndex(ctx, "idx", []gleann.Item{
		{Text: "date syrup cake and more", Metadata: map[string]any{"source": "d.md", "chunk_index": 0}},
	}, []string{"a.md"}); err != nil {
		t.Fatalf("UpdateIndex: %v", err)
	}

	for _, want := range []string{"cherry tart dessert", "banana bread", "date syrup cake and more"} {
		r := searchTop(t, h, "idx", want)
		if len(r) != 1 || r[0].Text != want {
			t.Errorf("search %q after the other process's sync = %+v; stale or mismatched passage", want, r)
		}
	}
	// The removed file's content is gone.
	if r := searchTop(t, h, "idx", "apple pie recipe"); len(r) == 1 && r[0].Text == "apple pie recipe" {
		t.Errorf("removed passage still served: %+v", r)
	}
}

// The same freshness applies to a graph that appears with no vector change:
// a searcher loaded before the graph was built attaches it on the next acquire.
func TestSearcherCache_SeesGraphBuiltLater(t *testing.T) {
	s, _, cfg, _ := newFreshServer(t)

	sr, release, err := s.acquireSearcher(context.Background(), "idx")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if sr.GraphDB() != nil {
		t.Fatal("no graph yet")
	}
	writeGraphRows(t, cfg.IndexDir, "idx")

	sr2, release2, err := s.acquireSearcher(context.Background(), "idx")
	if err != nil {
		t.Fatal(err)
	}
	defer release2()
	if sr2 == sr {
		t.Fatal("searcher not reloaded after the graph was written by another handle")
	}
	if sr2.GraphDB() == nil {
		t.Fatal("graph not attached after reload")
	}
}
