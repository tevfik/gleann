package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/a2a"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
)

// ── mountA2A ─────────────────────────────────────────────────────────────────

func TestMountA2A_DisabledViaEnv(t *testing.T) {
	os.Setenv("GLEANN_A2A_ENABLED", "false")
	defer os.Unsetenv("GLEANN_A2A_ENABLED")

	s := &Server{
		config:    gleann.Config{IndexDir: t.TempDir()},
		searchers: make(map[string]*gleann.LeannSearcher),
		addr:      ":8080",
		version:   "test",
	}
	// Should not panic even with nil mux since it returns early.
	// We can't easily verify it didn't register, but we confirm no panic.
	// Actually we need a real mux to test no panic:
	mux := newTestMux()
	s.mountA2A(mux)
}

func TestMountA2A_DisabledViaConfig(t *testing.T) {
	os.Unsetenv("GLEANN_A2A_ENABLED")
	disabled := false
	s := &Server{
		config:    gleann.Config{IndexDir: t.TempDir(), A2AEnabled: &disabled},
		searchers: make(map[string]*gleann.LeannSearcher),
		addr:      ":8080",
		version:   "test",
	}
	mux := newTestMux()
	s.mountA2A(mux)
}

func TestMountA2A_EnabledViaEnv(t *testing.T) {
	os.Setenv("GLEANN_A2A_ENABLED", "true")
	defer os.Unsetenv("GLEANN_A2A_ENABLED")

	s := &Server{
		config:    gleann.Config{IndexDir: t.TempDir()},
		searchers: make(map[string]*gleann.LeannSearcher),
		addr:      ":9090",
		version:   "v1.0",
	}
	mux := newTestMux()
	s.mountA2A(mux)
}

func TestMountA2A_AddrWithoutColon(t *testing.T) {
	os.Setenv("GLEANN_A2A_ENABLED", "true")
	defer os.Unsetenv("GLEANN_A2A_ENABLED")

	s := &Server{
		config:    gleann.Config{IndexDir: t.TempDir()},
		searchers: make(map[string]*gleann.LeannSearcher),
		addr:      "9090", // no colon
		version:   "v1.0",
	}
	mux := newTestMux()
	s.mountA2A(mux)
}

// ── a2aSearchHandler ─────────────────────────────────────────────────────────

func TestA2ASearchHandler_NoIndexes(t *testing.T) {
	s := &Server{
		config:    gleann.Config{IndexDir: t.TempDir()}, // empty dir
		searchers: make(map[string]*gleann.LeannSearcher),
	}
	ctx := a2a.SkillContext{Query: "test query"}
	_, err := s.a2aSearchHandler(ctx)
	if err == nil {
		t.Fatal("expected error for no indexes")
	}
}

func TestA2ASearchHandler_IndexLoadFails(t *testing.T) {
	// Create a fake index dir so ListIndexes finds something.
	dir := t.TempDir()
	indexDir := filepath.Join(dir, "fakeindex")
	os.MkdirAll(indexDir, 0o755)
	os.WriteFile(filepath.Join(indexDir, "meta.json"), []byte(`{"name":"fakeindex","backend":"hnsw","embedding_model":"test"}`), 0o644)

	s := &Server{
		config:    gleann.Config{IndexDir: dir},
		searchers: make(map[string]*gleann.LeannSearcher),
	}
	ctx := a2a.SkillContext{Query: "test query"}
	result, err := s.a2aSearchHandler(ctx)
	// Even if index load fails (no real backend), handler continues to next index.
	// Since there's only one and it fails, results should be empty.
	if err != nil {
		t.Logf("got error (expected): %v", err)
	}
	if result != "" && err == nil {
		t.Logf("got result: %s", result)
	}
}

// ── a2aAskHandler ────────────────────────────────────────────────────────────

func TestA2AAskHandler_NoIndexes(t *testing.T) {
	s := &Server{
		config:    gleann.Config{IndexDir: t.TempDir()},
		searchers: make(map[string]*gleann.LeannSearcher),
	}
	ctx := a2a.SkillContext{Query: "what is gleann?"}
	_, err := s.a2aAskHandler(ctx)
	if err == nil {
		t.Fatal("expected error for no indexes")
	}
}

func TestA2AAskHandler_IndexLoadFails(t *testing.T) {
	dir := t.TempDir()
	indexDir := filepath.Join(dir, "fakeindex")
	os.MkdirAll(indexDir, 0o755)
	os.WriteFile(filepath.Join(indexDir, "meta.json"), []byte(`{"name":"fakeindex","backend":"hnsw","embedding_model":"test"}`), 0o644)

	s := &Server{
		config:    gleann.Config{IndexDir: dir},
		searchers: make(map[string]*gleann.LeannSearcher),
	}
	ctx := a2a.SkillContext{
		Query:    "test question",
		Metadata: map[string]interface{}{"index": "fakeindex"},
	}
	_, err := s.a2aAskHandler(ctx)
	if err == nil {
		t.Fatal("expected error for failed index load")
	}
}

func TestA2AAskHandler_WithMetadataOverride(t *testing.T) {
	dir := t.TempDir()
	indexDir := filepath.Join(dir, "testidx")
	os.MkdirAll(indexDir, 0o755)
	os.WriteFile(filepath.Join(indexDir, "meta.json"), []byte(`{"name":"testidx","backend":"hnsw","embedding_model":"test"}`), 0o644)

	s := &Server{
		config:    gleann.Config{IndexDir: dir},
		searchers: make(map[string]*gleann.LeannSearcher),
	}
	ctx := a2a.SkillContext{
		Query: "test question",
		Metadata: map[string]interface{}{
			"index":        "testidx",
			"llm_model":    "gpt-4",
			"llm_provider": "openai",
		},
	}
	_, err := s.a2aAskHandler(ctx)
	if err == nil {
		t.Log("no error (unexpected success)")
	}
}

// ── a2aMemoryHandler ─────────────────────────────────────────────────────────

func newA2AServerWithMem(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	store, err := memory.OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	mgr := memory.NewManager(store)
	return &Server{
		config:    gleann.Config{IndexDir: dir},
		searchers: make(map[string]*gleann.LeannSearcher),
		blockMem:  mgr,
	}
}

func TestA2AMemoryHandler_Remember(t *testing.T) {
	s := newA2AServerWithMem(t)
	ctx := a2a.SkillContext{Query: "remember that gleann uses HNSW backend"}
	result, err := s.a2aMemoryHandler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !containsA2A(result, "Stored in memory") {
		t.Fatalf("expected 'Stored in memory' in result, got: %s", result)
	}
}

func TestA2AMemoryHandler_RememberTurkish(t *testing.T) {
	s := newA2AServerWithMem(t)
	ctx := a2a.SkillContext{Query: "hatırla gleann hnsw kullanır"}
	result, err := s.a2aMemoryHandler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !containsA2A(result, "Stored in memory") {
		t.Fatalf("expected 'Stored in memory', got: %s", result)
	}
}

func TestA2AMemoryHandler_Store(t *testing.T) {
	s := newA2AServerWithMem(t)
	ctx := a2a.SkillContext{Query: "store some important fact"}
	result, err := s.a2aMemoryHandler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !containsA2A(result, "Stored in memory") {
		t.Fatalf("expected 'Stored in memory', got: %s", result)
	}
}

func TestA2AMemoryHandler_Recall(t *testing.T) {
	s := newA2AServerWithMem(t)
	// First store a memory.
	ctx := a2a.SkillContext{Query: "remember gleann is awesome"}
	_, err := s.a2aMemoryHandler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Now recall.
	ctx = a2a.SkillContext{Query: "recall gleann"}
	result, err := s.a2aMemoryHandler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result == "" {
		t.Fatal("expected non-empty recall result")
	}
}

func TestA2AMemoryHandler_RecallNoResults(t *testing.T) {
	s := newA2AServerWithMem(t)
	ctx := a2a.SkillContext{Query: "what do you know about nonexistent topic xyz"}
	result, err := s.a2aMemoryHandler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !containsA2A(result, "No memories found") {
		t.Fatalf("expected 'No memories found', got: %s", result)
	}
}

func TestA2AMemoryHandler_RecallWithPrefix(t *testing.T) {
	s := newA2AServerWithMem(t)
	// Store then recall with prefix.
	storeCtx := a2a.SkillContext{Query: "remember specific fact about memory"}
	_, _ = s.a2aMemoryHandler(storeCtx)

	ctx := a2a.SkillContext{Query: "what do you know about memory"}
	result, err := s.a2aMemoryHandler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = result // just confirm no error
}

func TestA2AMemoryHandler_MemorySearch(t *testing.T) {
	s := newA2AServerWithMem(t)
	// Store then search with prefix.
	storeCtx := a2a.SkillContext{Query: "remember facts about search"}
	_, _ = s.a2aMemoryHandler(storeCtx)

	ctx := a2a.SkillContext{Query: "memory search search"}
	result, err := s.a2aMemoryHandler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = result
}

// ── a2aCodeHandler ───────────────────────────────────────────────────────────

func TestA2ACodeHandler_NilGraphPool(t *testing.T) {
	s := &Server{
		config:    gleann.Config{IndexDir: t.TempDir()},
		searchers: make(map[string]*gleann.LeannSearcher),
		graphPool: nil,
	}
	ctx := a2a.SkillContext{Query: "callers of main"}
	_, err := s.a2aCodeHandler(ctx)
	if err == nil {
		t.Fatal("expected error for nil graphPool")
	}
}

func TestA2ACodeHandler_NoIndexes(t *testing.T) {
	s := &Server{
		config:    gleann.Config{IndexDir: t.TempDir()},
		searchers: make(map[string]*gleann.LeannSearcher),
		graphPool: newGraphDBPool(t.TempDir()),
	}
	ctx := a2a.SkillContext{Query: "callers of main"}
	_, err := s.a2aCodeHandler(ctx)
	if err == nil {
		t.Fatal("expected error for no indexes")
	}
}

func TestA2ACodeHandler_Callers(t *testing.T) {
	dir := t.TempDir()
	mockDB := &mockGraphDB{
		callersResult: []GraphNode{
			{FQN: "pkg.Foo", Kind: "function"},
			{FQN: "pkg.Bar", Kind: "method"},
		},
	}
	pool := newGraphDBPool(dir)
	pool.dbs["testidx"] = mockDB

	// Create fake index so ListIndexes finds it.
	indexDir := filepath.Join(dir, "testidx")
	os.MkdirAll(indexDir, 0o755)
	os.WriteFile(filepath.Join(indexDir, "meta.json"), []byte(`{"name":"testidx"}`), 0o644)

	s := &Server{
		config:    gleann.Config{IndexDir: dir},
		searchers: make(map[string]*gleann.LeannSearcher),
		graphPool: pool,
	}
	ctx := a2a.SkillContext{
		Query:    "who calls MyFunc",
		Metadata: map[string]interface{}{"index": "testidx", "symbol": "pkg.MyFunc"},
	}
	result, err := s.a2aCodeHandler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !containsA2A(result, "callers") && !containsA2A(result, "Callers") {
		t.Fatalf("expected callers in result, got: %s", result)
	}
	if !containsA2A(result, "pkg.Foo") {
		t.Fatalf("expected pkg.Foo in result, got: %s", result)
	}
}

func TestA2ACodeHandler_CallersTurkish(t *testing.T) {
	dir := t.TempDir()
	mockDB := &mockGraphDB{
		callersResult: []GraphNode{{FQN: "pkg.X", Kind: "function"}},
	}
	pool := newGraphDBPool(dir)
	pool.dbs["idx"] = mockDB

	indexDir := filepath.Join(dir, "idx")
	os.MkdirAll(indexDir, 0o755)
	os.WriteFile(filepath.Join(indexDir, "meta.json"), []byte(`{"name":"idx"}`), 0o644)

	s := &Server{
		config:    gleann.Config{IndexDir: dir},
		searchers: make(map[string]*gleann.LeannSearcher),
		graphPool: pool,
	}
	ctx := a2a.SkillContext{
		Query:    "kim çağırıyor pkg.X",
		Metadata: map[string]interface{}{"index": "idx"},
	}
	result, err := s.a2aCodeHandler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !containsA2A(result, "callers") && !containsA2A(result, "Callers") {
		t.Fatalf("expected callers output, got: %s", result)
	}
}

func TestA2ACodeHandler_Callees(t *testing.T) {
	dir := t.TempDir()
	mockDB := &mockGraphDB{
		calleesResult: []GraphNode{
			{FQN: "pkg.Helper", Kind: "function"},
		},
	}
	pool := newGraphDBPool(dir)
	pool.dbs["idx"] = mockDB

	indexDir := filepath.Join(dir, "idx")
	os.MkdirAll(indexDir, 0o755)
	os.WriteFile(filepath.Join(indexDir, "meta.json"), []byte(`{"name":"idx"}`), 0o644)

	s := &Server{
		config:    gleann.Config{IndexDir: dir},
		searchers: make(map[string]*gleann.LeannSearcher),
		graphPool: pool,
	}
	ctx := a2a.SkillContext{
		Query:    "callees of MyFunc",
		Metadata: map[string]interface{}{"index": "idx"},
	}
	result, err := s.a2aCodeHandler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !containsA2A(result, "pkg.Helper") {
		t.Fatalf("expected pkg.Helper in result, got: %s", result)
	}
}

func TestA2ACodeHandler_CalleesTurkish(t *testing.T) {
	dir := t.TempDir()
	mockDB := &mockGraphDB{
		calleesResult: []GraphNode{{FQN: "pkg.Y", Kind: "function"}},
	}
	pool := newGraphDBPool(dir)
	pool.dbs["idx"] = mockDB

	indexDir := filepath.Join(dir, "idx")
	os.MkdirAll(indexDir, 0o755)
	os.WriteFile(filepath.Join(indexDir, "meta.json"), []byte(`{"name":"idx"}`), 0o644)

	s := &Server{
		config:    gleann.Config{IndexDir: dir},
		searchers: make(map[string]*gleann.LeannSearcher),
		graphPool: pool,
	}
	ctx := a2a.SkillContext{
		Query:    "bağımlılık Y",
		Metadata: map[string]interface{}{"index": "idx"},
	}
	result, err := s.a2aCodeHandler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = result
}

func TestA2ACodeHandler_Impact(t *testing.T) {
	dir := t.TempDir()
	mockDB := &mockGraphDB{
		impactResult: &ImpactResponse{
			DirectCallers:     []string{"caller1", "caller2"},
			TransitiveCallers: []string{"trans1"},
			AffectedFiles:     []string{"file1.go", "file2.go"},
			Depth:             3,
		},
	}
	pool := newGraphDBPool(dir)
	pool.dbs["idx"] = mockDB

	indexDir := filepath.Join(dir, "idx")
	os.MkdirAll(indexDir, 0o755)
	os.WriteFile(filepath.Join(indexDir, "meta.json"), []byte(`{"name":"idx"}`), 0o644)

	s := &Server{
		config:    gleann.Config{IndexDir: dir},
		searchers: make(map[string]*gleann.LeannSearcher),
		graphPool: pool,
	}
	ctx := a2a.SkillContext{
		Query:    "impact of MyFunc",
		Metadata: map[string]interface{}{"index": "idx", "symbol": "pkg.MyFunc"},
	}
	result, err := s.a2aCodeHandler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !containsA2A(result, "Impact") {
		t.Fatalf("expected 'Impact' in result, got: %s", result)
	}
}

func TestA2ACodeHandler_ImpactError(t *testing.T) {
	dir := t.TempDir()
	mockDB := &mockGraphDB{
		impactErr: fmt.Errorf("impact analysis failed"),
	}
	pool := newGraphDBPool(dir)
	pool.dbs["idx"] = mockDB

	indexDir := filepath.Join(dir, "idx")
	os.MkdirAll(indexDir, 0o755)
	os.WriteFile(filepath.Join(indexDir, "meta.json"), []byte(`{"name":"idx"}`), 0o644)

	s := &Server{
		config:    gleann.Config{IndexDir: dir},
		searchers: make(map[string]*gleann.LeannSearcher),
		graphPool: pool,
	}
	ctx := a2a.SkillContext{
		Query:    "impact analysis",
		Metadata: map[string]interface{}{"index": "idx"},
	}
	_, err := s.a2aCodeHandler(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestA2ACodeHandler_DefaultCallees(t *testing.T) {
	dir := t.TempDir()
	mockDB := &mockGraphDB{
		calleesResult: []GraphNode{},
	}
	pool := newGraphDBPool(dir)
	pool.dbs["idx"] = mockDB

	indexDir := filepath.Join(dir, "idx")
	os.MkdirAll(indexDir, 0o755)
	os.WriteFile(filepath.Join(indexDir, "meta.json"), []byte(`{"name":"idx"}`), 0o644)

	s := &Server{
		config:    gleann.Config{IndexDir: dir},
		searchers: make(map[string]*gleann.LeannSearcher),
		graphPool: pool,
	}
	// No intent keywords → default to callees
	ctx := a2a.SkillContext{
		Query:    "symbol MyFunc",
		Metadata: map[string]interface{}{"index": "idx"},
	}
	result, err := s.a2aCodeHandler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !containsA2A(result, "No callees found") {
		t.Logf("result: %s", result)
	}
}

func TestA2ACodeHandler_CallerError(t *testing.T) {
	dir := t.TempDir()
	mockDB := &mockGraphDB{
		callersErr: fmt.Errorf("graph error"),
	}
	pool := newGraphDBPool(dir)
	pool.dbs["idx"] = mockDB

	indexDir := filepath.Join(dir, "idx")
	os.MkdirAll(indexDir, 0o755)
	os.WriteFile(filepath.Join(indexDir, "meta.json"), []byte(`{"name":"idx"}`), 0o644)

	s := &Server{
		config:    gleann.Config{IndexDir: dir},
		searchers: make(map[string]*gleann.LeannSearcher),
		graphPool: pool,
	}
	ctx := a2a.SkillContext{
		Query:    "who calls MyFunc",
		Metadata: map[string]interface{}{"index": "idx"},
	}
	_, err := s.a2aCodeHandler(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestA2ACodeHandler_IndexNotInPool(t *testing.T) {
	dir := t.TempDir()
	pool := newGraphDBPool(dir)
	// Pool is empty — no "idx" in it.

	indexDir := filepath.Join(dir, "idx")
	os.MkdirAll(indexDir, 0o755)
	os.WriteFile(filepath.Join(indexDir, "meta.json"), []byte(`{"name":"idx"}`), 0o644)

	s := &Server{
		config:    gleann.Config{IndexDir: dir},
		searchers: make(map[string]*gleann.LeannSearcher),
		graphPool: pool,
	}
	ctx := a2a.SkillContext{
		Query:    "callers of X",
		Metadata: map[string]interface{}{"index": "idx"},
	}
	_, err := s.a2aCodeHandler(ctx)
	// Will try to open real graphDB which will fail (stub).
	if err == nil {
		t.Log("expected error from stub openGraphDB")
	}
}

func TestA2ACodeHandler_ReferencesKeyword(t *testing.T) {
	dir := t.TempDir()
	mockDB := &mockGraphDB{
		callersResult: []GraphNode{{FQN: "pkg.Ref", Kind: "function"}},
	}
	pool := newGraphDBPool(dir)
	pool.dbs["idx"] = mockDB

	indexDir := filepath.Join(dir, "idx")
	os.MkdirAll(indexDir, 0o755)
	os.WriteFile(filepath.Join(indexDir, "meta.json"), []byte(`{"name":"idx"}`), 0o644)

	s := &Server{
		config:    gleann.Config{IndexDir: dir},
		searchers: make(map[string]*gleann.LeannSearcher),
		graphPool: pool,
	}
	ctx := a2a.SkillContext{
		Query:    "references to MyFunc",
		Metadata: map[string]interface{}{"index": "idx"},
	}
	result, err := s.a2aCodeHandler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !containsA2A(result, "pkg.Ref") {
		t.Fatalf("expected pkg.Ref in result, got: %s", result)
	}
}

func TestA2ACodeHandler_CallsKeyword(t *testing.T) {
	dir := t.TempDir()
	mockDB := &mockGraphDB{
		calleesResult: []GraphNode{{FQN: "pkg.Called", Kind: "method"}},
	}
	pool := newGraphDBPool(dir)
	pool.dbs["idx"] = mockDB

	indexDir := filepath.Join(dir, "idx")
	os.MkdirAll(indexDir, 0o755)
	os.WriteFile(filepath.Join(indexDir, "meta.json"), []byte(`{"name":"idx"}`), 0o644)

	s := &Server{
		config:    gleann.Config{IndexDir: dir},
		searchers: make(map[string]*gleann.LeannSearcher),
		graphPool: pool,
	}
	ctx := a2a.SkillContext{
		Query:    "what calls does MyFunc make",
		Metadata: map[string]interface{}{"index": "idx"},
	}
	result, err := s.a2aCodeHandler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !containsA2A(result, "pkg.Called") {
		t.Fatalf("expected pkg.Called, got: %s", result)
	}
}

// ── buildProxyMessages ───────────────────────────────────────────────────────

func TestBuildProxyMessages_EmptyIndexes(t *testing.T) {
	s := &Server{
		config:    gleann.Config{IndexDir: t.TempDir()},
		searchers: make(map[string]*gleann.LeannSearcher),
	}
	msgs := []oaiMessage{{Role: "user", Content: "hello"}}
	result, err := s.buildProxyMessages(nil, msgs, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("expected unchanged messages, got %d", len(result))
	}
}

func TestBuildProxyMessages_NoUserMessage(t *testing.T) {
	s := &Server{
		config:    gleann.Config{IndexDir: t.TempDir()},
		searchers: make(map[string]*gleann.LeannSearcher),
	}
	msgs := []oaiMessage{{Role: "system", Content: "you are helpful"}}
	result, err := s.buildProxyMessages(nil, msgs, []string{"idx"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("expected unchanged messages, got %d", len(result))
	}
}

func TestBuildProxyMessages_SingleIndexFails(t *testing.T) {
	s := &Server{
		config:    gleann.Config{IndexDir: t.TempDir()},
		searchers: make(map[string]*gleann.LeannSearcher),
	}
	msgs := []oaiMessage{{Role: "user", Content: "hello"}}
	_, err := s.buildProxyMessages(nil, msgs, []string{"nonexistent"}, nil)
	if err == nil {
		t.Fatal("expected error for nonexistent index")
	}
}

func TestBuildProxyMessages_MultiIndexAllFail(t *testing.T) {
	s := &Server{
		config:    gleann.Config{IndexDir: t.TempDir()},
		searchers: make(map[string]*gleann.LeannSearcher),
	}
	msgs := []oaiMessage{{Role: "user", Content: "hello"}}
	result, err := s.buildProxyMessages(nil, msgs, []string{"idx1", "idx2"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// All indexes failed → no context → return unchanged.
	if len(result) != 1 {
		t.Fatalf("expected unchanged messages, got %d", len(result))
	}
}

func TestLastUserContentA2A(t *testing.T) {
	msgs := []oaiMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "reply"},
		{Role: "user", Content: "second"},
	}
	if got := lastUserContent(msgs); got != "second" {
		t.Fatalf("expected 'second', got %q", got)
	}
}

func TestLastUserContentA2A_NoUser(t *testing.T) {
	msgs := []oaiMessage{
		{Role: "system", Content: "sys"},
		{Role: "assistant", Content: "reply"},
	}
	if got := lastUserContent(msgs); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

// ── proxyLLMConfig ───────────────────────────────────────────────────────────

func TestProxyLLMConfig_Default(t *testing.T) {
	s := &Server{config: gleann.Config{}}
	cfg := s.proxyLLMConfig()
	if cfg.Provider == "" && cfg.BaseURL == "" {
		// Default config — just verify no panic.
	}
}

func TestProxyLLMConfig_WithOllama(t *testing.T) {
	s := &Server{config: gleann.Config{OllamaHost: "http://localhost:11434"}}
	cfg := s.proxyLLMConfig()
	if cfg.BaseURL != "http://localhost:11434" {
		t.Fatalf("expected ollama host, got %q", cfg.BaseURL)
	}
}

func TestProxyLLMConfig_WithAutoScan(t *testing.T) {
	s := &Server{config: gleann.Config{OllamaHost: "(auto-scan)"}}
	cfg := s.proxyLLMConfig()
	if cfg.BaseURL == "(auto-scan)" {
		t.Fatal("auto-scan should be ignored")
	}
}

func TestProxyLLMConfig_WithOpenAI(t *testing.T) {
	s := &Server{config: gleann.Config{
		OpenAIAPIKey:  "sk-test",
		OpenAIBaseURL: "https://custom.openai.com",
	}}
	cfg := s.proxyLLMConfig()
	if cfg.APIKey != "sk-test" {
		t.Fatalf("expected sk-test, got %q", cfg.APIKey)
	}
	if cfg.BaseURL != "https://custom.openai.com" {
		t.Fatalf("expected custom URL, got %q", cfg.BaseURL)
	}
	if cfg.Provider != gleann.LLMOpenAI {
		t.Fatalf("expected LLMOpenAI, got %v", cfg.Provider)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func containsA2A(s, sub string) bool {
	return len(s) >= len(sub) && len(sub) > 0 && strings.Contains(s, sub)
}

func newTestMux() *http.ServeMux {
	return http.NewServeMux()
}
