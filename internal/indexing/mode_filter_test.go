package indexing

import "testing"

// C-02: files outside the index mode must not be treated as replaceable.
func TestFileInMode(t *testing.T) {
	cases := []struct {
		mode Mode
		path string
		want bool
	}{
		{ModeCode, "main.go", true},
		{ModeCode, "README.md", true},
		{ModeCode, "config.json", false},
		{ModeCode, "report.pdf", false},
		{ModeDocs, "main.go", false},
		{ModeDocs, "report.docx", true},
		{ModeAll, "config.json", true},
	}
	for _, c := range cases {
		if got := fileInMode(c.mode, c.path); got != c.want {
			t.Errorf("fileInMode(%v,%q)=%v want %v", c.mode, c.path, got, c.want)
		}
	}
}
