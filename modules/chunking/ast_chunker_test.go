package chunking

import (
	"strings"
	"testing"
)

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		filename string
		want     Language
	}{
		{"main.go", LangGo},
		{"app.py", LangPython},
		{"index.js", LangJavaScript},
		{"app.tsx", LangTypeScript},
		{"Main.java", LangJava},
		{"prog.c", LangC},
		{"lib.cpp", LangCPP},
		{"main.rs", LangRust},
		{"Program.cs", LangCSharp},
		{"data.json", LangUnknown},
		{"README.md", LangUnknown},
	}

	for _, tt := range tests {
		got := DetectLanguage(tt.filename)
		if got != tt.want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", tt.filename, got, tt.want)
		}
	}
}

func TestIsCodeSourceFile(t *testing.T) {
	if !IsCodeSourceFile("main.go") {
		t.Error("expected main.go to be a code source file")
	}
	if IsCodeSourceFile("README.md") {
		t.Error("expected README.md to not be a code source file")
	}
}

func TestASTChunkerGoCode(t *testing.T) {
	source := `package main

import "fmt"

// Add adds two numbers.
func Add(a, b int) int {
	return a + b
}

// Person represents a person.
type Person struct {
	Name string
	Age  int
}

// Greet prints a greeting.
func (p *Person) Greet() {
	fmt.Printf("Hello, %s!\n", p.Name)
}

var version = "1.0.0"
`

	chunker := NewASTChunker(ASTChunkerConfig{
		MaxChunkSize:   2000,
		ChunkOverlap:   50,
		AddLineNumbers: false,
	})

	chunks := chunker.ChunkCode(source, "main.go")

	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks (preamble, func, type), got %d", len(chunks))
	}

	// Check we have the expected node types.
	nodeTypes := make(map[string]bool)
	for _, c := range chunks {
		nodeTypes[c.NodeType] = true
		// Verify metadata.
		if c.Metadata["language"] != "go" {
			t.Errorf("chunk %q: expected language 'go', got %v", c.Name, c.Metadata["language"])
		}
		if c.Metadata["file_path"] != "main.go" {
			t.Errorf("chunk %q: expected file_path 'main.go', got %v", c.Name, c.Metadata["file_path"])
		}
	}

	if !nodeTypes["preamble"] {
		t.Error("expected a 'preamble' chunk")
	}
	if !nodeTypes["function"] {
		t.Error("expected a 'function' chunk")
	}
	if !nodeTypes["method"] {
		t.Error("expected a 'method' chunk")
	}
	if !nodeTypes["struct"] {
		t.Error("expected a 'struct' chunk")
	}
}

func TestASTChunkerGoFunctionNames(t *testing.T) {
	source := `package math

func Add(a, b int) int { return a + b }

func Sub(a, b int) int { return a - b }

type Calculator struct{}

func (c *Calculator) Multiply(a, b int) int { return a * b }
`

	chunker := NewASTChunker(ASTChunkerConfig{
		MaxChunkSize:   2000,
		ChunkOverlap:   50,
		AddLineNumbers: false,
	})

	chunks := chunker.ChunkCode(source, "math.go")

	foundNames := make(map[string]string) // name -> nodeType
	for _, c := range chunks {
		if c.Name != "" {
			foundNames[c.Name] = c.NodeType
		}
	}

	if foundNames["Add"] != "function" {
		t.Error("expected to find function 'Add'")
	}
	if foundNames["Sub"] != "function" {
		t.Error("expected to find function 'Sub'")
	}
	if foundNames["Calculator.Multiply"] != "method" {
		t.Error("expected to find method 'Calculator.Multiply'")
	}
}

func TestASTChunkerPythonCode(t *testing.T) {
	source := `import os
import sys

class MyClass:
    def __init__(self):
        self.value = 0

    def method(self):
        return self.value

def standalone_func():
    pass

async def async_func():
    await something()
`

	chunker := NewASTChunker(ASTChunkerConfig{
		MaxChunkSize:   2000,
		ChunkOverlap:   50,
		AddLineNumbers: false,
	})

	chunks := chunker.ChunkCode(source, "app.py")

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}

	nodeTypes := make(map[string]bool)
	for _, c := range chunks {
		nodeTypes[c.NodeType] = true
	}

	if !nodeTypes["class"] {
		t.Error("expected a 'class' chunk for Python")
	}
	if !nodeTypes["function"] {
		t.Error("expected a 'function' chunk for Python")
	}
}

func TestASTChunkerTypeScriptCode(t *testing.T) {
	source := `import { Component } from 'react';

export interface Config {
    debug: boolean;
}

export class App {
    constructor() {}
    render() {}
}

export function createApp(): App {
    return new App();
}

export const helper = () => {
    return true;
};
`

	chunker := NewASTChunker(ASTChunkerConfig{
		MaxChunkSize:   2000,
		ChunkOverlap:   50,
		AddLineNumbers: false,
	})

	chunks := chunker.ChunkCode(source, "app.ts")

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}

	nodeTypes := make(map[string]bool)
	for _, c := range chunks {
		nodeTypes[c.NodeType] = true
	}

	if !nodeTypes["class"] {
		t.Error("expected a 'class' chunk for TypeScript")
	}
}

func TestASTChunkerRustCode(t *testing.T) {
	source := `use std::io;

pub struct Server {
    port: u16,
}

impl Server {
    pub fn new(port: u16) -> Self {
        Server { port }
    }

    pub async fn run(&self) {
        println!("Running on port {}", self.port);
    }
}

fn helper() -> bool {
    true
}

pub trait Handler {
    fn handle(&self);
}
`

	chunker := NewASTChunker(ASTChunkerConfig{
		MaxChunkSize:   2000,
		ChunkOverlap:   50,
		AddLineNumbers: false,
	})

	chunks := chunker.ChunkCode(source, "server.rs")

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}

	nodeTypes := make(map[string]bool)
	for _, c := range chunks {
		nodeTypes[c.NodeType] = true
	}

	if !nodeTypes["struct"] {
		t.Error("expected a 'struct' chunk for Rust")
	}
	if !nodeTypes["impl"] {
		t.Error("expected an 'impl' chunk for Rust")
	}
}

func TestASTChunkerLineNumbers(t *testing.T) {
	source := `package main

func Hello() {
	println("hello")
}
`

	chunker := NewASTChunker(ASTChunkerConfig{
		MaxChunkSize:   2000,
		ChunkOverlap:   50,
		AddLineNumbers: true,
	})

	chunks := chunker.ChunkCode(source, "main.go")

	// Line numbers should be added.
	for _, c := range chunks {
		if !strings.Contains(c.Text, "|") {
			t.Errorf("expected line numbers in chunk, got: %s", c.Text[:min(50, len(c.Text))])
		}
	}
}

func TestASTChunkerOversizedSplit(t *testing.T) {
	// Create a very large function.
	var sb strings.Builder
	sb.WriteString("package main\n\nfunc BigFunc() {\n")
	for i := 0; i < 100; i++ {
		sb.WriteString("\tprintln(\"line\")\n")
	}
	sb.WriteString("}\n")

	chunker := NewASTChunker(ASTChunkerConfig{
		MaxChunkSize:   200, // Very small to force splitting.
		ChunkOverlap:   20,
		AddLineNumbers: false,
	})

	chunks := chunker.ChunkCode(sb.String(), "big.go")

	if len(chunks) < 2 {
		t.Errorf("expected oversized function to be split into multiple chunks, got %d", len(chunks))
	}
}

func TestASTChunkerFallbackSlidingWindow(t *testing.T) {
	// Unknown language should fall back to sliding window.
	source := strings.Repeat("some data line\n", 20)

	chunker := NewASTChunker(ASTChunkerConfig{
		MaxChunkSize:   200,
		ChunkOverlap:   20,
		AddLineNumbers: false,
	})

	chunks := chunker.ChunkCode(source, "data.csv")

	if len(chunks) == 0 {
		t.Error("expected at least one chunk from sliding window fallback")
	}
	for _, c := range chunks {
		if c.NodeType != "block" {
			t.Errorf("expected block node type in sliding window, got %q", c.NodeType)
		}
	}
}

func TestASTChunkerChunkInterface(t *testing.T) {
	chunker := NewASTChunker(DefaultASTChunkerConfig())

	chunks := chunker.Chunk("func hello() {}\nfunc world() {}")
	if len(chunks) == 0 {
		t.Error("Chunk() should return at least one chunk")
	}
}

func TestASTChunkerChunkWithMetadata(t *testing.T) {
	chunker := NewASTChunker(DefaultASTChunkerConfig())

	metadata := map[string]any{
		"file_path": "main.go",
		"source":    "test",
	}

	items := chunker.ChunkWithMetadata("package main\n\nfunc Hello() {}\n", metadata)
	if len(items) == 0 {
		t.Error("ChunkWithMetadata() should return at least one item")
	}

	for _, item := range items {
		if item.Metadata["source"] != "test" {
			t.Error("expected source metadata to be preserved")
		}
		if item.Metadata["file_path"] != "main.go" {
			t.Error("expected file_path metadata to be preserved")
		}
	}
}

func TestAddLineNumbers(t *testing.T) {
	text := "line one\nline two\nline three"
	result := addLineNumbers(text, 10)

	if !strings.Contains(result, "10|line one") {
		t.Errorf("expected line 10 prefix, got: %s", result)
	}
	if !strings.Contains(result, "12|line three") {
		t.Errorf("expected line 12 prefix, got: %s", result)
	}
}

func TestASTChunkerGoParseError(t *testing.T) {
	// Invalid Go syntax should fall back to regex patterns.
	source := `package main

func broken( {
    // syntax error
}

func valid() {}
`

	chunker := NewASTChunker(ASTChunkerConfig{
		MaxChunkSize:   2000,
		ChunkOverlap:   50,
		AddLineNumbers: false,
	})

	chunks := chunker.ChunkCode(source, "broken.go")

	if len(chunks) == 0 {
		t.Error("expected fallback to produce chunks even with invalid Go syntax")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
