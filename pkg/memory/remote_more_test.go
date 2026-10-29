package memory

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRemoteClient_ExtendedMethods(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/health" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"engine":"gleann-go","status":"ok"}`))

		case r.URL.Path == "/api/blocks" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"blocks":[{"id":"blk-1","label":"test","tier":"short","content":"hello"}]}`))

		case r.URL.Path == "/api/blocks/search" && r.Method == http.MethodGet:
			q := r.URL.Query().Get("q")
			scope := r.URL.Query().Get("scope")
			if q == "fail" {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"search failed"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			if scope != "" {
				_, _ = w.Write([]byte(`{"blocks":[{"id":"blk-scoped","label":"scoped","content":"` + scope + `"}]}`))
			} else {
				_, _ = w.Write([]byte(`{"blocks":[{"id":"blk-search","label":"search","content":"` + q + `"}]}`))
			}

		case r.URL.Path == "/api/blocks/context" && r.Method == http.MethodGet:
			scope := r.URL.Query().Get("scope")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<memory_context scope="` + scope + `">test context</memory_context>`))

		case r.URL.Path == "/api/blocks" && r.Method == http.MethodDelete:
			tier := r.URL.Query().Get("tier")
			if tier == "error" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`plain text error`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"deleted":5}`))

		case r.URL.Path == "/api/blocks/compact" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"pruned":3}`))

		case r.URL.Path == "/api/blocks/stats" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(Stats{TotalCount: 10})

		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client := &RemoteClient{
		base:   ts.URL,
		client: ts.Client(),
	}

	// 1. List
	blocks, err := client.List(TierShort)
	if err != nil || len(blocks) != 1 {
		t.Fatalf("List failed: %v", err)
	}

	// 2. Search & SearchScoped
	results, err := client.Search("gleann")
	if err != nil || len(results) != 1 || results[0].ID != "blk-search" {
		t.Fatalf("Search failed: %v", err)
	}

	scopedResults, err := client.SearchScoped("project-a", "gleann")
	if err != nil || len(scopedResults) != 1 || scopedResults[0].ID != "blk-scoped" {
		t.Fatalf("SearchScoped failed: %v", err)
	}

	// Error branch in search
	if _, err := client.Search("fail"); err == nil {
		t.Fatalf("expected search error")
	}

	// 3. Context
	ctxXML, err := client.Context("my-scope")
	if err != nil || ctxXML == "" {
		t.Fatalf("Context failed: %v", err)
	}

	// 4. Clear
	deleted, err := client.Clear(TierShort)
	if err != nil || deleted != 5 {
		t.Fatalf("Clear failed: %v, deleted: %d", err, deleted)
	}
	if _, err := client.Clear("error"); err == nil {
		t.Fatalf("expected clear error with plain text")
	}

	// 5. Compact
	pruned, err := client.Compact(0.5)
	if err != nil || pruned != 3 {
		t.Fatalf("Compact failed: %v, pruned: %d", err, pruned)
	}

	// 6. Add convenience
	b, err := client.Add(TierLong, "mylabel", "mycontent", []string{"tag1"})
	// ts doesn't handle POST /api/blocks in this handler, so it should return 404 error
	if err == nil && b != nil {
		t.Fatalf("expected error on unhandled POST /api/blocks")
	}
}
