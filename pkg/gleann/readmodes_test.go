package gleann

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllReadModes(t *testing.T) {
	modes := AllReadModes()
	if len(modes) != 10 {
		t.Errorf("expected 10 read modes, got %d", len(modes))
	}
}

// helper to create a temp file with content.
func tmpFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadModeFull(t *testing.T) {
	path := tmpFile(t, "test.go", "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n")
	result, err := ReadFileWithMode(path, ReadModeOptions{Mode: ReadModeFull})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "package main") {
		t.Error("full mode should contain all content")
	}
}

func TestReadModeMap(t *testing.T) {
	content := `package main

import "fmt"

type Server struct {
	Port int
}

func NewServer(port int) *Server {
	return &Server{Port: port}
}

func (s *Server) Start() error {
	return nil
}

func main() {
	s := NewServer(8080)
	s.Start()
}
`
	path := tmpFile(t, "server.go", content)
	result, err := ReadFileWithMode(path, ReadModeOptions{Mode: ReadModeMap})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "package main") {
		t.Error("map should show package declaration")
	}
	if !strings.Contains(result, "Server struct") {
		t.Error("map should show struct declaration")
	}
	if !strings.Contains(result, "func") {
		t.Error("map should show function declarations")
	}
}

func TestReadModeSignatures(t *testing.T) {
	content := `package main

func Add(a, b int) int {
	return a + b
}

func Sub(a, b int) int {
	return a - b
}

type Calculator struct {
	value float64
}
`
	path := tmpFile(t, "calc.go", content)
	result, err := ReadFileWithMode(path, ReadModeOptions{Mode: ReadModeSignatures})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "func Add") {
		t.Error("should contain Add signature")
	}
	if !strings.Contains(result, "func Sub") {
		t.Error("should contain Sub signature")
	}
	if !strings.Contains(result, "Calculator struct") {
		t.Error("should contain Calculator struct")
	}
	// Should NOT contain function bodies
	if strings.Contains(result, "return a + b") {
		t.Error("should not contain function body")
	}
}

func TestReadModeEntropy(t *testing.T) {
	content := `package main

// Simple comment line
func handleRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	data, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("error reading body: %v", err)
		return
	}

	// Another simple comment
	name := "hello"
	_ = name
}
`
	path := tmpFile(t, "handler.go", content)
	result, err := ReadFileWithMode(path, ReadModeOptions{Mode: ReadModeEntropy})
	if err != nil {
		t.Fatal(err)
	}
	// Should include error handling and control flow lines
	if !strings.Contains(result, "error") || !strings.Contains(result, "if") {
		t.Error("entropy mode should capture high-information lines")
	}
}

func TestReadModeLines(t *testing.T) {
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line " + string(rune('A'+i))
	}
	content := strings.Join(lines, "\n")
	path := tmpFile(t, "data.txt", content)

	result, err := ReadFileWithMode(path, ReadModeOptions{Mode: ReadModeLines, LineRanges: "5:10"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "line E") {
		t.Error("should contain line 5 (E)")
	}
	if !strings.Contains(result, "line J") {
		t.Error("should contain line 10 (J)")
	}
	if strings.Contains(result, "line A") {
		t.Error("should not contain line 1 (A)")
	}
}

func TestReadModeLinesMultiRange(t *testing.T) {
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line " + string(rune('A'+i))
	}
	content := strings.Join(lines, "\n")
	path := tmpFile(t, "data.txt", content)

	result, err := ReadFileWithMode(path, ReadModeOptions{Mode: ReadModeLines, LineRanges: "1:3,18:20"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "line A") {
		t.Error("should contain first range")
	}
	if !strings.Contains(result, "line R") {
		t.Error("should contain second range")
	}
}

func TestReadModeLinesInvalid(t *testing.T) {
	path := tmpFile(t, "data.txt", "hello\nworld\n")
	_, err := ReadFileWithMode(path, ReadModeOptions{Mode: ReadModeLines, LineRanges: "invalid"})
	if err == nil {
		t.Error("expected error for invalid line range")
	}
}

func TestReadModeTask(t *testing.T) {
	content := `package main

import "database/sql"

func connectDB(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return db, nil
}

func handleUser(w http.ResponseWriter, r *http.Request) {
	user := r.URL.Query().Get("user")
	fmt.Fprintf(w, "Hello, %s", user)
}

func processOrder(order Order) error {
	return nil
}
`
	path := tmpFile(t, "app.go", content)

	result, err := ReadFileWithMode(path, ReadModeOptions{Mode: ReadModeTask, TaskQuery: "database connection error handling"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "connectDB") || !strings.Contains(result, "database") {
		t.Error("should find database-related lines")
	}
}

func TestReadModeTaskEmptyQuery(t *testing.T) {
	path := tmpFile(t, "test.txt", "hello world")
	result, err := ReadFileWithMode(path, ReadModeOptions{Mode: ReadModeTask, TaskQuery: ""})
	if err != nil {
		t.Fatal(err)
	}
	// Empty query should return full content
	if !strings.Contains(result, "hello world") {
		t.Error("empty query should return full content")
	}
}

func TestReadModeReference(t *testing.T) {
	content := `package main

import (
	"fmt"
	"os"
)

type Config struct {
	Port int
}

var defaultPort = 8080

const version = "1.0"

func main() {
	fmt.Println("hello")
	os.Exit(0)
}
`
	path := tmpFile(t, "main.go", content)
	result, err := ReadFileWithMode(path, ReadModeOptions{Mode: ReadModeReference})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "import") {
		t.Error("should contain imports")
	}
	if !strings.Contains(result, "Config struct") {
		t.Error("should contain type definitions")
	}
	if !strings.Contains(result, "var ") {
		t.Error("should contain var declarations")
	}
	if !strings.Contains(result, "const ") {
		t.Error("should contain const declarations")
	}
	// Should NOT contain function body
	if strings.Contains(result, "Println") {
		t.Error("should not contain function body")
	}
}

func TestReadModeAggressive(t *testing.T) {
	content := `package main

// This is a comment
/* Block comment */

import "fmt"

func main() {

	// Another comment
	fmt.Println("hello")

}
`
	path := tmpFile(t, "main.go", content)
	result, err := ReadFileWithMode(path, ReadModeOptions{Mode: ReadModeAggressive})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, "// This is") {
		t.Error("should strip comments")
	}
	if strings.Contains(result, "Block comment") {
		t.Error("should strip block comments")
	}
	if !strings.Contains(result, "fmt.Println") {
		t.Error("should keep code")
	}
}

func TestReadModeAutoSmallFile(t *testing.T) {
	content := "package main\n\nfunc main() {}\n"
	path := tmpFile(t, "small.go", content)
	result, err := ReadFileWithMode(path, ReadModeOptions{Mode: ReadModeAuto})
	if err != nil {
		t.Fatal(err)
	}
	// Small file → full mode
	if !strings.Contains(result, "package main") {
		t.Error("auto should use full mode for small files")
	}
}

func TestReadModeAutoLargeFile(t *testing.T) {
	// Create a large Go file (600+ lines)
	var lines []string
	lines = append(lines, "package main")
	for i := 0; i < 600; i++ {
		lines = append(lines, "func foo"+string(rune('A'+i%26))+"() {}")
	}
	content := strings.Join(lines, "\n")
	path := tmpFile(t, "large.go", content)

	result, err := ReadFileWithMode(path, ReadModeOptions{Mode: ReadModeAuto})
	if err != nil {
		t.Fatal(err)
	}
	// Large file → map mode
	if !strings.Contains(result, "lines)") {
		t.Error("auto should use map mode for large code files")
	}
}

func TestReadModeMaxLines(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "content line"
	}
	path := tmpFile(t, "big.txt", strings.Join(lines, "\n"))

	result, err := ReadFileWithMode(path, ReadModeOptions{Mode: ReadModeFull, MaxLines: 10})
	if err != nil {
		t.Fatal(err)
	}
	resultLines := strings.Split(result, "\n")
	if len(resultLines) > 15 { // 10 + omitted message
		t.Errorf("MaxLines should cap output, got %d lines", len(resultLines))
	}
}

func TestReadModeFileMissing(t *testing.T) {
	_, err := ReadFileWithMode("/nonexistent/file.go", ReadModeOptions{Mode: ReadModeFull})
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestAutoSelectModeJSON(t *testing.T) {
	// Small JSON → full
	mode := autoSelectMode("config.json", strings.Repeat("x\n", 50))
	if mode != ReadModeFull {
		t.Errorf("small JSON should be full, got %s", mode)
	}

	// Large JSON → aggressive
	mode = autoSelectMode("config.json", strings.Repeat("x\n", 300))
	if mode != ReadModeAggressive {
		t.Errorf("large JSON should be aggressive, got %s", mode)
	}
}

func TestAutoSelectModeMarkdown(t *testing.T) {
	mode := autoSelectMode("README.md", strings.Repeat("x\n", 400))
	if mode != ReadModeMap {
		t.Errorf("large markdown should be map, got %s", mode)
	}
}

func TestExtractKeywords(t *testing.T) {
	keywords := extractKeywords("find the database connection error handling")
	// Should filter stop words
	for _, kw := range keywords {
		if kw == "the" || kw == "find" {
			t.Errorf("should filter stop word: %q", kw)
		}
	}
	found := false
	for _, kw := range keywords {
		if kw == "database" {
			found = true
			break
		}
	}
	if !found {
		t.Error("should keep 'database'")
	}
}

func TestEntropyScore(t *testing.T) {
	// High entropy line (error + if + assignment)
	high := entropyScore("if err != nil { return fmt.Errorf(\"connect: %w\", err) }")
	// Low entropy line (blank)
	low := entropyScore("")
	// Medium entropy (simple assignment)
	med := entropyScore("name := \"hello\"")
	// Comment
	comment := entropyScore("// this is a comment")

	if high <= med {
		t.Error("error handling should score higher than assignment")
	}
	if low != 0 {
		t.Error("blank line should score 0")
	}
	if comment >= med {
		t.Error("comment should score lower than code")
	}
}

func TestGetStructurePatterns(t *testing.T) {
	tests := []string{".go", ".py", ".js", ".ts", ".rs", ".java", ".c", ".rb", ".md", ".txt"}
	for _, ext := range tests {
		patterns := getStructurePatterns(ext)
		if len(patterns) == 0 {
			t.Errorf("no structure patterns for %s", ext)
		}
	}
}

func TestReadModePython(t *testing.T) {
	content := `import os
from pathlib import Path

class Handler:
    def process(self, data):
        return data

    def validate(self, input):
        if not input:
            raise ValueError("empty")
        return True

def main():
    h = Handler()
    h.process("test")
`
	path := tmpFile(t, "handler.py", content)

	// Signatures mode
	result, err := ReadFileWithMode(path, ReadModeOptions{Mode: ReadModeSignatures})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "class Handler") {
		t.Error("should find class")
	}
	if !strings.Contains(result, "def process") {
		t.Error("should find methods")
	}

	// Reference mode
	result, err = ReadFileWithMode(path, ReadModeOptions{Mode: ReadModeReference})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "import os") {
		t.Error("should find imports")
	}
}

func TestReadModeRust(t *testing.T) {
	content := `use std::io;
use std::fs;

pub struct Config {
    port: u16,
}

pub fn new_config(port: u16) -> Config {
    Config { port }
}

impl Config {
    pub fn start(&self) -> io::Result<()> {
        Ok(())
    }
}
`
	path := tmpFile(t, "config.rs", content)

	result, err := ReadFileWithMode(path, ReadModeOptions{Mode: ReadModeSignatures})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "pub struct Config") {
		t.Error("should find struct")
	}
	if !strings.Contains(result, "pub fn new_config") {
		t.Error("should find function")
	}
}
