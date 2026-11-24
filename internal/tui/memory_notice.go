package tui

import "sync"

// memoryNoticeGate decides whether a failed memory-context refresh is shown to
// the user. A store that stays broken fails on every turn; the first failure is
// reported, repeats are not, and a failure that follows a success is reported
// again. It is safe for concurrent use (the chat commands run off the UI
// goroutine). A nil gate reports every failure.
type memoryNoticeGate struct {
	mu      sync.Mutex
	failing bool
}

// report records the outcome of one refresh and returns true when err should
// be shown to the user.
func (g *memoryNoticeGate) report(err error) bool {
	if err == nil {
		if g != nil {
			g.mu.Lock()
			g.failing = false
			g.mu.Unlock()
		}
		return false
	}
	if g == nil {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.failing {
		return false
	}
	g.failing = true
	return true
}

// reset forgets a recorded failure. It is called wherever the visible messages
// are cleared (/new, /clear, loading a conversation), so the new conversation
// shows its own notice for a store that is still broken. Nil-safe.
func (g *memoryNoticeGate) reset() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.failing = false
	g.mu.Unlock()
}
