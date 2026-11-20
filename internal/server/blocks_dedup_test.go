package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/tevfik/gleann/pkg/memory"
)

// A near-duplicate write is merged into the stored block; both the REST
// response and the remote client report the reinforced block (existing ID and
// content, Confirms > 0). Fails on the old code, which stored a second block.
func TestHandleAddBlock_NearDuplicateReinforcesExisting(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	first := doBlockAdd(t, mux, blockAddRequest{Content: "Always run gofmt and go vet before committing Go code", Scope: "proj"})
	var a memory.Block
	if err := json.NewDecoder(first.Body).Decode(&a); err != nil {
		t.Fatal(err)
	}

	w := doBlockAdd(t, mux, blockAddRequest{Content: "Always run gofmt and go vet before you commit the Go code.", Scope: "proj"})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var b memory.Block
	if err := json.NewDecoder(w.Body).Decode(&b); err != nil {
		t.Fatal(err)
	}
	if b.ID != a.ID || b.Confirms != 1 || b.Content != "Always run gofmt and go vet before you commit the Go code." {
		t.Fatalf("response = %+v, want reinforced %s", b, a.ID)
	}

	srv := httptest.NewServer(mux)
	defer srv.Close()
	rc := memory.NewRemoteClient(srv.URL)
	got, err := rc.AddBlock(&memory.Block{Tier: memory.TierLong, Scope: "proj", Content: "Always run gofmt and go vet before committing Go code!"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != a.ID || got.Confirms != 2 {
		t.Fatalf("remote AddBlock = %+v, want block %s with Confirms 2", got, a.ID)
	}
	list, err := rc.ListScoped("proj", memory.TierLong)
	if err != nil || len(list) != 1 {
		t.Fatalf("stored blocks = %d, %v; want 1", len(list), err)
	}
}

// The retrieval floor applies to GET /api/blocks/search: weak single-word
// matches are not returned. Fails on the old code.
func TestHandleSearchBlocks_RetrievalFloor(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	doBlockAdd(t, mux, blockAddRequest{Content: "Restart the billing service after rotating the database credentials"})

	search := func(q string) []memory.Block {
		r := httptest.NewRequest(http.MethodGet, "/api/blocks/search?q="+url.QueryEscape(q), nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		var resp struct {
			Blocks []memory.Block `json:"blocks"`
		}
		_ = json.NewDecoder(w.Body).Decode(&resp)
		return resp.Blocks
	}
	if got := search("how do I configure the nginx service for production traffic"); len(got) != 0 {
		t.Fatalf("weak match returned: %v", got)
	}
	if got := search("rotate database credentials"); len(got) != 1 {
		t.Fatalf("real match lost: %v", got)
	}
}
