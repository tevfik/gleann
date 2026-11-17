package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/memory"
)

// bFakeServer is a loopback stand-in for a running `gleann serve`: it answers
// the health probe and the /api/blocks routes the CLI routes memory commands
// through when a server holds the bbolt lock.
type bFakeServer struct {
	*httptest.Server
	mu     sync.Mutex
	blocks []memory.Block
	calls  []string
	fail   bool // every /api route answers 500
}

func bNewFakeServer(t *testing.T) *bFakeServer {
	t.Helper()
	s := &bFakeServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	t.Setenv("GLEANN_REMOTE_ADDR", s.URL)
	memory.ResetRemoteForTesting()
	t.Cleanup(memory.ResetRemoteForTesting)
	return s
}

func (s *bFakeServer) called(want string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.calls {
		if c == want {
			return true
		}
	}
	return false
}

func (s *bFakeServer) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/health" {
		_ = json.NewEncoder(w).Encode(map[string]string{"engine": "gleann-go", "status": "ok"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, r.Method+" "+r.URL.Path)
	if s.fail {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "server exploded"})
		return
	}
	writeBlocks := func(bs []memory.Block) {
		_ = json.NewEncoder(w).Encode(map[string]any{"blocks": bs})
	}
	switch {
	case r.URL.Path == "/api/blocks" && r.Method == http.MethodPost:
		var req struct {
			Content string   `json:"content"`
			Tier    string   `json:"tier"`
			Label   string   `json:"label"`
			Tags    []string `json:"tags"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		b := memory.Block{ID: "remote0000000001", Tier: memory.Tier(req.Tier), Label: req.Label,
			Content: req.Content, Tags: req.Tags, CreatedAt: time.Now()}
		s.blocks = append(s.blocks, b)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(b)
	case r.URL.Path == "/api/blocks" && r.Method == http.MethodGet:
		var out []memory.Block
		for _, b := range s.blocks {
			if tier := r.URL.Query().Get("tier"); tier == "" || string(b.Tier) == tier {
				out = append(out, b)
			}
		}
		writeBlocks(out)
	case r.URL.Path == "/api/blocks" && r.Method == http.MethodDelete:
		n := len(s.blocks)
		s.blocks = nil
		_ = json.NewEncoder(w).Encode(map[string]int{"deleted": n})
	case r.URL.Path == "/api/blocks/search":
		var out []memory.Block
		for _, b := range s.blocks {
			if strings.Contains(b.Content, r.URL.Query().Get("q")) {
				out = append(out, b)
			}
		}
		writeBlocks(out)
	case r.URL.Path == "/api/blocks/stats":
		_ = json.NewEncoder(w).Encode(memory.Stats{LongTermCount: len(s.blocks), TotalCount: len(s.blocks), DiskSizeBytes: 2048})
	case r.URL.Path == "/api/blocks/context":
		w.Header().Set("Content-Type", "application/xml")
		if len(s.blocks) == 0 {
			return
		}
		_, _ = w.Write([]byte("<memory>" + s.blocks[0].Content + "</memory>"))
	case r.URL.Path == "/api/blocks/compact":
		_ = json.NewEncoder(w).Encode(map[string]int{"pruned": 3})
	case strings.HasPrefix(r.URL.Path, "/api/blocks/") && r.Method == http.MethodDelete:
		_ = json.NewEncoder(w).Encode(map[string]int{"deleted": 1})
	default:
		http.NotFound(w, r)
	}
}

func TestCLIBMemoryViaRunningServer(t *testing.T) {
	e := newCLIEnv(t)
	srv := bNewFakeServer(t)

	r := e.run("memory", "context")
	wantExit(t, r, 0)
	mustContain(t, "remote empty context", r.Out, "(empty memory context)")

	r = e.run("memory", "remember", "served fact", "--tag", "x")
	wantExit(t, r, 0)
	mustContain(t, "remote remember", r.Out, "Remembered [remote00]: served fact  (via running server)")
	if !srv.called("POST /api/blocks") {
		t.Fatal("remember did not go through the server")
	}
	r = e.run("memory", "add", "medium", "served note")
	wantExit(t, r, 0)
	mustContain(t, "remote add", r.Out, "Added to medium [remote00]: served note  (via running server)")

	r = e.run("memory", "list", "--tier", "medium", "--json")
	wantExit(t, r, 0)
	var bs []memory.Block
	if err := json.Unmarshal([]byte(r.Out), &bs); err != nil || len(bs) != 1 || bs[0].Content != "served note" {
		t.Errorf("remote list = %q (%v)", r.Out, err)
	}
	r = e.run("memory", "search", "served")
	wantExit(t, r, 0)
	mustContain(t, "remote search", r.Out, "Found 2 memory block(s)")

	r = e.run("memory", "context", "--verbose")
	wantExit(t, r, 0)
	mustContain(t, "remote context", r.Out, "<memory>served fact</memory>")
	mustContain(t, "remote context verbose", r.Err, "memory budget: not reported by a running server")

	r = e.run("memory", "stats")
	wantExit(t, r, 0)
	mustContain(t, "remote stats", r.Out, "Memory Statistics (via running server)", "Long-term:   2 blocks", "Disk size:   2.0 KB")

	r = e.run("memory", "forget", "served fact")
	wantExit(t, r, 0)
	mustContain(t, "remote forget", r.Out, `Forgot 1 memory block(s) matching "served fact"  (via running server)`)

	r = e.run("memory", "compact")
	wantExit(t, r, 0)
	mustContain(t, "remote compact", r.Out, "3 stale/unreliable block(s) pruned (min validity: 0.20) (via running server)")

	r = e.run("memory", "clear", "--tier", "long")
	wantExit(t, r, 0)
	mustContain(t, "remote clear tier", r.Out, `Cleared 2 memory block(s) from tier "long"  (via running server)`)
	r = e.run("memory", "clear")
	wantExit(t, r, 0)
	mustContain(t, "remote clear", r.Out, "Cleared 0 memory block(s) from all tiers  (via running server)")

	// Nothing was written to the local store.
	t.Setenv("GLEANN_REMOTE_ADDR", "off")
	if got := bMemList(t, e); len(got) != 0 {
		t.Errorf("remote commands wrote locally: %+v", got)
	}
}

// When the server answers with errors the commands fall back to the local
// store (compact reports the error instead).
func TestCLIBMemoryServerErrorsFallBackToLocal(t *testing.T) {
	e := newCLIEnv(t)
	srv := bNewFakeServer(t)
	srv.fail = true

	r := e.run("memory", "remember", "local fallback fact")
	wantExit(t, r, 0)
	mustNotContain(t, "fallback remember", r.Out, "via running server")
	mustContain(t, "fallback remember", r.Out, "Remembered [")
	wantExit(t, e.run("memory", "add", "medium", "local medium"), 0)

	r = e.run("memory", "list")
	wantExit(t, r, 0)
	mustContain(t, "fallback list", r.Out, "Memories (2, all tiers)", "local fallback fact")
	r = e.run("memory", "search", "fallback")
	wantExit(t, r, 0)
	mustContain(t, "fallback search", r.Out, "Found 1 memory block(s)")
	r = e.run("memory", "stats")
	wantExit(t, r, 0)
	mustContain(t, "fallback stats", r.Out, "Store path:")
	r = e.run("memory", "context")
	wantExit(t, r, 0)
	mustContain(t, "fallback context", r.Out, "local fallback fact")
	r = e.run("memory", "forget", "fallback")
	wantExit(t, r, 0)
	mustContain(t, "fallback forget", r.Out, "Forgot 1 memory block(s)")
	mustNotContain(t, "fallback forget", r.Out, "via running server")
	r = e.run("memory", "clear")
	wantExit(t, r, 0)
	mustContain(t, "fallback clear", r.Out, "Cleared 1 memory block(s) from all tiers")
	mustNotContain(t, "fallback clear", r.Out, "via running server")

	r = e.run("memory", "compact")
	wantExit(t, r, 1)
	mustContain(t, "remote compact error", r.Err, "error (remote):", "server exploded")
}

func TestCLIBRemoteMemoryClientRespectsOff(t *testing.T) {
	newCLIEnv(t) // GLEANN_REMOTE_ADDR=off
	memory.ResetRemoteForTesting()
	if c := remoteMemoryClient(); c != nil {
		t.Fatal("remoteMemoryClient must be nil with GLEANN_REMOTE_ADDR=off")
	}
	bNewFakeServer(t)
	if c := remoteMemoryClient(); c == nil {
		t.Fatal("remoteMemoryClient must find the running server")
	}
}
