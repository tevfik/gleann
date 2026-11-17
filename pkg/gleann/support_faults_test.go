package gleann

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/internal/store"
	_ "modernc.org/sqlite"
)

// fxExec runs raw SQL against the index database at base+".db" through its
// own connection. The tests use it to damage an index or to install triggers
// that make one statement of the code under test fail with a SQL error.
func fxExec(t *testing.T, base string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", base+".db?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// fxFail is a trigger that aborts every matching statement of one kind
// (event INSERT/UPDATE/DELETE) on a table; cond restricts it to some rows.
func fxFail(name, event, table, cond string) string {
	where := ""
	if cond != "" {
		where = " WHEN " + cond
	}
	return `CREATE TRIGGER ` + name + ` BEFORE ` + event + ` ON ` + table + where +
		` BEGIN SELECT RAISE(ABORT, 'injected fault'); END`
}

// failMeta aborts every write of one meta key.
func failMeta(key string) string {
	return fxFail("_meta_"+key, "INSERT", "meta", "NEW.key = '"+key+"'")
}

// fxBuilt builds the standard four-passage format-2 index (a.md twice,
// b.md, c.md) and returns the index directory, the index base path and the
// builder. Updates always rebuild the main vector file (limits are zeroed).
func fxBuilt(t *testing.T) (string, string, *LeannBuilder) {
	t.Helper()
	dir := t.TempDir()
	b, _ := newFormat2Builder(t, dir, 8)
	b.limits = incrementalLimits{}
	if err := b.Build(context.Background(), "idx", f2Items()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	return dir, filepath.Join(dir, "idx", "idx"), b
}

// fxOpen opens the store of base and closes it with the test.
func fxOpen(t *testing.T, base string) *store.Store {
	t.Helper()
	st, err := store.Open(base+".db", store.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// fxCancelled is a context that is already cancelled: every database call
// made with it fails.
func fxCancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
