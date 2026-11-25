package memory

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Maintainer decision (v122): a content phrase matching several blocks deletes
// nothing unless the caller asked for all of them.
func TestForgetMultiMatchRefusedWithoutAll(t *testing.T) {
	mgr := newForgetStore(t)
	putBlockWithID(t, mgr, "1111111111111111", "deploy runs on friday")
	putBlockWithID(t, mgr, "2222222222222222", "deploy needs a green CI")
	putBlockWithID(t, mgr, "3333333333333333", "unrelated fact")

	n, err := mgr.Forget("deploy")
	var amb *ForgetAmbiguousError
	if !errors.As(err, &amb) || n != 0 {
		t.Fatalf("Forget(deploy) = %d, %v; want *ForgetAmbiguousError and 0", n, err)
	}
	if len(amb.Matches) != 2 {
		t.Fatalf("matches = %+v, want 2", amb.Matches)
	}
	for _, want := range []string{"11111111", "22222222", "deploy runs on friday", "[long]", "--all"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
	if blocks, _ := mgr.store.List(""); len(blocks) != 3 {
		t.Fatalf("a refused forget deleted blocks: %d left", len(blocks))
	}

	n, err = mgr.ForgetAll("deploy")
	if err != nil || n != 2 {
		t.Fatalf("ForgetAll(deploy) = %d, %v; want 2", n, err)
	}
	if blocks, _ := mgr.store.List(""); len(blocks) != 1 {
		t.Fatalf("left %d blocks, want 1", len(blocks))
	}
}

func TestForgetSingleContentMatchStillDeletes(t *testing.T) {
	mgr := newForgetStore(t)
	putBlockWithID(t, mgr, "1111111111111111", "deploy runs on friday")
	putBlockWithID(t, mgr, "2222222222222222", "other")
	if n, err := mgr.Forget("friday"); err != nil || n != 1 {
		t.Fatalf("Forget(friday) = %d, %v", n, err)
	}
}

// The remote client turns a 409 into the same typed error and sends ?all=true.
func TestRemoteForgetConflictAndAll(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		if r.URL.Query().Get("all") == "true" {
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "deleted": 2})
			return
		}
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]any{"error": "x", "matches": []ForgetMatch{
			{ID: "1111111111111111", Tier: TierLong, Excerpt: "a"}, {ID: "2222222222222222", Tier: TierLong, Excerpt: "b"}}})
	}))
	defer srv.Close()
	c := NewRemoteClient(srv.URL)

	_, err := c.Forget("deploy", false)
	var amb *ForgetAmbiguousError
	if !errors.As(err, &amb) || len(amb.Matches) != 2 {
		t.Fatalf("Forget over 409 = %v, want *ForgetAmbiguousError with 2 matches", err)
	}
	n, err := c.Forget("deploy", true)
	if err != nil || n != 2 || gotQuery != "all=true" {
		t.Fatalf("Forget all = %d, %v, query %q", n, err, gotQuery)
	}
}
