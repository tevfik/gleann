package main

import (
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/pkg/memory"
)

func TestCmdMemoryImportGraph(t *testing.T) {
	t.Setenv("GLEANN_MEMORY_DIR", t.TempDir())
	file := filepath.Join("..", "..", "pkg", "memory", "testdata", "v190", "mem.kuzu-export.json")
	cmdMemory([]string{"import-graph", file})
	cmdMemory([]string{"import-graph", file}) // second run is a no-op

	s, err := memory.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if m, _ := s.GraphImportMarker("mem"); m == "" {
		t.Fatal("no import marker after import-graph")
	}
	empty, err := s.EntityNamespaceEmpty("mem")
	if err != nil || empty {
		t.Fatalf("entities not imported: empty=%v err=%v", empty, err)
	}
}
