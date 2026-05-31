//go:build treesitter

package indexer

import (
	"testing"

	"github.com/tevfik/gleann/internal/graph/kuzu"
)

func TestDedupeFolders(t *testing.T) {
	in := []kuzu.FolderNode{
		{VPath: "a/b", Name: "b"},
		{VPath: "a/b", Name: "b-dup"}, // duplicate VPath, different name
		{VPath: "a/c", Name: "c"},
		{VPath: "a/b", Name: "b-dup2"},
	}
	out := dedupeFolders(in)
	if len(out) != 2 {
		t.Fatalf("dedupeFolders length = %d, want 2", len(out))
	}
	// First occurrence wins.
	if out[0].Name != "b" || out[1].Name != "c" {
		t.Errorf("unexpected order/contents: %+v", out)
	}
}

func TestDedupeDocs(t *testing.T) {
	in := []kuzu.DocumentNode{
		{VPath: "x.md", Name: "X"},
		{VPath: "y.md", Name: "Y"},
		{VPath: "x.md", Name: "X-dup"},
	}
	out := dedupeDocs(in)
	if len(out) != 2 {
		t.Fatalf("dedupeDocs length = %d, want 2", len(out))
	}
}

func TestDedupeHeadings(t *testing.T) {
	in := []kuzu.HeadingNode{
		{ID: "h1", Name: "Intro", Level: 1},
		{ID: "h2", Name: "Body", Level: 2},
		{ID: "h1", Name: "Intro-dup", Level: 1},
	}
	out := dedupeHeadings(in)
	if len(out) != 2 {
		t.Fatalf("dedupeHeadings length = %d, want 2", len(out))
	}
}

func TestDedupeEmptyAndSingle(t *testing.T) {
	if got := dedupeFolders(nil); got != nil && len(got) != 0 {
		t.Errorf("dedupeFolders(nil) = %v, want empty", got)
	}
	one := []kuzu.DocumentNode{{VPath: "only"}}
	if got := dedupeDocs(one); len(got) != 1 {
		t.Errorf("dedupeDocs(single) length = %d, want 1", len(got))
	}
}
