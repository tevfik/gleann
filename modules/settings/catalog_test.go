package settings

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCatalog downloads by flipping installed; "slow" waits for release and
// "bad" fails.
type fakeCatalog struct {
	mu        sync.Mutex
	installed map[string]bool
	release   chan struct{}
}

func (f *fakeCatalog) catalog() *Catalog {
	return &Catalog{
		List: func() []Model {
			f.mu.Lock()
			defer f.mu.Unlock()
			return []Model{
				{ID: "small", Name: "Small", Group: "b", Installed: f.installed["small"]},
				{ID: "slow", Name: "Slow", Group: "b", Installed: f.installed["slow"]},
				{ID: "bad", Name: "Bad", Group: "a"},
				{ID: "indra", Name: "Indra", Group: "a", Blocked: "accept the license first"},
			}
		},
		Download: func(ctx context.Context, id string, report func(Progress)) error {
			switch id {
			case "bad":
				return errors.New("checksum mismatch")
			case "slow":
				report(Progress{Done: 5, Total: 10, Message: "half"})
				<-f.release
			}
			f.mu.Lock()
			f.installed[id] = true
			f.mu.Unlock()
			return nil
		},
	}
}

func list(t *testing.T, url string) map[string]ModelState {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct{ Models []ModelState }
	_ = json.NewDecoder(resp.Body).Decode(&body)
	out := map[string]ModelState{}
	for _, m := range body.Models {
		out[m.ID] = m
	}
	return out
}

func post(t *testing.T, url, id string) int {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(`{"id":"`+id+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// waitFor polls the list until cond holds.
func waitFor(t *testing.T, url string, cond func(map[string]ModelState) bool) map[string]ModelState {
	t.Helper()
	for i := 0; i < 200; i++ {
		if l := list(t, url); cond(l) {
			return l
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not reached")
	return nil
}

func TestCatalogDownloads(t *testing.T) {
	f := &fakeCatalog{installed: map[string]bool{}, release: make(chan struct{})}
	ts := httptest.NewServer(f.catalog().Handler(context.Background(), nil))
	defer ts.Close()

	l := list(t, ts.URL)
	if l["small"].State != "available" || l["indra"].State != "blocked" {
		t.Fatalf("initial states: %+v", l)
	}

	if code := post(t, ts.URL, "small"); code != http.StatusAccepted {
		t.Fatalf("POST small: %d", code)
	}
	waitFor(t, ts.URL, func(l map[string]ModelState) bool { return l["small"].State == "installed" })

	// A running download reports progress and cannot be started twice.
	post(t, ts.URL, "slow")
	l = waitFor(t, ts.URL, func(l map[string]ModelState) bool { return l["slow"].Progress.Done == 5 })
	if l["slow"].State != "downloading" || l["slow"].Progress.Total != 10 {
		t.Errorf("slow: %+v", l["slow"])
	}
	if code := post(t, ts.URL, "slow"); code != http.StatusConflict {
		t.Errorf("second POST while downloading: %d", code)
	}
	close(f.release)
	waitFor(t, ts.URL, func(l map[string]ModelState) bool { return l["slow"].State == "installed" })

	// A failure is kept until it is retried.
	post(t, ts.URL, "bad")
	l = waitFor(t, ts.URL, func(l map[string]ModelState) bool { return l["bad"].State == "failed" })
	if l["bad"].Error != "checksum mismatch" {
		t.Errorf("bad: %+v", l["bad"])
	}

	if code := post(t, ts.URL, "indra"); code != http.StatusConflict {
		t.Errorf("blocked model: %d", code)
	}
	if code := post(t, ts.URL, "nope"); code != http.StatusNotFound {
		t.Errorf("unknown model: %d", code)
	}
	resp, _ := http.Post(ts.URL, "text/plain", strings.NewReader(`{"id":"small"}`))
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain POST: %d", resp.StatusCode)
	}
}

func TestCatalogAuth(t *testing.T) {
	f := &fakeCatalog{installed: map[string]bool{}}
	ts := httptest.NewServer(f.catalog().Handler(context.Background(), func(*http.Request) bool { return false }))
	defer ts.Close()
	if code := post(t, ts.URL, "small"); code != http.StatusUnauthorized {
		t.Errorf("POST without auth: %d", code)
	}
}

func TestHumanSize(t *testing.T) {
	for b, want := range map[int64]string{466 << 20: "466 MB", 2254857830: "2.1 GB", 2048: "2 KB", 12: "12 B"} {
		if got := HumanSize(b); got != want {
			t.Errorf("HumanSize(%d) = %q, want %q", b, got, want)
		}
	}
}
