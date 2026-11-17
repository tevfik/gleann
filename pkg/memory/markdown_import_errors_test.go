package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeBlockFile(t *testing.T, dir string, b *Block) {
	t.Helper()
	md, err := BlockToMarkdown(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, safeBlockFilename(b.ID)), md, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Import refuses paths that are not directories and files it cannot parse,
// and it changes nothing when it refuses.
func TestImport_RejectsBadInput(t *testing.T) {
	s := openTestStore(t)
	root := t.TempDir()

	_, err := s.Import(filepath.Join(root, "missing"))
	wantErr(t, "missing dir", err, "reading import directory")
	file := filepath.Join(root, "file.md")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = s.Import(file)
	wantErr(t, "file", err, "not a directory")

	for _, tc := range []struct {
		name, body, want string
	}{
		{"no front matter", "just text\n", "does not start with '---'"},
		{"unclosed front matter", "---\nid: x\ntier: long\n", "closing '---' front matter delimiter not found"},
		{"bad yaml", "---\nid: [unclosed\n---\nbody\n", "decoding block front matter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			good := &Block{ID: "good", Tier: TierLong, Content: "fine", CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)}
			writeBlockFile(t, dir, good)
			if err := os.WriteFile(filepath.Join(dir, "zz-bad.md"), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := s.Import(dir)
			wantErr(t, "Import", err, tc.want)
			if _, err := s.Get("good"); err == nil {
				t.Fatal("a refused import stored the good block")
			}
		})
	}

	// A broken summary file fails the import as well.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "summaries"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "summaries", "s.md"), []byte("no front matter"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = s.Import(dir)
	wantErr(t, "summary", err, "parsing summary file")
	if err := os.WriteFile(filepath.Join(dir, "summaries", "s.md"), []byte("---\nconversation_id: [x\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = s.Import(dir)
	wantErr(t, "summary yaml", err, "decoding summary front matter")
}

// The persisted part of an import is one transaction: when one block fails
// to store, the blocks before it are not left behind.
func TestImport_IsAtomic(t *testing.T) {
	s := openTestStore(t)
	dir := t.TempDir()
	ts := time.Unix(1700000000, 0)
	for _, id := range []string{"a1", "a2", "a3"} {
		writeBlockFile(t, dir, &Block{ID: id, Tier: TierLong, Content: "content " + id, CreatedAt: ts, UpdatedAt: ts})
	}
	execSQL(t, s, `CREATE TRIGGER fail_a3 BEFORE INSERT ON blocks WHEN NEW.id = 'a3' BEGIN SELECT RAISE(ABORT, 'disk full'); END`)
	_, err := s.Import(dir)
	wantErr(t, "Import", err, "importing into memory database")
	st, err := s.Stats()
	if err != nil || st.LongTermCount != 0 {
		t.Fatalf("after a failed import: %+v, %v; want no blocks", st, err)
	}
	execSQL(t, s, `DROP TRIGGER fail_a3`)
	stats, err := s.Import(dir)
	if err != nil || stats.BlocksImported != 3 {
		t.Fatalf("retry = %+v, %v; want 3 imported", stats, err)
	}
	if ids := searchIDs(t, s, "content"); len(ids) != 3 {
		t.Fatalf("lexical rows after import: %v", ids)
	}

	// A newer version whose lexical refresh fails rolls the block update back.
	newer := &Block{ID: "a1", Tier: TierLong, Content: "rewritten", CreatedAt: ts, UpdatedAt: ts.Add(time.Hour)}
	writeBlockFile(t, dir, newer)
	abortOn(t, s, "UPDATE", "blocks")
	_, err = s.Import(dir)
	wantErr(t, "Import update", err, "updating block a1")
	if b, _ := s.Get("a1"); b == nil || b.Content != "content a1" {
		t.Fatalf("a1 changed by a failed import: %+v", b)
	}
}

// Short-term blocks are imported into the session cache with the same
// newest-wins conflict rule as persisted blocks.
func TestImport_ShortTermConflicts(t *testing.T) {
	s := openTestStore(t)
	dir := t.TempDir()
	base := time.Unix(1700000000, 0)
	b := &Block{ID: "st", Tier: TierShort, Content: "v1", CreatedAt: base, UpdatedAt: base}
	writeBlockFile(t, dir, b)

	run := func() *ImportStats {
		t.Helper()
		st, err := s.Import(dir)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	if st := run(); st.BlocksImported != 1 {
		t.Fatalf("first import: %+v", st)
	}
	if st := run(); st.BlocksUnchanged != 1 || st.Conflicts != 0 {
		t.Fatalf("identical re-import: %+v", st)
	}
	newer := *b
	newer.Content, newer.UpdatedAt = "v2", base.Add(time.Hour)
	writeBlockFile(t, dir, &newer)
	if st := run(); st.Conflicts != 1 || st.BlocksUpdated != 1 {
		t.Fatalf("newer import: %+v", st)
	}
	older := *b
	older.Content, older.UpdatedAt = "v0", base.Add(-time.Hour)
	writeBlockFile(t, dir, &older)
	if st := run(); st.Conflicts != 1 || st.BlocksUnchanged != 1 {
		t.Fatalf("older import: %+v", st)
	}
	got, err := s.Get("st")
	if err != nil || got.Content != "v2" {
		t.Fatalf("short-term block = %+v, %v; want v2 kept", got, err)
	}
	if len(s.shortTerm) != 1 {
		t.Fatalf("%d short-term blocks, want 1", len(s.shortTerm))
	}
}

// Export reports targets it cannot write.
func TestExport_Errors(t *testing.T) {
	s := openTestStore(t)
	addLong(t, s, "e1", "exported")
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := s.Export(filepath.Join(file, "out"))
	wantErr(t, "Export under a file", err, "creating export directory")

	// A file where the summaries directory goes.
	if err := s.SaveSummary(&Summary{ConversationID: "c", Content: "sum"}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "summaries"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = s.Export(out)
	wantErr(t, "Export summaries", err, "creating summaries directory")

	// A directory where a block file goes.
	out2 := filepath.Join(root, "out2")
	if err := os.MkdirAll(filepath.Join(out2, safeBlockFilename("e1")), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = s.Export(out2)
	wantErr(t, "Export block", err, "writing block file")

	execSQL(t, s, `UPDATE blocks SET metadata = 'nope' WHERE id = 'e1'`)
	_, err = s.Export(filepath.Join(root, "out3"))
	wantErr(t, "Export undecodable", err, "reading block for export")
}

// Front matter with CRLF line ends and a closing delimiter on the last line
// without a newline parses.
func TestSplitFrontMatterVariants(t *testing.T) {
	yamlPart, body, err := splitFrontMatter([]byte("---\r\nid: x\r\n---"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(yamlPart)) != "id: x" || len(body) != 0 {
		t.Fatalf("yaml %q body %q", yamlPart, body)
	}
}

func TestBlocksEqual(t *testing.T) {
	ts := time.Unix(10, 0)
	base := Block{ID: "x", Tags: []string{"a"}, Metadata: map[string]string{"k": "v"}, ExpiresAt: &ts}
	same := base
	if !blocksEqual(&base, &same) {
		t.Fatal("identical blocks differ")
	}
	other := ts.Add(time.Second)
	cases := map[string]func(b *Block){
		"tags length":  func(b *Block) { b.Tags = []string{"a", "b"} },
		"tag value":    func(b *Block) { b.Tags = []string{"z"} },
		"meta length":  func(b *Block) { b.Metadata = map[string]string{} },
		"meta value":   func(b *Block) { b.Metadata = map[string]string{"k": "w"} },
		"meta key":     func(b *Block) { b.Metadata = map[string]string{"j": "v"} },
		"expiry nil":   func(b *Block) { b.ExpiresAt = nil },
		"expiry value": func(b *Block) { b.ExpiresAt = &other },
	}
	for name, mutate := range cases {
		c := base
		mutate(&c)
		if blocksEqual(&base, &c) {
			t.Errorf("%s: blocks reported equal", name)
		}
	}
	if blocksEqual(&base, nil) || !blocksEqual(nil, nil) {
		t.Error("nil handling wrong")
	}
}

func TestSanitizeFilenameEdgeCases(t *testing.T) {
	for in, want := range map[string]string{
		"":     "_unnamed",
		"...":  "_unnamed",
		" . ":  "_unnamed",
		"CON":  "_CON",
		"a<b>": "a_b_",
	} {
		if got := sanitizeFilename(in); got != want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}
