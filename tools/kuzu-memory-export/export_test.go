package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	kuzu "github.com/kuzudb/go-kuzu"
)

// The v1.9 DDL of the Memory Engine tables (internal/graph/kuzu/db.go).
const (
	ddlEntity = `CREATE NODE TABLE IF NOT EXISTS Entity(id STRING, type STRING, content STRING, attributes STRING, PRIMARY KEY (id))`
	ddlRel    = `CREATE REL TABLE IF NOT EXISTS RELATES_TO(FROM Entity TO Entity, relation_type STRING, weight DOUBLE, attributes STRING)`
	ddlArch   = `CREATE NODE TABLE IF NOT EXISTS Entity_Archive(archive_id STRING, id STRING, version_id STRING, type STRING, content STRING, attributes STRING, valid_to STRING, PRIMARY KEY (archive_id))`
	ddlPrev   = `CREATE REL TABLE IF NOT EXISTS PREVIOUS_VERSION(FROM Entity TO Entity_Archive, MANY_MANY)`
)

func exec(t *testing.T, conn *kuzu.Connection, qs ...string) {
	t.Helper()
	for _, q := range qs {
		res, err := conn.Query(q)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		res.Close()
	}
}

var seedQueries = []string{
	`CREATE (:Entity {id: "a", type: "person", content: "alice \"q\" é", attributes: "{\"role\":\"x\"}"})`,
	`CREATE (:Entity {id: "b", type: "note", content: "", attributes: "{}"})`,
	`MATCH (a:Entity {id:"a"}), (b:Entity {id:"b"}) CREATE (a)-[:RELATES_TO {relation_type:"WROTE", weight: 2.5, attributes:"{\"created_at\":\"2026-01-01T00:00:00Z\"}"}]->(b)`,
	`MATCH (a:Entity {id:"a"}), (b:Entity {id:"b"}) CREATE (a)-[:RELATES_TO {relation_type:"SELF2", weight: 0.0, attributes:"{}"}]->(b)`,
}

const archiveQuery = `CREATE (:Entity_Archive {archive_id: "a_1", id: "a", version_id: "1", type: "person", content: "old", attributes: "{}", valid_to: "2026-01-02T00:00:00Z"})`

func makeStore(t *testing.T, path string, withArchive bool) (*kuzu.Database, *kuzu.Connection) {
	t.Helper()
	db, err := kuzu.OpenDatabase(path, kuzu.DefaultSystemConfig())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := kuzu.OpenConnection(db)
	if err != nil {
		t.Fatal(err)
	}
	exec(t, conn, ddlEntity, ddlRel)
	if withArchive {
		exec(t, conn, ddlArch, ddlPrev)
	}
	exec(t, conn, seedQueries...)
	if withArchive {
		exec(t, conn, archiveQuery)
	}
	return db, conn
}

func checkSeed(t *testing.T, ex *Export, wantArchive int) {
	t.Helper()
	if ex.Format != FormatName || ex.Version != FormatVersion || ex.Namespace != "mem" {
		t.Fatalf("header: %+v", ex)
	}
	if len(ex.Entities) != 2 || ex.Entities[0].Content != `alice "q" é` || ex.Entities[0].Attributes != `{"role":"x"}` {
		t.Fatalf("entities: %+v", ex.Entities)
	}
	if len(ex.Relations) != 2 {
		t.Fatalf("relations: %+v", ex.Relations)
	}
	if r := ex.Relations[1]; r.RelationType != "WROTE" || r.Weight != 2.5 || r.Attributes != `{"created_at":"2026-01-01T00:00:00Z"}` {
		t.Fatalf("relation: %+v", r)
	}
	if len(ex.Archive) != wantArchive {
		t.Fatalf("archive rows = %d, want %d", len(ex.Archive), wantArchive)
	}
	if wantArchive == 1 && (ex.Archive[0].ValidTo != "2026-01-02T00:00:00Z" || ex.Archive[0].Content != "old") {
		t.Fatalf("archive: %+v", ex.Archive[0])
	}
}

func TestExportCurrentSchema(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mem_memory")
	db, conn := makeStore(t, p, true)
	conn.Close()
	db.Close()
	ex, err := ExportStore(p)
	if err != nil {
		t.Fatal(err)
	}
	checkSeed(t, ex, 1)
}

func TestExportPreArchiveSchema(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mem_memory")
	db, conn := makeStore(t, p, false)
	conn.Close()
	db.Close()
	ex, err := ExportStore(p)
	if err != nil {
		t.Fatal(err)
	}
	checkSeed(t, ex, 0)
	if ex.Archive == nil {
		t.Fatal("archive must serialise as [] not null")
	}
}

// A store whose write-ahead log was never checkpointed: the files are copied
// while the writer still holds the database open.
func TestExportLeftoverWAL(t *testing.T) {
	src := filepath.Join(t.TempDir(), "w_memory")
	db, conn := makeStore(t, src, true)
	defer db.Close()
	defer conn.Close()
	dir := t.TempDir()
	dst := filepath.Join(dir, "mem_memory")
	if err := copyStore(src, dst); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(dst + ".wal"); err != nil || fi.Size() == 0 {
		t.Skipf("no pending wal could be reproduced (%v)", err)
	}
	before := sum(t, dst, dst+".wal")
	ex, err := ExportStore(dst)
	if err != nil {
		t.Fatal(err)
	}
	checkSeed(t, ex, 1)
	if sum(t, dst, dst+".wal") != before {
		t.Fatal("export modified the user's files")
	}
}

func TestExportDirectoryStoreAndDiscovery(t *testing.T) {
	idx := t.TempDir()
	p := filepath.Join(idx, "mem_memory")
	db, conn := makeStore(t, p, true)
	conn.Close()
	db.Close()
	// Noise that must not be discovered.
	_ = os.MkdirAll(filepath.Join(idx, "mem_memory_vectors"), 0o755)
	_ = os.WriteFile(filepath.Join(idx, "mem.db"), []byte("x"), 0o644)
	stores, err := DiscoverStores(idx)
	if err != nil || len(stores) != 1 || stores[0] != p {
		t.Fatalf("stores = %v, %v", stores, err)
	}
	out := t.TempDir()
	if err := run(idx, out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(out, "mem.kuzu-export.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ex Export
	if err := json.Unmarshal(data, &ex); err != nil {
		t.Fatal(err)
	}
	checkSeed(t, &ex, 1)
}

func TestExportNotAStore(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x_memory")
	db, err := kuzu.OpenDatabase(p, kuzu.DefaultSystemConfig())
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := ExportStore(p); err == nil {
		t.Fatal("empty database must be rejected")
	}
}

func sum(t *testing.T, paths ...string) string {
	t.Helper()
	h := sha256.New()
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		h.Write(b)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// The real v1.9.0 store (RFC 004 A0 fixture). Its export must equal the file
// committed as pkg/memory/testdata/v190/mem.kuzu-export.json in gleann, which
// the importer's proof test replays against the v1.9.0 traverse goldens.
func TestExportRealV190Fixture(t *testing.T) {
	src := os.Getenv("GLEANN_V190_MEMORY_FIXTURE")
	if src == "" {
		src = "/home/tevfik/WORKSPACE/gleann-review/fixtures/v190-memory/store/mem_memory"
	}
	if _, err := os.Stat(src); err != nil {
		t.Skipf("v1.9.0 fixture not available: %v", err)
	}
	cp := filepath.Join(t.TempDir(), "mem_memory")
	if err := copyFile(src, cp); err != nil {
		t.Fatal(err)
	}
	before := sum(t, cp)
	ex, err := ExportStore(cp)
	if err != nil {
		t.Fatal(err)
	}
	if sum(t, cp) != before {
		t.Fatal("export modified the store")
	}
	if len(ex.Entities) != 8 || len(ex.Relations) != 8 || len(ex.Archive) != 2 {
		t.Fatalf("counts: %d/%d/%d", len(ex.Entities), len(ex.Relations), len(ex.Archive))
	}
	want, err := os.ReadFile("../../pkg/memory/testdata/v190/mem.kuzu-export.json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := WriteExport(ex, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != string(want) {
		t.Fatalf("export differs from the committed testdata copy; regenerate it with the tool")
	}
}
