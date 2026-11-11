package gleann

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestMeasureIndexUsage(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string, n int) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	meta := func(name string, passages int) {
		p := filepath.Join(dir, name, name+".meta.json")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		data := []byte(`{"name":"` + name + `","num_passages":` + strconv.Itoa(passages) + `}`)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	meta("a", 12)
	meta("b", 30)
	write("a/a.passages.jsonl", 1000)
	write("b/b.index", 4096)
	write("b/sub/graph.bin", 100)
	write("stray/file.tmp", 7) // a directory without metadata still takes space

	st, err := MeasureIndexUsage(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Indexes != 2 || st.Passages != 42 {
		t.Fatalf("indexes %d passages %d, want 2 and 42", st.Indexes, st.Passages)
	}
	metaBytes := int64(len(`{"name":"a","num_passages":12}`) + len(`{"name":"b","num_passages":30}`))
	if want := 1000 + 4096 + 100 + 7 + metaBytes; st.Bytes != want {
		t.Fatalf("bytes %d, want %d", st.Bytes, want)
	}
	if st.IndexDir != dir {
		t.Fatalf("index dir %q", st.IndexDir)
	}
}

func TestMeasureIndexUsageMissingDir(t *testing.T) {
	st, err := MeasureIndexUsage(filepath.Join(t.TempDir(), "none"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Indexes != 0 || st.Bytes != 0 {
		t.Fatalf("got %+v", st)
	}
}
