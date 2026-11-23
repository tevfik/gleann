package doccheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// architecturePathRef matches a backticked repository path to a package or
// file (internal/..., pkg/..., cmd/...) in docs/architecture.md.
var architecturePathRef = regexp.MustCompile("`((?:internal|pkg|cmd)/[^`\\s]*)`")

// plannedRefactorsHeading starts the section of docs/architecture.md that
// names packages which are planned but do not exist yet on purpose.
const plannedRefactorsHeading = "## Planned refactors"

// cleanArchitecturePath drops a line anchor or fragment ("file.go:12",
// "file.go#L12"), a trailing slash and glob-only references such as
// "cmd/..." that cannot be checked on disk.
func cleanArchitecturePath(ref string) string {
	if i := strings.IndexAny(ref, ":#"); i >= 0 {
		ref = ref[:i]
	}
	ref = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(ref), "/"))
	ref = strings.TrimSpace(strings.TrimSuffix(ref, "/..."))
	if ref == "" || ref == "..." {
		return ""
	}
	return ref
}

// repoPathExists reports whether ref exists under root. Wildcards in ref
// (for example "plugin*.go") are matched with filepath.Glob.
func repoPathExists(root, ref string) bool {
	pattern := filepath.Join(root, filepath.FromSlash(ref))
	if strings.ContainsAny(ref, "*?[") {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return false
		}
		for _, m := range matches {
			if _, err := os.Stat(m); err == nil {
				return true
			}
		}
		return false
	}
	_, err := os.Stat(pattern)
	return err == nil
}

// TestArchitecturePathsExist guards the frozen-component inventory and the
// package tables of docs/architecture.md: every internal/, pkg/ or cmd/ path
// cited before "Planned refactors" must exist in the repository. The planned
// section deliberately names future packages (internal/plugins, internal/llm,
// pkg/gleann/core), so it is out of scope.
func TestArchitecturePathsExist(t *testing.T) {
	root := repoRoot(t)
	lines := strings.Split(readFile(t, filepath.Join(root, "docs", "architecture.md")), "\n")

	inPlanned := false
	sawPlanned := false
	checked := 0
	for i, line := range lines {
		if strings.TrimSpace(line) == plannedRefactorsHeading {
			inPlanned = true
			sawPlanned = true
			continue
		}
		if inPlanned {
			continue
		}
		for _, m := range architecturePathRef.FindAllStringSubmatch(line, -1) {
			ref := cleanArchitecturePath(m[1])
			if ref == "" {
				continue
			}
			checked++
			if !repoPathExists(root, ref) {
				t.Errorf("docs/architecture.md:%d cites %q, which does not exist in the repository", i+1, m[1])
			}
		}
	}
	if !sawPlanned {
		t.Fatalf("heading %q not found in docs/architecture.md", plannedRefactorsHeading)
	}
	if checked == 0 {
		t.Fatal("found no internal/, pkg/ or cmd/ paths in docs/architecture.md; the scanner is broken")
	}
}
