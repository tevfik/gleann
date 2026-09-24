package walker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWalker_NestedIgnoreAndSubmodules(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gleann-walker-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Setup directory structure:
	// tmpDir/
	//   .gitmodules (defines submodule "third_party/nuttx")
	//   .gitignore ("*.log", "ignored_dir/")
	//   .git/info/exclude ("*.secret")
	//   main.go
	//   test.log
	//   app.secret
	//   node_modules/
	//     dep.js
	//   third_party/
	//     nuttx/
	//       os.c
	//     custom/
	//       sub.go
	//   pkg/
	//     nested/
	//       .gitignore ("*.nested_ignored", "!allowed.nested_ignored")
	//       file.nested_ignored
	//       allowed.nested_ignored
	//       normal.go

	dirs := []string{
		filepath.Join(tmpDir, ".git", "info"),
		filepath.Join(tmpDir, "node_modules"),
		filepath.Join(tmpDir, "third_party", "nuttx"),
		filepath.Join(tmpDir, "third_party", "custom"),
		filepath.Join(tmpDir, "pkg", "nested"),
		filepath.Join(tmpDir, "ignored_dir"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	// Write files
	writeFile(t, filepath.Join(tmpDir, ".gitmodules"), `[submodule "third_party/nuttx"]
	path = third_party/nuttx
	url = https://github.com/foo/nuttx.git
`)
	writeFile(t, filepath.Join(tmpDir, ".gitignore"), "*.log\nignored_dir/\n")
	writeFile(t, filepath.Join(tmpDir, ".git", "info", "exclude"), "*.secret\n")
	writeFile(t, filepath.Join(tmpDir, "main.go"), "package main\n")
	writeFile(t, filepath.Join(tmpDir, "test.log"), "log line\n")
	writeFile(t, filepath.Join(tmpDir, "app.secret"), "secret key\n")
	writeFile(t, filepath.Join(tmpDir, "node_modules", "dep.js"), "console.log(1)\n")
	writeFile(t, filepath.Join(tmpDir, "vendor", "dep", "dep.go"), "package dep\n")
	writeFile(t, filepath.Join(tmpDir, "third_party", "nuttx", "os.c"), "int os_main() { return 0; }\n")
	writeFile(t, filepath.Join(tmpDir, "third_party", "custom", "sub.go"), "package custom\n")
	writeFile(t, filepath.Join(tmpDir, "ignored_dir", "foo.txt"), "ignored\n")

	// Nested ignore with negation
	writeFile(t, filepath.Join(tmpDir, "pkg", "nested", ".gitignore"), "*.nested_ignored\n!allowed.nested_ignored\n")
	writeFile(t, filepath.Join(tmpDir, "pkg", "nested", "file.nested_ignored"), "ignore me\n")
	writeFile(t, filepath.Join(tmpDir, "pkg", "nested", "allowed.nested_ignored"), "keep me\n")
	writeFile(t, filepath.Join(tmpDir, "pkg", "nested", "normal.go"), "package nested\n")

	// 1. Walk without submodules (default)
	files, err := CollectFiles(tmpDir, Options{IncludeSubmodules: false}, nil)
	if err != nil {
		t.Fatalf("CollectFiles failed: %v", err)
	}

	fileSet := make(map[string]bool)
	for _, f := range files {
		rel, _ := filepath.Rel(tmpDir, f)
		fileSet[filepath.ToSlash(rel)] = true
	}

	// Assertions for default walk:
	// Allowed:
	expectedFiles := []string{
		"main.go",
		"third_party/custom/sub.go",
		"pkg/nested/allowed.nested_ignored",
		"pkg/nested/normal.go",
	}
	for _, exp := range expectedFiles {
		if !fileSet[exp] {
			t.Errorf("Expected file %q to be included, but it was not", exp)
		}
	}

	// Should be excluded:
	excludedFiles := []string{
		"test.log",                                // root .gitignore
		"app.secret",                              // .git/info/exclude
		"node_modules/dep.js",                     // default ignored dir
		"vendor/dep/dep.go",                       // default ignored vendor dir
		"third_party/nuttx/os.c",                  // submodule
		"ignored_dir/foo.txt",                     // root .gitignore
		"pkg/nested/file.nested_ignored",          // nested .gitignore
	}
	for _, excl := range excludedFiles {
		if fileSet[excl] {
			t.Errorf("File %q should have been excluded, but was included", excl)
		}
	}

	// 2. Walk WITH submodules
	filesWithSub, err := CollectFiles(tmpDir, Options{IncludeSubmodules: true}, nil)
	if err != nil {
		t.Fatalf("CollectFiles with submodules failed: %v", err)
	}
	fileSetWithSub := make(map[string]bool)
	for _, f := range filesWithSub {
		rel, _ := filepath.Rel(tmpDir, f)
		fileSetWithSub[filepath.ToSlash(rel)] = true
	}

	if !fileSetWithSub["third_party/nuttx/os.c"] {
		t.Errorf("Expected submodule file third_party/nuttx/os.c to be included when IncludeSubmodules=true")
	}
}

func TestWalker_DoublestarAndFilter(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gleann-walker-doublestar-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	writeFile(t, filepath.Join(tmpDir, ".gitignore"), "**/temp/**\n*.tmp\n")
	writeFile(t, filepath.Join(tmpDir, "a", "temp", "b", "c.txt"), "c")
	writeFile(t, filepath.Join(tmpDir, "docs", "deep", "nested", "doc.tmp"), "tmp")
	writeFile(t, filepath.Join(tmpDir, "docs", "guide.txt"), "guide")
	writeFile(t, filepath.Join(tmpDir, "src", "main.go"), "package main")

	// Collect only .go files using filter
	goFiles, err := CollectFiles(tmpDir, Options{}, func(path string, d os.DirEntry) bool {
		return filepath.Ext(path) == ".go"
	})
	if err != nil {
		t.Fatalf("CollectFiles error: %v", err)
	}
	if len(goFiles) != 1 || filepath.Base(goFiles[0]) != "main.go" {
		t.Errorf("Expected 1 main.go file, got %v", goFiles)
	}

	// Verify doublestar ignore
	allFiles, err := CollectFiles(tmpDir, Options{}, nil)
	if err != nil {
		t.Fatalf("CollectFiles error: %v", err)
	}
	for _, f := range allFiles {
		rel, _ := filepath.Rel(tmpDir, f)
		slash := filepath.ToSlash(rel)
		if slash == "a/temp/b/c.txt" || slash == "docs/deep/nested/doc.tmp" {
			t.Errorf("File %q should have been ignored by doublestar pattern", slash)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
