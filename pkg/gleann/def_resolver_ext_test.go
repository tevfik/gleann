package gleann

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// ── Go-to-definition: passage fallback ────────────────────────────────────

func TestScanPassageForSymbol_IdentifierBoundaries(t *testing.T) {
	passage := "func GetUserByID(id int) *User {\n}\n\nfunc Get(key string) string {\n\treturn key\n}\n"
	def := scanPassageForSymbol(passage, "Get", "store.go")
	if def == nil {
		t.Fatal("expected Get to be found")
	}
	// Regression: substring matching returned GetUserByID (line 1) for "Get".
	if def.Line != 4 || !strings.HasPrefix(def.Signature, "func Get(key string)") {
		t.Fatalf("matched wrong declaration: line=%d sig=%q", def.Line, def.Signature)
	}
	if scanPassageForSymbol("func GetUserByID() {}", "User", "x.go") != nil {
		t.Fatal("partial identifier must not match")
	}
}

func TestScanPassageForSymbol_Kinds(t *testing.T) {
	tests := []struct {
		name, passage, query, kind string
	}{
		{"go func", "func Run() {}", "Run", "function"},
		{"go method", "func (s *Server) Run() error {", "Run", "method"},
		{"go method case-insensitive query", "func (s *Server) Run() error {", "run", "method"},
		{"go struct", "type Server struct {", "Server", "struct"},
		{"go interface", "type Runner interface {", "Runner", "interface"},
		{"go alias", "type ID = string", "ID", "type"},
		{"python def", "def handle_request(req):", "handle_request", "function"},
		{"python class", "class Handler(Base):", "Handler", "class"},
		{"indented python method", "    def close(self):", "close", "function"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := scanPassageForSymbol(tt.passage, tt.query, "f")
			if def == nil {
				t.Fatalf("no definition found")
			}
			if def.Kind != tt.kind {
				t.Fatalf("kind = %q, want %q", def.Kind, tt.kind)
			}
		})
	}
}

func TestIndexIdent(t *testing.T) {
	tests := []struct {
		s, id string
		want  int
	}{
		{"func get()", "get", 5},
		{"func getall() get", "get", 14},
		{"func _get()", "get", -1},
		{"get", "get", 0},
		{"", "get", -1},
		{"get", "", -1},
		{"func héget()", "get", -1},
	}
	for _, tt := range tests {
		if got := indexIdent(tt.s, tt.id); got != tt.want {
			t.Errorf("indexIdent(%q,%q) = %d, want %d", tt.s, tt.id, got, tt.want)
		}
	}
}

func TestExtractSignature_EdgeCases(t *testing.T) {
	tests := []struct {
		name, content string
		line          int
		sig, doc      string
	}{
		{"empty", "", 1, "", ""},
		{"line zero", "func A() {}", 0, "", ""},
		{"past end", "func A() {}", 5, "", ""},
		{"crlf", "// Doc line\r\nfunc A() {\r\n}\r\n", 2, "func A() {", "Doc line"},
		{"multi-line signature", "func A(\n\ta int,\n\tb int,\n) error {\n", 1, "func A(\n\ta int,\n\tb int,\n) error {", ""},
		{"blank line breaks doc", "// unrelated\n\nfunc A() {", 3, "func A() {", ""},
		{"python colon", "# Handles it.\ndef f(x):\n    pass", 2, "def f(x):", "Handles it."},
		{"caps at six lines", "a\nb\nc\nd\ne\nf\ng\nh", 1, "a\nb\nc\nd\ne\nf", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sig, doc := ExtractSignature(tt.content, tt.line)
			if sig != tt.sig || doc != tt.doc {
				t.Fatalf("got (%q, %q), want (%q, %q)", sig, doc, tt.sig, tt.doc)
			}
		})
	}
}

func TestFindDefinitions_FallbackUsesFileLineAndDedups(t *testing.T) {
	dir := t.TempDir()
	builder, embedder := newTestBuilder(t, dir, 8)
	chunk := "// Validate checks a token.\nfunc Validate(tok string) bool {\n\treturn tok != \"\"\n}\n"
	items := []Item{
		// The chunk starts at line 41 of the file.
		{Text: chunk, Metadata: map[string]any{"source": "auth/token.go", "start_line": 41}},
		// An overlapping chunk containing the same definition must not
		// produce a duplicate result.
		{Text: "package auth\n\n" + chunk, Metadata: map[string]any{"source": "auth/token.go", "start_line": 39}},
		{Text: "unrelated validate prose without declarations", Metadata: map[string]any{"source": "README.md"}},
	}
	if err := builder.BuildLexicalOnly("defidx", items); err != nil {
		t.Fatal(err)
	}
	initTestHNSW()
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	ctx := context.Background()
	if err := s.Load(ctx, "defidx"); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	defs, err := FindDefinitions(ctx, s, "Validate", DefinitionOptions{TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 {
		t.Fatalf("expected exactly 1 deduplicated definition, got %d: %+v", len(defs), defs)
	}
	// Regression: the line was passage-relative (2) instead of file-relative.
	if defs[0].File != "auth/token.go" || defs[0].Line != 42 {
		t.Fatalf("definition location = %s:%d, want auth/token.go:42", defs[0].File, defs[0].Line)
	}
	if defs[0].Doc != "Validate checks a token." {
		t.Fatalf("doc = %q", defs[0].Doc)
	}

	if _, err := FindDefinitions(ctx, s, "   ", DefinitionOptions{}); err == nil {
		t.Fatal("expected error for blank query")
	}
	none, err := FindDefinitions(ctx, s, "Validate", DefinitionOptions{Kind: "struct"})
	if err != nil || len(none) != 0 {
		t.Fatalf("kind filter should exclude function: %+v %v", none, err)
	}
}

func TestMetaInt(t *testing.T) {
	for _, v := range []any{int(7), int32(7), int64(7), float64(7), float32(7)} {
		if metaInt(v) != 7 {
			t.Errorf("metaInt(%T) != 7", v)
		}
	}
	if metaInt("7") != 0 || metaInt(nil) != 0 {
		t.Error("non-numeric metadata must yield 0")
	}
}

// ── Dual-embedding: dynamic embedder resolution ───────────────────────────

type namedEmbedder struct {
	testEmbedder
	name string
}

func (e *namedEmbedder) ModelName() string { return e.name }

type recordingResolver struct {
	mu    sync.Mutex
	calls []string
	dims  []int
	ret   EmbeddingComputer
	err   error
}

func (r *recordingResolver) ResolveEmbedder(_ context.Context, model string, dims int) (EmbeddingComputer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, model)
	r.dims = append(r.dims, dims)
	return r.ret, r.err
}

func buildVectorIndex(t *testing.T, dir, name string, emb EmbeddingComputer) {
	t.Helper()
	b, _ := newTestBuilder(t, dir, 8)
	b.embedder = emb
	if err := b.Build(context.Background(), name, []Item{
		{Text: "func main() {}", Metadata: map[string]any{"source": "main.go"}},
		{Text: "installation guide", Metadata: map[string]any{"source": "README.md"}},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSearcherLoad_ResolvesIndexSpecificEmbedder(t *testing.T) {
	initTestHNSW()
	dir := t.TempDir()
	codeEmb := &namedEmbedder{testEmbedder{dim: 8}, "code-model"}
	buildVectorIndex(t, dir, "repo-code", codeEmb)

	// The default (docs) embedder has a different model and dimensionality.
	defaultEmb := &namedEmbedder{testEmbedder{dim: 16}, "doc-model"}

	t.Run("resolver supplies matching model", func(t *testing.T) {
		res := &recordingResolver{ret: &namedEmbedder{testEmbedder{dim: 8}, "code-model"}}
		s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw", EmbedderResolver: res}, defaultEmb)
		if err := s.Load(context.Background(), "repo-code"); err != nil {
			t.Fatalf("Load with resolver: %v", err)
		}
		defer s.Close()
		if len(res.calls) != 1 || res.calls[0] != "code-model" || res.dims[0] != 8 {
			t.Fatalf("resolver calls = %v dims=%v", res.calls, res.dims)
		}
		if _, err := s.Search(context.Background(), "main", WithTopK(2)); err != nil {
			t.Fatalf("search after resolution: %v", err)
		}
	})

	t.Run("no resolve when default already matches", func(t *testing.T) {
		res := &recordingResolver{ret: defaultEmb}
		s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw", EmbedderResolver: res}, &namedEmbedder{testEmbedder{dim: 8}, "code-model"})
		if err := s.Load(context.Background(), "repo-code"); err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if len(res.calls) != 0 {
			t.Fatalf("resolver should not be consulted, calls=%v", res.calls)
		}
	})

	t.Run("resolver error falls back and dimension check rejects", func(t *testing.T) {
		res := &recordingResolver{err: errors.New("model not installed")}
		s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw", EmbedderResolver: res}, defaultEmb)
		err := s.Load(context.Background(), "repo-code")
		if err == nil || !strings.Contains(err.Error(), "dimension mismatch") {
			t.Fatalf("expected dimension mismatch error, got %v", err)
		}
	})

	t.Run("resolver returning nil is ignored", func(t *testing.T) {
		res := &recordingResolver{}
		s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw", EmbedderResolver: res}, defaultEmb)
		if err := s.Load(context.Background(), "repo-code"); err == nil {
			t.Fatal("expected dimension mismatch when resolver yields nil")
		}
	})

	t.Run("nil default embedder gets resolved", func(t *testing.T) {
		res := &recordingResolver{ret: &namedEmbedder{testEmbedder{dim: 8}, "code-model"}}
		s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw", EmbedderResolver: res}, nil)
		if err := s.Load(context.Background(), "repo-code"); err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if len(res.calls) != 1 {
			t.Fatalf("expected resolver call for nil embedder, got %v", res.calls)
		}
	})
}

func TestEmbedderResolverFunc_Adapter(t *testing.T) {
	var gotModel string
	var gotDims int
	f := EmbedderResolverFunc(func(_ context.Context, m string, d int) (EmbeddingComputer, error) {
		gotModel, gotDims = m, d
		return nil, nil
	})
	if _, err := f.ResolveEmbedder(context.Background(), "bge", 384); err != nil {
		t.Fatal(err)
	}
	if gotModel != "bge" || gotDims != 384 {
		t.Fatalf("adapter passed (%q,%d)", gotModel, gotDims)
	}
}
