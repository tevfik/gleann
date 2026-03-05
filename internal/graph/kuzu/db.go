// Package kuzu provides an embedded property graph database backed by KuzuDB
// for storing and querying AST (Abstract Syntax Tree) relationships extracted
// from a codebase.
//
// Schema:
//
//	Node tables  : CodeFile, Symbol (Function | Method | Type | Struct | Interface | Const | Var)
//	Relationship tables: DECLARES (CodeFile→Symbol), CALLS (Symbol→Symbol),
//	                     IMPLEMENTS (Symbol→Symbol), REFERENCES (Symbol→Symbol)
package kuzu

import (
	"fmt"
	"path/filepath"

	kuzu "github.com/kuzudb/go-kuzu"
)

// DB wraps a KuzuDB database and a primary connection.
// For concurrent use, call NewConn() to get an independent connection
// per goroutine — KuzuDB connections are NOT goroutine-safe.
type DB struct {
	db   *kuzu.Database
	conn *kuzu.Connection
}

// NewConn creates and returns a new separate connection.
// IMPORTANT: KuzuDB CGo connections are NOT goroutine-safe. If multiple
// goroutines need to query the DB concurrently, each MUST have its own Conn.
func (g *DB) NewConn() (*kuzu.Connection, error) {
	return kuzu.OpenConnection(g.db)
}

// Conn returns the primary database connection.
func (g *DB) Conn() *kuzu.Connection {
	return g.conn
}

// ExecOn runs a Cypher query on an arbitrary connection and discards the result.
func ExecOn(conn *kuzu.Connection, cypher string) error {
	res, err := conn.Query(cypher)
	if err != nil {
		return err
	}
	defer res.Close()
	return nil
}

// Open opens (or creates) a KuzuDB database at the given directory path.
// Pass an empty string to use in-memory mode.
func Open(dir string) (*DB, error) {
	var (
		db  *kuzu.Database
		err error
	)
	if dir == "" {
		db, err = kuzu.OpenInMemoryDatabase(kuzu.DefaultSystemConfig())
	} else {
		dbPath := filepath.Clean(dir)
		db, err = kuzu.OpenDatabase(dbPath, kuzu.DefaultSystemConfig())
	}
	if err != nil {
		return nil, fmt.Errorf("kuzu open: %w", err)
	}

	conn, err := kuzu.OpenConnection(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("kuzu connection: %w", err)
	}

	g := &DB{db: db, conn: conn}
	if err := g.initSchema(); err != nil {
		g.Close()
		return nil, fmt.Errorf("kuzu init schema: %w", err)
	}
	return g, nil
}

// Close releases all database resources.
func (g *DB) Close() {
	if g.conn != nil {
		g.conn.Close()
	}
	if g.db != nil {
		g.db.Close()
	}
}

// exec runs a Cypher query and discards the result.
func (g *DB) exec(cypher string) error {
	res, err := g.conn.Query(cypher)
	if err != nil {
		return err
	}
	defer res.Close()
	return nil
}

// initSchema creates node/relationship tables if they don't already exist.
func (g *DB) initSchema() error {
	ddls := []string{
		// ── Code Node tables ───────────────────────────────────
		`CREATE NODE TABLE IF NOT EXISTS CodeFile(
			path   STRING,
			lang   STRING,
			PRIMARY KEY (path)
		)`,
		`CREATE NODE TABLE IF NOT EXISTS Symbol(
			fqn    STRING,
			kind   STRING,
			file   STRING,
			line   INT64,
			name   STRING,
			doc    STRING,
			PRIMARY KEY (fqn)
		)`,

		// ── Document Node tables ───────────────────────────────
		`CREATE NODE TABLE IF NOT EXISTS Document(
			path        STRING,
			title       STRING,
			format      STRING,
			summary     STRING,
			word_count  INT64,
			page_count  INT64,
			PRIMARY KEY (path)
		)`,
		`CREATE NODE TABLE IF NOT EXISTS Section(
			id          STRING,
			heading     STRING,
			level       INT64,
			content     STRING,
			summary     STRING,
			doc_path    STRING,
			PRIMARY KEY (id)
		)`,
		`CREATE NODE TABLE IF NOT EXISTS DocChunk(
			id          STRING,
			text        STRING,
			chunk_index INT64,
			section_id  STRING,
			passage_id  INT64,
			PRIMARY KEY (id)
		)`,

		// ── Code Relationship tables ───────────────────────────
		// CodeFile → Symbol  (a file declares a symbol)
		`CREATE REL TABLE IF NOT EXISTS DECLARES(
			FROM CodeFile TO Symbol,
			MANY_MANY
		)`,
		// Symbol → Symbol  (a function calls another)
		`CREATE REL TABLE IF NOT EXISTS CALLS(
			FROM Symbol TO Symbol,
			MANY_MANY
		)`,
		// Symbol → Symbol  (a struct implements an interface)
		`CREATE REL TABLE IF NOT EXISTS IMPLEMENTS(
			FROM Symbol TO Symbol,
			MANY_MANY
		)`,
		// Symbol → Symbol  (a symbol references another — e.g. uses a type)
		`CREATE REL TABLE IF NOT EXISTS REFERENCES(
			FROM Symbol TO Symbol,
			MANY_MANY
		)`,

		// ── Document Relationship tables ───────────────────────
		// Document → Section  (document has top-level sections)
		`CREATE REL TABLE IF NOT EXISTS HAS_SECTION(
			FROM Document TO Section,
			MANY_MANY
		)`,
		// Section → Section  (section contains subsections)
		`CREATE REL TABLE IF NOT EXISTS HAS_SUBSECTION(
			FROM Section TO Section,
			MANY_MANY
		)`,
		// Section → DocChunk  (section contains chunks)
		`CREATE REL TABLE IF NOT EXISTS HAS_CHUNK(
			FROM Section TO DocChunk,
			MANY_MANY
		)`,
		// DocChunk → Symbol  (a chunk explains/references a code symbol)
		`CREATE REL TABLE IF NOT EXISTS EXPLAINS(
			FROM DocChunk TO Symbol,
			MANY_MANY
		)`,
	}

	for _, ddl := range ddls {
		if err := g.exec(ddl); err != nil {
			return fmt.Errorf("DDL error (%q): %w", ddl[:min(40, len(ddl))], err)
		}
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
