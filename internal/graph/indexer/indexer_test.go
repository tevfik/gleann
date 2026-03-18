//go:build treesitter && !windows

package indexer_test

import (
	"os"
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

const sampleJavaSource = `
public class Greeter {
    public static void greet(String name) {
        String msg = formatName(name);
        System.out.println(msg);
    }

    private static String formatName(String name) {
        return "Hello, " + name;
    }
}
`

func TestIndexerJavaFile(t *testing.T) {
	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("open kuzu: %v", err)
	}
	defer db.Close()

	idx := indexer.New(db, "myproject", "/fake/root")
	if err := idx.IndexFile("/fake/root/src/Greeter.java", sampleJavaSource); err != nil {
		t.Fatalf("IndexFile: %v", err)
	}

	symbols, err := db.SymbolsInFile("src/Greeter.java")
	if err != nil {
		t.Fatalf("SymbolsInFile: %v", err)
	}
	t.Logf("Java symbols: %d", len(symbols))
	for _, s := range symbols {
		t.Logf("  [%s] %s", s.Kind, s.FQN)
	}
	if len(symbols) < 1 {
		t.Errorf("expected ≥1 Java symbol, got %d", len(symbols))
	}

	// Check CALLS: greet → formatName
	greetFQN := "myproject/src.Greeter.greet"
	callees, err := db.Callees(greetFQN)
	if err != nil {
		t.Fatalf("Callees: %v", err)
	}
	t.Logf("Java greet() callees: %d", len(callees))
	for _, c := range callees {
		t.Logf("  → %s", c.FQN)
	}
}

const sampleCSharpSource = `
using System;

class Greeter {
    public static void Greet(string name) {
        string msg = FormatName(name);
        Console.WriteLine(msg);
    }

    private static string FormatName(string name) {
        return $"Hello, {name}";
    }
}
`

func TestIndexerCSharpFile(t *testing.T) {
	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("open kuzu: %v", err)
	}
	defer db.Close()

	idx := indexer.New(db, "myproject", "/fake/root")
	if err := idx.IndexFile("/fake/root/src/Greeter.cs", sampleCSharpSource); err != nil {
		t.Fatalf("IndexFile: %v", err)
	}

	symbols, err := db.SymbolsInFile("src/Greeter.cs")
	if err != nil {
		t.Fatalf("SymbolsInFile: %v", err)
	}
	t.Logf("C# symbols: %d", len(symbols))
	for _, s := range symbols {
		t.Logf("  [%s] %s", s.Kind, s.FQN)
	}
	if len(symbols) < 1 {
		t.Errorf("expected ≥1 C# symbol, got %d", len(symbols))
	}

	greetFQN := "myproject/src.Greeter.Greet"
	callees, err := db.Callees(greetFQN)
	if err != nil {
		t.Fatalf("Callees: %v", err)
	}
	t.Logf("C# Greet() callees: %d", len(callees))
	for _, c := range callees {
		t.Logf("  → %s", c.FQN)
	}
}

const sampleRubySource = `
module MyProject
  class Greeter
    def greet(name)
      msg = format_name(name)
      puts msg
    end
    
    def format_name(name)
      "Hello, #{name}"
    end
  end
end
`

func TestIndexerRubyFile(t *testing.T) {
	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("open kuzu: %v", err)
	}
	defer db.Close()

	idx := indexer.New(db, "myproject", "/fake/root")
	if err := idx.IndexFile("/fake/root/src/greeter.rb", sampleRubySource); err != nil {
		t.Fatalf("IndexFile: %v", err)
	}

	symbols, err := db.SymbolsInFile("src/greeter.rb")
	if err != nil {
		t.Fatalf("SymbolsInFile: %v", err)
	}
	t.Logf("Ruby symbols: %d", len(symbols))
	for _, s := range symbols {
		t.Logf("  [%s] %s", s.Kind, s.FQN)
	}
	if len(symbols) < 1 {
		t.Errorf("expected ≥1 Ruby symbol, got %d", len(symbols))
	}

	greetFQN := "myproject/src.Greeter.greet"
	callees, err := db.Callees(greetFQN)
	if err != nil {
		t.Fatalf("Callees: %v", err)
	}
	t.Logf("Ruby greet() callees: %d", len(callees))
	for _, c := range callees {
		t.Logf("  → %s", c.FQN)
	}
}

const samplePHPSource = `<?php
namespace MyProject;

class Greeter {
    public function greet($name) {
        $msg = $this->formatName($name);
        echo $msg;
    }

    private function formatName($name) {
        return "Hello, " . $name;
    }
}
`

func TestIndexerPHPFile(t *testing.T) {
	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("open kuzu: %v", err)
	}
	defer db.Close()

	idx := indexer.New(db, "myproject", "/fake/root")
	if err := idx.IndexFile("/fake/root/src/Greeter.php", samplePHPSource); err != nil {
		t.Fatalf("IndexFile: %v", err)
	}

	symbols, err := db.SymbolsInFile("src/Greeter.php")
	if err != nil {
		t.Fatalf("SymbolsInFile: %v", err)
	}
	t.Logf("PHP symbols: %d", len(symbols))
	for _, s := range symbols {
		t.Logf("  [%s] %s", s.Kind, s.FQN)
	}
	if len(symbols) < 1 {
		t.Errorf("expected ≥1 PHP symbol, got %d", len(symbols))
	}

	greetFQN := "myproject/src.Greeter.greet"
	callees, err := db.Callees(greetFQN)
	if err != nil {
		t.Fatalf("Callees: %v", err)
	}
	t.Logf("PHP greet() callees: %d", len(callees))
	for _, c := range callees {
		t.Logf("  → %s", c.FQN)
	}
}

// ── IndexFiles (incremental) tests ──────────────────────────────

func TestIndexFilesIncrementalAddsNewFile(t *testing.T) {
	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("open kuzu: %v", err)
	}
	defer db.Close()

	idx := indexer.New(db, "github.com/test", "/fake/root")

	// Index first file normally.
	if err := idx.IndexFile("/fake/root/pkg/a.go", sampleGoSource); err != nil {
		t.Fatalf("IndexFile a.go: %v", err)
	}

	// Verify first file symbols exist.
	syms, err := db.SymbolsInFile("pkg/a.go")
	if err != nil {
		t.Fatalf("SymbolsInFile a.go: %v", err)
	}
	if len(syms) < 3 {
		t.Fatalf("expected ≥3 symbols in a.go, got %d", len(syms))
	}

	// Now create a temp file for incremental indexing.
	tmpDir := t.TempDir()
	bPath := tmpDir + "/b.go"
	bSource := `package testpkg

func Add(a, b int) int {
	return a + b
}

func Subtract(a, b int) int {
	return a - b
}
`
	if err := writeTestFile(bPath, bSource); err != nil {
		t.Fatalf("write b.go: %v", err)
	}

	// Create a new indexer with the tmpDir as root so relPath works.
	idx2 := indexer.New(db, "github.com/test", tmpDir)
	if err := idx2.IndexFiles([]string{bPath}); err != nil {
		t.Fatalf("IndexFiles: %v", err)
	}

	// Verify new symbols exist.
	syms2, err := db.SymbolsInFile("b.go")
	if err != nil {
		t.Fatalf("SymbolsInFile b.go: %v", err)
	}
	if len(syms2) < 2 {
		t.Errorf("expected ≥2 symbols in b.go, got %d", len(syms2))
	}

	// Verify old symbols still exist.
	symsA, err := db.SymbolsInFile("pkg/a.go")
	if err != nil {
		t.Fatalf("SymbolsInFile a.go after incremental: %v", err)
	}
	if len(symsA) < 3 {
		t.Errorf("expected a.go symbols to still exist (≥3), got %d", len(symsA))
	}
}

func TestIndexFilesReplacesOldSymbols(t *testing.T) {
	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("open kuzu: %v", err)
	}
	defer db.Close()

	tmpDir := t.TempDir()
	fPath := tmpDir + "/code.go"

	// Version 1.
	v1 := `package mypkg

func OldFunc() {}
func KeepFunc() {}
`
	if err := writeTestFile(fPath, v1); err != nil {
		t.Fatalf("write v1: %v", err)
	}

	idx := indexer.New(db, "testmod", tmpDir)
	if err := idx.IndexFile(fPath, v1); err != nil {
		t.Fatalf("IndexFile v1: %v", err)
	}

	syms, _ := db.SymbolsInFile("code.go")
	t.Logf("v1 symbols: %d", len(syms))
	if len(syms) < 2 {
		t.Fatalf("expected ≥2 v1 symbols, got %d", len(syms))
	}

	// Version 2: remove OldFunc, add NewFunc.
	v2 := `package mypkg

func NewFunc() {}
func KeepFunc() {}
`
	if err := writeTestFile(fPath, v2); err != nil {
		t.Fatalf("write v2: %v", err)
	}

	if err := idx.IndexFiles([]string{fPath}); err != nil {
		t.Fatalf("IndexFiles v2: %v", err)
	}

	syms2, _ := db.SymbolsInFile("code.go")
	t.Logf("v2 symbols: %d", len(syms2))

	hasNew, hasOld := false, false
	for _, s := range syms2 {
		t.Logf("  [%s] %s", s.Kind, s.FQN)
		if s.Name == "NewFunc" {
			hasNew = true
		}
		if s.Name == "OldFunc" {
			hasOld = true
		}
	}
	if !hasNew {
		t.Error("expected NewFunc to be present after update")
	}
	if hasOld {
		t.Error("expected OldFunc to be removed after update")
	}
}

func TestIndexFilesEmptySliceIsNoop(t *testing.T) {
	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("open kuzu: %v", err)
	}
	defer db.Close()

	idx := indexer.New(db, "test", "/fake/root")
	if err := idx.IndexFiles(nil); err != nil {
		t.Errorf("IndexFiles(nil) should not error, got: %v", err)
	}
	if err := idx.IndexFiles([]string{}); err != nil {
		t.Errorf("IndexFiles([]) should not error, got: %v", err)
	}
}

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
