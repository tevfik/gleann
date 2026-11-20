package mcp

import (
	"context"
	"strings"
	"testing"
)

// memory_search returns nothing for a loosely matching query, and
// memory_remember reports a near-duplicate as reinforced. Both fail on the
// old code (any single shared word matched; near duplicates were stored as
// new blocks).
func TestMemorySearchFloorAndNearDuplicateRemember(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()
	ctx := context.Background()

	res, _ := s.handleMemoryRemember(ctx, mkCallArgs(map[string]any{
		"content": "Always run gofmt and go vet before committing Go code",
		"scope":   "proj",
	}))
	if res.IsError {
		t.Fatalf("remember: %s", textOf(res))
	}
	res, _ = s.handleMemoryRemember(ctx, mkCallArgs(map[string]any{
		"content": "Always run gofmt and go vet before you commit the Go code.",
		"scope":   "proj",
	}))
	if res.IsError || !strings.Contains(textOf(res), "reinforced 2x") {
		t.Fatalf("near duplicate not reported as reinforced: %s", textOf(res))
	}

	res, _ = s.handleMemorySearch(ctx, mkCallArgs(map[string]any{
		"query": "how do I configure the nginx code review policy for production",
		"scope": "proj",
	}))
	if !strings.Contains(textOf(res), "No memories found") {
		t.Fatalf("weak match returned: %s", textOf(res))
	}
	res, _ = s.handleMemorySearch(ctx, mkCallArgs(map[string]any{"query": "gofmt go vet before committing", "scope": "proj"}))
	if !strings.Contains(textOf(res), "Found 1 memory block") {
		t.Fatalf("real match lost: %s", textOf(res))
	}
}
