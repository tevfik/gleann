package chunking

import (
	"strings"
	"testing"
)

func TestSentenceSplitterSimple(t *testing.T) {
	s := NewSentenceSplitter(10, 2) // Small chunk for testing.
	text := "Hello world. This is a test. Another sentence here. Final one."

	chunks := s.Chunk(text)
	if len(chunks) == 0 {
		t.Fatal("expected at least 1 chunk")
	}

	// All text should be covered.
	combined := strings.Join(chunks, " ")
	if !strings.Contains(combined, "Hello") {
		t.Error("missing 'Hello'")
	}
	if !strings.Contains(combined, "Final") {
		t.Error("missing 'Final'")
	}
}

func TestSentenceSplitterParagraphs(t *testing.T) {
	s := NewSentenceSplitter(20, 5)
	text := "First paragraph with some content.\n\nSecond paragraph with different content.\n\nThird paragraph here."

	chunks := s.Chunk(text)
	if len(chunks) == 0 {
		t.Fatal("expected at least 1 chunk")
	}
}

func TestSentenceSplitterEmpty(t *testing.T) {
	s := NewSentenceSplitter(100, 10)
	chunks := s.Chunk("")
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks, got %d", len(chunks))
	}
}

func TestSentenceSplitterWithMetadata(t *testing.T) {
	s := NewSentenceSplitter(500, 50)
	meta := map[string]any{"source": "test.txt"}
	items := s.ChunkWithMetadata("This is a test document. With multiple sentences.", meta)

	if len(items) == 0 {
		t.Fatal("expected at least 1 item")
	}

	for _, item := range items {
		if item.Metadata["source"] != "test.txt" {
			t.Error("metadata not preserved")
		}
		if item.Metadata["chunk_index"] == nil {
			t.Error("missing chunk_index")
		}
	}
}

func TestCodeChunker(t *testing.T) {
	c := NewCodeChunker(50, 10)
	code := `package main

import "fmt"

func hello() {
	fmt.Println("hello")
}

func world() {
	fmt.Println("world")
}

func main() {
	hello()
	world()
}`

	chunks := c.Chunk(code)
	if len(chunks) == 0 {
		t.Fatal("expected at least 1 chunk")
	}

	t.Logf("got %d code chunks", len(chunks))
}

func TestCodeChunkerEmpty(t *testing.T) {
	c := NewCodeChunker(100, 10)
	chunks := c.Chunk("")
	// Empty string splits into [""] which produces 1 empty-ish chunk.
	if len(chunks) > 1 {
		t.Errorf("expected 0 or 1 chunks, got %d", len(chunks))
	}
}

func TestCodeChunkerWithMetadata(t *testing.T) {
	c := NewCodeChunker(500, 50)
	meta := map[string]any{"file": "main.go", "language": "go"}
	items := c.ChunkWithMetadata("func main() {\n\tfmt.Println(\"hi\")\n}", meta)

	if len(items) == 0 {
		t.Fatal("expected at least 1 item")
	}
	if items[0].Metadata["file"] != "main.go" {
		t.Error("metadata not preserved")
	}
}

func TestIsCodeFile(t *testing.T) {
	tests := map[string]bool{
		"main.go":      true,
		"app.py":       true,
		"index.ts":     true,
		"readme.md":    false, // markdown files are handled by MarkdownChunker, not CodeChunker
		"photo.jpg":    false,
		"data.csv":     false,
		"Program.java": true,
		"lib.rs":       true,
		"Makefile":     false,
	}

	for file, expected := range tests {
		got := IsCodeFile(file)
		if got != expected {
			t.Errorf("IsCodeFile(%q) = %v, want %v", file, got, expected)
		}
	}
}

func TestIsCodeBoundary(t *testing.T) {
	tests := map[string]bool{
		"func main() {":          true,
		"def hello():":           true,
		"class MyClass:":         true,
		"    x = 1":              false,
		"type Config struct {":   true,
		"    fmt.Println()":      false,
		"public void method() {": true,
	}

	for line, expected := range tests {
		got := isCodeBoundary(line)
		if got != expected {
			t.Errorf("isCodeBoundary(%q) = %v, want %v", line, got, expected)
		}
	}
}

func TestSplitSentences(t *testing.T) {
	sentences := splitSentences("Hello world. How are you? I am fine!")
	if len(sentences) != 3 {
		t.Errorf("expected 3 sentences, got %d: %v", len(sentences), sentences)
	}
}

func TestCountWords(t *testing.T) {
	if countWords("hello world foo") != 3 {
		t.Error("expected 3 words")
	}
	if countWords("") != 0 {
		t.Error("expected 0 words")
	}
}

func TestGetOverlapText(t *testing.T) {
	text := "one two three four five"
	overlap := getOverlapText(text, 2)
	if overlap != "four five" {
		t.Errorf("expected 'four five', got %q", overlap)
	}

	// Short text.
	short := getOverlapText("hello", 10)
	if short != "hello" {
		t.Errorf("expected 'hello', got %q", short)
	}
}

func TestSplitParagraphs(t *testing.T) {
	text := "First para.\n\nSecond para.\n\nThird para."
	parts := splitParagraphs(text)
	if len(parts) != 3 {
		t.Errorf("expected 3 paragraphs, got %d", len(parts))
	}
}

func TestNewSentenceSplitterDefaults(t *testing.T) {
	s := NewSentenceSplitter(0, -1)
	if s.ChunkSize != 512 {
		t.Errorf("expected default 512, got %d", s.ChunkSize)
	}
	if s.ChunkOverlap != 50 {
		t.Errorf("expected default 50, got %d", s.ChunkOverlap)
	}
}
