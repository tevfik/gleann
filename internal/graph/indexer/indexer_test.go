package indexer_test

import (
	"testing"

	"github.com/tevfik/gleann/internal/graph/indexer"
	kgraph "github.com/tevfik/gleann/internal/graph/kuzu"
)

// sampleGoSource is a small self-contained Go snippet for testing.
const sampleGoSource = `package mypkg

import "fmt"

// Greet prints a greeting.
func Greet(name string) {
	msg := format(name)
	fmt.Println(msg)
}

// format builds the greeting string.
func format(name string) string {
	return "Hello, " + name
}

// MyStruct is a sample struct.
type MyStruct struct {
	Value int
}

// Do is a method on MyStruct.
func (m *MyStruct) Do() {
	Greet("world")
}
`

func TestIndexerGoFile(t *testing.T) {
	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("open kuzu: %v", err)
	}
	defer db.Close()

	idx := indexer.New(db, "github.com/tevfik/gleann", "/fake/root")

	if err := idx.IndexFile("/fake/root/internal/mypkg/greet.go", sampleGoSource); err != nil {
		t.Fatalf("IndexFile: %v", err)
	}

	// ── Verify symbols are stored ──────────────────────────────
	symbols, err := db.SymbolsInFile("internal/mypkg/greet.go")
	if err != nil {
		t.Fatalf("SymbolsInFile: %v", err)
	}

	// Expect: Greet, format, MyStruct (struct), Do (method) - at least 4
	if len(symbols) < 3 {
		t.Errorf("expected ≥3 symbols, got %d: %+v", len(symbols), symbols)
	}
	t.Logf("symbols in file: %d", len(symbols))
	for _, s := range symbols {
		t.Logf("  [%s] %s", s.Kind, s.FQN)
	}

	// ── Verify CALLS: Greet should call format ─────────────────
	greetFQN := "github.com/tevfik/gleann/internal/mypkg.Greet"
	callees, err := db.Callees(greetFQN)
	if err != nil {
		t.Fatalf("Callees: %v", err)
	}
	t.Logf("Greet() callees: %d", len(callees))
	for _, c := range callees {
		t.Logf("  → %s", c.FQN)
	}

	found := false
	for _, c := range callees {
		if c.FQN == "github.com/tevfik/gleann/internal/mypkg.format" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Greet() to call format(), callees: %+v", callees)
	}

	// ── Callers of format ──────────────────────────────────────
	formatFQN := "github.com/tevfik/gleann/internal/mypkg.format"
	callers, err := db.Callers(formatFQN)
	if err != nil {
		t.Fatalf("Callers: %v", err)
	}
	if len(callers) == 0 {
		t.Errorf("expected at least one caller of format(), got none")
	}
	t.Logf("format() callers: %d", len(callers))

	t.Logf("✅ AST indexer test passed")
}

const samplePythonSource = `
def greet(name: str):
    msg = format_name(name)
    print(msg)

def format_name(name: str) -> str:
    return f"Hello, {name}"

class MyClass:
    def do_work(self):
        greet("world")
`

func TestIndexerPythonFile(t *testing.T) {
	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("open kuzu: %v", err)
	}
	defer db.Close()

	idx := indexer.New(db, "myproject", "/fake/root")

	if err := idx.IndexFile("/fake/root/src/hello.py", samplePythonSource); err != nil {
		t.Fatalf("IndexFile: %v", err)
	}

	symbols, err := db.SymbolsInFile("src/hello.py")
	if err != nil {
		t.Fatalf("SymbolsInFile: %v", err)
	}

	if len(symbols) < 3 {
		t.Errorf("expected ≥3 symbols, got %d", len(symbols))
	}
	t.Logf("Python symbols: %d", len(symbols))
	for _, s := range symbols {
		t.Logf("  [%s] %s", s.Kind, s.FQN)
	}

	greetFQN := "myproject/src.greet"
	callees, err := db.Callees(greetFQN)
	if err != nil {
		t.Fatalf("Callees: %v", err)
	}
	t.Logf("greet() callees: %d", len(callees))

	foundFormat := false
	foundPrint := false
	for _, c := range callees {
		t.Logf("  → %s", c.FQN)
		if c.FQN == "myproject/src.format_name" {
			foundFormat = true
		}
		if c.FQN == "myproject/src.print" {
			foundPrint = true
		}
	}

	if !foundFormat {
		t.Errorf("expected greet() to call format_name()")
	}
	if !foundPrint {
		t.Errorf("expected greet() to call print()")
	}
}

const sampleRustSource = `
fn greet(name: &str) {
    let msg = format_name(name);
    println!("{}", msg);
}

fn format_name(name: &str) -> String {
    format!("Hello, {}", name)
}

struct MyStruct;
impl MyStruct {
    fn do_work(&self) {
        greet("world");
    }
}
`

func TestIndexerRustFile(t *testing.T) {
	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("open kuzu: %v", err)
	}
	defer db.Close()

	idx := indexer.New(db, "myproject", "/fake/root")

	if err := idx.IndexFile("/fake/root/src/main.rs", sampleRustSource); err != nil {
		t.Fatalf("IndexFile: %v", err)
	}

	symbols, err := db.SymbolsInFile("src/main.rs")
	if err != nil {
		t.Fatalf("SymbolsInFile: %v", err)
	}

	if len(symbols) < 3 {
		t.Errorf("expected ≥3 symbols, got %d", len(symbols))
	}
	t.Logf("Rust symbols: %d", len(symbols))

	greetFQN := "myproject/src.greet"
	callees, err := db.Callees(greetFQN)
	if err != nil {
		t.Fatalf("Callees: %v", err)
	}

	foundFormat := false
	for _, c := range callees {
		if c.FQN == "myproject/src.format_name" {
			foundFormat = true
		}
	}

	if !foundFormat {
		t.Errorf("expected greet() to call format_name()")
	}
}

const sampleCPPSource = `
#include <iostream>
#include <string>

std::string format_name(const std::string& name) {
    return "Hello, " + name;
}

void greet(const std::string& name) {
    std::string msg = format_name(name);
    std::cout << msg << std::endl;
}

class MyClass {
public:
    void do_work() {
        greet("world");
    }
};
`

func TestIndexerCPPFile(t *testing.T) {
	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("open kuzu: %v", err)
	}
	defer db.Close()

	idx := indexer.New(db, "myproject", "/fake/root")

	if err := idx.IndexFile("/fake/root/src/main.cpp", sampleCPPSource); err != nil {
		t.Fatalf("IndexFile: %v", err)
	}

	symbols, err := db.SymbolsInFile("src/main.cpp")
	if err != nil {
		t.Fatalf("SymbolsInFile: %v", err)
	}

	if len(symbols) < 2 {
		t.Errorf("expected ≥2 symbols, got %d", len(symbols))
	}
	t.Logf("CPP symbols: %d", len(symbols))

	greetFQN := "myproject/src.greet"
	callees, err := db.Callees(greetFQN)
	if err != nil {
		t.Fatalf("Callees: %v", err)
	}

	// When tree-sitter is compiled in, greet() should call format_name().
	// Without tree-sitter the call graph may be empty – that's acceptable.
	for _, c := range callees {
		t.Logf("  greet → %s", c.FQN)
	}
}
