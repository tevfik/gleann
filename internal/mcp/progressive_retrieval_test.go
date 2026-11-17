package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The progressive retrieval flow: gleann_search_ids returns compact refs,
// gleann_fetch / gleann_get resolve them to full text.
func TestProgressiveRetrieval_SearchIDsFetchGet(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	s := newFederatedServer(t)

	raw := callTool(t, s.handleSearchIDs, map[string]interface{}{"index": "alpha", "query": "kiwi parser tokens", "top_k": 5.0})
	var hits []struct {
		Ref    string `json:"ref"`
		ID     int64  `json:"id"`
		Source string `json:"source"`
		Peek   string `json:"peek"`
	}
	if err := json.Unmarshal([]byte(raw), &hits); err != nil {
		t.Fatalf("search_ids output is not JSON: %v\n%s", err, raw)
	}
	if len(hits) != 2 {
		t.Fatalf("want the 2 alpha passages, got %d: %s", len(hits), raw)
	}
	for _, h := range hits {
		if h.Ref != fmt.Sprintf("alpha:%d", h.ID) || h.Source != "a.go" || !strings.HasPrefix(h.Peek, "kiwi parser") {
			t.Errorf("malformed compact result: %+v", h)
		}
	}

	// fetch: full text of the requested passages.
	fetched := callTool(t, s.handleFetch, map[string]interface{}{
		"index": "alpha", "ids": []interface{}{float64(hits[0].ID), float64(hits[1].ID)},
	})
	if !strings.Contains(fetched, fmt.Sprintf("--- ID:%d [a.go] ---", hits[0].ID)) || !strings.Contains(fetched, "kiwi parser handles tokens") {
		t.Errorf("fetch output lacks the passage:\n%s", fetched)
	}

	got := callTool(t, s.handleGet, map[string]interface{}{"ref": hits[0].Ref})
	if !strings.HasPrefix(got, "Ref: "+hits[0].Ref+" [a.go]") || !strings.Contains(got, "kiwi parser") {
		t.Errorf("get output = %q", got)
	}
}

func TestSearchIDs_FilteredAndEmpty(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	s := newFederatedServer(t)
	// A filter that excludes everything yields an empty JSON array.
	out := callTool(t, s.handleSearchIDs, map[string]interface{}{
		"index": "alpha", "query": "kiwi",
		"filters":       []interface{}{map[string]interface{}{"field": "source", "operator": "eq", "value": "nonexistent.go"}},
		"include_tests": false, "kind": "code",
	})
	if out != "[]" {
		t.Errorf("want [], got %s", out)
	}
}

func TestProgressiveRetrieval_Errors(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	s := newFederatedServer(t)
	cases := []struct {
		name string
		h    toolHandler
		args map[string]interface{}
		want string
	}{
		{"fetch without ids", s.handleFetch, map[string]interface{}{"index": "alpha"}, "ids must be a non-empty array"},
		{"fetch empty ids", s.handleFetch, map[string]interface{}{"index": "alpha", "ids": []interface{}{}}, "ids must be a non-empty array"},
		{"fetch unknown ids", s.handleFetch, map[string]interface{}{"index": "alpha", "ids": []interface{}{float64(9999)}}, "passage 9999 not found"},
		{"fetch private index", s.handleFetch, map[string]interface{}{"index": "hidden", "ids": []interface{}{1.0}}, "access denied"},
		{"search_ids private index", s.handleSearchIDs, map[string]interface{}{"index": "hidden", "query": "x"}, "access denied"},
		{"get without colon", s.handleGet, map[string]interface{}{"ref": "alpha"}, "ref must be in the form"},
		{"get leading colon", s.handleGet, map[string]interface{}{"ref": ":5"}, "ref must be in the form"},
		{"get non numeric id", s.handleGet, map[string]interface{}{"ref": "alpha:abc"}, "invalid passage ID"},
		{"get missing passage", s.handleGet, map[string]interface{}{"ref": "alpha:424242"}, "passage not found"},
		{"get private index", s.handleGet, map[string]interface{}{"ref": "hidden:1"}, "access denied"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := callToolResult(t, tc.h, tc.args)
			text := textOf(res)
			if !res.IsError || !strings.Contains(text, tc.want) {
				t.Errorf("want error containing %q, got error=%v %q", tc.want, res.IsError, text)
			}
		})
	}
}

func TestIndexBusyError_MessagesByCause(t *testing.T) {
	s := NewServer(Config{IndexDir: t.TempDir()})
	t.Cleanup(s.Close)

	if res := s.indexBusyError("x", nil); res != nil {
		t.Errorf("nil error must give a nil result, got %v", res)
	}

	plain := s.indexBusyError("x", fmt.Errorf("corrupt header"))
	if !plain.IsError || !strings.Contains(textOf(plain), `Error loading index "x": corrupt header`) {
		t.Errorf("plain failure message = %q", textOf(plain))
	}

	for _, cause := range []string{"database is locked", "operation timeout", "bbolt: open", "resource temporarily unavailable", "exit status 1"} {
		res := s.indexBusyError("x", fmt.Errorf("%s", cause))
		if !strings.Contains(textOf(res), "currently being built or synced") {
			t.Errorf("%q must be reported as busy, got %q", cause, textOf(res))
		}
	}

	// A running sync task is reported busy even for an unrelated error, with
	// its progress when it has any.
	task := &syncTask{indexName: "x", done: make(chan struct{}), totalItems: 200, itemsDone: 50, pct: 25, etaSec: 30}
	s.syncMu.Lock()
	s.syncTasks["x"] = task
	s.syncMu.Unlock()
	if !s.isSyncRunning("x") {
		t.Fatal("task with an open done channel must be running")
	}
	res := s.indexBusyError("x", fmt.Errorf("anything"))
	if !strings.Contains(textOf(res), "(25.0% complete, 50/200 items, ETA: 30s)") {
		t.Errorf("progress missing from %q", textOf(res))
	}

	close(task.done)
	if s.isSyncRunning("x") {
		t.Error("a finished task must not count as running")
	}
	if s.isSyncRunning("other") {
		t.Error("unknown index has no sync running")
	}
	// Once the task is done an unrelated error is no longer reported as busy.
	res = s.indexBusyError("x", fmt.Errorf("anything"))
	if strings.Contains(textOf(res), "currently being built") {
		t.Errorf("finished sync must not mask the real error: %q", textOf(res))
	}
}
