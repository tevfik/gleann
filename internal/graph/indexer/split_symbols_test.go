package indexer

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/modules/chunking"
)

// Issue #12: the chunker cuts an oversized declaration into "Name_partN"
// passages for search; the graph must still see one symbol per declaration.

func bigGoFunc(name string, lines int) string {
	var b strings.Builder
	b.WriteString("func " + name + "() {\n")
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, "\thelper(%d)\n\tx = append(x, len(x)+int(int64(%d)))\n", i, i)
	}
	b.WriteString("}\n")
	return b.String()
}

func TestExtractFileSplitFunctionIsOneSymbol(t *testing.T) {
	root := t.TempDir()
	src := "package p\n\nfunc helper(int) {}\n\nvar x []int\n\n" + bigGoFunc("Big", 150) +
		"\nfunc foo_part2() {}\n\nfunc Caller() { Big() }\n"
	abs := filepath.Join(root, "p.go")

	// Precondition: the chunker really does split Big (else this test is vacuous).
	split := 0
	for _, ch := range chunking.NewASTChunker(chunking.DefaultASTChunkerConfig()).ChunkCode(src, abs) {
		if strings.HasPrefix(ch.Name, "Big_part") {
			split++
		}
	}
	if split < 2 {
		t.Fatalf("fixture not split by the chunker (%d parts)", split)
	}

	fg := ExtractFile("m", root, abs, src)
	var big, foo int
	for _, s := range fg.Symbols {
		if s.File == "" {
			continue
		}
		switch s.Name {
		case "Big":
			big++
			if s.FQN != "m.Big" || s.Line != 7 || s.EndLine != 7+150*2+1 {
				t.Errorf("Big = %+v, want m.Big spanning its whole declaration", s)
			}
		case "foo_part2":
			foo++ // a genuine declaration that merely looks like a piece
		}
		if strings.Contains(s.Name, "_part") && s.Name != "foo_part2" {
			t.Errorf("split symbol leaked: %s", s.FQN)
		}
	}
	if big != 1 {
		t.Errorf("Big declared %d times, want 1", big)
	}
	if foo != 1 {
		t.Errorf("genuine foo_part2 symbols: %d, want 1", foo)
	}
	declares := 0
	for _, d := range fg.Declares {
		if d.SymbolFQN == "m.Big" {
			declares++
		}
		if strings.Contains(d.SymbolFQN, "Big_part") {
			t.Errorf("DECLARES for a piece: %s", d.SymbolFQN)
		}
	}
	if declares != 1 {
		t.Errorf("DECLARES m.Big: %d, want 1", declares)
	}
	// The split function is the caller of what its body calls, and the callee
	// of what calls it, under the one logical FQN.
	var bigToHelper, callerToBig bool
	for _, e := range fg.Calls {
		if strings.Contains(e.CallerFQN+e.CalleeFQN, "_part") {
			t.Errorf("edge on a piece: %+v", e)
		}
		bigToHelper = bigToHelper || (e.CallerFQN == "m.Big" && e.CalleeFQN == "m.helper")
		callerToBig = callerToBig || (e.CallerFQN == "m.Caller" && e.CalleeFQN == "m.Big")
	}
	if !bigToHelper || !callerToBig {
		t.Errorf("Big->helper=%v Caller->Big=%v", bigToHelper, callerToBig)
	}
}

func TestMergeSplitChunks(t *testing.T) {
	in := []chunking.CodeChunk{
		{Name: "A", NodeType: "function", StartLine: 1, EndLine: 3},
		{Name: "B_part1", LogicalName: "B", NodeType: "function", StartLine: 5, EndLine: 40},
		{Name: "B_part2", LogicalName: "B", NodeType: "function", StartLine: 39, EndLine: 80},
		{Name: "B_part3", LogicalName: "B", NodeType: "function", StartLine: 79, EndLine: 90},
		{Name: "B_part1", NodeType: "method", StartLine: 100, EndLine: 101}, // genuine name, not a piece
		{Name: "C", LogicalName: "", NodeType: "type", StartLine: 120, EndLine: 130},
	}
	out := mergeSplitChunks(in)
	if len(out) != 4 {
		t.Fatalf("got %d chunks: %+v", len(out), out)
	}
	if out[1].Name != "B" || out[1].StartLine != 5 || out[1].EndLine != 90 || out[1].LogicalName != "" {
		t.Errorf("merged B = %+v", out[1])
	}
	if out[2].Name != "B_part1" || out[2].StartLine != 100 {
		t.Errorf("genuine B_part1 must pass through: %+v", out[2])
	}
	if in[1].Name != "B_part1" {
		t.Error("input chunks must not be mutated")
	}
}

// The passages keep their split pieces (search is unchanged); only the graph
// identity is logical.
func TestChunkerKeepsPiecesForSearchAndTagsLogicalName(t *testing.T) {
	src := "package p\n\nvar x []int\nfunc helper(int) {}\n\n" + bigGoFunc("Big", 150)
	chunks := chunking.NewASTChunker(chunking.DefaultASTChunkerConfig()).ChunkCode(src, "p.go")
	pieces := 0
	for _, ch := range chunks {
		if strings.HasPrefix(ch.Name, "Big_part") {
			pieces++
			if ch.LogicalName != "Big" {
				t.Errorf("%s: LogicalName = %q, want Big", ch.Name, ch.LogicalName)
			}
		} else if ch.LogicalName != "" {
			t.Errorf("%s is not a piece but has LogicalName %q", ch.Name, ch.LogicalName)
		}
	}
	if pieces < 2 {
		t.Fatalf("chunker produced %d pieces for Big", pieces)
	}
}
