//go:build treesitter

package kuzu

import "testing"

// A caller's placeholder node can be written before the real declaration, which
// leaves the declared symbol with an empty file property. Edges into it must
// still be found through DECLARES, or re-indexing its file loses its callers.
func TestIncomingEdgesFollowDeclaresNotTheFileProperty(t *testing.T) {
	db, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`CREATE (:CodeFile {path: "lib/lib.go", lang: "go"})`,
		`CREATE (:CodeFile {path: "app/app.go", lang: "go"})`,
		// Declared by lib/lib.go, but written as a file-less placeholder.
		`CREATE (:Symbol {fqn: "m/lib.Target", name: "Target", kind: "function", file: "", line: 0, doc: "", weight: 1.0, is_test: false})`,
		`CREATE (:Symbol {fqn: "m/lib.Helper", name: "Helper", kind: "function", file: "lib/lib.go", line: 9, doc: "", weight: 1.0, is_test: false})`,
		`CREATE (:Symbol {fqn: "m/app.Run", name: "Run", kind: "function", file: "app/app.go", line: 3, doc: "", weight: 1.0, is_test: false})`,
		`MATCH (f:CodeFile {path: "lib/lib.go"}), (s:Symbol {fqn: "m/lib.Target"}) CREATE (f)-[:DECLARES]->(s)`,
		`MATCH (f:CodeFile {path: "lib/lib.go"}), (s:Symbol {fqn: "m/lib.Helper"}) CREATE (f)-[:DECLARES]->(s)`,
		`MATCH (f:CodeFile {path: "app/app.go"}), (s:Symbol {fqn: "m/app.Run"}) CREATE (f)-[:DECLARES]->(s)`,
		`MATCH (a:Symbol {fqn: "m/app.Run"}), (b:Symbol {fqn: "m/lib.Target"}) CREATE (a)-[:CALLS]->(b)`,
		// Both ends in the changed file: re-derived by the re-parse, not restored.
		`MATCH (a:Symbol {fqn: "m/lib.Helper"}), (b:Symbol {fqn: "m/lib.Target"}) CREATE (a)-[:CALLS]->(b)`,
	} {
		if err := db.exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	in, err := db.IncomingEdges([]string{"lib/lib.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Calls) != 1 || in.Calls[0].CallerFQN != "m/app.Run" || in.Calls[0].CalleeFQN != "m/lib.Target" {
		t.Errorf("incoming calls = %+v, want only app.Run -> lib.Target", in.Calls)
	}
	existing, err := db.ExistingSymbolFQNs([]string{"m/lib.Target", "m/nope"})
	if err != nil || !existing["m/lib.Target"] || existing["m/nope"] {
		t.Errorf("ExistingSymbolFQNs = %v, %v", existing, err)
	}
}
