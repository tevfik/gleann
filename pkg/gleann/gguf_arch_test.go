package gleann

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestGGUF(t *testing.T, arch string) string {
	t.Helper()
	var b bytes.Buffer
	str := func(s string) { binary.Write(&b, binary.LittleEndian, uint64(len(s))); b.WriteString(s) }
	b.WriteString("GGUF")
	binary.Write(&b, binary.LittleEndian, uint32(3))
	binary.Write(&b, binary.LittleEndian, uint64(0))
	binary.Write(&b, binary.LittleEndian, uint64(3))
	// an array of strings and a scalar come first, to exercise skipping
	str("tokenizer.ggml.tokens")
	binary.Write(&b, binary.LittleEndian, uint32(9))
	binary.Write(&b, binary.LittleEndian, uint32(8))
	binary.Write(&b, binary.LittleEndian, uint64(2))
	str("a")
	str("bb")
	str("general.file_type")
	binary.Write(&b, binary.LittleEndian, uint32(4))
	binary.Write(&b, binary.LittleEndian, uint32(15))
	str("general.architecture")
	binary.Write(&b, binary.LittleEndian, uint32(8))
	str(arch)
	p := filepath.Join(t.TempDir(), "m.gguf")
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGGUFArchitectureAndEIFCheck(t *testing.T) {
	p := writeTestGGUF(t, "lfm2")
	if a, err := GGUFArchitecture(p); err != nil || a != "lfm2" {
		t.Fatalf("arch = %q, %v", a, err)
	}
	if err := CheckEIFLLMModel(p); err == nil || !strings.Contains(err.Error(), "lfm2") {
		t.Fatalf("lfm2 must be rejected, got %v", err)
	}
	if err := CheckEIFLLMModel(writeTestGGUF(t, "qwen35")); err != nil {
		t.Fatalf("qwen35 GGUF must pass: %v", err)
	}
	if err := CheckEIFLLMModel(writeTestGGUF(t, "qwen3")); err == nil {
		t.Fatal("plain qwen3 has no GGUF path in the runtime")
	}
	if err := CheckEIFLLMModel(writeTestGGUF(t, "qwen2")); err != nil {
		t.Fatalf("qwen2 must pass: %v", err)
	}
	if err := CheckEIFLLMModel(filepath.Join(t.TempDir(), "missing.gguf")); err != nil {
		t.Fatalf("unreadable file is left to the runtime: %v", err)
	}
}
