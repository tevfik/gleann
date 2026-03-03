package kuzu

import "fmt"

// SymbolNode represents any code symbol (function, type, struct, etc.).
type SymbolNode struct {
	FQN  string // Fully Qualified Name, e.g. "github.com/foo/bar.MyFunc"
	Kind string // "function" | "method" | "type" | "struct" | "interface" | "const" | "var"
	File string // source file path
	Line int64  // line number in source file
	Name string // short name, e.g. "MyFunc"
	Doc  string // documentation comment (optional)
}

// UpsertFile inserts or ignores a CodeFile node.
func (g *DB) UpsertFile(path, lang string) error {
	cypher := fmt.Sprintf(
		`MERGE (f:CodeFile {path: %q}) ON CREATE SET f.lang = %q`,
		path, lang,
	)
	return g.exec(cypher)
}

// UpsertSymbol inserts or ignores a Symbol node.
func (g *DB) UpsertSymbol(s SymbolNode) error {
	cypher := fmt.Sprintf(
		`MERGE (sym:Symbol {fqn: %q})
         ON CREATE SET sym.kind=%q, sym.file=%q, sym.line=%d, sym.name=%q, sym.doc=%q`,
		s.FQN, s.Kind, s.File, s.Line, s.Name, s.Doc,
	)
	return g.exec(cypher)
}

// AddDeclares records that a CodeFile declares a Symbol.
func (g *DB) AddDeclares(filePath, symbolFQN string) error {
	cypher := fmt.Sprintf(
		`MATCH (f:CodeFile {path: %q}), (s:Symbol {fqn: %q})
         MERGE (f)-[:DECLARES]->(s)`,
		filePath, symbolFQN,
	)
	return g.exec(cypher)
}

// AddCalls records that caller (by FQN) calls callee (by FQN).
func (g *DB) AddCalls(callerFQN, calleeFQN string) error {
	cypher := fmt.Sprintf(
		`MATCH (a:Symbol {fqn: %q}), (b:Symbol {fqn: %q})
         MERGE (a)-[:CALLS]->(b)`,
		callerFQN, calleeFQN,
	)
	return g.exec(cypher)
}

// AddImplements records that implFQN implements ifaceFQN.
func (g *DB) AddImplements(implFQN, ifaceFQN string) error {
	cypher := fmt.Sprintf(
		`MATCH (a:Symbol {fqn: %q}), (b:Symbol {fqn: %q})
         MERGE (a)-[:IMPLEMENTS]->(b)`,
		implFQN, ifaceFQN,
	)
	return g.exec(cypher)
}

// AddReferences records that referer (by FQN) references referee (by FQN).
func (g *DB) AddReferences(refererFQN, refereeFQN string) error {
	cypher := fmt.Sprintf(
		`MATCH (a:Symbol {fqn: %q}), (b:Symbol {fqn: %q})
         MERGE (a)-[:REFERENCES]->(b)`,
		refererFQN, refereeFQN,
	)
	return g.exec(cypher)
}
