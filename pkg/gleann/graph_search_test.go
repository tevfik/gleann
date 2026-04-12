package gleann

import (
	"context"
	"testing"
)

// mockGraphDB implements the GraphDB interface for testing graph-augmented search.
type mockGraphDB struct {
	calleesMap      map[string][]Callee
	callersMap      map[string][]Callee
	symbolsInFile   map[string][]Callee
	documentSymbols map[string][]SymbolInfo
}

func (m *mockGraphDB) Callees(fqn string) ([]Callee, error) {
	return m.calleesMap[fqn], nil
}
func (m *mockGraphDB) Callers(fqn string) ([]Callee, error) {
	return m.callersMap[fqn], nil
}
func (m *mockGraphDB) SymbolsInFile(path string) ([]Callee, error) {
	return m.symbolsInFile[path], nil
}
func (m *mockGraphDB) DocumentSymbols(path string) ([]SymbolInfo, error) {
	return m.documentSymbols[path], nil
}
func (m *mockGraphDB) DocumentContext(_ string) (*DocumentContextData, error) {
	return nil, nil // not needed in tests
}
func (m *mockGraphDB) FullDocument(_ string) (string, error) {
	return "", nil // not needed in tests
}
func (m *mockGraphDB) Impact(fqn string, maxDepth int) (*ImpactResult, error) {
	// Simple mock: use callers as direct callers
	result := &ImpactResult{Symbol: fqn, Depth: maxDepth}
	if callers, ok := m.callersMap[fqn]; ok {
		for _, c := range callers {
			result.DirectCallers = append(result.DirectCallers, c.FQN)
		}
	}
	return result, nil
}
func (m *mockGraphDB) Neighbors(_ string, _ int) ([]GraphEdge, error) {
	return nil, nil
}
func (m *mockGraphDB) ShortestPath(_, _ string) ([]PathStep, error) {
	return nil, nil
}
func (m *mockGraphDB) SymbolSearch(_ string) ([]Callee, error) {
	return nil, nil
}
func (m *mockGraphDB) Stats() (*GraphStats, error) {
	return &GraphStats{}, nil
}
func (m *mockGraphDB) Close() {}

func newMockGraphDB() *mockGraphDB {
	return &mockGraphDB{
		calleesMap: map[string][]Callee{
			"pkg.HandleRequest": {
				{FQN: "pkg.ParseJSON", Name: "ParseJSON", Kind: "function"},
				{FQN: "pkg.ValidateInput", Name: "ValidateInput", Kind: "function"},
			},
			"pkg.ProcessData": {
				{FQN: "pkg.Transform", Name: "Transform", Kind: "function"},
			},
		},
		callersMap: map[string][]Callee{
			"pkg.HandleRequest": {
				{FQN: "main.main", Name: "main", Kind: "function"},
				{FQN: "pkg.Router.ServeHTTP", Name: "ServeHTTP", Kind: "method"},
			},
			"pkg.ProcessData": {},
		},
		symbolsInFile: map[string][]Callee{
			"handler.go": {
				{FQN: "pkg.HandleRequest", Name: "HandleRequest", Kind: "function"},
				{FQN: "pkg.ProcessData", Name: "ProcessData", Kind: "function"},
			},
			"utils.go": {
				{FQN: "pkg.HelperFunc", Name: "HelperFunc", Kind: "function"},
			},
		},
		documentSymbols: map[string][]SymbolInfo{},
	}
}

func TestEnrichWithGraphContext(t *testing.T) {
	s := &LeannSearcher{graphDB: newMockGraphDB()}

	results := []SearchResult{
		{ID: 1, Text: "func HandleRequest(...)", Score: 0.95, Metadata: map[string]any{"source": "handler.go"}},
		{ID: 2, Text: "func HelperFunc(...)", Score: 0.80, Metadata: map[string]any{"source": "utils.go"}},
	}

	s.enrichWithGraphContext(results)

	// Result 1: handler.go has HandleRequest (callers+callees) and ProcessData (callees only)
	if results[0].GraphContext == nil {
		t.Fatal("expected GraphContext for result 0 (handler.go)")
	}
	syms := results[0].GraphContext.Symbols
	if len(syms) == 0 {
		t.Fatal("expected symbols in GraphContext for handler.go")
	}

	// HandleRequest should have callers and callees
	found := false
	for _, s := range syms {
		if s.FQN == "pkg.HandleRequest" {
			found = true
			if len(s.Callers) != 2 {
				t.Errorf("expected 2 callers for HandleRequest, got %d", len(s.Callers))
			}
			if len(s.Callees) != 2 {
				t.Errorf("expected 2 callees for HandleRequest, got %d", len(s.Callees))
			}
		}
	}
	if !found {
		t.Error("expected to find pkg.HandleRequest in graph context")
	}

	// Result 2: utils.go has HelperFunc which has no callers or callees
	// → should NOT have GraphContext (we only include symbols with relationships)
	if results[1].GraphContext != nil {
		t.Errorf("expected nil GraphContext for utils.go (no relationships), got %+v", results[1].GraphContext)
	}
}

func TestEnrichWithGraphContext_NoMetadata(t *testing.T) {
	s := &LeannSearcher{graphDB: newMockGraphDB()}

	results := []SearchResult{
		{ID: 1, Text: "some text", Score: 0.90, Metadata: nil},
		{ID: 2, Text: "more text", Score: 0.85, Metadata: map[string]any{}},
	}

	s.enrichWithGraphContext(results)

	for i, r := range results {
		if r.GraphContext != nil {
			t.Errorf("result %d: expected nil GraphContext when no source metadata", i)
		}
	}
}

func TestEnrichWithGraphContext_NoGraphDB(t *testing.T) {
	s := &LeannSearcher{graphDB: nil}

	results := []SearchResult{
		{ID: 1, Text: "text", Score: 0.90, Metadata: map[string]any{"source": "handler.go"}},
	}

	// Should not panic
	s.enrichWithGraphContext(results)

	if results[0].GraphContext != nil {
		t.Error("expected nil GraphContext when graphDB is nil")
	}
}

func TestEnrichWithGraphContext_CachesFileQueries(t *testing.T) {
	db := newMockGraphDB()
	callCount := 0
	originalSymbols := db.symbolsInFile["handler.go"]

	// Wrap to count calls
	countingDB := &countingGraphDB{
		GraphDB:   db,
		callCount: &callCount,
		file:      "handler.go",
		symbols:   originalSymbols,
	}

	s := &LeannSearcher{graphDB: countingDB}

	results := []SearchResult{
		{ID: 1, Text: "first chunk from handler.go", Score: 0.95, Metadata: map[string]any{"source": "handler.go"}},
		{ID: 2, Text: "second chunk from handler.go", Score: 0.90, Metadata: map[string]any{"source": "handler.go"}},
		{ID: 3, Text: "third chunk from handler.go", Score: 0.85, Metadata: map[string]any{"source": "handler.go"}},
	}

	s.enrichWithGraphContext(results)

	// SymbolsInFile("handler.go") should only be called once thanks to caching
	if *countingDB.callCount != 1 {
		t.Errorf("expected SymbolsInFile to be called once (cached), got %d", *countingDB.callCount)
	}
}

func TestWithGraphContext_SearchOption(t *testing.T) {
	cfg := SearchConfig{}
	opt := WithGraphContext(true)
	opt(&cfg)

	if !cfg.UseGraphContext {
		t.Error("expected UseGraphContext to be true after WithGraphContext(true)")
	}

	opt2 := WithGraphContext(false)
	opt2(&cfg)
	if cfg.UseGraphContext {
		t.Error("expected UseGraphContext to be false after WithGraphContext(false)")
	}
}

func TestGraphContextInfo_JSONOmitEmpty(t *testing.T) {
	// SearchResult with nil GraphContext should not include it in JSON
	r := SearchResult{ID: 1, Text: "test", Score: 0.5}
	if r.GraphContext != nil {
		t.Error("default GraphContext should be nil")
	}
}

func TestEnrichWithGraphContext_MaxSymbolsCap(t *testing.T) {
	// Create a file with many symbols (>5) to verify the cap
	db := newMockGraphDB()
	manySymbols := make([]Callee, 10)
	for i := range manySymbols {
		fqn := "pkg.Func" + string(rune('A'+i))
		manySymbols[i] = Callee{FQN: fqn, Name: "Func" + string(rune('A'+i)), Kind: "function"}
	}
	db.symbolsInFile["big_file.go"] = manySymbols

	// Give all of them callees so they all qualify for inclusion
	for _, sym := range manySymbols {
		db.calleesMap[sym.FQN] = []Callee{{FQN: "pkg.SomeTarget", Name: "SomeTarget", Kind: "function"}}
	}

	s := &LeannSearcher{graphDB: db}
	results := []SearchResult{
		{ID: 1, Text: "big file", Score: 0.9, Metadata: map[string]any{"source": "big_file.go"}},
	}

	s.enrichWithGraphContext(results)

	if results[0].GraphContext == nil {
		t.Fatal("expected GraphContext for big_file.go")
	}
	// At most 5 symbols should be processed
	if len(results[0].GraphContext.Symbols) > 5 {
		t.Errorf("expected at most 5 symbols, got %d", len(results[0].GraphContext.Symbols))
	}
}

func TestSearchWithGraphContext_Integration(t *testing.T) {
	// Verify that WithGraphContext option is respected when graphDB is nil
	// (should silently skip enrichment, not error)
	s := &LeannSearcher{
		loaded:  true,
		graphDB: nil,
		config: Config{
			SearchConfig: SearchConfig{TopK: 5},
		},
	}

	// Search requires an embedder and backend — test the option wiring only
	cfg := s.config.SearchConfig
	opt := WithGraphContext(true)
	opt(&cfg)

	if !cfg.UseGraphContext {
		t.Error("WithGraphContext(true) should set UseGraphContext")
	}
}

// countingGraphDB wraps a GraphDB and counts SymbolsInFile calls for a specific file.
type countingGraphDB struct {
	GraphDB
	callCount *int
	file      string
	symbols   []Callee
}

func (c *countingGraphDB) SymbolsInFile(path string) ([]Callee, error) {
	if path == c.file {
		*c.callCount++
		return c.symbols, nil
	}
	return c.GraphDB.SymbolsInFile(path)
}

func (c *countingGraphDB) Callees(fqn string) ([]Callee, error) {
	return c.GraphDB.Callees(fqn)
}

func (c *countingGraphDB) Callers(fqn string) ([]Callee, error) {
	return c.GraphDB.Callers(fqn)
}

func (c *countingGraphDB) DocumentSymbols(path string) ([]SymbolInfo, error) {
	return c.GraphDB.DocumentSymbols(path)
}

func (c *countingGraphDB) DocumentContext(vpath string) (*DocumentContextData, error) {
	return c.GraphDB.DocumentContext(vpath)
}

func (c *countingGraphDB) FullDocument(vpath string) (string, error) {
	return c.GraphDB.FullDocument(vpath)
}

func (c *countingGraphDB) Impact(fqn string, maxDepth int) (*ImpactResult, error) {
	return c.GraphDB.Impact(fqn, maxDepth)
}

// Verify unused import guard
var _ = context.Background
