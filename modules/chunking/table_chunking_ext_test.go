package chunking

import (
	"fmt"
	"strings"
	"testing"
)

// tablePieces returns only the table pieces produced for content.
func tablePieces(mc *MarkdownChunker, content string) []contentPiece {
	var out []contentPiece
	for _, p := range mc.chunkSectionContent(content) {
		if p.isTable {
			out = append(out, p)
		}
	}
	return out
}

func buildTable(cols, rows int, cell func(r, c int) string) (header, sep string, body []string) {
	h := make([]string, cols)
	s := make([]string, cols)
	for c := 0; c < cols; c++ {
		h[c] = fmt.Sprintf("Col%d", c)
		s[c] = "---"
	}
	header = "| " + strings.Join(h, " | ") + " |"
	sep = "|" + strings.Join(s, "|") + "|"
	for r := 0; r < rows; r++ {
		cells := make([]string, cols)
		for c := 0; c < cols; c++ {
			cells[c] = cell(r, c)
		}
		body = append(body, "| "+strings.Join(cells, " | ")+" |")
	}
	return
}

// checkTableInvariants verifies that the table pieces reproduce the original
// rows exactly once and in order, every piece starts with header+separator,
// and no piece exceeds chunkSize words unless it holds a single row.
func checkTableInvariants(t *testing.T, mc *MarkdownChunker, pieces []contentPiece, header, sep string, rows []string) {
	t.Helper()
	if len(pieces) == 0 {
		t.Fatalf("expected at least one table piece")
	}
	var got []string
	for i, p := range pieces {
		lines := strings.Split(p.text, "\n")
		if len(lines) < 2 || lines[0] != header || lines[1] != sep {
			t.Fatalf("piece %d does not start with header+separator:\n%s", i, p.text)
		}
		body := lines[2:]
		if len(pieces) > 1 && len(body) > 1 && countWords(p.text) > mc.ChunkSize {
			t.Errorf("piece %d has %d rows and %d words > chunk size %d", i, len(body), countWords(p.text), mc.ChunkSize)
		}
		if n, _ := p.tableMeta["table_rows"].(int); n != len(body) {
			t.Errorf("piece %d table_rows=%v, want %d", i, p.tableMeta["table_rows"], len(body))
		}
		if len(pieces) > 1 {
			if idx, _ := p.tableMeta["table_subchunk"].(int); idx != i {
				t.Errorf("piece %d table_subchunk=%v", i, p.tableMeta["table_subchunk"])
			}
			if tot, _ := p.tableMeta["total_table_subchunks"].(int); tot != len(pieces) {
				t.Errorf("piece %d total_table_subchunks=%v, want %d", i, p.tableMeta["total_table_subchunks"], len(pieces))
			}
		}
		got = append(got, body...)
	}
	if strings.Join(got, "\n") != strings.Join(rows, "\n") {
		t.Fatalf("rows not reproduced exactly once in order:\n got=%q\nwant=%q", got, rows)
	}
}

func TestChunkTable_RowsPreservedAcrossChunkSizes(t *testing.T) {
	header, sep, rows := buildTable(4, 40, func(r, c int) string {
		return strings.Repeat(fmt.Sprintf("w%d_%d ", r, c), 1+(r+c)%3)
	})
	content := "Intro paragraph.\n\n" + header + "\n" + sep + "\n" + strings.Join(rows, "\n") + "\n\nOutro paragraph."
	for _, size := range []int{1, 5, 20, 37, 100, 10000} {
		t.Run(fmt.Sprintf("size=%d", size), func(t *testing.T) {
			mc := NewMarkdownChunker(size, 0)
			checkTableInvariants(t, mc, tablePieces(mc, content), header, sep, rows)
		})
	}
}

func TestChunkTable_WideSingleRowExceedsChunkSize(t *testing.T) {
	header, sep, rows := buildTable(3, 3, func(r, c int) string {
		return strings.Repeat("word ", 50)
	})
	mc := NewMarkdownChunker(20, 0)
	pieces := tablePieces(mc, header+"\n"+sep+"\n"+strings.Join(rows, "\n"))
	if len(pieces) != 3 {
		t.Fatalf("expected one sub-chunk per oversized row, got %d", len(pieces))
	}
	checkTableInvariants(t, mc, pieces, header, sep, rows)
}

func TestChunkTable_ZeroChunkSizeUsesDefault(t *testing.T) {
	// Regression: NewMarkdownChunker(0, ...) used to keep ChunkSize=0 for
	// tables (one row per chunk) while prose used the 512-word default.
	mc := NewMarkdownChunker(0, 0)
	if mc.ChunkSize != 512 {
		t.Fatalf("ChunkSize = %d, want normalised 512", mc.ChunkSize)
	}
	header, sep, rows := buildTable(2, 5, func(r, c int) string { return "x" })
	pieces := tablePieces(mc, header+"\n"+sep+"\n"+strings.Join(rows, "\n"))
	if len(pieces) != 1 {
		t.Fatalf("small table should stay atomic, got %d pieces", len(pieces))
	}
}

func TestChunkTable_InsideCodeFenceIsNotATable(t *testing.T) {
	// Regression: pipe tables inside fenced code blocks were extracted as
	// tables, tearing the fence apart into separate unbalanced chunks.
	content := "Example usage:\n\n```text\n| a | b |\n|---|---|\n| 1 | 2 |\n```\n\nAfter the fence."
	mc := NewMarkdownChunker(512, 0)
	pieces := mc.chunkSectionContent(content)
	for _, p := range pieces {
		if p.isTable {
			t.Fatalf("table inside code fence must not be detected: %q", p.text)
		}
	}
	joined := ""
	for _, p := range pieces {
		joined += p.text + "\n"
	}
	if strings.Count(joined, "```") != 2 {
		t.Fatalf("fence markers must stay balanced, got: %q", joined)
	}
	if len(pieces) != 1 || !strings.Contains(pieces[0].text, "```text\n| a | b |\n|---|---|\n| 1 | 2 |\n```") {
		t.Fatalf("fenced block should be kept contiguous, got %+v", pieces)
	}

	// A real table after the fence is still detected.
	content2 := content + "\n\n| x | y |\n|---|---|\n| 3 | 4 |"
	tp := tablePieces(mc, content2)
	if len(tp) != 1 || !strings.Contains(tp[0].text, "| 3 | 4 |") {
		t.Fatalf("table after fence should be detected, got %+v", tp)
	}
}

func TestChunkTable_TildeFence(t *testing.T) {
	content := "~~~\n| a | b |\n|---|---|\n~~~"
	mc := NewMarkdownChunker(512, 0)
	if tp := tablePieces(mc, content); len(tp) != 0 {
		t.Fatalf("table in ~~~ fence must not be detected: %+v", tp)
	}
}

func TestChunkTable_CRLF(t *testing.T) {
	content := "Intro.\r\n\r\n| a | b |\r\n|---|---|\r\n| 1 | 2 |\r\n| 3 | 4 |\r\n\r\nAfter."
	mc := NewMarkdownChunker(512, 0)
	tp := tablePieces(mc, content)
	if len(tp) != 1 {
		t.Fatalf("expected 1 CRLF table, got %d", len(tp))
	}
	if n := tp[0].tableMeta["table_rows"]; n != 2 {
		t.Fatalf("expected 2 rows, got %v", n)
	}
	if strings.Contains(tp[0].text, "After") || strings.Contains(tp[0].text, "Intro") {
		t.Fatalf("table absorbed surrounding prose: %q", tp[0].text)
	}
}

func TestChunkTable_Variants(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantTable bool
		wantRows  int
	}{
		{"alignment colons", "| a | b |\n|:--|--:|\n| 1 | 2 |", true, 1},
		{"no outer pipes", "a | b\n--- | ---\n1 | 2\n3 | 4", true, 2},
		{"header only", "| a | b |\n|---|---|", true, 0},
		{"no separator", "| a | b |\n| 1 | 2 |", false, 0},
		{"dash line without pipe is hr", "a | b\n---\n1 | 2", false, 0},
		{"separator with text", "| a | b |\n|-x-|---|\n| 1 | 2 |", false, 0},
		{"table immediately after text", "Some text\n| a | b |\n|---|---|\n| 1 | 2 |", true, 1},
		{"table ends at blank line", "| a | b |\n|---|---|\n| 1 | 2 |\n\n| not | row |", true, 1},
		{"table ends at line without pipe", "| a | b |\n|---|---|\n| 1 | 2 |\nplain text", true, 1},
		{"tab separator", "| a | b |\n|\t---\t|\t---\t|\n| 1 | 2 |", true, 1},
	}
	mc := NewMarkdownChunker(512, 0)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tp := tablePieces(mc, tt.content)
			if (len(tp) > 0) != tt.wantTable {
				t.Fatalf("table detected = %v, want %v (%+v)", len(tp) > 0, tt.wantTable, tp)
			}
			if tt.wantTable && tp[0].tableMeta["table_rows"] != tt.wantRows {
				t.Fatalf("table_rows = %v, want %d", tp[0].tableMeta["table_rows"], tt.wantRows)
			}
		})
	}
}

func TestChunkMarkdown_HeadinglessTableMetadata(t *testing.T) {
	header, sep, rows := buildTable(2, 30, func(r, c int) string { return fmt.Sprintf("cell%d %d", r, c) })
	md := "Prefix text.\n\n" + header + "\n" + sep + "\n" + strings.Join(rows, "\n")
	mc := NewMarkdownChunker(30, 0)
	chunks := mc.ChunkMarkdown(md, "doc.md")
	tables := 0
	for i, ch := range chunks {
		if ch.Metadata["chunk_index"] != i || ch.Metadata["total_chunks"] != len(chunks) {
			t.Errorf("chunk %d has inconsistent index metadata: %v", i, ch.Metadata)
		}
		if ch.Metadata["is_table"] == true {
			tables++
			if !strings.HasPrefix(ch.Text, header+"\n"+sep+"\n") {
				t.Errorf("table chunk %d lacks header carry-over: %q", i, ch.Text)
			}
		}
	}
	if tables < 2 {
		t.Fatalf("expected the table to be split into >=2 chunks, got %d", tables)
	}
}

func TestChunkDocument_TableUnderHeadingKeepsContextHeader(t *testing.T) {
	header, sep, rows := buildTable(2, 20, func(r, c int) string { return fmt.Sprintf("v%d", r) })
	md := "# API\n\n## Endpoints\n\n" + header + "\n" + sep + "\n" + strings.Join(rows, "\n") + "\n"
	sections := ParseMarkdownHeadings(md)
	doc := &StructuredDocument{Document: DocumentMeta{Title: "T"}, Sections: sections}
	mc := NewMarkdownChunker(15, 0)
	var tableRows []string
	for _, ch := range mc.ChunkDocument(doc) {
		if ch.Metadata["is_table"] != true {
			continue
		}
		if !strings.Contains(ch.Text, "Endpoints") {
			t.Errorf("table chunk missing breadcrumb: %q", ch.Text)
		}
		lines := strings.Split(ch.RawText, "\n")
		tableRows = append(tableRows, lines[2:]...)
	}
	if strings.Join(tableRows, "\n") != strings.Join(rows, "\n") {
		t.Fatalf("rows not preserved through ChunkDocument: %q", tableRows)
	}
}

// FuzzChunkTableRows builds a table from fuzz-chosen dimensions and checks that
// splitting never loses, duplicates, reorders or cuts a row.
func FuzzChunkTableRows(f *testing.F) {
	f.Add(uint8(3), uint8(10), uint8(5), "abc def")
	f.Add(uint8(1), uint8(1), uint8(1), "x")
	f.Add(uint8(8), uint8(60), uint8(200), "héllo wörld 表格")
	f.Add(uint8(2), uint8(5), uint8(0), "")
	f.Fuzz(func(t *testing.T, cols, rows, size uint8, word string) {
		word = strings.NewReplacer("|", "", "\n", "", "\r", "", "`", "", "~", "").Replace(word)
		if strings.TrimSpace(word) == "" {
			word = "w"
		}
		c := int(cols%10) + 1
		r := int(rows%80) + 1
		header, sep, body := buildTable(c, r, func(ri, ci int) string {
			return fmt.Sprintf("%s%d", strings.Repeat(word+" ", 1+(ri*ci)%4), ri)
		})
		mc := NewMarkdownChunker(int(size), 0)
		content := "lead\n\n" + header + "\n" + sep + "\n" + strings.Join(body, "\n") + "\n\ntail"
		checkTableInvariants(t, mc, tablePieces(mc, content), header, sep, body)
	})
}

// FuzzChunkSectionContent feeds arbitrary markdown and checks robustness and
// that every table piece is structurally a table.
func FuzzChunkSectionContent(f *testing.F) {
	seeds := []string{
		"",
		"| a | b |\n|---|---|\n| 1 | 2 |",
		"```\n| a | b |\n|---|---|\n```",
		"text\r\n| a |\r\n|---|\r\n| 1 |\r\n",
		"|\n-|",
		"| a |\n|---|\n```\n| b |\n|---|\n",
		"# H\n\n| a | b |\n| - | - |\n",
	}
	for _, s := range seeds {
		f.Add(s, uint8(4))
	}
	f.Fuzz(func(t *testing.T, content string, size uint8) {
		mc := NewMarkdownChunker(int(size), 0)
		for _, p := range mc.chunkSectionContent(content) {
			if strings.TrimSpace(p.text) == "" {
				t.Fatalf("empty piece produced for %q", content)
			}
			if p.isTable {
				lines := strings.Split(p.text, "\n")
				if len(lines) < 2 || !strings.Contains(lines[0], "|") || !isTableSeparator(lines[1]) {
					t.Fatalf("table piece without header/separator: %q", p.text)
				}
			}
		}
		_ = mc.ChunkMarkdown(content, "f.md")
	})
}
