package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestIntegration_BuildInstantLexical(t *testing.T) {
	docsDir := t.TempDir()
	indexDir := t.TempDir()

	// Create test documents
	file1 := filepath.Join(docsDir, "search_algo.go")
	content1 := `package search

// BinarySearch looks up a target value in a sorted slice.
func BinarySearch(arr []int, target int) int {
	low := 0
	high := len(arr) - 1
	for low <= high {
		mid := low + (high-low)/2
		if arr[mid] == target {
			return mid
		} else if arr[mid] < target {
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	return -1
}
`
	if err := os.WriteFile(file1, []byte(content1), 0o644); err != nil {
		t.Fatalf("write file1: %v", err)
	}

	file2 := filepath.Join(docsDir, "recipe.txt")
	content2 := `Pizza dough recipe: 500g flour, 325ml water, 10g salt, 2g yeast. Ferment for 24 hours.`
	if err := os.WriteFile(file2, []byte(content2), 0o644); err != nil {
		t.Fatalf("write file2: %v", err)
	}

	indexName := "instant_idx"
	args := []string{
		indexName,
		"--docs", docsDir,
		"--index-dir", indexDir,
		"--instant",
		"--no-report",
		"--no-agents",
	}

	stdout, stderr, code, err := runCmdBuildWithMockExit(args)
	if err != nil {
		t.Fatalf("runCmdBuildWithMockExit failed: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr: %s, stdout: %s", code, stderr, stdout)
	}

	if !strings.Contains(stdout, "Instant lexical index ready") {
		t.Errorf("expected stdout to mention 'Instant lexical index ready', got: %s", stdout)
	}

	// Verify metadata
	meta, err := gleann.GetIndexMeta(indexDir, indexName)
	if err != nil {
		t.Fatalf("GetIndexMeta failed: %v", err)
	}
	if !meta.LexicalOnly {
		t.Errorf("expected meta.LexicalOnly to be true")
	}
	if meta.VectorReady {
		t.Errorf("expected meta.VectorReady to be false")
	}

	// Verify .index file does not exist
	indexPath := filepath.Join(indexDir, indexName, indexName+".index")
	if _, err := os.Stat(indexPath); !os.IsNotExist(err) {
		t.Errorf("expected %s to not exist, err: %v", indexPath, err)
	}

	// Verify Searcher loads and searches via BM25 fallback
	cfg := gleann.Config{
		IndexDir: indexDir,
	}
	searcher := gleann.NewSearcher(cfg, nil)
	ctx := context.Background()
	if err := searcher.Load(ctx, indexName); err != nil {
		t.Fatalf("searcher.Load failed: %v", err)
	}
	defer searcher.Close()

	if searcher.VectorAvailable() {
		t.Errorf("expected VectorAvailable to be false")
	}

	results, err := searcher.Search(ctx, "BinarySearch", gleann.WithTopK(2))
	if err != nil {
		t.Fatalf("searcher.Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected search results for BinarySearch, got 0")
	}
	if !strings.Contains(results[0].Text, "BinarySearch") {
		t.Errorf("expected top result to contain 'BinarySearch', got %s", results[0].Text)
	}
}

func TestIntegration_BuildSignaturesOnly_ChunkReduction(t *testing.T) {
	docsDir := t.TempDir()
	indexDir1 := t.TempDir()
	indexDir2 := t.TempDir()

	// Generate a substantial Go file with multiple functions containing long bodies
	var sb strings.Builder
	sb.WriteString("package bigpkg\n\n")
	for i := 0; i < 8; i++ {
		sb.WriteString(fmt.Sprintf("// ComputeData%d processes dataset %d with heavy calculations.\n", i, i))
		sb.WriteString(fmt.Sprintf("func ComputeData%d(input []float64, factor float64) (float64, error) {\n", i))
		for j := 0; j < 50; j++ {
			sb.WriteString(fmt.Sprintf("\tstep%d := float64(%d) * factor + 1.23456789\n", j, j))
		}
		sb.WriteString("\treturn step49, nil\n}\n\n")
	}

	goFilePath := filepath.Join(docsDir, "heavy.go")
	if err := os.WriteFile(goFilePath, []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write heavy.go: %v", err)
	}

	// 1. Build with standard chunking (small chunk size to trigger chunking across bodies)
	argsFull := []string{
		"full_idx",
		"--docs", docsDir,
		"--index-dir", indexDir1,
		"--instant",
		"--chunk-size", "300",
		"--no-report",
		"--no-agents",
	}
	_, _, code1, _ := runCmdBuildWithMockExit(argsFull)
	if code1 != 0 {
		t.Fatalf("full build failed with code %d", code1)
	}
	metaFull, err := gleann.GetIndexMeta(indexDir1, "full_idx")
	if err != nil {
		t.Fatalf("GetIndexMeta full failed: %v", err)
	}

	// 2. Build with --signatures
	argsSig := []string{
		"sig_idx",
		"--docs", docsDir,
		"--index-dir", indexDir2,
		"--instant",
		"--signatures",
		"--chunk-size", "300",
		"--no-report",
		"--no-agents",
	}
	stdoutSig, stderrSig, code2, _ := runCmdBuildWithMockExit(argsSig)
	if code2 != 0 {
		t.Fatalf("sig build failed with code %d, stderr: %s, stdout: %s", code2, stderrSig, stdoutSig)
	}
	metaSig, err := gleann.GetIndexMeta(indexDir2, "sig_idx")
	if err != nil {
		t.Fatalf("GetIndexMeta sig failed: %v", err)
	}

	t.Logf("Full chunks: %d vs Signatures-only chunks: %d", metaFull.NumPassages, metaSig.NumPassages)

	// Signatures only should produce fewer chunks (at most 1 per function)
	if metaSig.NumPassages >= metaFull.NumPassages {
		t.Errorf("expected SignaturesOnly passages (%d) to be less than full passages (%d)",
			metaSig.NumPassages, metaFull.NumPassages)
	}

	// Check that passages in sig_idx have function bodies omitted
	basePath := filepath.Join(indexDir2, "sig_idx", "sig_idx")
	pm := gleann.NewReadOnlyPassageManager(basePath)
	if err := pm.Load(); err != nil {
		t.Fatalf("load pm: %v", err)
	}
	defer pm.Close()
	foundFuncSig := false
	for i := 0; i < pm.Count(); i++ {
		p, err := pm.Get(int64(i))
		if err != nil {
			continue
		}
		// Implementation body lines must not be present in signature-only chunks
		if strings.Contains(p.Text, "step49") || strings.Contains(p.Text, "step0 :=") {
			t.Errorf("passage %d contained function body implementation:\n%s", i, p.Text)
		}
		if strings.Contains(p.Text, "func ComputeData0") {
			foundFuncSig = true
		}
	}
	if !foundFuncSig {
		t.Errorf("expected to find 'func ComputeData0' signature in passages")
	}
}
