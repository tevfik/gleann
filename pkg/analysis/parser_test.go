package analysis

import (
	"testing"
)

func TestParsePython(t *testing.T) {
	source := `import os

class Calculator:
    def add(self, a, b):
        return a + b

    def subtract(self, a, b):
        return a - b

def main():
    c = Calculator()
    print(c.add(1, 2))
`
	symbols := NewParser(DefaultConfig()).Parse(source, "calc.py")
	if len(symbols) == 0 {
		t.Fatal("expected symbols from Python source")
	}

	// Check we got function and class symbols
	var hasClass, hasFunc bool
	for _, s := range symbols {
		if s.NodeType == "class" {
			hasClass = true
		}
		if s.NodeType == "function" || s.NodeType == "method" {
			hasFunc = true
		}
		if s.Language != "python" {
			t.Errorf("expected language=python, got %s", s.Language)
		}
	}
	if !hasClass {
		t.Error("expected a class symbol")
	}
	if !hasFunc {
		t.Error("expected a function symbol")
	}
}

func TestParseGo(t *testing.T) {
	source := `package main

import "fmt"

// Calculator provides arithmetic.
type Calculator struct{}

// Add returns a + b.
func (c *Calculator) Add(a, b int) int {
	return a + b
}

func main() {
	fmt.Println("hello")
}
`
	symbols := NewParser(DefaultConfig()).Parse(source, "main.go")
	if len(symbols) == 0 {
		t.Fatal("expected symbols from Go source")
	}

	var hasStruct, hasMethod, hasFunc bool
	for _, s := range symbols {
		t.Logf("symbol: type=%q name=%q lines=%d-%d", s.NodeType, s.Name, s.StartLine, s.EndLine)
		switch {
		case s.NodeType == "struct" && s.Name == "Calculator":
			hasStruct = true
		case s.NodeType == "method" && s.Name == "Calculator.Add":
			hasMethod = true
		case s.NodeType == "function" && s.Name == "main":
			hasFunc = true
		}
		if s.Language != "go" {
			t.Errorf("expected language=go, got %s", s.Language)
		}
	}
	if !hasStruct {
		t.Error("expected Calculator struct")
	}
	if !hasMethod {
		t.Error("expected Add method")
	}
	if !hasFunc {
		t.Error("expected main function")
	}
}

func TestParseJavaScript(t *testing.T) {
	source := `const express = require('express');

class Router {
    constructor() {
        this.routes = [];
    }

    get(path, handler) {
        this.routes.push({path, handler});
    }
}

function startServer(port) {
    const app = new Router();
    app.listen(port);
}
`
	symbols := NewParser(DefaultConfig()).Parse(source, "server.js")
	if len(symbols) == 0 {
		t.Fatal("expected symbols from JS source")
	}

	for _, s := range symbols {
		if s.Language != "javascript" {
			t.Errorf("expected language=javascript, got %s", s.Language)
		}
	}
}

func TestParseRust(t *testing.T) {
	source := `use std::io;

struct Point {
    x: f64,
    y: f64,
}

impl Point {
    fn new(x: f64, y: f64) -> Self {
        Point { x, y }
    }

    fn distance(&self, other: &Point) -> f64 {
        ((self.x - other.x).powi(2) + (self.y - other.y).powi(2)).sqrt()
    }
}

fn main() {
    let p1 = Point::new(0.0, 0.0);
    let p2 = Point::new(3.0, 4.0);
    println!("{}", p1.distance(&p2));
}
`
	symbols := NewParser(DefaultConfig()).Parse(source, "main.rs")
	if len(symbols) == 0 {
		t.Fatal("expected symbols from Rust source")
	}
}

func TestDetectLanguage(t *testing.T) {
	tests := map[string]Language{
		"main.go":     LangGo,
		"app.py":      LangPython,
		"index.js":    LangJavaScript,
		"server.ts":   LangTypeScript,
		"Main.java":   LangJava,
		"parser.c":    LangC,
		"engine.cpp":  LangCPP,
		"lib.rs":      LangRust,
		"Program.cs":  LangCSharp,
		"readme.md":   LangUnknown,
	}
	for file, want := range tests {
		got := DetectLanguage(file)
		if got != want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", file, got, want)
		}
	}
}

func TestIsCodeFile(t *testing.T) {
	if !IsCodeFile("main.go") {
		t.Error("main.go should be a code file")
	}
	if IsCodeFile("README.md") {
		t.Error("README.md should not be a code file")
	}
}

func TestExtractFunctions(t *testing.T) {
	source := `def add(a, b):
    return a + b

def subtract(a, b):
    return a - b

x = 42
`
	funcs := NewParser(DefaultConfig()).ExtractFunctions(source, "math.py")
	if len(funcs) < 2 {
		t.Errorf("expected >= 2 functions, got %d", len(funcs))
	}
	for _, f := range funcs {
		if f.NodeType != "function" && f.NodeType != "method" {
			t.Errorf("expected function/method, got %s", f.NodeType)
		}
	}
}

func TestExtractClasses(t *testing.T) {
	source := `package main

type Server struct {
	port int
}

type Handler interface {
	Handle()
}

func main() {}
`
	classes := NewParser(DefaultConfig()).ExtractClasses(source, "main.go")
	if len(classes) < 2 {
		t.Errorf("expected >= 2 classes/interfaces, got %d", len(classes))
	}
}

func TestTreeSitterAvailable(t *testing.T) {
	// Just ensure it doesn't panic
	_ = TreeSitterAvailable()
}
