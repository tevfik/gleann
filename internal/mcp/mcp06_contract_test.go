package mcp

// MCP-06 contract test: the server instructions, tool descriptions and tool
// annotations are part of the public MCP surface, and every advertised tool
// must both advertise a complete schema and actually be callable.
//
// The fixture lives entirely in t.TempDir(): a format-2 index with passages
// and a small code graph, an isolated memory store, a stub sync runner and a
// fake local LLM. Nothing writes to ~/.gleann, ~/.config or the network.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
)

const mcp06Index = "mcp06idx"

type mcp06Fixture struct {
	srv       *Server
	index     string
	passageID int64
	readPath  string
}

// mcp06Server builds the isolated fixture used by all MCP-06 tests.
func mcp06Server(t *testing.T) *mcp06Fixture {
	t.Helper()
	// This fixture exercises local SQLite/index behaviour. Pin the remote
	// memory path off so a developer's running `gleann serve` (or a stale
	// ~/.gleann/server.pid) can never change what the test observes.
	t.Setenv("GLEANN_REMOTE_ADDR", "off")
	memory.ResetRemoteForTesting()
	dir := t.TempDir()
	embedder := &testBudgetEmbedder{dim: 4}
	glCfg := gleann.DefaultConfig()
	glCfg.IndexDir = dir
	glCfg.Backend = "hnsw"

	builder, err := gleann.NewBuilder(glCfg, embedder)
	if err != nil {
		t.Fatalf("new builder: %v", err)
	}
	passages := []gleann.Item{
		{Text: "func Hub() { Leaf() }", Metadata: map[string]any{"source": "core/hub.go", "kind": "code"}},
		{Text: "func Leaf() {}", Metadata: map[string]any{"source": "core/leaf.go", "kind": "code"}},
		{Text: "the full guide text", Metadata: map[string]any{"source": "docs/guide.md", "kind": "docs"}},
	}
	if err := builder.Build(context.Background(), mcp06Index, passages); err != nil {
		t.Fatalf("build index: %v", err)
	}

	st, err := store.Open(filepath.Join(dir, mcp06Index, mcp06Index+".db"), store.Options{})
	if err != nil {
		t.Fatalf("open index db: %v", err)
	}
	g := graphstore.New(st)
	ctx := context.Background()

	hub := graphstore.FileDelta{File: "core/hub.go"}
	hub.Symbols = append(hub.Symbols,
		gqSym("pkg.Hub", "function", "core/hub.go", 1, false),
		gqSym("pkg.Leaf", "function", "core/hub.go", 10, false),
		gqSym("pkg.Lonely", "function", "core/hub.go", 20, false),
	)
	hub.Edges = append(hub.Edges, graphstore.Edge{Src: "pkg.Hub", Dst: "pkg.Leaf"})
	if err := g.ApplyFileDelta(ctx, hub); err != nil {
		t.Fatalf("apply hub delta: %v", err)
	}

	caller := graphstore.FileDelta{File: "core/caller.go"}
	caller.Symbols = append(caller.Symbols, gqSym("pkg.Caller", "function", "core/caller.go", 3, false))
	caller.Edges = append(caller.Edges, graphstore.Edge{Src: "pkg.Caller", Dst: "pkg.Hub"})
	if err := g.ApplyFileDelta(ctx, caller); err != nil {
		t.Fatalf("apply caller delta: %v", err)
	}

	if err := g.ApplyDocDelta(ctx, graphstore.DocDelta{
		RPath: "docs/guide.md",
		Nodes: []graphstore.DocNode{
			{ID: "f1", Type: "folder", Name: "docs"},
			{ID: "d1", Type: "document", Parent: "f1", Name: "Guide", RPath: "docs/guide.md", Summary: "how it works"},
			{ID: "h1", Type: "heading", Parent: "d1", Name: "Intro", Level: 1},
			{ID: "c1", Type: "chunk", Parent: "d1", Text: "the full guide text", StartChar: 0, EndChar: 19},
		},
		Explains: []graphstore.DocExplains{{ChunkID: "c1", FQN: "pkg.Hub"}},
	}); err != nil {
		t.Fatalf("apply doc delta: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close index db: %v", err)
	}

	llm := newMCP06LLM(t)
	s := NewServer(Config{
		IndexDir:          dir,
		EmbeddingProvider: "custom",
		EmbeddingModel:    "test-model",
		LLMProvider:       "ollama",
		LLMModel:          "mcp06-fake",
		OllamaHost:        llm,
		ToolsProfile:      "full",
	})
	s.embedder = embedder
	t.Cleanup(s.Close)

	searcher := gleann.NewSearcher(glCfg, embedder)
	if err := searcher.Load(ctx, mcp06Index); err != nil {
		t.Fatalf("load searcher: %v", err)
	}
	if searcher.GraphDB() == nil {
		t.Fatal("fixture index has no code graph")
	}
	// Pick a real passage ID for gleann_get/gleann_fetch.
	ids := []int64{0, 1, 2}
	ps, err := searcher.PassageManager().GetBatch(ids)
	if err != nil || len(ps) == 0 {
		t.Fatalf("read passage ids: %v (passages=%d)", err, len(ps))
	}
	passageID := ps[0].ID
	s.searcherMu.Lock()
	s.searchers[mcp06Index] = searcher
	s.searcherLRU = append(s.searcherLRU, mcp06Index)
	s.searcherMu.Unlock()

	// An isolated block-memory store, so the test never touches the user's.
	mstore, err := memory.OpenStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatalf("open memory store: %v", err)
	}
	s.blockMem.close()
	s.blockMem = &blockMemPool{mgr: memory.NewManager(mstore)}

	// Sync is a write of the real index; the contract only needs a completed
	// call, so stub the runner.
	s.syncRunner = func(context.Context, SyncOptions) (string, error) {
		return "mcp06 mock sync complete", nil
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return &mcp06Fixture{
		srv:       s,
		index:     mcp06Index,
		passageID: passageID,
		readPath:  filepath.Join(wd, "server.go"),
	}
}

func newMCP06LLM(t *testing.T) string {
	t.Helper()
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/chat" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"message": map[string]string{"role": "assistant", "content": "mcp06 answer"},
				"done":    true,
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "mcp06 answer"}}},
		})
	}))
	t.Cleanup(hs.Close)
	return hs.URL
}

// mcp06Call invokes a tool through the handler the server actually
// registered, so the contract covers registration too.
func (f *mcp06Fixture) call(t *testing.T, name string, args any) *mcpsdk.CallToolResult {
	t.Helper()
	st := f.srv.mcpServer.GetTool(name)
	if st == nil {
		t.Fatalf("tool %q is not registered", name)
	}
	var req mcpsdk.CallToolRequest
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := st.Handler(context.Background(), req)
	if err != nil {
		t.Fatalf("%s: handler returned Go error: %v", name, err)
	}
	if res == nil {
		t.Fatalf("%s: handler returned nil result", name)
	}
	return res
}

// mcp06ExpectedAnnotations is the full advertised surface. Every entry must be
// registered, and every registered tool must appear here.
var mcp06ExpectedAnnotations = map[string]struct {
	readOnly    bool
	destructive bool
	idempotent  bool
	openWorld   bool
}{
	// gleann_search and gleann_search_ids accept rerank/expand, which may
	// call a configured cross-encoder or LLM endpoint; they also embed every
	// query through the configured embedder: open-world.
	"gleann_search":       {true, false, true, true},
	"gleann_search_multi": {true, false, true, true},
	"gleann_search_ids":   {true, false, true, true},
	"gleann_fetch":        {true, false, true, false},
	"gleann_get":          {true, false, true, false},
	// gleann_def falls back to passage search (and therefore the embedder)
	// when the code graph has no declaration for the symbol.
	"gleann_def":                {true, false, true, true},
	"gleann_list":               {true, false, true, false},
	"gleann_ask":                {true, false, true, true},
	"gleann_batch_ask":          {true, false, true, true},
	"gleann_graph_neighbors":    {true, false, true, false},
	"gleann_impact":             {true, false, true, false},
	"gleann_graph_stats":        {true, false, true, false},
	"gleann_symbols_in_file":    {true, false, true, false},
	"gleann_communities":        {true, false, true, false},
	"gleann_risk_analysis":      {true, false, true, false},
	"gleann_repo_map":           {true, false, true, false},
	"gleann_navigate_symbol":    {true, false, true, false},
	"gleann_document_links":     {true, false, true, false},
	"gleann_read_full_document": {true, false, true, false},
	"gleann_document_toc":       {true, false, true, false},
	"traverse_knowledge_graph":  {true, false, true, false},
	"gleann_read":               {true, false, true, false},
	"gleann_shell":              {true, false, true, false},
	// gleann_gain {reset:true} zeroes the accumulated counters, so it is a
	// (non-destructive, idempotent) write rather than a read.
	"gleann_gain": {false, false, true, false},
	// memory_search/memory_list/memory_context route through
	// pkg/memory.Remote() and answer from a configured gleann REST server
	// when one is healthy: open-world.
	"memory_search": {true, false, true, true},
	"memory_list":   {true, false, true, true},
	// memory_context flags stale blocks as suspect (Suspect, StaleReason,
	// UpdatedAt) in SQLite: a non-destructive, idempotent write. It also
	// forwards to /api/blocks/context unless a command/path filter forces
	// the local store.
	"memory_context":        {false, false, true, true},
	"memory_failure_list":   {true, false, true, false},
	"gleann_session_status": {true, false, true, false},
	// Repeated identical remembers/failures reinforce confidence (Confirms++)
	// and refresh UpdatedAt, so they are not idempotent. memory_remember
	// POSTs the fact to /api/blocks when a remote server is healthy, while
	// memory_failure_add is local-only.
	"memory_remember":    {false, false, false, true},
	"memory_failure_add": {false, false, false, false},
	// Every identical re-inject archives the previous node versions and
	// re-embeds node content, so the call is not idempotent; the vector sync
	// also sends the content to the configured embedder.
	"inject_knowledge_graph": {false, false, false, true},
	// Both session tools write their note through pkg/memory.Remote() when a
	// gleann REST server answers (POST /api/blocks); session_end also prunes
	// expired short-term blocks, which is destructive.
	"gleann_session_start": {false, false, false, true},
	"gleann_session_end":   {false, true, false, true},
	// gleann_sync embeds the source contents through the configured embedder.
	"gleann_sync": {false, false, true, true},
	// memory_forget DELETEs the selector from the remote /api/blocks/{id}
	// endpoint when a server is healthy.
	"memory_forget":       {false, true, true, true},
	"delete_graph_entity": {false, true, true, false},
}

// mcp06AuditedOpenWorld is the independent, per-tool classification the
// openWorldHint must agree with. It covers all 37 tools: a newly registered
// tool fails the contract until it is deliberately classified here.
//
// A tool is open-world when a normal call may reach a configured network
// service: the LLM-backed ask tools, the search tools that accept
// rerank/expand, every tool that computes embeddings through the configured
// embedder (HTTP /api/embed for the default Ollama provider), and every memory
// or session tool whose handler consults pkg/memory.Remote() and may forward
// its I/O to a healthy gleann REST server (/api/blocks*) configured through
// GLEANN_REMOTE_ADDR, ~/.gleann/config.json, ~/.gleann/server.pid or the
// http://localhost:8081 default.
var mcp06AuditedOpenWorld = map[string]bool{
	"gleann_search":             true, // embedder + optional rerank/expand
	"gleann_search_multi":       true, // embedder on every query
	"gleann_search_ids":         true, // embedder + optional rerank
	"gleann_fetch":              false,
	"gleann_get":                false,
	"gleann_def":                true, // semantic fallback embeds the symbol
	"gleann_list":               false,
	"gleann_ask":                true, // LLM
	"gleann_batch_ask":          true, // LLM
	"gleann_graph_neighbors":    false,
	"gleann_impact":             false,
	"gleann_graph_stats":        false,
	"gleann_symbols_in_file":    false,
	"gleann_communities":        false,
	"gleann_risk_analysis":      false,
	"gleann_repo_map":           false,
	"gleann_navigate_symbol":    false,
	"gleann_document_links":     false,
	"gleann_read_full_document": false,
	"gleann_document_toc":       false,
	"traverse_knowledge_graph":  false,
	"gleann_read":               false,
	"gleann_shell":              false,
	"gleann_gain":               false,
	"memory_search":             true, // GET /api/blocks/search via remoteMemoryClient
	"memory_list":               true, // GET /api/blocks via remoteMemoryClient
	"memory_failure_list":       false,
	"gleann_session_status":     false,
	"memory_context":            true, // GET /api/blocks/context via remoteMemoryClient
	"memory_remember":           true, // POST /api/blocks via remoteMemoryClient
	"memory_failure_add":        false,
	"inject_knowledge_graph":    true, // vector sync embeds node content
	"gleann_session_start":      true, // POST /api/blocks via remoteMemoryClient
	"gleann_session_end":        true, // POST /api/blocks via remoteMemoryClient
	"gleann_sync":               true, // embeds source contents
	"memory_forget":             true, // DELETE /api/blocks/{id} via remoteMemoryClient
	"delete_graph_entity":       false,
}

func TestMCP06_InitializeInstructions(t *testing.T) {
	f := mcp06Server(t)
	init := mcp06Initialize(t, f.srv)
	if strings.TrimSpace(init.Instructions) == "" {
		t.Fatal("initialize response has no server instructions")
	}
	lower := strings.ToLower(init.Instructions)
	for _, want := range []string{"search", "def", "graph", "memory", "grep"} {
		if !strings.Contains(lower, want) {
			t.Errorf("instructions do not mention %q:\n%s", want, init.Instructions)
		}
	}
	for _, want := range []string{"gleann_search", "gleann_def", "gleann_graph_neighbors", "memory_search", "gleann_read"} {
		if !strings.Contains(init.Instructions, want) {
			t.Errorf("instructions do not name %q:\n%s", want, init.Instructions)
		}
	}
	// A full-profile server may only recommend tools it actually registered.
	mcp06AssertMentionsRegistered(t, init.Instructions, f.srv)

	// The core profile registers 11 tools, so its instructions must guide the
	// model using exactly those tools and nothing else — whether the profile
	// came from Config.ToolsProfile or from GLEANN_TOOLS.
	coreTools := map[string]bool{
		"gleann_search": true, "gleann_read": true, "gleann_def": true,
		"gleann_graph_neighbors": true, "gleann_impact": true, "gleann_sync": true,
		"memory_remember": true, "memory_failure_add": true, "memory_context": true,
		"memory_search": true, "memory_forget": true,
	}
	for _, tc := range []struct {
		name string
		cfg  Config
		env  string
	}{
		{"core-flag", Config{ToolsProfile: "core"}, ""},
		{"core-env", Config{}, "core"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := mcp06ProfileServer(t, tc.cfg, tc.env)
			res := mcp06Initialize(t, s)
			if strings.TrimSpace(res.Instructions) == "" {
				t.Fatal("core initialize response has no server instructions")
			}
			if len(s.mcpServer.ListTools()) != len(coreTools) {
				t.Fatalf("core profile advertises %d tools, want %d", len(s.mcpServer.ListTools()), len(coreTools))
			}
			mcp06AssertMentionsRegistered(t, res.Instructions, s)
			for name := range coreTools {
				if !strings.Contains(res.Instructions, name) {
					t.Errorf("core instructions do not mention core tool %q:\n%s", name, res.Instructions)
				}
			}
			if !strings.Contains(res.Instructions, "--tools full") {
				t.Errorf("core instructions do not point at --tools full:\n%s", res.Instructions)
			}
		})
	}

	// A custom profile registers an arbitrary subset. Its closing note claims
	// that only the tools named above are available, so every registered tool
	// must actually be named, and nothing else may be recommended.
	for _, profile := range []string{"fetch", "get,list", "traverse_knowledge_graph", "memory_list", "search,def"} {
		profile := profile
		t.Run("custom-"+strings.ReplaceAll(profile, ",", "-"), func(t *testing.T) {
			s := mcp06ProfileServer(t, Config{ToolsProfile: profile}, "")
			res := mcp06Initialize(t, s)
			registered := s.mcpServer.ListTools()
			if len(registered) == 0 {
				t.Fatalf("custom profile %q registers no tools", profile)
			}
			for name := range registered {
				if !strings.Contains(res.Instructions, name) {
					t.Errorf("custom profile %q registers %q but the instructions never name it:\n%s",
						profile, name, res.Instructions)
				}
			}
			mcp06AssertMentionsRegistered(t, res.Instructions, s)
			if !strings.Contains(res.Instructions, "custom tool profile") {
				t.Errorf("custom profile %q instructions lost the custom-profile note:\n%s",
					profile, res.Instructions)
			}
		})
	}
}

// mcp06Initialize performs a raw initialize handshake and returns its result.
func mcp06Initialize(t *testing.T, s *Server) mcpsdk.InitializeResult {
	t.Helper()
	resp := s.mcpServer.HandleMessage(context.Background(), []byte(`{
		"jsonrpc":"2.0","id":1,"method":"initialize",
		"params":{"protocolVersion":"2025-06-18","capabilities":{},
		"clientInfo":{"name":"mcp06-test","version":"1.0.0"}}}`))
	rpc, ok := resp.(mcpsdk.JSONRPCResponse)
	if !ok {
		t.Fatalf("initialize returned %T, want JSONRPCResponse", resp)
	}
	init, ok := rpc.Result.(mcpsdk.InitializeResult)
	if !ok {
		t.Fatalf("initialize result is %T, want InitializeResult", rpc.Result)
	}
	return init
}

// mcp06ProfileServer builds a lightweight server for a tools profile. env, when
// set, is exported as GLEANN_TOOLS and Config.ToolsProfile is left empty.
func mcp06ProfileServer(t *testing.T, cfg Config, env string) *Server {
	t.Helper()
	if env != "" {
		t.Setenv("GLEANN_TOOLS", env)
	}
	cfg.IndexDir = t.TempDir()
	s := NewServer(cfg)
	t.Cleanup(s.Close)
	return s
}

var mcp06ToolName = regexp.MustCompile(`\b(?:(?:gleann|memory)_[a-z0-9_]+|inject_knowledge_graph|delete_graph_entity|traverse_knowledge_graph)\b`)

// mcp06AssertMentionsRegistered fails when server instructions recommend a
// tool that the same server does not advertise, which would make the model
// call a tool that answers "tool not found".
func mcp06AssertMentionsRegistered(t *testing.T, instructions string, s *Server) {
	t.Helper()
	registered := make(map[string]bool)
	for name := range s.mcpServer.ListTools() {
		registered[name] = true
	}
	mentioned := map[string]bool{}
	for _, name := range mcp06ToolName.FindAllString(instructions, -1) {
		mentioned[name] = true
	}
	if len(mentioned) == 0 {
		t.Fatalf("instructions name no tools at all:\n%s", instructions)
	}
	for name := range mentioned {
		if !registered[name] {
			t.Errorf("instructions recommend %q, which this server does not register (registered: %v)",
				name, sortedToolNames(registered))
		}
	}
}

func sortedToolNames(set map[string]bool) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TestMCP06_ToolsDescribedAndAnnotated(t *testing.T) {
	f := mcp06Server(t)
	tools := f.srv.mcpServer.ListTools()
	if len(tools) != len(mcp06ExpectedAnnotations) {
		t.Errorf("server advertises %d tools, contract expects %d", len(tools), len(mcp06ExpectedAnnotations))
	}

	for name, st := range tools {
		tool := st.Tool
		if strings.TrimSpace(tool.Description) == "" {
			t.Errorf("%s: empty description", name)
		}
		// A valid input schema: object type, required keys present in properties.
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Errorf("%s: marshal inputSchema: %v", name, err)
			continue
		}
		var schema struct {
			Type       string         `json:"type"`
			Properties map[string]any `json:"properties"`
			Required   []string       `json:"required"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Errorf("%s: inputSchema is not valid JSON: %v", name, err)
			continue
		}
		if schema.Type != "object" {
			t.Errorf("%s: inputSchema.type = %q, want object", name, schema.Type)
		}
		for _, req := range schema.Required {
			if _, ok := schema.Properties[req]; !ok {
				t.Errorf("%s: required property %q missing from properties", name, req)
			}
		}

		a := tool.Annotations
		if strings.TrimSpace(a.Title) == "" {
			t.Errorf("%s: empty annotation title", name)
		}
		if a.ReadOnlyHint == nil || a.DestructiveHint == nil || a.IdempotentHint == nil || a.OpenWorldHint == nil {
			t.Errorf("%s: incomplete annotations: %+v", name, a)
			continue
		}
		want, ok := mcp06ExpectedAnnotations[name]
		if !ok {
			t.Errorf("%s: advertised but absent from the annotation contract", name)
			continue
		}
		if *a.ReadOnlyHint != want.readOnly {
			t.Errorf("%s: readOnlyHint = %v, want %v", name, *a.ReadOnlyHint, want.readOnly)
		}
		if *a.DestructiveHint != want.destructive {
			t.Errorf("%s: destructiveHint = %v, want %v", name, *a.DestructiveHint, want.destructive)
		}
		if *a.IdempotentHint != want.idempotent {
			t.Errorf("%s: idempotentHint = %v, want %v", name, *a.IdempotentHint, want.idempotent)
		}
		if *a.OpenWorldHint != want.openWorld {
			t.Errorf("%s: openWorldHint = %v, want %v", name, *a.OpenWorldHint, want.openWorld)
		}
		if want.readOnly && *a.DestructiveHint {
			t.Errorf("%s: a read-only tool cannot be destructive", name)
		}
		// Any tool whose schema exposes an LLM-backed parameter must be
		// advertised open-world; the table above pins the inverse direction.
		for _, p := range []string{"rerank", "expand"} {
			if _, ok := schema.Properties[p]; ok && !*a.OpenWorldHint {
				t.Errorf("%s: schema exposes %q but openWorldHint = false", name, p)
			}
		}
		// The audited per-tool classification is the second, independent
		// check: a newly added tool is unclassified and fails here.
		wantOpen, classified := mcp06AuditedOpenWorld[name]
		if !classified {
			t.Errorf("%s: not classified in mcp06AuditedOpenWorld", name)
		} else if *a.OpenWorldHint != wantOpen {
			t.Errorf("%s: openWorldHint = %v, but the audited classification is %v",
				name, *a.OpenWorldHint, wantOpen)
		}
	}
	for name := range mcp06ExpectedAnnotations {
		if _, ok := tools[name]; !ok {
			t.Errorf("%s: in the annotation contract but not advertised", name)
		}
	}
	if len(mcp06AuditedOpenWorld) != len(mcp06ExpectedAnnotations) {
		t.Errorf("audited open-world classification covers %d tools, want all %d",
			len(mcp06AuditedOpenWorld), len(mcp06ExpectedAnnotations))
	}
	for name := range mcp06AuditedOpenWorld {
		if _, ok := tools[name]; !ok {
			t.Errorf("%s: classified but not advertised", name)
		}
	}
}

func TestMCP06_EveryToolCallable(t *testing.T) {
	f := mcp06Server(t)
	idx := f.index

	// Every tool gets one valid call. Values are chosen so the call reaches the
	// real work (search, graph, memory, LLM, sync, filesystem), not a no-op.
	valid := []struct {
		name string
		args any
	}{
		{"gleann_search", map[string]any{"index": idx, "query": "hub", "top_k": 2.0}},
		{"gleann_search_multi", map[string]any{"query": "hub", "indexes": idx, "top_k": 2.0}},
		{"gleann_def", map[string]any{"index": idx, "symbol": "Hub", "top_k": 3.0}},
		{"gleann_list", map[string]any{}},
		{"gleann_ask", map[string]any{"index": idx, "question": "what is Hub?"}},
		{"gleann_graph_neighbors", map[string]any{"index": idx, "node_fqn": "pkg.Hub"}},
		{"gleann_document_links", map[string]any{"index": idx, "doc_path": "docs/guide.md"}},
		{"gleann_read_full_document", map[string]any{"index": idx, "vpath": "docs/guide.md"}},
		{"gleann_document_toc", map[string]any{"index": idx}},
		{"gleann_impact", map[string]any{"index": idx, "symbol": "pkg.Hub"}},
		{"gleann_search_ids", map[string]any{"index": idx, "query": "hub"}},
		{"gleann_fetch", map[string]any{"index": idx, "ids": []any{float64(f.passageID)}}},
		{"gleann_get", map[string]any{"ref": fmt.Sprintf("%s:%d", idx, f.passageID)}},
		{"gleann_session_start", map[string]any{"name": "mcp06-session"}},
		{"gleann_session_end", map[string]any{"summary": "mcp06 contract"}},
		{"gleann_session_status", map[string]any{}},
		{"memory_remember", map[string]any{"content": "mcp06 remembers this fact"}},
		{"memory_forget", map[string]any{"id_or_query": "mcp06 remembers this fact"}},
		{"memory_search", map[string]any{"query": "mcp06"}},
		{"memory_list", map[string]any{}},
		{"memory_context", map[string]any{}},
		{"memory_failure_add", map[string]any{"command": "go test ./...", "error": "boom", "fix": "retry"}},
		{"memory_failure_list", map[string]any{}},
		{"gleann_batch_ask", map[string]any{"index": idx, "questions": []any{"what is Hub?"}}},
		{"inject_knowledge_graph", map[string]any{
			"index": idx,
			"nodes": []any{
				map[string]any{"id": "mcp06-e1", "type": "Entity"},
				map[string]any{"id": "mcp06-e2", "type": "Entity"},
			},
			"edges": []any{map[string]any{"from": "mcp06-e1", "to": "mcp06-e2", "relation_type": "RELATES_TO"}},
		}},
		{"delete_graph_entity", map[string]any{"index": idx, "id": "mcp06-e2"}},
		{"traverse_knowledge_graph", map[string]any{"index": idx, "start_id": "mcp06-e1", "depth": 1.0}},
		{"gleann_graph_stats", map[string]any{"index": idx}},
		{"gleann_symbols_in_file", map[string]any{"index": idx, "file_path": "core/hub.go"}},
		{"gleann_shell", map[string]any{"command": "git log -n 5", "output": "commit 1\ncommit 2"}},
		{"gleann_read", map[string]any{"path": f.readPath, "mode": "signatures"}},
		{"gleann_gain", map[string]any{}},
		{"gleann_sync", map[string]any{"index": idx}},
		{"gleann_communities", map[string]any{"index": idx}},
		{"gleann_risk_analysis", map[string]any{"index": idx, "top": 10.0}},
		{"gleann_repo_map", map[string]any{"index": idx, "top_k": 10.0}},
		{"gleann_navigate_symbol", map[string]any{"index": idx, "symbol": "Hub"}},
	}

	seen := make(map[string]bool, len(valid))
	for _, tc := range valid {
		tc := tc
		t.Run("valid/"+tc.name, func(t *testing.T) {
			res := f.call(t, tc.name, tc.args)
			if res.IsError {
				t.Fatalf("valid call returned a tool error: %s", extractText(res))
			}
			if strings.TrimSpace(extractText(res)) == "" {
				t.Fatalf("valid call returned an empty payload")
			}
		})
		seen[tc.name] = true
	}
	for name := range mcp06ExpectedAnnotations {
		if !seen[name] {
			t.Errorf("%s: no valid-call case in the contract", name)
		}
	}
}

func TestMCP06_BadArgumentsAreRejected(t *testing.T) {
	f := mcp06Server(t)
	idx := f.index

	// Tools that validate their arguments: each bad call must be a tool error
	// (never a Go error, never a panic).
	bad := []struct {
		name string
		args map[string]any
	}{
		{"gleann_search", map[string]any{}},
		{"gleann_search_multi", map[string]any{}},
		{"gleann_search_ids", map[string]any{"index": "does-not-exist", "query": "x"}},
		{"gleann_fetch", map[string]any{"index": idx, "ids": []any{}}},
		{"gleann_get", map[string]any{"ref": "not-a-ref"}},
		{"gleann_def", map[string]any{}},
		{"gleann_ask", map[string]any{}},
		{"gleann_batch_ask", map[string]any{}},
		{"gleann_graph_neighbors", map[string]any{}},
		{"gleann_impact", map[string]any{}},
		{"gleann_graph_stats", map[string]any{}},
		{"gleann_symbols_in_file", map[string]any{}},
		{"gleann_communities", map[string]any{"index": "does-not-exist"}},
		{"gleann_risk_analysis", map[string]any{"index": "does-not-exist"}},
		{"gleann_repo_map", map[string]any{"index": "does-not-exist"}},
		{"gleann_navigate_symbol", map[string]any{"index": idx}},
		{"gleann_document_links", map[string]any{}},
		{"gleann_read_full_document", map[string]any{}},
		{"gleann_document_toc", map[string]any{}},
		{"traverse_knowledge_graph", map[string]any{}},
		{"gleann_read", map[string]any{}},
		{"gleann_shell", map[string]any{}},
		{"memory_remember", map[string]any{}},
		{"memory_forget", map[string]any{}},
		{"memory_search", map[string]any{}},
		{"memory_failure_add", map[string]any{}},
		{"inject_knowledge_graph", map[string]any{}},
		{"delete_graph_entity", map[string]any{}},
		{"gleann_session_start", map[string]any{}},
		{"gleann_sync", map[string]any{"index": "../escape"}},
	}
	for _, tc := range bad {
		tc := tc
		t.Run("bad/"+tc.name, func(t *testing.T) {
			res := f.call(t, tc.name, tc.args)
			if !res.IsError {
				t.Fatalf("bad arguments produced success: %s", extractText(res))
			}
		})
	}

	// gleann_list has no parameters; a broken index directory is the bad path.
	t.Run("bad/gleann_list", func(t *testing.T) {
		notADir := filepath.Join(t.TempDir(), "not-a-dir")
		if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		s := NewServer(Config{IndexDir: notADir, ToolsProfile: "full"})
		defer s.Close()
		res := f.callOn(t, s, "gleann_list", map[string]any{})
		if !res.IsError {
			t.Fatalf("gleann_list on a broken index dir produced success: %s", extractText(res))
		}
	})

	// The memory-list tools fail cleanly when their store is closed.
	for _, name := range []string{"memory_list", "memory_context", "memory_failure_list"} {
		name := name
		t.Run("bad/"+name, func(t *testing.T) {
			broken := mcp06Server(t)
			mgr, err := broken.srv.blockMem.get()
			if err != nil {
				t.Fatal(err)
			}
			if err := mgr.Close(); err != nil {
				t.Fatal(err)
			}
			res := broken.call(t, name, map[string]any{})
			if !res.IsError {
				t.Fatalf("%s with a closed store produced success: %s", name, extractText(res))
			}
		})
	}
}

// callOn invokes a tool on an auxiliary server with the same contract as call.
func (f *mcp06Fixture) callOn(t *testing.T, s *Server, name string, args any) *mcpsdk.CallToolResult {
	t.Helper()
	st := s.mcpServer.GetTool(name)
	if st == nil {
		t.Fatalf("tool %q is not registered", name)
	}
	var req mcpsdk.CallToolRequest
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := st.Handler(context.Background(), req)
	if err != nil {
		t.Fatalf("%s: handler returned Go error: %v", name, err)
	}
	if res == nil {
		t.Fatalf("%s: handler returned nil result", name)
	}
	return res
}

// TestMCP06_GainResetZeroesCounters pins the write half of gleann_gain: the
// tool must not be advertised read-only, and {reset:true} must actually zero
// the accumulated counters and keep working on malformed arguments.
func TestMCP06_GainResetZeroesCounters(t *testing.T) {
	f := mcp06Server(t)
	f.srv.recordGain(1000, 250)

	var before gleann.TokenGain
	if err := json.Unmarshal([]byte(extractText(f.call(t, "gleann_gain", map[string]any{}))), &before); err != nil {
		t.Fatalf("gain report is not JSON: %v", err)
	}
	if before.Calls != 1 || before.RawTokens != 1000 || before.CompressedTokens != 250 {
		t.Fatalf("gain before reset = %+v, want one 1000→250 call", before)
	}

	res := f.call(t, "gleann_gain", map[string]any{"reset": true})
	if res.IsError {
		t.Fatalf("gain reset returned a tool error: %s", extractText(res))
	}

	var after gleann.TokenGain
	if err := json.Unmarshal([]byte(extractText(f.call(t, "gleann_gain", map[string]any{}))), &after); err != nil {
		t.Fatalf("gain report after reset is not JSON: %v", err)
	}
	if after != (gleann.TokenGain{}) {
		t.Errorf("gain after reset = %+v, want the zero value", after)
	}
	// The reset path must not panic on malformed arguments either.
	if res := f.call(t, "gleann_gain", "not-an-object"); res == nil {
		t.Error("gain with malformed arguments returned nil")
	}
}

// TestMCP06_NoErrorPathToolsAreSafe covers the tools whose handlers have no
// argument-validation error path: gleann_gain only branches on the optional
// reset flag, gleann_session_status takes no arguments at all, and
// gleann_session_end has no required fields. They must still tolerate
// malformed argument types without a panic, and an unknown tool must be
// rejected by the SDK.
// mcp06EmbedProbe records /api/embed request bodies so a test can prove that a
// tool sent its input to the configured embedder over HTTP.
type mcp06EmbedProbe struct {
	mu     sync.Mutex
	bodies []string
}

func (p *mcp06EmbedProbe) record(body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bodies = append(p.bodies, body)
}

func (p *mcp06EmbedProbe) snapshot() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.bodies)
}

func (p *mcp06EmbedProbe) since(n int) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n > len(p.bodies) {
		n = len(p.bodies)
	}
	return append([]string(nil), p.bodies[n:]...)
}

// newMCP06Embedder runs a real loopback Ollama-compatible /api/embed endpoint.
func newMCP06Embedder(t *testing.T) (*httptest.Server, *mcp06EmbedProbe) {
	t.Helper()
	probe := &mcp06EmbedProbe{}
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		probe.record(string(body))

		var req struct {
			Input any `json:"input"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var texts []string
		switch v := req.Input.(type) {
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					texts = append(texts, s)
				}
			}
		case string:
			texts = []string{v}
		}
		embs := make([][]float32, len(texts))
		for i, txt := range texts {
			e := make([]float32, 8)
			for j := range e {
				e[j] = float32((len(txt)+j*5)%11) / 11
			}
			e[i%8]++
			embs[i] = e
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": embs})
	}))
	t.Cleanup(hs.Close)
	return hs, probe
}

type mcp06EgressFixture struct {
	*mcp06Fixture
	probe   *mcp06EmbedProbe
	docsDir string
}

// mcp06EgressServer builds a real index and server whose embedder is a
// loopback HTTP endpoint, not the in-process test embedder. That is the
// configuration gleann actually ships (EmbeddingProvider "ollama"), and the
// only way to observe whether a handler sends its input off-process.
func mcp06EgressServer(t *testing.T) *mcp06EgressFixture {
	t.Helper()
	// Embedder egress is the only network path this fixture models; keep the
	// memory REST path off so the local SQLite fixture stays deterministic.
	t.Setenv("GLEANN_REMOTE_ADDR", "off")
	memory.ResetRemoteForTesting()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GLEANN_MEMORY_DIR", t.TempDir())

	fake, probe := newMCP06Embedder(t)
	dir := t.TempDir()
	model := fmt.Sprintf("mcp06-embed-%d", time.Now().UnixNano())

	emb := embedding.NewComputer(embedding.Options{
		Provider: embedding.ProviderOllama,
		Model:    model,
		BaseURL:  fake.URL,
	})
	glCfg := gleann.DefaultConfig()
	glCfg.IndexDir = dir
	glCfg.EmbeddingProvider = "ollama"
	glCfg.EmbeddingModel = model
	glCfg.OllamaHost = fake.URL

	ctx := context.Background()
	builder, err := gleann.NewBuilder(glCfg, emb)
	if err != nil {
		t.Fatalf("new builder: %v", err)
	}
	passages := []gleann.Item{
		{Text: "func ZzHub() { ZzLeaf() }", Metadata: map[string]any{"source": "core/hub.go", "kind": "code"}},
		{Text: "func ZzLeaf() {}", Metadata: map[string]any{"source": "core/leaf.go", "kind": "code"}},
	}
	if err := builder.Build(ctx, mcp06Index, passages); err != nil {
		t.Fatalf("build index: %v", err)
	}

	st, err := store.Open(filepath.Join(dir, mcp06Index, mcp06Index+".db"), store.Options{})
	if err != nil {
		t.Fatalf("open index db: %v", err)
	}
	g := graphstore.New(st)
	delta := graphstore.FileDelta{File: "core/hub.go"}
	delta.Symbols = append(delta.Symbols, gqSym("pkg.ZzHub", "function", "core/hub.go", 1, false))
	if err := g.ApplyFileDelta(ctx, delta); err != nil {
		t.Fatalf("apply delta: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close index db: %v", err)
	}

	s := NewServer(Config{
		IndexDir:          dir,
		EmbeddingProvider: "ollama",
		EmbeddingModel:    model,
		OllamaHost:        fake.URL,
		ToolsProfile:      "full",
	})
	t.Cleanup(s.Close)

	docsDir := filepath.Join(home, "sync-src")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "main.go"),
		[]byte("package main\n\n// ZzSyncSecret is embedded during the build.\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write docs file: %v", err)
	}

	return &mcp06EgressFixture{
		mcp06Fixture: &mcp06Fixture{srv: s, index: mcp06Index},
		probe:        probe,
		docsDir:      docsDir,
	}
}

// TestMCP06_OpenWorldToolsReachEmbedder pins the behavior behind the
// openWorldHint on every embedder-backed tool: the advertised open-world tools
// that compute embeddings really do POST their input to the configured
// /api/embed endpoint during a normal call.
func TestMCP06_OpenWorldToolsReachEmbedder(t *testing.T) {
	f := mcp06EgressServer(t)
	idx := f.index

	cases := []struct {
		name   string
		args   any
		marker string
	}{
		{
			"gleann_search_multi",
			map[string]any{"indexes": idx, "query": "zzmulti query marker", "top_k": 2.0},
			"zzmulti query marker",
		},
		{
			"gleann_def",
			map[string]any{"index": idx, "symbol": "ZzNoSuchSymbol"},
			"ZzNoSuchSymbol",
		},
		{
			"gleann_sync",
			map[string]any{"index": "mcp06egresssync", "docs_dir": f.docsDir, "mode": "code", "no_plugins": true},
			"ZzSyncSecret",
		},
		{
			"inject_knowledge_graph",
			map[string]any{
				"index": idx,
				"nodes": []any{
					map[string]any{"id": "zz-e1", "type": "Entity", "content": "zz confidential entity knowledge"},
				},
			},
			"zz confidential entity knowledge",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			st := f.srv.mcpServer.GetTool(tc.name)
			if st == nil {
				t.Fatalf("%s is not registered", tc.name)
			}
			if st.Tool.Annotations.OpenWorldHint == nil || !*st.Tool.Annotations.OpenWorldHint {
				t.Fatalf("%s is not advertised open-world", tc.name)
			}
			before := f.probe.snapshot()
			res := f.call(t, tc.name, tc.args)
			if res.IsError {
				t.Fatalf("%s returned a tool error: %s", tc.name, extractText(res))
			}
			bodies := f.probe.since(before)
			if len(bodies) == 0 {
				t.Fatalf("%s made no /api/embed request during a normal call", tc.name)
			}
			for _, body := range bodies {
				if strings.Contains(body, tc.marker) {
					return
				}
			}
			t.Fatalf("%s made %d /api/embed request(s) but none contained %q",
				tc.name, len(bodies), tc.marker)
		})
	}
}

// mcp06RemoteRESTProbe counts the HTTP requests a handler makes to the
// configured gleann REST endpoint and serves the /health + /api/blocks*
// surface pkg/memory.RemoteClient expects.
type mcp06RemoteRESTProbe struct {
	mu      sync.Mutex
	entries []string
}

func (p *mcp06RemoteRESTProbe) record(method, uri, body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entries = append(p.entries, method+" "+uri+"\n"+body)
}

func (p *mcp06RemoteRESTProbe) snapshot() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}

func (p *mcp06RemoteRESTProbe) since(n int) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n > len(p.entries) {
		n = len(p.entries)
	}
	return append([]string(nil), p.entries[n:]...)
}

// newMCP06RemoteREST runs a fake gleann REST server: a healthy /health probe
// followed by the /api/blocks endpoints pkg/memory.RemoteClient calls.
func newMCP06RemoteREST(t *testing.T) (*httptest.Server, *mcp06RemoteRESTProbe) {
	t.Helper()
	probe := &mcp06RemoteRESTProbe{}
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		probe.record(r.Method, r.URL.RequestURI(), string(body))

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"engine": "gleann-go", "status": "ok"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/blocks":
			var req struct {
				Content string `json:"content"`
				Tier    string `json:"tier"`
				Label   string `json:"label"`
			}
			_ = json.Unmarshal(body, &req)
			tier := memory.Tier(req.Tier)
			if tier == "" {
				tier = memory.TierLong
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(memory.Block{
				ID: "zz-remote-block", Content: req.Content, Tier: tier, Label: req.Label,
			})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/blocks/"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"deleted": 1})
		case r.Method == http.MethodGet && r.URL.Path == "/api/blocks/search":
			q := r.URL.Query().Get("q")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"blocks": []memory.Block{
					{ID: "zz-remote-search", Content: "remote hit for " + q, Tier: memory.TierLong},
				},
				"count": 1,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/blocks/context":
			w.Header().Set("Content-Type", "text/xml; charset=utf-8")
			_, _ = w.Write([]byte("<memory_context><block>zz remote context</block></memory_context>"))
		case r.Method == http.MethodGet && r.URL.Path == "/api/blocks":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"blocks": []memory.Block{
					{ID: "zz-remote-list", Content: "zz remote listed block", Tier: memory.TierLong},
				},
				"count": 1,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(hs.Close)
	return hs, probe
}

// TestMCP06_OpenWorldMemoryToolsReachRemoteREST pins the second egress path
// behind openWorldHint: the seven memory/session tools consult
// pkg/memory.Remote() (remoteMemoryClient) and forward their I/O over REST
// when a healthy gleann server answers at GLEANN_REMOTE_ADDR, a saved
// server_addr, ~/.gleann/server.pid or http://localhost:8081. The three
// local-only memory/session tools must stay at openWorldHint:false and make no
// HTTP request even while that same server is healthy.
func TestMCP06_OpenWorldMemoryToolsReachRemoteREST(t *testing.T) {
	f := mcp06Server(t)
	// This test starts a real (global) serverSession; never leak it.
	t.Cleanup(func() {
		serverSession.mu.Lock()
		serverSession.name = ""
		serverSession.logCount = 0
		serverSession.mu.Unlock()
	})

	remote, probe := newMCP06RemoteREST(t)
	t.Setenv("GLEANN_REMOTE_ADDR", remote.URL)
	memory.ResetRemoteForTesting()
	t.Cleanup(memory.ResetRemoteForTesting)

	remoteCases := []struct {
		name   string
		args   any
		marker string
	}{
		{
			"memory_remember",
			map[string]any{"content": "zzRemoteRememberMarker"},
			"zzRemoteRememberMarker",
		},
		{
			"memory_search",
			map[string]any{"query": "zzRemoteSearchMarker"},
			"zzRemoteSearchMarker",
		},
		{
			"memory_list",
			map[string]any{},
			"/api/blocks",
		},
		{
			"memory_context",
			map[string]any{},
			"/api/blocks/context",
		},
		{
			"memory_forget",
			map[string]any{"id_or_query": "zzRemoteForgetMarker"},
			"zzRemoteForgetMarker",
		},
		{
			"gleann_session_start",
			map[string]any{"name": "zzRemoteSessionName"},
			"zzRemoteSessionName",
		},
		{
			"gleann_session_end",
			map[string]any{"summary": "zzRemoteSessionSummary"},
			"zzRemoteSessionSummary",
		},
	}

	for _, tc := range remoteCases {
		tc := tc
		t.Run("remote/"+tc.name, func(t *testing.T) {
			st := f.srv.mcpServer.GetTool(tc.name)
			if st == nil {
				t.Fatalf("%s is not registered", tc.name)
			}
			before := probe.snapshot()
			res := f.call(t, tc.name, tc.args)
			if res.IsError {
				t.Fatalf("%s returned a tool error: %s", tc.name, extractText(res))
			}
			entries := probe.since(before)
			if len(entries) == 0 {
				t.Fatalf("%s made no HTTP request to the healthy configured gleann REST server", tc.name)
			}
			matched := false
			for _, entry := range entries {
				if strings.Contains(entry, tc.marker) {
					matched = true
					break
				}
			}
			if !matched {
				t.Fatalf("%s made %d request(s) but none contained %q: %v",
					tc.name, len(entries), tc.marker, entries)
			}
			// The hint must match the egress we just observed.
			if st.Tool.Annotations.OpenWorldHint == nil || !*st.Tool.Annotations.OpenWorldHint {
				t.Fatalf("%s forwarded %d REST request(s) (including %q) to the configured gleann server while advertising openWorldHint:false",
					tc.name, len(entries), tc.marker)
			}
		})
	}

	localCases := []struct {
		name string
		args any
	}{
		{"memory_failure_add", map[string]any{
			"command": "zz remote control command",
			"error":   "zz remote control error",
			"fix":     "zz remote control fix",
		}},
		{"memory_failure_list", map[string]any{}},
		{"gleann_session_status", map[string]any{}},
	}

	for _, tc := range localCases {
		tc := tc
		t.Run("local/"+tc.name, func(t *testing.T) {
			st := f.srv.mcpServer.GetTool(tc.name)
			if st == nil {
				t.Fatalf("%s is not registered", tc.name)
			}
			if st.Tool.Annotations.OpenWorldHint == nil || *st.Tool.Annotations.OpenWorldHint {
				t.Fatalf("%s advertises openWorldHint:true, but its handler is local-only", tc.name)
			}
			before := probe.snapshot()
			res := f.call(t, tc.name, tc.args)
			if res.IsError {
				t.Fatalf("%s returned a tool error: %s", tc.name, extractText(res))
			}
			if entries := probe.since(before); len(entries) != 0 {
				t.Fatalf("%s is advertised local-only but made %d HTTP request(s) while a healthy server was configured: %v",
					tc.name, len(entries), entries)
			}
		})
	}
}

func TestMCP06_NoErrorPathToolsAreSafe(t *testing.T) {
	f := mcp06Server(t)
	for _, name := range []string{"gleann_gain", "gleann_session_status", "gleann_session_end"} {
		res := f.call(t, name, "not-an-object")
		if res == nil {
			t.Errorf("%s: nil result on malformed arguments", name)
		}
	}

	resp := f.srv.mcpServer.HandleMessage(context.Background(), []byte(`{
		"jsonrpc":"2.0","id":2,"method":"tools/call",
		"params":{"name":"no_such_tool","arguments":{}}}`))
	rpcErr, ok := resp.(mcpsdk.JSONRPCError)
	if !ok {
		t.Fatalf("unknown tool returned %T, want JSONRPCError", resp)
	}
	if !strings.Contains(strings.ToLower(rpcErr.Error.Message), "not found") {
		t.Errorf("unknown tool error = %q, want a not-found message", rpcErr.Error.Message)
	}
}
