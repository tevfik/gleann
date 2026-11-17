package mcp

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

// h2RootsSession is a client session that answers roots/list requests.
type h2RootsSession struct {
	roots []mcpsdk.Root
	err   error
	ch    chan mcpsdk.JSONRPCNotification
}

func (s *h2RootsSession) Initialize()                                            {}
func (s *h2RootsSession) Initialized() bool                                      { return true }
func (s *h2RootsSession) NotificationChannel() chan<- mcpsdk.JSONRPCNotification { return s.ch }
func (s *h2RootsSession) SessionID() string                                      { return "h2-roots" }
func (s *h2RootsSession) ListRoots(ctx context.Context, _ mcpsdk.ListRootsRequest) (*mcpsdk.ListRootsResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &mcpsdk.ListRootsResult{Roots: s.roots}, nil
}

func h2RestoreActiveRoot(t *testing.T) {
	rootMutex.RLock()
	prev := activeRoot
	rootMutex.RUnlock()
	t.Cleanup(func() {
		rootMutex.Lock()
		activeRoot = prev
		rootMutex.Unlock()
	})
}

func TestH2RootsListChanged_SwitchesIndexDirAndDropsSearchers(t *testing.T) {
	h2RestoreActiveRoot(t)
	srv, _ := h2LLMServer(t, nil)
	_, release, err := srv.acquireSearcher("idx")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if len(srv.searchers) != 1 {
		t.Fatalf("want one cached searcher, have %d", len(srv.searchers))
	}

	ws := filepath.ToSlash(t.TempDir())
	sess := &h2RootsSession{
		roots: []mcpsdk.Root{{URI: "file://" + ws, Name: "ws"}, {URI: "file:///ignored"}},
		ch:    make(chan mcpsdk.JSONRPCNotification, 1),
	}
	ctx := srv.mcpServer.WithContext(context.Background(), sess)
	srv.handleRootsListChanged(ctx, mcpsdk.JSONRPCNotification{})

	rootMutex.RLock()
	got := activeRoot
	rootMutex.RUnlock()
	if got != ws {
		t.Errorf("activeRoot = %q, want %q", got, ws)
	}
	if want := filepath.Join(ws, ".gleann"); srv.config.IndexDir != want {
		t.Errorf("IndexDir = %q, want %q", srv.config.IndexDir, want)
	}
	if len(srv.searchers) != 0 || len(srv.searcherLRU) != 0 {
		t.Errorf("cached searchers survived the root change: %d / %v", len(srv.searchers), srv.searcherLRU)
	}
	// The old index is gone from the new workspace.
	if _, _, err := srv.acquireSearcher("idx"); err == nil {
		t.Error("index of the old workspace still loads after the root change")
	}
}

func TestH2RootsListChanged_NoRootsOrErrorKeepsConfig(t *testing.T) {
	h2RestoreActiveRoot(t)
	for _, sess := range []*h2RootsSession{
		{err: errors.New("client refused")},
		{roots: nil},
	} {
		srv := NewServer(Config{IndexDir: t.TempDir(), EmbeddingProvider: "custom"})
		before := srv.config.IndexDir
		sess.ch = make(chan mcpsdk.JSONRPCNotification, 1)
		srv.handleRootsListChanged(srv.mcpServer.WithContext(context.Background(), sess), mcpsdk.JSONRPCNotification{})
		if srv.config.IndexDir != before {
			t.Errorf("IndexDir changed to %q", srv.config.IndexDir)
		}
		if strings.HasSuffix(srv.config.IndexDir, ".gleann") {
			t.Errorf("unexpected workspace switch")
		}
		srv.Close()
	}
}
