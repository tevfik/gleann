//go:build treesitter && !windows

package indexer

import (
	"strings"
	"testing"
)

func TestExtractRationale(t *testing.T) {
	source := `package mypkg

// WHY: We use a mutex here because KuzuDB connections are not goroutine-safe.
func safePut(key string) {
	// normal comment
	mu.Lock()
	// NOTE: This must be held during the entire transaction.
	defer mu.Unlock()
}

// HACK: Temporary workaround for the CSV import limitation.
func importCSV() {}

# TODO: Support IMPLEMENTS edges for Python
def analyze():
    pass

// FIXME: Memory leak when processing large graphs.
// IMPORTANT: Do not remove the dedup step.
func processGraph() {}
`

	rationale := extractRationale(source)

	// Should find WHY on line 3
	if r, ok := rationale[3]; ok {
		if r.Tag != "WHY" {
			t.Errorf("line 3: expected tag WHY, got %s", r.Tag)
		}
		if !strings.Contains(r.Content, "mutex") {
			t.Errorf("line 3: expected content about mutex, got %q", r.Content)
		}
	} else {
		t.Error("expected rationale on line 3 (WHY comment)")
	}

	// NOTE on line 7
	if r, ok := rationale[7]; ok {
		if r.Tag != "NOTE" {
			t.Errorf("line 7: expected tag NOTE, got %s", r.Tag)
		}
	} else {
		t.Error("expected rationale on line 7 (NOTE comment)")
	}

	// HACK on line 11
	if r, ok := rationale[11]; ok {
		if r.Tag != "HACK" {
			t.Errorf("line 11: expected tag HACK, got %s", r.Tag)
		}
	} else {
		t.Error("expected rationale on line 11 (HACK comment)")
	}

	// TODO on line 14 (# prefix)
	if r, ok := rationale[14]; ok {
		if r.Tag != "TODO" {
			t.Errorf("line 14: expected tag TODO, got %s", r.Tag)
		}
	} else {
		t.Error("expected rationale on line 14 (TODO comment with # prefix)")
	}

	// FIXME on line 18
	if _, ok := rationale[18]; !ok {
		t.Error("expected rationale on line 18 (FIXME comment)")
	}

	// IMPORTANT on line 19
	if _, ok := rationale[19]; !ok {
		t.Error("expected rationale on line 19 (IMPORTANT comment)")
	}

	// Normal comment on line 5 should NOT be rationale
	if _, ok := rationale[5]; ok {
		t.Error("line 5 should not be rationale (normal comment)")
	}

	t.Logf("✅ extractRationale found %d rationale comments", len(rationale))
}

func TestAttachRationale(t *testing.T) {
	rationale := map[int]rationaleComment{
		3:  {Line: 3, Tag: "WHY", Content: "We use a mutex here"},
		7:  {Line: 7, Tag: "NOTE", Content: "Must be held during entire tx"},
		20: {Line: 20, Tag: "HACK", Content: "Temporary workaround"},
	}

	// Symbol at lines 4-9: should pick up WHY (line 3, within 3-line preamble) and NOTE (line 7, within range)
	doc := attachRationale(rationale, 4, 9)
	if !strings.Contains(doc, "[WHY]") {
		t.Errorf("expected [WHY] in doc for lines 4-9, got %q", doc)
	}
	if !strings.Contains(doc, "[NOTE]") {
		t.Errorf("expected [NOTE] in doc for lines 4-9, got %q", doc)
	}
	if strings.Contains(doc, "[HACK]") {
		t.Errorf("should NOT include [HACK] (line 20) in doc for lines 4-9, got %q", doc)
	}

	// Symbol at lines 18-22: should pick up HACK (line 20, within range)
	doc2 := attachRationale(rationale, 18, 22)
	if !strings.Contains(doc2, "[HACK]") {
		t.Errorf("expected [HACK] in doc for lines 18-22, got %q", doc2)
	}

	// Empty rationale
	doc3 := attachRationale(map[int]rationaleComment{}, 1, 10)
	if doc3 != "" {
		t.Errorf("expected empty doc for empty rationale, got %q", doc3)
	}

	// Symbol far from any rationale
	doc4 := attachRationale(rationale, 50, 60)
	if doc4 != "" {
		t.Errorf("expected empty doc for lines 50-60, got %q", doc4)
	}
}
