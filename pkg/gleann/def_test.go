package gleann

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractSignature_Go(t *testing.T) {
	code := `package auth

import "time"

// TokenManager handles JWT lifecycle.
type TokenManager struct {
	secret []byte
	ttl    time.Duration
}

// NewTokenManager initializes a manager.
func NewTokenManager(
	secret []byte,
	ttl time.Duration,
) (*TokenManager, error) {
	return &TokenManager{secret: secret, ttl: ttl}, nil
}
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "token.go")
	if err := os.WriteFile(filePath, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Test Struct signature on line 6
	sigStruct, docStruct := ExtractSignatureFromFile(filePath, 6)
	if !strings.Contains(sigStruct, "type TokenManager struct") {
		t.Errorf("expected struct signature, got: %q", sigStruct)
	}
	if !strings.Contains(docStruct, "TokenManager handles JWT lifecycle") {
		t.Errorf("expected doc for struct, got: %q", docStruct)
	}

	// 2. Test Multiline Function signature on line 12
	sigFunc, docFunc := ExtractSignatureFromFile(filePath, 12)
	if !strings.Contains(sigFunc, "func NewTokenManager(") || !strings.Contains(sigFunc, "*TokenManager, error") {
		t.Errorf("expected multiline func signature, got: %q", sigFunc)
	}
	if !strings.Contains(docFunc, "NewTokenManager initializes a manager") {
		t.Errorf("expected doc for func, got: %q", docFunc)
	}
}

func TestExtractSignature_Python(t *testing.T) {
	pyCode := `
# Service class definition
class AuthService:
    def __init__(self, key: str):
        self.key = key

    # Validates access token
    def validate_token(self, token: str) -> bool:
        return len(token) > 0
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "auth.py")
	if err := os.WriteFile(filePath, []byte(pyCode), 0o644); err != nil {
		t.Fatal(err)
	}

	// Line 8: def validate_token
	sig, doc := ExtractSignatureFromFile(filePath, 8)
	if !strings.Contains(sig, "def validate_token(self, token: str) -> bool:") {
		t.Errorf("expected python func signature, got: %q", sig)
	}
	if !strings.Contains(doc, "Validates access token") {
		t.Errorf("expected doc comment, got: %q", doc)
	}
}

// mockGraphDBForDef implements a minimal GraphDB for definition testing.
type mockGraphDBForDef struct {
	symbols []Callee
}

func (m *mockGraphDBForDef) SymbolSearch(pattern string) ([]Callee, error) {
	var matched []Callee
	lower := strings.ToLower(pattern)
	for _, s := range m.symbols {
		if strings.Contains(strings.ToLower(s.Name), lower) || strings.Contains(strings.ToLower(s.FQN), lower) {
			matched = append(matched, s)
		}
	}
	return matched, nil
}

func (m *mockGraphDBForDef) Callees(callerFQN string) ([]Callee, error)            { return nil, nil }
func (m *mockGraphDBForDef) Callers(calleeFQN string) ([]Callee, error)            { return nil, nil }
func (m *mockGraphDBForDef) SymbolsInFile(filePath string) ([]Callee, error)       { return nil, nil }
func (m *mockGraphDBForDef) DocumentSymbols(docPath string) ([]SymbolInfo, error)  { return nil, nil }
func (m *mockGraphDBForDef) DocumentContext(vpath string) (*DocumentContextData, error) {
	return nil, nil
}
func (m *mockGraphDBForDef) DocumentTOC(vpath string) (*DocumentTOCInfo, error) { return nil, nil }
func (m *mockGraphDBForDef) ListDocuments() ([]DocumentTOCInfo, error)          { return nil, nil }
func (m *mockGraphDBForDef) FullDocument(vpath string) (string, error)          { return "", nil }
func (m *mockGraphDBForDef) Impact(fqn string, maxDepth int) (*ImpactResult, error) {
	return nil, nil
}
func (m *mockGraphDBForDef) Neighbors(fqn string, maxDepth int) ([]GraphEdge, error) {
	return nil, nil
}
func (m *mockGraphDBForDef) ShortestPath(fromFQN, toFQN string) ([]PathStep, error) {
	return nil, nil
}
func (m *mockGraphDBForDef) Stats() (*GraphStats, error) { return nil, nil }
func (m *mockGraphDBForDef) Close()                      {}

func TestFindDefinitions_WithGraph(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "calculator.go")
	content := `package calc

// CalculateSum adds two numbers together.
func CalculateSum(a, b int) int {
	return a + b
}

type Calculator struct {
	precision int
}
`
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	mockDB := &mockGraphDBForDef{
		symbols: []Callee{
			{
				FQN:    "calc.CalculateSum",
				Name:   "CalculateSum",
				Kind:   "function",
				File:   filePath,
				Line:   4,
				IsTest: false,
			},
			{
				FQN:    "calc.Calculator",
				Name:   "Calculator",
				Kind:   "struct",
				File:   filePath,
				Line:   8,
				IsTest: false,
			},
		},
	}

	searcher := &LeannSearcher{
		graphDB: mockDB,
	}

	ctx := context.Background()

	// 1. Exact search for CalculateSum
	defs, err := FindDefinitions(ctx, searcher, "CalculateSum", DefinitionOptions{TopK: 5})
	if err != nil {
		t.Fatalf("FindDefinitions failed: %v", err)
	}
	if len(defs) == 0 {
		t.Fatal("expected at least 1 definition, got 0")
	}
	if defs[0].Name != "CalculateSum" {
		t.Errorf("expected CalculateSum, got %s", defs[0].Name)
	}
	if defs[0].Line != 4 {
		t.Errorf("expected line 4, got %d", defs[0].Line)
	}
	if !strings.Contains(defs[0].Signature, "func CalculateSum(a, b int) int") {
		t.Errorf("expected signature, got: %q", defs[0].Signature)
	}
	if !strings.Contains(defs[0].Doc, "CalculateSum adds two numbers") {
		t.Errorf("expected docstring, got: %q", defs[0].Doc)
	}

	// 2. Filter by Kind
	defsStruct, err := FindDefinitions(ctx, searcher, "Calc", DefinitionOptions{TopK: 5, Kind: "struct"})
	if err != nil {
		t.Fatal(err)
	}
	if len(defsStruct) != 1 || defsStruct[0].Kind != "struct" {
		t.Errorf("expected 1 struct, got: %v", defsStruct)
	}
}

func TestFormatDefinitions(t *testing.T) {
	defs := []DefinitionResult{
		{
			FQN:       "pkg.AuthManager",
			Name:      "AuthManager",
			Kind:      "struct",
			File:      "auth/manager.go",
			Line:      15,
			Signature: "type AuthManager struct { secret []byte }",
			Doc:       "AuthManager coordinates authentication tokens.",
			Score:     1.0,
		},
	}

	formatted := FormatDefinitions(defs)
	if !strings.Contains(formatted, "Symbol: pkg.AuthManager (struct)") {
		t.Errorf("expected symbol header, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "Defined at: auth/manager.go:15") {
		t.Errorf("expected file:line location, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "type AuthManager struct") {
		t.Errorf("expected signature, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "AuthManager coordinates authentication tokens.") {
		t.Errorf("expected doc, got:\n%s", formatted)
	}
}

func TestScanPassageForSymbol(t *testing.T) {
	passageText := `package token

// TokenValidator verifies cryptographic signatures on incoming tokens.
type TokenValidator struct {
	key string
}

func ValidateBearer(token string) bool {
	return len(token) > 0
}
`

	// 1. Scan struct
	defStruct := scanPassageForSymbol(passageText, "TokenValidator", "token/val.go")
	if defStruct == nil {
		t.Fatal("expected to find TokenValidator, got nil")
	}
	if defStruct.Kind != "struct" {
		t.Errorf("expected struct, got: %s", defStruct.Kind)
	}
	if !strings.Contains(defStruct.Signature, "type TokenValidator struct") {
		t.Errorf("expected signature, got: %q", defStruct.Signature)
	}
	if !strings.Contains(defStruct.Doc, "TokenValidator verifies") {
		t.Errorf("expected doc, got: %q", defStruct.Doc)
	}

	// 2. Scan function
	defFunc := scanPassageForSymbol(passageText, "ValidateBearer", "token/val.go")
	if defFunc == nil {
		t.Fatal("expected to find ValidateBearer, got nil")
	}
	if defFunc.Kind != "function" {
		t.Errorf("expected function, got: %s", defFunc.Kind)
	}
	if !strings.Contains(defFunc.Signature, "func ValidateBearer(token string) bool") {
		t.Errorf("expected func signature, got: %q", defFunc.Signature)
	}
}
