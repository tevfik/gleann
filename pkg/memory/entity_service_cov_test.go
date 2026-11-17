package memory

import (
	"context"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// Storage faults and unencodable attributes surface as errors from the
// entity service and leave the transaction rolled back.

func TestEntityService_InjectFaults(t *testing.T) {
	ctx := context.Background()

	t.Run("attributes that cannot be encoded", func(t *testing.T) {
		s := openTestStore(t)
		es := s.EntityService("ns", nil)
		err := es.InjectEntities(ctx, gleann.GraphInjectionPayload{
			Nodes: []gleann.MemoryGraphNode{{ID: "n", Type: "T", Attributes: map[string]any{"bad": func() {}}}},
		})
		wantErr(t, "InjectEntities", err, `encoding node "n" attributes`)
	})

	t.Run("edge attributes that cannot be encoded", func(t *testing.T) {
		s := openTestStore(t)
		es := s.EntityService("ns", nil)
		err := es.InjectEntities(ctx, gleann.GraphInjectionPayload{
			Nodes: []gleann.MemoryGraphNode{{ID: "a", Type: "T"}, {ID: "b", Type: "T"}},
			Edges: []gleann.MemoryGraphEdge{{From: "a", To: "b", RelationType: "R", Attributes: map[string]any{"bad": make(chan int)}}},
		})
		wantErr(t, "InjectEntities", err, `encoding edge "a"->"b" attributes`)
		var n int
		if err := s.reader.QueryRow(`SELECT count(*) FROM entities`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("entities = %d, %v; the failed inject must roll back its nodes", n, err)
		}
	})

	t.Run("archive lookup fails", func(t *testing.T) {
		s := openTestStore(t)
		es := s.EntityService("ns", nil)
		execSQL(t, s, `ALTER TABLE entities DROP COLUMN attributes`)
		err := es.InjectEntities(ctx, gleann.GraphInjectionPayload{Nodes: []gleann.MemoryGraphNode{{ID: "n", Type: "T"}}})
		wantErr(t, "InjectEntities", err, `checking entity "n" for archive`)
	})
}

func TestEntityService_DeleteEntityFault(t *testing.T) {
	s := openTestStore(t)
	es := s.EntityService("ns", nil)
	execSQL(t, s, `DROP TABLE relations`)
	wantErr(t, "DeleteEntity", es.DeleteEntity(context.Background(), "x"), `deleting incident relations for entity "x"`)
}

func TestEntityService_TraverseFaults(t *testing.T) {
	ctx := context.Background()
	seed := func(t *testing.T) (*Store, *EntityService) {
		s := openTestStore(t)
		es := s.EntityService("ns", nil)
		err := es.InjectEntities(ctx, gleann.GraphInjectionPayload{
			Nodes: []gleann.MemoryGraphNode{{ID: "a", Type: "T", Content: "a"}, {ID: "b", Type: "T", Content: "b"}},
			Edges: []gleann.MemoryGraphEdge{{From: "a", To: "b", RelationType: "R", Weight: 1}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return s, es
	}

	t.Run("entities table missing", func(t *testing.T) {
		s, es := seed(t)
		execSQL(t, s, `DROP TABLE entities`)
		_, _, err := es.Traverse(ctx, "a", 1)
		wantErr(t, "Traverse", err, `traverse start node "a"`)
	})
	t.Run("relations table missing", func(t *testing.T) {
		s, es := seed(t)
		execSQL(t, s, `DROP TABLE relations`)
		_, _, err := es.Traverse(ctx, "a", 1)
		wantErr(t, "Traverse", err, `traverse nodes from "a"`)
	})
	t.Run("neighbour row that does not scan", func(t *testing.T) {
		s, es := seed(t)
		// The same table without NOT NULL constraints, with a NULL content on
		// the neighbour: the start node still reads, the walk cannot.
		execSQL(t, s, `DROP TABLE entities`)
		execSQL(t, s, `CREATE TABLE entities (ns TEXT, id TEXT, type TEXT, content TEXT, attributes TEXT)`)
		execSQL(t, s, `INSERT INTO entities VALUES ('ns', 'a', 'T', 'a', '{}'), ('ns', 'b', 'T', NULL, '{}')`)
		_, _, err := es.Traverse(ctx, "a", 1)
		wantErr(t, "Traverse", err, "scan traverse node")
	})
	t.Run("edge row that does not scan", func(t *testing.T) {
		s, es := seed(t)
		execSQL(t, s, `DROP TABLE relations`)
		execSQL(t, s, `CREATE TABLE relations (ns TEXT, src TEXT, dst TEXT, relation_type TEXT, weight REAL, attributes TEXT)`)
		execSQL(t, s, `INSERT INTO relations VALUES ('ns', 'a', 'b', 'R', NULL, '{}')`)
		_, _, err := es.Traverse(ctx, "a", 1)
		wantErr(t, "Traverse", err, "scan traverse edge")
	})
}
