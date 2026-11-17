package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

const fullExport = `{"format":"gleann-kuzu-memory-export","version":1,"namespace":"ns",
"entities":[{"id":"a","type":"t","content":"A","attributes":"{}"},{"id":"b","type":"t","content":"B","attributes":"{}"}],
"relations":[{"from":"a","to":"b","relation_type":"USES","weight":1,"attributes":"{}"}],
"archive":[{"archive_id":"x1","id":"a","version_id":"v1","type":"t","content":"A0","attributes":"{}","valid_to":"2026-01-01T00:00:00Z"}]}`

// A graph import is one transaction: when any table write fails, no entity,
// relation, archive row or import marker is left, and the import can be
// re-run once the cause is gone.
func TestImportGraphFailureLeavesNothing(t *testing.T) {
	for _, timing := range []string{"INSERT ON entities", "INSERT ON relations", "INSERT ON entity_archive", "INSERT ON memory_meta"} {
		t.Run(timing, func(t *testing.T) {
			s := newImportStore(t)
			p := filepath.Join(t.TempDir(), "ns.json")
			if err := os.WriteFile(p, []byte(fullExport), 0o644); err != nil {
				t.Fatal(err)
			}
			injectFault(t, s, timing)
			if _, err := s.ImportGraphFile(p); err == nil || !strings.Contains(err.Error(), "injected fault") || !strings.Contains(err.Error(), `namespace "ns"`) {
				t.Fatalf("err = %v", err)
			}
			for _, table := range []string{"entities", "relations", "entity_archive"} {
				if n := rowCount(t, s, table); n != 0 {
					t.Fatalf("%d %s rows left by a failed import", n, table)
				}
			}
			if m, _ := s.GraphImportMarker("ns"); m != "" {
				t.Fatal("a failed import recorded its marker")
			}
			dropFault(t, s)
			r, err := s.ImportGraphFile(p)
			if err != nil || r.Entities != 2 || r.Relations != 1 || r.Archive != 1 {
				t.Fatalf("retry = %+v, %v", r, err)
			}
		})
	}
}

func TestImportGraphValidatesRelationsAndArchive(t *testing.T) {
	s := newImportStore(t)
	dir := t.TempDir()
	head := `{"format":"gleann-kuzu-memory-export","version":1,"namespace":"x","entities":[{"id":"a","attributes":"{}"}],`
	bad := map[string]string{
		`"relations":[{"from":"","to":"a"}]}`:                       "empty endpoint",
		`"relations":[{"from":"a","to":"a","attributes":"[1]"}]}`:   "attributes",
		`"archive":[{"archive_id":"","id":"a"}]}`:                   "no archive_id",
		`"archive":[{"archive_id":"z","id":"a","attributes":"7"}]}`: `archive row "z" attributes`,
	}
	i := 0
	for tail, want := range bad {
		i++
		p := filepath.Join(dir, fmt.Sprintf("%d.json", i))
		if err := os.WriteFile(p, []byte(head+tail), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ImportGraphFile(p); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", tail, err, want)
		}
	}
	if n := rowCount(t, s, "entities"); n != 0 {
		t.Fatalf("a rejected export wrote %d entities", n)
	}
}

// InjectEntities archives, upserts and links in one transaction: a failing
// step leaves the namespace as it was, and the vector index is only fed
// after the commit.
func TestInjectEntitiesFailureIsAtomic(t *testing.T) {
	ctx := context.Background()
	update := gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{{ID: "a", Type: "t", Content: "A2"}, {ID: "d", Type: "t", Content: "D"}},
		Edges: []gleann.MemoryGraphEdge{{From: "a", To: "d", RelationType: "USES"}},
	}
	for _, timing := range []string{"INSERT ON entity_archive", "UPDATE ON entities", "INSERT ON relations"} {
		t.Run(timing, func(t *testing.T) {
			s := newImportStore(t)
			sync := &recordingSyncer{}
			es := NewEntityService(s, "ns", sync)
			seedGraph(t, es)
			injectFault(t, s, timing)
			if err := es.InjectEntities(ctx, update); err == nil || !strings.Contains(err.Error(), "injected fault") {
				t.Fatalf("err = %v", err)
			}
			if rowCount(t, s, "entities") != 3 || rowCount(t, s, "relations") != 3 || rowCount(t, s, "entity_archive") != 0 {
				t.Fatal("a failed injection changed the graph")
			}
			var content string
			if err := s.reader.QueryRow(`SELECT content FROM entities WHERE ns = 'ns' AND id = 'a'`).Scan(&content); err != nil || content != "A" {
				t.Fatalf("entity a = %q, %v", content, err)
			}
		})
	}

	// A vector index failure is reported after the commit.
	s := newImportStore(t)
	sync := &recordingSyncer{}
	es := NewEntityService(s, "ns", &failingAddSyncer{recordingSyncer: sync})
	if err := es.InjectEntities(ctx, update); err == nil || !strings.Contains(err.Error(), "vector sync node") {
		t.Fatalf("err = %v", err)
	}
	if rowCount(t, s, "entities") != 2 {
		t.Fatal("the rows must be committed before the vector sync")
	}
}

type failingAddSyncer struct{ *recordingSyncer }

func (f *failingAddSyncer) AddContent(context.Context, string, string, map[string]any) error {
	return errors.New("embedding server down")
}

// Every write path of the manager reports a store failure instead of
// returning a block that was never stored.
func TestManagerWritesReportStoreFailure(t *testing.T) {
	s := openTestStore(t)
	m := NewManager(s)
	injectFault(t, s, "INSERT ON blocks")
	checks := map[string]error{}
	_, checks["Remember"] = m.Remember("x")
	_, checks["AddNote"] = m.AddNote(TierLong, "l", "x")
	_, checks["AddScopedNote"] = m.AddScopedNote("scope", TierLong, "l", "x")
	_, checks["RecordFailure"] = m.RecordFailure(Failure{Command: "go test ./...", Error: "FAIL"})
	for name, err := range checks {
		if err == nil || !strings.Contains(err.Error(), "injected fault") {
			t.Errorf("%s: err = %v, want the injected fault", name, err)
		}
	}
	dropFault(t, s)
	if got, err := m.List(""); err != nil || len(got) != 0 {
		t.Fatalf("blocks after failed writes = %d, %v", len(got), err)
	}
}
