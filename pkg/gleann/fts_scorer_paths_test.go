package gleann

import (
	"testing"

	"github.com/tevfik/gleann/internal/analysis"
)

// The FTS scorer works in two ID spaces: raw lex rowids, and the searcher's
// IDs (vec_slot, or the negated rowid of a slotless passage). Both must score
// the same passages, and a closed store degrades to zero scores / no hits
// instead of failing the hybrid search.
func TestFTSScorerIDSpaces(t *testing.T) {
	sp, _ := seedPassageStore(t) // slots 0..2 (alpha one, alpha two, beta one) + one slotless "staged text"
	an := analysis.New("")
	slotted := &FTSScorer{store: sp.st, analyzer: an, slotIDs: true}
	raw := &FTSScorer{store: sp.st, analyzer: an}
	if !slotted.Persisted() {
		t.Fatal("the FTS scorer is persisted")
	}
	slotted.AddDocuments([]Passage{{ID: 9, Text: "ignored"}}) // persisted: nothing to add

	var stagedID int64
	all := sp.All()
	for _, p := range all {
		if p.ID < 0 {
			stagedID = p.ID
		}
	}

	scores := slotted.Score("staged alpha", []Passage{{ID: 0}, {ID: 2}, {ID: stagedID}})
	if scores[0] <= 0 || scores[1] != 0 || scores[2] <= 0 {
		t.Fatalf("slot-space scores = %v, want alpha and staged scored, beta zero", scores)
	}

	ids, sc := slotted.TopK("staged", 5)
	if len(ids) != 1 || ids[0] != stagedID || sc[0] <= 0 {
		t.Fatalf("TopK of the slotless passage = %v %v, want its negative ID %d", ids, sc, stagedID)
	}
	ids, _ = slotted.TopK("beta", 5)
	if len(ids) != 1 || ids[0] != 2 {
		t.Fatalf("TopK(beta) = %v, want slot 2", ids)
	}

	// Raw rowids: the staged passage is rowid -stagedID.
	rowScores := raw.Score("staged", []Passage{{ID: -stagedID}, {ID: 1}})
	if rowScores[0] <= 0 || rowScores[1] != 0 {
		t.Fatalf("rowid-space scores = %v", rowScores)
	}
	rids, _ := raw.TopK("staged", 5)
	if len(rids) != 1 || rids[0] != -stagedID {
		t.Fatalf("rowid TopK = %v", rids)
	}

	sp.Close()
	if got := slotted.Score("alpha", []Passage{{ID: 0}, {ID: stagedID}}); got[0] != 0 || got[1] != 0 {
		t.Fatalf("closed store scores = %v, want zeros", got)
	}
	if got := raw.Score("alpha", []Passage{{ID: 1}}); got[0] != 0 {
		t.Fatalf("closed store raw scores = %v", got)
	}
	if ids, _ := slotted.TopK("alpha", 3); ids != nil {
		t.Fatalf("closed store TopK = %v", ids)
	}
	if m := slotted.ScoreRowIDs("alpha", []int64{1}); len(m) != 0 {
		t.Fatalf("closed store ScoreRowIDs = %v", m)
	}
}
