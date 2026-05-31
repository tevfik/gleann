//go:build treesitter

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
	"log"
	"os"
	"path/filepath"
	"time"

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
//
// Corrupted on-disk databases are automatically quarantined (renamed to
// <dir>.corrupted-<unix-ts>) and a fresh database is created in their place,
// unless GLEANN_KUZU_AUTOREPAIR=0 is set. This recovers from upstream
// crashes that leave behind a half-written WAL/header.
func Open(dir string) (*DB, error) {
	if dir == "" {
		db, err := kuzu.OpenInMemoryDatabase(kuzu.DefaultSystemConfig())
		if err != nil {
			return nil, fmt.Errorf("kuzu open: %w", err)
		}
		return finishOpen(db)
	}

	dbPath := filepath.Clean(dir)
	db, err := kuzu.OpenDatabase(dbPath, kuzu.DefaultSystemConfig())
	if err == nil {
		return finishOpen(db)
	}
	openErr := fmt.Errorf("kuzu open: %w", err)

	// Best-effort auto-repair: only attempt when the directory actually
	// exists (so a missing-path error is surfaced as-is) and the user has
	// not opted out.
	if os.Getenv("GLEANN_KUZU_AUTOREPAIR") == "0" {
		return nil, openErr
	}
	if info, statErr := os.Stat(dbPath); statErr != nil || !info.IsDir() {
		return nil, openErr
	}

	quarantine := fmt.Sprintf("%s.corrupted-%d", dbPath, time.Now().Unix())
	log.Printf("[WARN] kuzu: open failed (%v); quarantining %s -> %s and re-creating", err, dbPath, quarantine)
	if renameErr := os.Rename(dbPath, quarantine); renameErr != nil {
		return nil, fmt.Errorf("%w; auto-repair failed: %v", openErr, renameErr)
	}
	db, err = kuzu.OpenDatabase(dbPath, kuzu.DefaultSystemConfig())
	if err != nil {
		return nil, fmt.Errorf("kuzu open after quarantine: %w", err)
	}
	return finishOpen(db)
}

// finishOpen attaches a connection and initialises the schema. Called from
// every Open path so the in-memory and on-disk branches stay symmetric.
func finishOpen(db *kuzu.Database) (*DB, error) {
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

		// ── Hierarchical Context Node tables ───────────────────────────────
		`CREATE NODE TABLE IF NOT EXISTS Folder(
			vpath  STRING,
			name   STRING,
			PRIMARY KEY (vpath)
		)`,
		`CREATE NODE TABLE IF NOT EXISTS Document(
			vpath      STRING,
			rpath      STRING,
			name       STRING,
			hash       STRING,
			summary    STRING,
			PRIMARY KEY (vpath)
		)`,
		`CREATE NODE TABLE IF NOT EXISTS Heading(
			id         STRING,
			name       STRING,
			level      INT64,
			PRIMARY KEY (id)
		)`,
		`CREATE NODE TABLE IF NOT EXISTS Chunk(
			id         STRING,
			text       STRING,
			start_char INT64,
			end_char   INT64,
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

		// ── Hierarchical Context Relationship tables ───────────────────────
		// Folder → Document
		`CREATE REL TABLE IF NOT EXISTS CONTAINS_DOC(
			FROM Folder TO Document,
			MANY_MANY
		)`,
		// Document → Heading
		`CREATE REL TABLE IF NOT EXISTS HAS_HEADING(
			FROM Document TO Heading,
			MANY_MANY
		)`,
		// Heading → Heading (H1 -> H2)
		`CREATE REL TABLE IF NOT EXISTS CHILD_HEADING(
			FROM Heading TO Heading,
			MANY_MANY
		)`,
		// Heading → Chunk OR Document → Chunk
		`CREATE REL TABLE IF NOT EXISTS HAS_CHUNK_HEADING(
			FROM Heading TO Chunk,
			MANY_MANY
		)`,
		`CREATE REL TABLE IF NOT EXISTS HAS_CHUNK_DOC(
			FROM Document TO Chunk,
			MANY_MANY
		)`,
		// Chunk → Symbol (Optional code link)
		`CREATE REL TABLE IF NOT EXISTS EXPLAINS(
			FROM Chunk TO Symbol,
			MANY_MANY
		)`,

		// ── Memory Engine: generic Entity / RELATES_TO schema ──────────────
		// These tables support the Knowledge Graph Memory Engine that external
		// AI agents (e.g. Yaver, Claude) can read from and write to without
		// coupling to gleann's internal AST / document schemas.
		`CREATE NODE TABLE IF NOT EXISTS Entity(
			id         STRING,
			type       STRING,
			content    STRING,
			attributes STRING,
			PRIMARY KEY (id)
		)`,
		// RELATES_TO is a generic directed, labeled, weighted relationship.
		// relation_type distinguishes semantically different edge classes
		// (e.g. "DEPENDS_ON", "IMPLEMENTS", "RELATED_TO") without requiring
		// separate table definitions per relationship kind.
		`CREATE REL TABLE IF NOT EXISTS RELATES_TO(
			FROM Entity TO Entity,
			relation_type STRING,
			weight        DOUBLE,
			attributes    STRING
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
