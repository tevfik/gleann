package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// applyPassages applies one source whose passages are given in chunk order.
func applyPassages(t *testing.T, s *Store, source string, ps []NewPassage) []PassageID {
	t.Helper()
	ids, err := s.ApplySource(context.Background(), SourceDelta{
		Source:   source,
		File:     FileRow{Hash: "hash-" + source},
		Passages: ps,
	})
	if err != nil {
		t.Fatalf("ApplySource(%s): %v", source, err)
	}
	return ids
}

// TestListPassages_OrderAndPaging: ListPassages returns passages in ascending
// internal id order and honours limit/offset, so a page is deterministic.
func TestListPassages_OrderAndPaging(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	ids := applyPassages(t, s, "a.md", []NewPassage{
		{Text: "first", Meta: `{"lang":"en"}`},
		{Text: "second"},
	})
	ids = append(ids, applyPassages(t, s, "b.md", []NewPassage{
		{Text: "third"}, {Text: "fourth"}, {Text: "fifth"},
	})...)

	all, err := s.ListPassages(ctx, 100, 0)
	if err != nil {
		t.Fatalf("ListPassages: %v", err)
	}
	if len(all) != len(ids) {
		t.Fatalf("got %d passages, want %d", len(all), len(ids))
	}
	for i, p := range all {
		if p.ID != ids[i] {
			t.Errorf("passage %d has id %d, want %d", i, p.ID, ids[i])
		}
		if i > 0 && all[i-1].ID >= p.ID {
			t.Errorf("ids are not ascending: %d then %d", all[i-1].ID, p.ID)
		}
	}
	if all[0].Source != "a.md" || all[0].Text != "first" || all[0].Meta != `{"lang":"en"}` {
		t.Errorf("first passage not returned whole: %+v", all[0])
	}

	page, err := s.ListPassages(ctx, 2, 1)
	if err != nil {
		t.Fatalf("ListPassages page: %v", err)
	}
	if len(page) != 2 || page[0].Text != "second" || page[1].Text != "third" {
		t.Fatalf("page = %+v, want second, third", page)
	}

	// A page past the end is empty, not an error.
	past, err := s.ListPassages(ctx, 10, 100)
	if err != nil {
		t.Fatalf("ListPassages past end: %v", err)
	}
	if len(past) != 0 {
		t.Errorf("page past the end returned %d passages", len(past))
	}
}

// TestListPassages_DefaultAndBounds: limit 0 means the default page size, and
// a negative limit/offset or one above MaxListLimit is refused instead of
// silently clamped.
func TestListPassages_DefaultAndBounds(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	applyPassages(t, s, "a.md", []NewPassage{{Text: "one"}, {Text: "two"}})

	got, err := s.ListPassages(ctx, 0, 0)
	if err != nil {
		t.Fatalf("ListPassages(0,0): %v", err)
	}
	if len(got) != 2 {
		t.Errorf("default limit returned %d passages, want 2", len(got))
	}
	if DefaultListLimit <= 0 || MaxListLimit < DefaultListLimit {
		t.Fatalf("list bounds are inconsistent: default %d, max %d", DefaultListLimit, MaxListLimit)
	}
	for _, c := range []struct {
		name        string
		limit, offs int
	}{
		{"negative limit", -1, 0},
		{"limit above max", MaxListLimit + 1, 0},
		{"negative offset", 1, -1},
	} {
		if _, err := s.ListPassages(ctx, c.limit, c.offs); err == nil {
			t.Errorf("%s (%d, %d): want an error", c.name, c.limit, c.offs)
		}
	}
}

// TestListPassages_EmptyAndClosed: an empty store lists nothing without an
// error, and a closed store reports the failure.
func TestListPassages_EmptyAndClosed(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	got, err := s.ListPassages(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListPassages on an empty store: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty store returned %d passages", len(got))
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := s.ListPassages(ctx, 10, 0); err == nil {
		t.Error("ListPassages on a closed store: want an error")
	}
}

// TestListPassages_UsesIDOrder: the page query must be answered by the integer
// primary key, with no temporary b-tree for the ORDER BY, so LIMIT stops the
// scan early instead of sorting the whole table.
func TestListPassages_UsesIDOrder(t *testing.T) {
	s := openTestStore(t)
	rows, err := s.reader.Query(`EXPLAIN QUERY PLAN `+listPassagesQuery, DefaultListLimit, 0)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scanning plan: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading plan: %v", err)
	}
	joined := strings.Join(plan, "\n")
	if strings.Contains(joined, "TEMP B-TREE") {
		t.Errorf("the page query sorts instead of scanning the id index:\n%s", joined)
	}
}

// sectionFixture seeds two documents:
//   - guide.md: section s0 (two chunks) and section s1 (one chunk), the shape
//     modules/chunking/markdown_chunker.go writes;
//   - notes.md: passages with only a "heading" fallback and one without any
//     section metadata;
//   - plain.txt: passages with no section metadata at all.
func sectionFixture(t *testing.T, s *Store) {
	t.Helper()
	applyPassages(t, s, "guide.md", []NewPassage{
		{Text: "intro one", Meta: `{"section_id":"s0","section_title":"Introduction","section_path":"Introduction","section_summary":"Overview.","heading_level":1}`},
		{Text: "intro two", Meta: `{"section_id":"s0","section_title":"Introduction","section_path":"Introduction","section_summary":"Overview.","heading_level":1}`},
		{Text: "detail", Meta: `{"section_id":"s1","section_title":"Details","section_path":"Introduction > Details","heading_level":2}`},
	})
	applyPassages(t, s, "notes.md", []NewPassage{
		{Text: "fallback heading", Meta: `{"heading":"Fallback heading"}`},
		{Text: "no section", Meta: `{}`},
	})
	applyPassages(t, s, "plain.txt", []NewPassage{{Text: "just text"}})
}

// TestListSections_GroupsPassageMetadata: sections are the distinct heading
// groups of the stored passages, with their passage count, chunk range and
// id range; passages without section metadata are left out (no bogus groups).
func TestListSections_GroupsPassageMetadata(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	sectionFixture(t, s)

	got, err := s.ListSections(ctx, 100, 0)
	if err != nil {
		t.Fatalf("ListSections: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d sections, want 3: %+v", len(got), got)
	}

	intro, details, fallback := got[0], got[1], got[2]
	if intro.Source != "guide.md" || intro.SectionID != "s0" || intro.Title != "Introduction" ||
		intro.Path != "Introduction" || intro.Summary != "Overview." {
		t.Errorf("first section = %+v, want guide.md s0 Introduction", intro)
	}
	if intro.PassageCount != 2 || intro.FirstChunk != 0 || intro.LastChunk != 1 {
		t.Errorf("intro group = count %d, chunks %d-%d; want 2, 0-1",
			intro.PassageCount, intro.FirstChunk, intro.LastChunk)
	}
	if intro.FirstPassage >= intro.LastPassage {
		t.Errorf("intro ids %d..%d are not an ascending range", intro.FirstPassage, intro.LastPassage)
	}
	if details.Source != "guide.md" || details.SectionID != "s1" || details.Title != "Details" ||
		details.Path != "Introduction > Details" || details.PassageCount != 1 {
		t.Errorf("second section = %+v, want guide.md s1 Details", details)
	}
	if fallback.Source != "notes.md" || fallback.SectionID != "" || fallback.Title != "Fallback heading" {
		t.Errorf("third section = %+v, want the notes.md heading fallback", fallback)
	}
	for _, sec := range got {
		if sec.Source == "plain.txt" {
			t.Errorf("a passage without section metadata produced a group: %+v", sec)
		}
	}
}

// TestListSections_Paging: offset/limit page over the deterministic order
// (source, then first passage id).
func TestListSections_Paging(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	sectionFixture(t, s)

	all, err := s.ListSections(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListSections: %v", err)
	}
	page, err := s.ListSections(ctx, 2, 1)
	if err != nil {
		t.Fatalf("ListSections page: %v", err)
	}
	if len(page) != 2 || page[0] != all[1] || page[1] != all[2] {
		t.Fatalf("page = %+v, want sections 1..2", page)
	}
}

// TestListSections_InvalidJSONAndEmptyStore: a damaged meta value is skipped
// (never a crash or a bogus group) and an empty store lists nothing.
func TestListSections_InvalidJSONAndEmptyStore(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	got, err := s.ListSections(ctx, 10, 0)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty store: %v, %d sections", err, len(got))
	}

	applyPassages(t, s, "damaged.md", []NewPassage{
		{Text: "broken", Meta: `not-json`},
		{Text: "valid", Meta: `{"section_id":"s0","section_title":"Valid"}`},
	})
	got, err = s.ListSections(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListSections: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Valid" || got[0].PassageCount != 1 {
		t.Fatalf("damaged meta handling: %+v", got)
	}
}

// TestListSections_BoundsAndClosed: the same page bounds as ListPassages.
func TestListSections_BoundsAndClosed(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	sectionFixture(t, s)

	for _, c := range []struct {
		name        string
		limit, offs int
	}{
		{"negative limit", -1, 0},
		{"limit above max", MaxListLimit + 1, 0},
		{"negative offset", 1, -1},
	} {
		if _, err := s.ListSections(ctx, c.limit, c.offs); err == nil {
			t.Errorf("%s (%d, %d): want an error", c.name, c.limit, c.offs)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := s.ListSections(ctx, 10, 0); err == nil {
		t.Error("ListSections on a closed store: want an error")
	}
}

// benchListStore builds a synthetic store of n passages spread over files of
// 50 chunks, each chunk carrying markdown section metadata, without touching
// the vector files.
func benchListStore(b *testing.B, n int) *Store {
	b.Helper()
	path := filepath.Join(b.TempDir(), "idx", "idx.db")
	s, err := Open(path, Options{})
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { s.Close() })
	ctx := context.Background()
	const perFile = 50
	for start := 0; start < n; start += perFile {
		end := start + perFile
		if end > n {
			end = n
		}
		ps := make([]NewPassage, 0, end-start)
		for i := start; i < end; i++ {
			ps = append(ps, NewPassage{
				Text: strings.Repeat("passage text ", 40),
				Meta: fmt.Sprintf(`{"section_id":"s%d","section_title":"Section %d","section_path":"Doc > Section %d","lang":"en"}`, i/10, i/10, i/10),
			})
		}
		if _, err := s.ApplySource(ctx, SourceDelta{
			Source:   fmt.Sprintf("doc%03d.md", start/perFile),
			Passages: ps,
		}); err != nil {
			b.Fatalf("ApplySource: %v", err)
		}
	}
	return s
}

// BenchmarkListPassages100 measures the page served for ?limit=100: the query
// must stop after 100 rows of the id index, independent of the table size.
func BenchmarkListPassages100(b *testing.B) {
	s := benchListStore(b, 20000)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.ListPassages(ctx, 100, 0); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkListSections measures the section aggregation over the same store
// (it reads the meta column of every row, so it is the heavier of the two).
func BenchmarkListSections(b *testing.B) {
	s := benchListStore(b, 20000)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.ListSections(ctx, 100, 0); err != nil {
			b.Fatal(err)
		}
	}
}
