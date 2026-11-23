package indexing

import (
	"path/filepath"
	"testing"
)

// In a go.work workspace the packages of the OTHER main modules report
// Module.Main=true, but a full build derives IMPLEMENTS only against the
// packages "./..." matches from the index root — the root module's. A sync
// must judge the same package set, or it adds an IMPLEMENTS edge to another
// main module's interface that the full build does not have (review Q2).
func TestSyncGoWorkImplementsParity(t *testing.T) {
	docs := t.TempDir()
	writeFile(t, filepath.Join(docs, "go.mod"), "module example.com/zzwa\n\ngo 1.21\n")
	writeFile(t, filepath.Join(docs, "go.work"), "go 1.21\n\nuse (\n\t.\n\t./wb\n)\n")
	writeFile(t, filepath.Join(docs, "wb", "go.mod"), "module example.com/zzwb\n\ngo 1.21\n")
	writeFile(t, filepath.Join(docs, "wb", "wb.go"), "package zzwb\n\n// Reader is an interface of the second main module.\ntype Reader interface{ Read() int }\n\n// Buf implements Reader.\ntype Buf struct{}\n\n// Read implements Reader.\nfunc (Buf) Read() int { return 0 }\n")
	const app = "package zzwa\n\nimport \"example.com/zzwb\"\n\n// T satisfies zzwb.Reader by method set, but zzwb is not part of the indexed module.\ntype T struct{}\n\n// Read looks like Reader.Read.\nfunc (T) Read() int { return 1 }\n\n// Use calls through the other module's type.\nfunc Use(b zzwb.Buf) int { return b.Read() }\n"
	writeFile(t, filepath.Join(docs, "app.go"), app)

	env := newEnv(t)
	buildFormat2(t, env, docs)
	db := openGraphDB(t, env)
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE kind = 'IMPLEMENTS' AND src = 'example.com/zzwa.T' AND dst = 'example.com/zzwb.Reader'`); n != 0 {
		t.Fatalf("the full build already has T IMPLEMENTS zzwb.Reader (%d edge(s)): the fixture does not reproduce the precondition", n)
	}

	writeFile(t, filepath.Join(docs, "app.go"), app+"\n// Extra is added by the edit.\nfunc Extra() int { return Use(zzwb.Buf{}) }\n")
	syncGraph(t, env)

	full := newEnv(t)
	buildFormat2(t, full, docs)
	assertGraphEquals(t, "go.work workspace", openGraphDB(t, env), openGraphDB(t, full))
	assertNoOrphans(t, openGraphDB(t, env))
}
