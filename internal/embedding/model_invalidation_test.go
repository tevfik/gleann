package embedding

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModelFingerprint_Deterministic(t *testing.T) {
	fp1 := ModelFingerprint("bge-m3")
	fp2 := ModelFingerprint("bge-m3")
	if fp1 != fp2 {
		t.Errorf("same model should produce same fingerprint: %s vs %s", fp1, fp2)
	}
}

func TestModelFingerprint_CaseInsensitive(t *testing.T) {
	fp1 := ModelFingerprint("bge-m3")
	fp2 := ModelFingerprint("BGE-M3")
	if fp1 != fp2 {
		t.Errorf("model fingerprint should be case-insensitive: %s vs %s", fp1, fp2)
	}
}

func TestModelFingerprint_TrimSpaces(t *testing.T) {
	fp1 := ModelFingerprint("bge-m3")
	fp2 := ModelFingerprint("  bge-m3  ")
	if fp1 != fp2 {
		t.Errorf("model fingerprint should trim spaces: %s vs %s", fp1, fp2)
	}
}

func TestModelFingerprint_DifferentModels(t *testing.T) {
	fp1 := ModelFingerprint("bge-m3")
	fp2 := ModelFingerprint("all-minilm-l6-v2")
	if fp1 == fp2 {
		t.Error("different models should produce different fingerprints")
	}
}

func TestDetectModelChange_NoChange(t *testing.T) {
	if DetectModelChange("bge-m3", "bge-m3") {
		t.Error("same model should not be detected as changed")
	}
}

func TestDetectModelChange_Changed(t *testing.T) {
	if !DetectModelChange("bge-m3", "all-minilm-l6-v2") {
		t.Error("different models should be detected as changed")
	}
}

func TestDetectModelChange_EmptyStrings(t *testing.T) {
	if DetectModelChange("", "bge-m3") {
		t.Error("empty previous model should not be detected as changed")
	}
	if DetectModelChange("bge-m3", "") {
		t.Error("empty current model should not be detected as changed")
	}
	if DetectModelChange("", "") {
		t.Error("both empty should not be detected as changed")
	}
}

func TestInvalidateL2CacheForModel_EmptyDir(t *testing.T) {
	tmp := t.TempDir()
	removed, err := InvalidateL2CacheForModel(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 0 {
		t.Errorf("expected 0 removed from empty dir, got %d", removed)
	}
}

func TestInvalidateL2CacheForModel_NonExistentDir(t *testing.T) {
	removed, err := InvalidateL2CacheForModel("/tmp/nonexistent_cache_dir_xyz")
	if err != nil {
		t.Fatalf("non-existent dir should not error: %v", err)
	}
	if removed != 0 {
		t.Errorf("expected 0, got %d", removed)
	}
}

func TestInvalidateL2CacheForModel_RemovesBinFiles(t *testing.T) {
	tmp := t.TempDir()

	// Create fake cache files.
	for _, name := range []string{"abc.bin", "def.bin", "ghi.bin"} {
		os.WriteFile(filepath.Join(tmp, name), []byte("data"), 0644)
	}
	// Non-bin files should be left alone.
	os.WriteFile(filepath.Join(tmp, "readme.txt"), []byte("keep"), 0644)

	removed, err := InvalidateL2CacheForModel(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 3 {
		t.Errorf("expected 3 removed, got %d", removed)
	}

	// Check readme.txt still exists.
	if _, err := os.Stat(filepath.Join(tmp, "readme.txt")); os.IsNotExist(err) {
		t.Error("non-bin files should not be removed")
	}
}

func TestInvalidateOnModelChange_NoChange(t *testing.T) {
	result, err := InvalidateOnModelChange("bge-m3", "bge-m3", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Changed {
		t.Error("same model should not trigger invalidation")
	}
	if result.InvalidatedL2 != 0 || result.InvalidatedMemo != 0 {
		t.Error("no invalidation expected when model hasn't changed")
	}
}

func TestInvalidateOnModelChange_WithChange(t *testing.T) {
	tmp := t.TempDir()
	cacheDir := filepath.Join(tmp, "cache")
	os.MkdirAll(cacheDir, 0755)

	// Create some fake L2 cache files.
	os.WriteFile(filepath.Join(cacheDir, "hash1.bin"), []byte("vec"), 0644)
	os.WriteFile(filepath.Join(cacheDir, "hash2.bin"), []byte("vec"), 0644)

	// Create a memo store with old model entries.
	memoPath := filepath.Join(tmp, "test.chunk_memo.json")
	memo := NewChunkMemoStoreFromPath(memoPath)
	memo.RecordBatch([]string{"a", "b", "c"}, "old-model", "src.go")

	result, err := InvalidateOnModelChange("old-model", "new-model", cacheDir, memo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Changed {
		t.Error("different models should trigger invalidation")
	}
	if result.InvalidatedL2 != 2 {
		t.Errorf("expected 2 L2 cache files removed, got %d", result.InvalidatedL2)
	}
	if result.InvalidatedMemo != 3 {
		t.Errorf("expected 3 memo entries removed, got %d", result.InvalidatedMemo)
	}
	if memo.Len() != 0 {
		t.Errorf("memo should be empty after invalidation, got %d", memo.Len())
	}
}

func TestInvalidateOnModelChange_NilMemo(t *testing.T) {
	tmp := t.TempDir()
	cacheDir := filepath.Join(tmp, "cache")
	os.MkdirAll(cacheDir, 0755)
	os.WriteFile(filepath.Join(cacheDir, "x.bin"), []byte("v"), 0644)

	result, err := InvalidateOnModelChange("old", "new", cacheDir, nil)
	if err != nil {
		t.Fatalf("unexpected error with nil memo: %v", err)
	}
	if !result.Changed {
		t.Error("should detect change")
	}
	if result.InvalidatedL2 != 1 {
		t.Errorf("expected 1 L2 removed, got %d", result.InvalidatedL2)
	}
	if result.InvalidatedMemo != 0 {
		t.Errorf("nil memo should not report removals, got %d", result.InvalidatedMemo)
	}
}

func TestInvalidateOnModelChange_PreservesNewModelEntries(t *testing.T) {
	tmp := t.TempDir()

	memoPath := filepath.Join(tmp, "test.chunk_memo.json")
	memo := NewChunkMemoStoreFromPath(memoPath)
	memo.RecordBatch([]string{"old1"}, "old-model", "a.go")
	memo.RecordBatch([]string{"new1"}, "new-model", "b.go")

	result, err := InvalidateOnModelChange("old-model", "new-model", tmp, memo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.InvalidatedMemo != 1 {
		t.Errorf("expected 1 memo removed, got %d", result.InvalidatedMemo)
	}
	// New model entries should be preserved.
	if memo.Len() != 1 {
		t.Errorf("new model entries should be preserved, got %d", memo.Len())
	}
}
