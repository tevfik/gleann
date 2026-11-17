package memory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// injectFault installs a trigger that aborts the matching statement, the way
// an I/O error or a full disk would; it is dropped at cleanup.
func injectFault(t *testing.T, s *Store, timing string) {
	t.Helper()
	if _, err := s.writer.Exec(`CREATE TRIGGER fault BEFORE ` + timing + ` BEGIN SELECT RAISE(ABORT, 'injected fault'); END`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.writer.Exec(`DROP TRIGGER IF EXISTS fault`) })
}

func dropFault(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.writer.Exec(`DROP TRIGGER IF EXISTS fault`); err != nil {
		t.Fatal(err)
	}
}

// recordingSyncer records the vector deletes and can be made to fail.
type recordingSyncer struct {
	deleted []string
	fail    error
}

func (r *recordingSyncer) AddContent(context.Context, string, string, map[string]any) error {
	return nil
}

func (r *recordingSyncer) DeleteContent(_ context.Context, id string) error {
	if r.fail != nil {
		return r.fail
	}
	r.deleted = append(r.deleted, id)
	return nil
}

func seedGraph(t *testing.T, es *EntityService) {
	t.Helper()
	err := es.InjectEntities(context.Background(), gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{{ID: "a", Type: "t", Content: "A"}, {ID: "b", Type: "t", Content: "B"}, {ID: "c", Type: "t", Content: "C"}},
		Edges: []gleann.MemoryGraphEdge{{From: "a", To: "b", RelationType: "USES"}, {From: "c", To: "a", RelationType: "USES"}, {From: "b", To: "c", RelationType: "USES"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

// DeleteEntity removes the entity and every incident relation in one
// transaction: when a step fails nothing is removed, and the vector index is
// not touched.
func TestDeleteEntityIsAtomic(t *testing.T) {
	ctx := context.Background()
	s := newImportStore(t)
	sync := &recordingSyncer{}
	es := NewEntityService(s, "ns", sync)
	seedGraph(t, es)

	injectFault(t, s, "DELETE ON entities")
	if err := es.DeleteEntity(ctx, "a"); err == nil || !strings.Contains(err.Error(), "injected fault") {
		t.Fatalf("err = %v", err)
	}
	if rowCount(t, s, "entities") != 3 || rowCount(t, s, "relations") != 3 {
		t.Fatal("a failed DeleteEntity removed rows")
	}
	if len(sync.deleted) != 0 {
		t.Fatal("a failed DeleteEntity removed the vector")
	}
	dropFault(t, s)

	if err := es.DeleteEntity(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if rowCount(t, s, "entities") != 2 || rowCount(t, s, "relations") != 1 {
		t.Fatalf("after delete: %d entities, %d relations; want 2 and the b->c relation", rowCount(t, s, "entities"), rowCount(t, s, "relations"))
	}
	if len(sync.deleted) != 1 || sync.deleted[0] != "a" {
		t.Fatalf("vector deletes = %v", sync.deleted)
	}

	// A vector index failure is reported after the rows are gone.
	sync.fail = errors.New("vector store down")
	if err := es.DeleteEntity(ctx, "b"); err == nil || !strings.Contains(err.Error(), "vector sync") {
		t.Fatalf("err = %v, want the vector sync error", err)
	}
}

func TestDeleteEdgeFailureKeepsRelation(t *testing.T) {
	ctx := context.Background()
	s := newImportStore(t)
	es := NewEntityService(s, "ns", nil)
	seedGraph(t, es)
	injectFault(t, s, "DELETE ON relations")
	if err := es.DeleteEdge(ctx, "a", "b", "USES"); err == nil || !strings.Contains(err.Error(), `"a"->"b"`) {
		t.Fatalf("err = %v", err)
	}
	if rowCount(t, s, "relations") != 3 {
		t.Fatal("a failed DeleteEdge removed a relation")
	}
	dropFault(t, s)
	if err := es.DeleteEdge(ctx, "a", "b", "USES"); err != nil || rowCount(t, s, "relations") != 2 {
		t.Fatalf("DeleteEdge: %v, %d relations", err, rowCount(t, s, "relations"))
	}
}

// A revert that fails part-way keeps the run record, so it can be retried;
// the retry finishes the job and skips blocks already gone.
func TestRevertReflectionIsRetryable(t *testing.T) {
	s := openTestStore(t)
	m := NewManager(s)
	original, err := m.Remember("written by hand")
	if err != nil {
		t.Fatal(err)
	}
	var added []string
	for _, c := range []string{"reflected one", "reflected two", "reflected three"} {
		b, err := m.AddNote(TierLong, "reflection", c)
		if err != nil {
			t.Fatal(err)
		}
		added = append(added, b.ID)
	}
	if err := s.SaveChangeSet(&ChangeSet{RunID: "run-1", BlocksAdded: added}); err != nil {
		t.Fatal(err)
	}
	// One added block was already deleted by hand.
	if err := s.Delete(added[1]); err != nil {
		t.Fatal(err)
	}

	injectFault(t, s, "DELETE ON blocks")
	if _, err := m.RevertReflection("run-1"); err == nil || !strings.Contains(err.Error(), "reverting reflection run run-1") {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.GetChangeSet("run-1"); err != nil {
		t.Fatalf("the run record was dropped by a failed revert: %v", err)
	}
	dropFault(t, s)

	if _, err := m.RevertReflection("run-1"); err != nil {
		t.Fatal(err)
	}
	for _, id := range added {
		if _, err := s.Get(id); err == nil {
			t.Fatalf("block %s survived the revert", id)
		}
	}
	if _, err := s.Get(original.ID); err != nil {
		t.Fatal("the revert removed a block the run did not add")
	}
	if _, err := s.GetChangeSet("run-1"); err == nil {
		t.Fatal("the run record survived a completed revert")
	}
	if _, err := m.RevertReflection("run-1"); err == nil {
		t.Fatal("reverting an unknown run succeeded")
	}
}
