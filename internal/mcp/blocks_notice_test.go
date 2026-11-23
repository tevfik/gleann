package mcp

import (
	"testing"

	"github.com/tevfik/gleann/pkg/memory"
)

// v3 repair test (second independent review, memory-fixes-v2.gemini.md).
// MINOR: contradictionNotice must tolerate a nil block or nil metadata,
// matching the guard in cmd/gleann/cmd_memory.go. Before the fix a nil
// block panicked with a nil pointer dereference.
func TestContradictionNotice_NilBlock(t *testing.T) {
	if got := contradictionNotice(nil); got != "" {
		t.Errorf("contradictionNotice(nil) = %q, want empty", got)
	}
	if got := contradictionNotice(&memory.Block{}); got != "" {
		t.Errorf("contradictionNotice with nil metadata = %q, want empty", got)
	}
	b := &memory.Block{Metadata: map[string]string{
		"has_contradictions": "true",
		"contradicts":        "blk-1,blk-2",
	}}
	if got := contradictionNotice(b); got != " (contradicts: blk-1,blk-2)" {
		t.Errorf("contradictionNotice = %q, want the contradicts notice", got)
	}
}
