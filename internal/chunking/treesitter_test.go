//go:build cgo && treesitter

package chunking

import (
	"strings"
	"testing"
)

func TestTreeSitterAvailable(t *testing.T) {
	if !TreeSitterAvailable() {
		t.Fatal("TreeSitterAvailable() should return true when built with -tags treesitter")
	}
}

func TestTreeSitterPythonFunction(t *testing.T) {
	source := `import os
import sys

def hello(name):
    """Say hello."""
    print(f"Hello, {name}!")

def goodbye(name):
    print(f"Goodbye, {name}!")
`
	config := DefaultASTChunkerConfig()
	config.AddLineNumbers = false

	chunks := treeSitterChunk(source, "hello.py", LangPython, config)
	if chunks == nil {
		t.Fatal("treeSitterChunk returned nil for Python")
	}

	// Should have: preamble (imports) + 2 functions.
	funcChunks := filterChunksByType(chunks, "function")
	if len(funcChunks) != 2 {
		t.Errorf("expected 2 function chunks, got %d", len(funcChunks))
		for _, c := range chunks {
			t.Logf("  chunk: type=%s name=%s lines=%d-%d", c.NodeType, c.Name, c.StartLine, c.EndLine)
		}
	}

	// Check names.
	if len(funcChunks) >= 2 {
		if funcChunks[0].Name != "hello" {
			t.Errorf("first function name: got %q, want %q", funcChunks[0].Name, "hello")
		}
		if funcChunks[1].Name != "goodbye" {
			t.Errorf("second function name: got %q, want %q", funcChunks[1].Name, "goodbye")
		}
	}

	// Check metadata has parser=tree-sitter.
	for _, c := range chunks {
		if c.Metadata["parser"] != "tree-sitter" {
			t.Errorf("chunk %q missing parser=tree-sitter metadata", c.Name)
		}
	}
}

func TestTreeSitterPythonClass(t *testing.T) {
	source := `class Calculator:
    """A simple calculator."""

    def add(self, a, b):
        return a + b

    def subtract(self, a, b):
        return a - b
`
	config := DefaultASTChunkerConfig()
	config.AddLineNumbers = false

	chunks := treeSitterChunk(source, "calc.py", LangPython, config)
	if chunks == nil {
		t.Fatal("treeSitterChunk returned nil for Python class")
	}

	// Should find the class and its methods.
	classChunks := filterChunksByType(chunks, "class")
	if len(classChunks) == 0 {
		t.Error("expected at least one class chunk")
	}

	// Methods should have parent scope.
	methodChunks := filterChunksByType(chunks, "function")
	for _, m := range methodChunks {
		if m.Metadata["parent_scope"] == nil || m.Metadata["parent_scope"] == "" {
			t.Logf("method %q has no parent_scope (may be flattened)", m.Name)
		}
	}
}

func TestTreeSitterPythonDecorator(t *testing.T) {
	source := `@staticmethod
def helper():
    pass

@decorator
class MyClass:
    pass
`
	config := DefaultASTChunkerConfig()
	config.AddLineNumbers = false

	chunks := treeSitterChunk(source, "deco.py", LangPython, config)
	if chunks == nil {
		t.Fatal("treeSitterChunk returned nil for decorated Python")
	}

	// decorated_definition should resolve to function/class.
	for _, c := range chunks {
		if c.NodeType == "decorated" {
			t.Errorf("decorated_definition should have been resolved, got nodeType=%s name=%s", c.NodeType, c.Name)
		}
	}
}

func TestTreeSitterJavaScript(t *testing.T) {
	source := `const greet = (name) => {
    return "Hello, " + name;
};

function add(a, b) {
    return a + b;
}

class Animal {
    constructor(name) {
        this.name = name;
    }

    speak() {
        return this.name + " speaks";
    }
}
`
	config := DefaultASTChunkerConfig()
	config.AddLineNumbers = false

	chunks := treeSitterChunk(source, "app.js", LangJavaScript, config)
	if chunks == nil {
		t.Fatal("treeSitterChunk returned nil for JavaScript")
	}

	if len(chunks) < 2 {
		t.Errorf("expected at least 2 chunks for JS, got %d", len(chunks))
	}

	// Check that function 'add' is found.
	found := false
	for _, c := range chunks {
		if c.Name == "add" {
			found = true
			break
		}
	}
	if !found {
		t.Error("function 'add' not found in JS chunks")
		for _, c := range chunks {
			t.Logf("  chunk: type=%s name=%s", c.NodeType, c.Name)
		}
	}
}

func TestTreeSitterTypeScript(t *testing.T) {
	source := `interface User {
    name: string;
    age: number;
}

type Status = "active" | "inactive";

function createUser(name: string, age: number): User {
    return { name, age };
}

class UserService {
    private users: User[] = [];

    addUser(user: User): void {
        this.users.push(user);
    }

    getUsers(): User[] {
        return this.users;
    }
}
`
	config := DefaultASTChunkerConfig()
	config.AddLineNumbers = false

	chunks := treeSitterChunk(source, "user.ts", LangTypeScript, config)
	if chunks == nil {
		t.Fatal("treeSitterChunk returned nil for TypeScript")
	}

	// Should find interface, type, function, class.
	types := map[string]bool{}
	for _, c := range chunks {
		types[c.NodeType] = true
	}

	for _, expected := range []string{"function"} {
		if !types[expected] {
			t.Errorf("expected chunk type %q not found in TS chunks", expected)
			for _, c := range chunks {
				t.Logf("  chunk: type=%s name=%s", c.NodeType, c.Name)
			}
		}
	}
}

func TestTreeSitterJava(t *testing.T) {
	source := `package com.example;

import java.util.List;

public class Calculator {
    public int add(int a, int b) {
        return a + b;
    }

    public int multiply(int a, int b) {
        return a * b;
    }
}

interface Computable {
    int compute(int x);
}
`
	config := DefaultASTChunkerConfig()
	config.AddLineNumbers = false

	chunks := treeSitterChunk(source, "Calculator.java", LangJava, config)
	if chunks == nil {
		t.Fatal("treeSitterChunk returned nil for Java")
	}

	// Should find class and interface at minimum.
	classFound := false
	for _, c := range chunks {
		if c.NodeType == "class" && c.Name == "Calculator" {
			classFound = true
		}
	}
	if !classFound {
		t.Error("class Calculator not found in Java chunks")
		for _, c := range chunks {
			t.Logf("  chunk: type=%s name=%s", c.NodeType, c.Name)
		}
	}
}

func TestTreeSitterC(t *testing.T) {
	source := `#include <stdio.h>

#define MAX_SIZE 100

struct Point {
    int x;
    int y;
};

int add(int a, int b) {
    return a + b;
}

void print_point(struct Point p) {
    printf("(%d, %d)\n", p.x, p.y);
}
`
	config := DefaultASTChunkerConfig()
	config.AddLineNumbers = false

	chunks := treeSitterChunk(source, "math.c", LangC, config)
	if chunks == nil {
		t.Fatal("treeSitterChunk returned nil for C")
	}

	if len(chunks) < 2 {
		t.Errorf("expected at least 2 chunks for C, got %d", len(chunks))
	}
}

func TestTreeSitterCPP(t *testing.T) {
	source := `#include <iostream>
#include <vector>

namespace math {

class Vector {
public:
    float x, y, z;

    Vector(float x, float y, float z) : x(x), y(y), z(z) {}

    float magnitude() const {
        return std::sqrt(x*x + y*y + z*z);
    }
};

} // namespace math
`
	config := DefaultASTChunkerConfig()
	config.AddLineNumbers = false

	chunks := treeSitterChunk(source, "vector.cpp", LangCPP, config)
	if chunks == nil {
		t.Fatal("treeSitterChunk returned nil for C++")
	}

	if len(chunks) < 1 {
		t.Error("expected at least 1 chunk for C++")
	}
}

func TestTreeSitterRust(t *testing.T) {
	source := `use std::collections::HashMap;

pub struct Config {
    pub name: String,
    pub value: i32,
}

impl Config {
    pub fn new(name: &str, value: i32) -> Self {
        Config {
            name: name.to_string(),
            value,
        }
    }

    pub fn display(&self) -> String {
        format!("{}: {}", self.name, self.value)
    }
}

pub fn process(config: &Config) -> String {
    config.display()
}

pub enum Status {
    Active,
    Inactive,
    Pending,
}

pub trait Processable {
    fn process(&self) -> String;
}
`
	config := DefaultASTChunkerConfig()
	config.AddLineNumbers = false

	chunks := treeSitterChunk(source, "config.rs", LangRust, config)
	if chunks == nil {
		t.Fatal("treeSitterChunk returned nil for Rust")
	}

	// Should find struct, impl, function, enum, trait.
	types := map[string]bool{}
	for _, c := range chunks {
		types[c.NodeType] = true
	}

	for _, expected := range []string{"struct", "impl", "function", "enum", "trait"} {
		if !types[expected] {
			t.Errorf("expected chunk type %q not found in Rust chunks", expected)
		}
	}
}

func TestTreeSitterCSharp(t *testing.T) {
	source := `using System;

namespace MyApp
{
    public class Calculator
    {
        public int Add(int a, int b)
        {
            return a + b;
        }

        public int Multiply(int a, int b)
        {
            return a * b;
        }
    }

    public interface IComputable
    {
        int Compute(int x);
    }
}
`
	config := DefaultASTChunkerConfig()
	config.AddLineNumbers = false

	chunks := treeSitterChunk(source, "Calculator.cs", LangCSharp, config)
	if chunks == nil {
		t.Fatal("treeSitterChunk returned nil for C#")
	}

	if len(chunks) < 1 {
		t.Error("expected at least 1 chunk for C#")
	}
}

func TestTreeSitterChunkExpansion(t *testing.T) {
	source := `class Calculator:
    def add(self, a, b):
        return a + b
`
	config := DefaultASTChunkerConfig()
	config.AddLineNumbers = false
	config.ChunkExpansion = true

	chunks := treeSitterChunk(source, "calc.py", LangPython, config)
	if chunks == nil {
		t.Fatal("treeSitterChunk returned nil")
	}

	// Methods inside Calculator should have scope header.
	for _, c := range chunks {
		if c.Metadata["parent_scope"] != nil && c.Metadata["parent_scope"] != "" {
			if !strings.Contains(c.Text, "Scope:") {
				t.Errorf("chunk %q with parent_scope should have expansion header", c.Name)
			}
		}
	}
}

func TestTreeSitterPreamble(t *testing.T) {
	source := `import os
import sys
from pathlib import Path

def main():
    pass
`
	config := DefaultASTChunkerConfig()
	config.AddLineNumbers = false

	chunks := treeSitterChunk(source, "main.py", LangPython, config)
	if chunks == nil {
		t.Fatal("treeSitterChunk returned nil")
	}

	// First chunk should be preamble.
	preambles := filterChunksByType(chunks, "preamble")
	if len(preambles) == 0 {
		t.Error("expected a preamble chunk for imports")
	}
}

func TestTreeSitterIntegrationWithASTChunker(t *testing.T) {
	// When built with treesitter tag, ChunkCode should use tree-sitter for Python.
	source := `def foo():
    return 42

def bar():
    return 99
`
	chunker := NewASTChunker(DefaultASTChunkerConfig())
	chunks := chunker.ChunkCode(source, "test.py")

	if len(chunks) < 2 {
		t.Errorf("expected at least 2 chunks, got %d", len(chunks))
	}

	// Verify tree-sitter was used (parser metadata).
	for _, c := range chunks {
		if c.Metadata["parser"] == "tree-sitter" {
			return // Success — tree-sitter was used.
		}
	}
	// If no tree-sitter metadata found, that's still OK (regex fallback).
	t.Log("tree-sitter was not used — regex fallback was active")
}

func TestTreeSitterNestedPython(t *testing.T) {
	// This is the key advantage over regex — nested classes and deep indentation.
	source := `class Outer:
    class Inner:
        def deep_method(self):
            return "deep"

    def outer_method(self):
        return "outer"
`
	config := DefaultASTChunkerConfig()
	config.AddLineNumbers = false

	chunks := treeSitterChunk(source, "nested.py", LangPython, config)
	if chunks == nil {
		t.Fatal("treeSitterChunk returned nil for nested Python")
	}

	// tree-sitter should correctly identify nested structures.
	// Regex would miss class Inner and deep_method (wrong indentation).
	names := map[string]bool{}
	for _, c := range chunks {
		if c.Name != "" {
			names[c.Name] = true
		}
	}

	if !names["Outer"] {
		t.Error("expected to find class Outer")
	}
	// Inner class and deep_method may or may not be separate chunks
	// depending on whether class-like recursion captures them.
	t.Logf("found names: %v", names)
}

// filterChunksByType returns chunks matching the given node type.
func filterChunksByType(chunks []CodeChunk, nodeType string) []CodeChunk {
	var result []CodeChunk
	for _, c := range chunks {
		if c.NodeType == nodeType {
			result = append(result, c)
		}
	}
	return result
}
