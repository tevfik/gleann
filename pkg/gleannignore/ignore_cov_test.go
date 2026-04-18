package gleannignore

import (
	"os"
	"path/filepath"
	"testing"
)

// ── matchPattern ──────────────────────────────────────────────

func TestMatchPattern_BasenameGlob(t *testing.T) {
	// Pattern without slash matches basename
	if !matchPattern("*.log", "output/debug.log", false) {
		t.Fatal("*.log should match debug.log")
	}
	if matchPattern("*.log", "output/debug.txt", false) {
		t.Fatal("*.log should not match debug.txt")
	}
}

func TestMatchPattern_PathComponent(t *testing.T) {
	// Match against path components
	if !matchPattern("vendor", "project/vendor/lib.go", false) {
		t.Fatal("should match 'vendor' component")
	}
}

func TestMatchPattern_FullPathMatch(t *testing.T) {
	// Pattern with slash matches full path
	if !matchPattern("docs/internal", "docs/internal", false) {
		t.Fatal("exact path match")
	}
}

func TestMatchPattern_AnchoredPattern(t *testing.T) {
	// Leading / is stripped and treated as anchored
	if !matchPattern("/build", "build", true) {
		t.Fatal("anchored /build should match build")
	}
}

func TestMatchPattern_DirectoryTrailingSlash(t *testing.T) {
	// isDir=true should match patterns with / suffix check
	if !matchPattern("tmp/", "tmp", true) {
		t.Fatal("dir pattern should match dir")
	}
}

// ── matchDoublestar ───────────────────────────────────────────

func TestMatchDoublestar_PrefixOnly(t *testing.T) {
	// "docs/**" should match anything under docs
	if !matchDoublestar("docs/**", "docs/README.md") {
		t.Fatal("docs/** should match docs/README.md")
	}
	if !matchDoublestar("docs/**", "docs/sub/file.txt") {
		t.Fatal("docs/** should match docs/sub/file.txt")
	}
}

func TestMatchDoublestar_SuffixOnly(t *testing.T) {
	// "**/*.log" should match any .log file
	if !matchDoublestar("**/*.log", "output/debug.log") {
		t.Fatal("**/*.log should match output/debug.log")
	}
	if !matchDoublestar("**/*.log", "deep/nested/app.log") {
		t.Fatal("**/*.log should match deep path")
	}
}

func TestMatchDoublestar_PrefixAndSuffix(t *testing.T) {
	// "src/**/*.go" should match .go files under src
	if !matchDoublestar("src/**/*.go", "src/main.go") {
		t.Fatal("should match src/main.go")
	}
	if !matchDoublestar("src/**/*.go", "src/pkg/util.go") {
		t.Fatal("should match src/pkg/util.go")
	}
}

func TestMatchDoublestar_NoPrefix(t *testing.T) {
	// "**" alone matches everything
	if !matchDoublestar("**", "any/path/here.txt") {
		t.Fatal("** should match everything")
	}
}

func TestMatchDoublestar_Multiple(t *testing.T) {
	// Multiple ** — fallback to simple contains
	if !matchDoublestar("**/test/**", "a/test/b") {
		t.Fatal("multiple ** fallback should match")
	}
}

func TestMatchDoublestar_PrefixMismatch(t *testing.T) {
	if matchDoublestar("src/**/*.go", "lib/main.go") {
		t.Fatal("prefix mismatch should not match")
	}
}

// ── Matcher.Match with negation ───────────────────────────────

func TestMatcher_Match_Combined(t *testing.T) {
	dir := t.TempDir()
	content := "*.log\n!important.log\nbuild/\n"
	os.WriteFile(filepath.Join(dir, ".gleannignore"), []byte(content), 0644)

	m := Load(dir)

	// *.log should be ignored
	if !m.Match("debug.log", false) {
		t.Fatal("debug.log should be ignored")
	}

	// !important.log negates
	if m.Match("important.log", false) {
		t.Fatal("important.log should NOT be ignored (negated)")
	}

	// build/ is dir-only pattern
	if m.Match("build", false) {
		t.Fatal("build as FILE should not match dir-only pattern")
	}
	if !m.Match("build", true) {
		t.Fatal("build as DIR should be ignored")
	}
}

func TestMatcher_Match_Comments(t *testing.T) {
	dir := t.TempDir()
	content := "# This is a comment\n\n*.tmp\n"
	os.WriteFile(filepath.Join(dir, ".gleannignore"), []byte(content), 0644)

	m := Load(dir)
	if !m.Match("test.tmp", false) {
		t.Fatal("should ignore .tmp files")
	}
}
