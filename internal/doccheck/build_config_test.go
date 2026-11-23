package doccheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// This file keeps the build entry points honest: `make full` and `task full`
// must produce the same gleann-full binary. The release workflow calls
// `task full` (see .github/workflows/release.yml), so the Taskfile tag set is
// the reference; the Makefile must not drift from it. The tests also guard the
// RUNPATH: the bundled build must find its libraries through $ORIGIN, never
// through /usr/local/lib or a user's ~/.local/lib, which would silently hide a
// missing bundled library and pick up a mismatched system FAISS.

// canonicalFullTags is the tag set of every gleann-full build: FAISS vectors
// (faiss), tree-sitter ASTs (treesitter), the embedded Rust engine (native)
// and the EIF runtime (eif). Release artifacts built with a subset of these
// quietly lose features.
var canonicalFullTags = []string{"eif", "faiss", "native", "treesitter"}

// normalizeTagSet splits on spaces and commas, trims quotes, lowercases,
// dedupes and sorts, so "faiss,treesitter native" and "native eif faiss
// treesitter" compare equal.
func normalizeTagSet(s string) []string {
	seen := make(map[string]bool)
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == ',' || r == '\t'
	}) {
		p := strings.ToLower(strings.Trim(strings.TrimSpace(part), `"'`))
		if p != "" {
			seen[p] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

var (
	makeFullTagsRe = regexp.MustCompile(`(?m)^FULL_TAGS\s*\?=\s*(.+?)\s*$`)
	taskFullTagsRe = regexp.MustCompile(`(?m)^\s*FULL_TAGS:\s*['"]?([^'"\n]+?)['"]?\s*$`)
	tagsFlagRe     = regexp.MustCompile(`-tags\s+(?:"([^"]*)"|'([^']*)'|([^\s'"]+))`)
	// rpathValueRe captures the argument of -Wl,-rpath,<dir>, in the quoting
	// styles used by the Makefile and the Taskfile.
	rpathValueRe = regexp.MustCompile(`-Wl,-rpath[,=]\s*(?:'([^']*)'|"([^"]*)"|([^\s'"]+))`)
	// setRpathRe captures the argument of patchelf --set-rpath '<dirs>'.
	setRpathRe = regexp.MustCompile(`--set-rpath\s+'([^']*)'`)
	// cgoLDFLAGSRe captures the value of a CGO_LDFLAGS assignment in either
	// quoting style the Makefile uses (double quotes on the host, single
	// quotes inside the Docker shell script).
	cgoLDFLAGSRe = regexp.MustCompile(`CGO_LDFLAGS=("([^"]*)"|'([^']*)')`)
)

// tagsFlagOnLine returns the value of the -tags flag on a single line, if any.
func tagsFlagOnLine(line string) (string, bool) {
	m := tagsFlagRe.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	for _, g := range m[1:] {
		if g != "" {
			return g, true
		}
	}
	return "", false
}

// normalizeShellDollars undoes the Makefile/shell escaping of $ORIGIN so that
// '$$ORIGIN', '\$$ORIGIN' and '$ORIGIN' compare equal.
func normalizeShellDollars(s string) string {
	s = strings.ReplaceAll(s, `\\`, ``)
	s = strings.ReplaceAll(s, `\`, ``)
	s = strings.ReplaceAll(s, `$$`, `$`)
	return s
}

// logicalLines joins backslash-newline continuations, so a recipe that GNU
// Make echoes as one continued shell command is analysed as one unit. A
// CGO_LDFLAGS assignment and the `go build` that consumes it are separate
// physical lines of the same command.
func logicalLines(text string) []string {
	return strings.Split(strings.ReplaceAll(text, "\\\n", " "), "\n")
}

// cgoLDFLAGSValue returns the value of the first CGO_LDFLAGS assignment in
// text, with the surrounding quotes removed.
func cgoLDFLAGSValue(text string) string {
	m := cgoLDFLAGSRe.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	if m[2] != "" {
		return m[2]
	}
	return m[3]
}

// makeTargetRecipe returns the tab-indented recipe lines of target in mk.
func makeTargetRecipe(mk, target string) string {
	var b strings.Builder
	in := false
	for _, line := range strings.Split(mk, "\n") {
		if !in {
			if strings.HasPrefix(line, target+":") {
				in = true
			}
			continue
		}
		if !strings.HasPrefix(line, "\t") {
			break
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// ── Makefile probes ────────────────────────────────────────────────────────
//
// The probes run GNU make against a throwaway copy of the Makefile in a fresh
// git repository. That keeps a dry run from touching this worktree (which the
// v1 prepare-assets target did: it deleted the tracked internal/server/dist on
// `make -n full`) and makes the host-vs-Docker decision deterministic instead
// of depending on the toolchain of the machine running the tests.

type makeProbe struct {
	dir string
	bin string
	env []string
}

// probeEnvDrop lists the variables that would otherwise leak the outer
// environment into a probe (a caller's SKIP_WEB would hide Finding 6, a
// caller's PATH would decide the build mode, ...).
var probeEnvDrop = map[string]bool{
	"PATH": true, "HOME": true, "SKIP_WEB": true, "MAKEFLAGS": true,
	"MAKELEVEL": true, "MFLAGS": true, "MAKE": true, "MAKEOVERRIDES": true,
	"FULL_TAGS": true, "FULL_BUILD_MODE": true, "BUILDER_IMAGE": true,
	"BUILD_DIR": true, "FAISS_LIB_DIR": true, "FAISS_INC_DIR": true,
	"CGO_LDFLAGS": true, "CGO_CFLAGS": true, "GOFLAGS": true,
}

// probeTools are the host tools a recipe needs if a regression makes a dry
// run execute something. They are symlinked from the real system.
var probeTools = []string{
	"uname", "git", "sh", "rm", "cp", "mkdir", "sed", "grep", "find",
	"touch", "cat", "tar", "install", "dirname", "basename", "id", "chmod",
	"readlink", "ln", "mv", "printf",
}

// newProbe creates <tmp>/repo with the current Makefile and a tracked web
// asset, plus <tmp>/bin holding symlinks to host tools and stub executables
// for every toolchain the test wants to simulate (go, cargo, docker, ...).
// The probe PATH contains only that directory, so the build mode is chosen by
// the test rather than by whatever happens to be installed.
func newProbe(t *testing.T, stubs ...string) makeProbe {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("make probes are not portable to native Windows")
	}
	root := repoRoot(t)
	base := t.TempDir()
	dir := filepath.Join(base, "repo")
	binDir := filepath.Join(base, "bin")
	home := filepath.Join(base, "home")

	for _, d := range []string{dir, binDir, home, filepath.Join(dir, "internal", "server", "dist")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	writeFile := func(path, content string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	writeFile(filepath.Join(dir, "Makefile"), readFile(t, filepath.Join(root, "Makefile")), 0o644)
	writeFile(filepath.Join(dir, "internal", "server", "dist", "index.html"), "<html>tracked web asset</html>\n", 0o644)

	for _, tool := range probeTools {
		if p, err := exec.LookPath(tool); err == nil {
			if err := os.Symlink(p, filepath.Join(binDir, tool)); err != nil {
				t.Fatalf("symlink %s: %v", tool, err)
			}
		}
	}
	for _, tool := range stubs {
		writeFile(filepath.Join(binDir, tool), "#!/bin/sh\nexit 0\n", 0o755)
	}

	env := []string{
		"PATH=" + binDir,
		"HOME=" + home,
		"GIT_CONFIG_NOSYSTEM=1",
		"LC_ALL=C",
	}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if probeEnvDrop[name] {
			continue
		}
		env = append(env, kv)
	}

	gitBin := filepath.Join(binDir, "git")
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"-c", "user.name=doccheck", "-c", "user.email=doccheck@example.invalid",
			"commit", "-q", "-m", "probe"},
	} {
		cmd := exec.Command(gitBin, args...)
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	return makeProbe{dir: dir, bin: binDir, env: env}
}

// stub replaces a tool in the probe PATH with a shell script.
func (p makeProbe) stub(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(p.bin, name), []byte(body), 0o755); err != nil {
		t.Fatalf("write stub %s: %v", name, err)
	}
}

// withEnv returns a copy of the probe with extra environment variables.
func (p makeProbe) withEnv(kv ...string) makeProbe {
	out := makeProbe{dir: p.dir, bin: p.bin}
	out.env = append(append([]string{}, p.env...), kv...)
	return out
}

// make runs make in the probe repository and returns its combined output. A
// non-zero exit fails the test: every probe below expects a successful run.
func (p makeProbe) make(t *testing.T, args ...string) string {
	t.Helper()
	makeBin := commandPath(t, "make")
	cmd := exec.Command(makeBin, args...)
	cmd.Dir = p.dir
	cmd.Env = p.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// gitStatus returns `git status --porcelain` of the probe repository.
func (p makeProbe) gitStatus(t *testing.T) string {
	t.Helper()
	gitBin := commandPath(t, "git")
	cmd := exec.Command(gitBin, "status", "--porcelain")
	cmd.Dir = p.dir
	cmd.Env = p.env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	return string(out)
}

// commandPath returns the absolute path of tool, skipping the test when it is
// not installed.
func commandPath(t *testing.T, tool string) string {
	t.Helper()
	p, err := exec.LookPath(tool)
	if err != nil {
		t.Skipf("%s not installed", tool)
	}
	return p
}

// makePrereqs returns the prerequisites of build/gleann-full as GNU Make
// resolved them in the probe (`make -p` prints the database after parsing).
func makePrereqs(t *testing.T, p makeProbe) []string {
	t.Helper()
	return prereqsFromDatabase(t, p.make(t, "-p", "-n", "full"))
}

// prereqsFromDatabase extracts the build/gleann-full prerequisites from the
// database GNU Make prints for -p.
func prereqsFromDatabase(t *testing.T, database string) []string {
	t.Helper()
	for _, line := range strings.Split(database, "\n") {
		rest, ok := strings.CutPrefix(line, "build/gleann-full:")
		if !ok {
			continue
		}
		var deps []string
		for _, f := range strings.Fields(rest) {
			if strings.HasPrefix(f, "|") {
				continue
			}
			deps = append(deps, f)
		}
		return deps
	}
	t.Fatalf("make -p: no `build/gleann-full:` rule in the database")
	return nil
}

// makeVar expands a Makefile variable through make itself, exactly as a recipe
// would see it after Make's own `$$` -> `$` pass.
func makeVar(t *testing.T, dir, expr string) string {
	t.Helper()
	makeBin := commandPath(t, "make")
	const target = "doccheck-print-var"
	eval := target + ":\n\t@printf '%s\\n' \"" + expr + "\"\n"
	cmd := exec.Command(makeBin, "-s", "--no-print-directory", "--eval="+eval, target)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make --eval %q: %v\n%s", expr, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Full-build tag parity.

func TestFullTagSetIsSharedCanonical(t *testing.T) {
	root := repoRoot(t)
	mk := readFile(t, filepath.Join(root, "Makefile"))
	tf := readFile(t, filepath.Join(root, "Taskfile.yml"))

	mkTags := makeFullTagsRe.FindStringSubmatch(mk)
	if mkTags == nil {
		t.Fatalf("Makefile: no `FULL_TAGS ?= ...` variable; make full and task full must share one tag set")
	}
	tfTags := taskFullTagsRe.FindStringSubmatch(tf)
	if tfTags == nil {
		t.Fatalf("Taskfile.yml: no `FULL_TAGS:` variable; make full and task full must share one tag set")
	}
	if got := normalizeTagSet(mkTags[1]); !equalStrings(got, canonicalFullTags) {
		t.Errorf("Makefile FULL_TAGS = %q -> %v, want %v", strings.TrimSpace(mkTags[1]), got, canonicalFullTags)
	}
	if got := normalizeTagSet(tfTags[1]); !equalStrings(got, canonicalFullTags) {
		t.Errorf("Taskfile.yml FULL_TAGS = %q -> %v, want %v", strings.TrimSpace(tfTags[1]), got, canonicalFullTags)
	}

	// Every command that builds build/stage/gleann-full-bin -- the host and
	// Docker branches of the Makefile, the single Taskfile task -- must expand
	// the shared variable instead of listing tags itself.
	for _, f := range []struct {
		name string
		text string
		ref  string
		want int
	}{
		{"Makefile", mk, "$(FULL_TAGS)", 2},
		{"Taskfile.yml", tf, "{{.FULL_TAGS}}", 1},
	} {
		found := 0
		for i, line := range strings.Split(f.text, "\n") {
			if !strings.Contains(line, "go build") || !strings.Contains(line, "gleann-full-bin") {
				continue
			}
			found++
			tags, ok := tagsFlagOnLine(line)
			if !ok {
				t.Errorf("%s:%d: gleann-full-bin build has no -tags flag: %s", f.name, i+1, strings.TrimSpace(line))
				continue
			}
			if !strings.Contains(tags, f.ref) {
				t.Errorf("%s:%d: gleann-full-bin -tags %q must be the shared %s variable", f.name, i+1, tags, f.ref)
			}
			for _, tag := range canonicalFullTags {
				for _, got := range normalizeTagSet(tags) {
					if got == tag {
						t.Errorf("%s:%d: gleann-full-bin -tags %q hardcodes %q; use %s", f.name, i+1, tags, tag, f.ref)
					}
				}
			}
		}
		if found != f.want {
			t.Errorf("%s: found %d gleann-full-bin build commands, want %d (host and Docker branches)", f.name, found, f.want)
		}
	}
}

func TestFullBuildStagesNativeAndFAISSLibraries(t *testing.T) {
	root := repoRoot(t)
	mk := readFile(t, filepath.Join(root, "Makefile"))
	tf := readFile(t, filepath.Join(root, "Taskfile.yml"))

	// The Rust core path is spelled once, in NATIVE_CORE_LIB: both branches
	// stage the platform library and the host branch also consumes it.
	if !regexp.MustCompile(`(?m)^NATIVE_CORE_LIB\s*:=\s*ext/gleann-core-rs/target/release/libgleann_core_rs\.\$\(SO_EXT\)\s*$`).MatchString(mk) {
		t.Errorf("Makefile: NATIVE_CORE_LIB must point at ext/gleann-core-rs/target/release/libgleann_core_rs.$(SO_EXT)")
	}

	// The runtime libraries of the native and FAISS tags have to be staged
	// beside the executable: RUNPATH only searches $ORIGIN.
	for _, f := range []struct {
		name                   string
		text                   string
		wantCore, wantFaissLib int
		wantLink               int
	}{
		{"Makefile", mk, 2, 2, 2},
		{"Taskfile.yml", tf, 1, 1, 1},
	} {
		var stagedCore, stagedFaiss, linked int
		for _, line := range strings.Split(f.text, "\n") {
			if strings.Contains(line, "cp") && strings.Contains(line, "stage") {
				if strings.Contains(line, "libgleann_core_rs") || strings.Contains(line, "$(NATIVE_CORE_LIB)") {
					stagedCore++
				}
				if strings.Contains(line, "libfaiss") {
					stagedFaiss++
				}
			}
			if strings.Contains(line, "-lgleann_core_rs") {
				linked++
			}
		}
		if stagedCore < f.wantCore {
			t.Errorf("%s: %d branches stage libgleann_core_rs, want %d (host and Docker)", f.name, stagedCore, f.wantCore)
		}
		if stagedFaiss < f.wantFaissLib {
			t.Errorf("%s: %d branches stage libfaiss*, want %d (host and Docker)", f.name, stagedFaiss, f.wantFaissLib)
		}
		if linked < f.wantLink {
			t.Errorf("%s: %d CGO_LDFLAGS link -lgleann_core_rs, want %d (host and Docker)", f.name, linked, f.wantLink)
		}
	}
}

// RUNPATH hygiene.

// forbiddenRunpath lists the directories a self-contained build must never
// search first: a system or per-user library directory can hold an unrelated
// FAISS and hide a library that failed to be bundled.
func forbiddenRunpath(value string) []string {
	var bad []string
	for _, want := range []string{"/usr/local/lib", "USER_LIB_DIR", "$HOME", ".local/lib"} {
		if strings.Contains(value, want) {
			bad = append(bad, want)
		}
	}
	return bad
}

func TestRunpathNeverContainsSystemLibDirs(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{"Makefile", "Taskfile.yml"} {
		text := readFile(t, filepath.Join(root, name))
		norm := normalizeShellDollars(text)

		var rpaths []string
		for i, line := range strings.Split(norm, "\n") {
			for _, m := range rpathValueRe.FindAllStringSubmatch(line, -1) {
				value := ""
				for _, g := range m[1:] {
					if g != "" {
						value = g
						break
					}
				}
				rpaths = append(rpaths, value)
				if bad := forbiddenRunpath(value); len(bad) > 0 {
					t.Errorf("%s:%d: -Wl,-rpath,%q embeds %s; a bundled build must resolve libraries via $ORIGIN", name, i+1, value, strings.Join(bad, ", "))
				}
			}
			for _, m := range setRpathRe.FindAllStringSubmatch(line, -1) {
				rpaths = append(rpaths, m[1])
				if bad := forbiddenRunpath(m[1]); len(bad) > 0 {
					t.Errorf("%s:%d: patchelf --set-rpath '%s' embeds %s", name, i+1, m[1], strings.Join(bad, ", "))
				}
				if !strings.Contains(m[1], "$ORIGIN") || !strings.Contains(m[1], "$ORIGIN/../lib") {
					t.Errorf("%s:%d: patchelf --set-rpath '%s' must contain $ORIGIN and $ORIGIN/../lib", name, i+1, m[1])
				}
			}
		}

		if len(rpaths) < 2 {
			t.Fatalf("%s: found %d rpath arguments, want at least 2 (Linux and Darwin)", name, len(rpaths))
		}
		joined := strings.Join(rpaths, "\n")
		if !strings.Contains(joined, "$ORIGIN/../lib") {
			t.Errorf("%s: no rpath entry contains $ORIGIN/../lib", name)
		}
		if !strings.Contains(joined, "@loader_path/../lib") {
			t.Errorf("%s: no rpath entry contains @loader_path/../lib (Darwin sibling: install layout puts libraries in ../lib)", name)
		}
	}
}

// TestTaskDryRunFullCommands expands `task full` -- the entry point the
// release workflow uses -- and checks the tag set and the rpath escaping.
// Task runs commands with mvdan/sh, where "$$" is the process id: "$ORIGIN"
// inside the double-quoted CGO_LDFLAGS assignment needs exactly one backslash.
func TestTaskDryRunFullCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("task dry run is not portable to native Windows")
	}
	taskBin, err := exec.LookPath("task")
	if err != nil {
		t.Skip("task not installed")
	}
	cmd := exec.Command(taskBin, "--dry", "full")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("task --dry full: %v\n%s", err, out)
	}

	var builds, rpaths int
	for i, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "go build") && strings.Contains(line, "gleann-full-bin") {
			builds++
			tags, ok := tagsFlagOnLine(line)
			if !ok {
				t.Fatalf("task dry run line %d: no -tags: %s", i+1, line)
			}
			if got := normalizeTagSet(tags); !equalStrings(got, canonicalFullTags) {
				t.Errorf("task dry run line %d: -tags %q -> %v, want %v", i+1, tags, got, canonicalFullTags)
			}
		}
		if strings.Contains(line, "-Wl,-rpath") {
			rpaths++
			if strings.Contains(line, "$$ORIGIN") {
				t.Errorf("task dry run line %d: %q uses $$ORIGIN; mvdan/sh expands $$ to the PID, use \\$ORIGIN", i+1, strings.TrimSpace(line))
			}
			if !strings.Contains(line, `\$ORIGIN/../lib`) && !strings.Contains(line, "@loader_path/../lib") {
				t.Errorf("task dry run line %d: rpath must contain \\$ORIGIN/../lib (Linux) or @loader_path/../lib (Darwin): %s", i+1, line)
			}
		}
	}
	if builds != 1 {
		t.Errorf("task dry run: %d gleann-full-bin go build commands, want 1", builds)
	}
	if rpaths == 0 {
		t.Error("task dry run: no -Wl,-rpath line found in the full recipe")
	}
}

// TestMakeDryRunFullCommands expands `make full` for both build modes and
// checks the real command lines, including the Docker branch that static
// parsing only approximates. Only the selected branch may be emitted: the
// other one is a parse-time conditional, not an unreachable `elif`.
func TestMakeDryRunFullCommands(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stubs []string
	}{
		{"host", []string{"go", "cargo"}},
		{"docker", []string{"docker"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// SKIP_WEB keeps this test about the link commands: the missing
			// ui/dist path is covered by TestMakeDryRunFullDoesNotTouchTheWorktree.
			p := newProbe(t, tc.stubs...).withEnv("SKIP_WEB=1")
			out := p.make(t, "-n", "full")

			// The link flags a bundled build must carry: $ORIGIN and its ../lib
			// sibling, set at link time. The Docker branch always produces a
			// Linux ELF, so it must use the Linux spelling even on a macOS host.
			wantRpath := "$ORIGIN/../lib"
			if tc.name == "host" && runtime.GOOS == "darwin" {
				wantRpath = "@loader_path/../lib"
			}

			var builds int
			for i, line := range logicalLines(out) {
				if !strings.Contains(line, "go build") || !strings.Contains(line, "gleann-full-bin") {
					continue
				}
				builds++
				tags, ok := tagsFlagOnLine(line)
				if !ok {
					t.Fatalf("%s dry run line %d: no -tags: %s", tc.name, i+1, line)
				}
				if got := normalizeTagSet(tags); !equalStrings(got, canonicalFullTags) {
					t.Errorf("%s dry run line %d: -tags %q -> %v, want %v", tc.name, i+1, tags, got, canonicalFullTags)
				}
				if strings.Contains(line, "$$ORIGIN") {
					t.Errorf("%s dry run line %d: $$ORIGIN becomes the PID in sh, use \\$ORIGIN", tc.name, i+1)
				}
				ld := cgoLDFLAGSValue(line)
				if ld == "" {
					t.Errorf("%s dry run line %d: no CGO_LDFLAGS assignment: %s", tc.name, i+1, line)
					continue
				}
				if n := strings.Count(ld, "-Wl,-rpath"); n != 2 {
					t.Errorf("%s dry run: CGO_LDFLAGS has %d -Wl,-rpath entries, want 2 ($ORIGIN and ../lib): %s", tc.name, n, ld)
				}
				if !strings.Contains(ld, wantRpath) {
					t.Errorf("%s dry run: CGO_LDFLAGS lacks %s: %s", tc.name, wantRpath, ld)
				}
				if bad := forbiddenRunpath(ld); len(bad) > 0 {
					t.Errorf("%s dry run: CGO_LDFLAGS embeds %s", tc.name, strings.Join(bad, ", "))
				}
			}
			if builds != 1 {
				t.Errorf("%s dry run: %d gleann-full-bin go build commands, want 1 (only the active branch)", tc.name, builds)
			}

			// The post-link RUNPATH rewrite must mirror the mode: patchelf on
			// Linux, install_name_tool on Darwin. Both branches are in the
			// Makefile, but only the active one is emitted. The Docker branch
			// prints the $ORIGIN escaped for its nested sh -c.
			normalized := normalizeShellDollars(out)
			if !strings.Contains(normalized, "patchelf --set-rpath '$ORIGIN:$ORIGIN/../lib'") {
				t.Errorf("%s dry run: no patchelf --set-rpath '$ORIGIN:$ORIGIN/../lib'", tc.name)
			}
			if tc.name == "host" && !strings.Contains(out, "install_name_tool -add_rpath @loader_path/../lib") {
				t.Errorf("host dry run: no install_name_tool @loader_path/../lib rewrite for Darwin")
			}
		})
	}
}

// TestMakeFullPrerequisitesFollowTheToolchain guards the BLOCKER: a host-only
// prerequisite (build-rust-core needs cargo) must not run before the Docker
// fallback can be considered, or the container path is dead code on every
// machine without a Rust toolchain.
func TestMakeFullPrerequisitesFollowTheToolchain(t *testing.T) {
	t.Run("host toolchain", func(t *testing.T) {
		p := newProbe(t, "go", "cargo").withEnv("SKIP_WEB=1")
		got := makePrereqs(t, p)
		for _, want := range []string{"build-rust-core", "build-eif-lib", "prepare-assets"} {
			if !containsString(got, want) {
				t.Errorf("build/gleann-full prerequisites = %v, want %q for a host build", got, want)
			}
		}
	})
	t.Run("docker only", func(t *testing.T) {
		p := newProbe(t, "docker").withEnv("SKIP_WEB=1")
		got := makePrereqs(t, p)
		for _, unwanted := range []string{"build-rust-core", "build-eif-lib"} {
			if containsString(got, unwanted) {
				t.Errorf("build/gleann-full prerequisites = %v: %q runs on the host first and aborts the Docker fallback", got, unwanted)
			}
		}
		for _, want := range []string{"prepare-assets", "builder-image"} {
			if !containsString(got, want) {
				t.Errorf("build/gleann-full prerequisites = %v, want %q", got, want)
			}
		}
	})
	// Overriding the mode must switch the prerequisites too, or a forced
	// Docker build would still run the host-only Rust prerequisite first.
	t.Run("forced docker on a host toolchain", func(t *testing.T) {
		p := newProbe(t, "go", "cargo").withEnv("SKIP_WEB=1")
		out := p.make(t, "-p", "-n", "full", "FULL_BUILD_MODE=docker")
		got := prereqsFromDatabase(t, out)
		if containsString(got, "build-rust-core") || containsString(got, "build-eif-lib") {
			t.Errorf("FULL_BUILD_MODE=docker prerequisites = %v: host-only targets would run first", got)
		}
		if !containsString(got, "builder-image") {
			t.Errorf("FULL_BUILD_MODE=docker prerequisites = %v, want builder-image", got)
		}
	})
}

// TestMakeFullFallsBackToDockerWithoutHostToolchain runs the dry run with Go
// and Cargo absent but Docker present: the container recipe must be the one
// selected, with no host cargo invocation anywhere.
func TestMakeFullFallsBackToDockerWithoutHostToolchain(t *testing.T) {
	mk := readFile(t, filepath.Join(repoRoot(t), "Makefile"))
	image := regexp.MustCompile(`(?m)^BUILDER_IMAGE\s*\?=\s*(\S+)`).FindStringSubmatch(mk)
	if image == nil {
		t.Fatal("Makefile: no BUILDER_IMAGE variable")
	}

	p := newProbe(t, "docker").withEnv("SKIP_WEB=1")
	out := p.make(t, "-n", "full")

	for _, hostOnly := range []string{
		"cd ext/gleann-core-rs && cargo build --release",
		"cargo not found and libgleann_core_rs",
	} {
		if strings.Contains(out, hostOnly) {
			t.Errorf("make -n full started the host Rust build (%q) before considering Docker", hostOnly)
		}
	}
	for _, want := range []string{
		"docker run",
		image[1],
		"cargo build --release --manifest-path ext/gleann-core-rs/Cargo.toml",
		"gleann-full-bin",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Docker fallback never reaches %q; output:\n%s", want, out)
		}
	}
}

// TestMakeFullDockerBuildsEifStaticLibrary guards the MAJOR: the Docker branch
// now compiles with the eif tag, whose cgo directives link
// ext/bitllm.c/build/libbitllm.a. The builder image installs cmake, so the
// container must build bitllm_static itself instead of relying on a host
// archive (which is missing, or Mach-O on a macOS host).
func TestMakeFullDockerBuildsEifStaticLibrary(t *testing.T) {
	p := newProbe(t, "docker").withEnv("SKIP_WEB=1")
	out := p.make(t, "-n", "full")
	dockerAt := strings.Index(out, "docker run")
	if dockerAt < 0 {
		t.Fatalf("no docker branch in `make -n full`; output:\n%s", out)
	}
	docker := out[dockerAt:]

	configureAt := strings.Index(docker, "cmake -B ext/bitllm.c/build -S ext/bitllm.c")
	if configureAt < 0 {
		t.Fatalf("Docker build never configures ext/bitllm.c; the eif tag links ext/bitllm.c/build/libbitllm.a:\n%s", docker)
	}
	if !strings.Contains(docker, "--target bitllm_static") {
		t.Errorf("Docker build never builds the bitllm_static target; output:\n%s", docker)
	}
	if !strings.Contains(docker, "git submodule update --init") {
		t.Errorf("Docker build does not initialize the ext/bitllm.c submodule; output:\n%s", docker)
	}
	buildAt := strings.Index(docker, "gleann-full-bin")
	if buildAt < 0 || buildAt < configureAt {
		t.Errorf("go build of gleann-full-bin must run after libbitllm.a is built")
	}
	if !strings.Contains(docker, "/app/ext/bitllm.c/build") {
		t.Errorf("chown -R must include /app/ext/bitllm.c/build so the archive is owned by the caller; output:\n%s", docker)
	}
}

// TestMakeFullDockerShellEscaping runs the Docker recipe for real with a stub
// docker that prints the script it was asked to run. That script is exactly
// what the container shell receives, so it proves the make -> sh -c "..."
// quoting: CGO_LDFLAGS must arrive as literal $ORIGIN, not $PID ("$$ORIGIN"),
// not an empty string and not with the backslashes kept.
func TestMakeFullDockerShellEscaping(t *testing.T) {
	p := newProbe(t, "docker")
	p.stub(t, "docker", "#!/bin/sh\nfor a in \"$@\"; do last=\"$a\"; done\nprintf '%s\\n' \"$last\"\n")

	faissDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(faissDir, "libfaiss.a"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out := p.withEnv("SKIP_WEB=1").make(t, "full",
		"FULL_BUILD_MODE=docker", "FAISS_LIB_DIR="+faissDir)

	ld := cgoLDFLAGSValue(out)
	if ld == "" {
		t.Fatalf("the container script has no CGO_LDFLAGS; output:\n%s", out)
	}
	if n := strings.Count(ld, "-Wl,-rpath"); n != 2 {
		t.Errorf("container CGO_LDFLAGS has %d -Wl,-rpath entries, want 2: %s", n, ld)
	}
	if !strings.HasPrefix(ld, "-Wl,-rpath,$ORIGIN -Wl,-rpath,$ORIGIN/../lib") {
		t.Errorf("container CGO_LDFLAGS does not start with the literal $ORIGIN rpaths: %s", ld)
	}
	if strings.Contains(ld, `\`) {
		t.Errorf("container CGO_LDFLAGS still contains a backslash: %s", ld)
	}
	if bad := forbiddenRunpath(ld); len(bad) > 0 {
		t.Errorf("container CGO_LDFLAGS embeds %s", strings.Join(bad, ", "))
	}
}

// check libfaiss.$(SO_EXT), link the platform OpenMP runtime, and rewrite
// Mach-O run paths on Darwin like Taskfile.yml does.
func TestMakeFullHostBranchIsDarwinAware(t *testing.T) {
	mk := readFile(t, filepath.Join(repoRoot(t), "Makefile"))

	var faissCheck string
	for _, line := range strings.Split(mk, "\n") {
		if strings.Contains(line, "[ ! -f") && strings.Contains(line, "libfaiss.a") {
			faissCheck = line
		}
	}
	if faissCheck == "" {
		t.Fatal("Makefile: no FAISS existence check found")
	}
	if !strings.Contains(faissCheck, "libfaiss.$(SO_EXT)") {
		t.Errorf("Makefile: FAISS check accepts only .a/.so; Darwin builds libfaiss.dylib:\n\t%s", strings.TrimSpace(faissCheck))
	}
	if !strings.Contains(faissCheck, "$(SO_EXT)") || !regexp.MustCompile(`(?m)^\s*SO_EXT\s*:=\s*dylib\s*$`).MatchString(mk) {
		t.Errorf("Makefile: SO_EXT must be dylib on Darwin")
	}

	var cgoValues []string
	for _, m := range cgoLDFLAGSRe.FindAllStringSubmatch(mk, -1) {
		if m[2] != "" {
			cgoValues = append(cgoValues, m[2])
		} else {
			cgoValues = append(cgoValues, m[3])
		}
	}
	if len(cgoValues) != 2 {
		t.Fatalf("Makefile: found %d CGO_LDFLAGS assignments, want 2 (host and Docker)", len(cgoValues))
	}
	var host, docker string
	for _, v := range cgoValues {
		if strings.Contains(v, "$(LINUX_RPATH_FLAGS)") {
			docker = v
		} else {
			host = v
		}
	}
	if host == "" || docker == "" {
		t.Fatalf("Makefile: could not tell the host and Docker CGO_LDFLAGS apart: %q", cgoValues)
	}
	if !strings.Contains(host, "$(OMP_LDFLAGS)") {
		t.Errorf("host CGO_LDFLAGS must link $(OMP_LDFLAGS) (libomp on Darwin): %s", host)
	}
	if !strings.Contains(host, "$(ZNOW_FLAG)") {
		t.Errorf("host CGO_LDFLAGS must pass $(ZNOW_FLAG): %s", host)
	}
	if strings.Contains(host, "-lgomp") {
		t.Errorf("host CGO_LDFLAGS hardcodes -lgomp; use $(OMP_LDFLAGS): %s", host)
	}
	if !strings.Contains(docker, "$(LINUX_RPATH_FLAGS)") || !strings.Contains(docker, "$(LINUX_ZNOW_FLAG)") {
		t.Errorf("docker CGO_LDFLAGS must pass $(LINUX_RPATH_FLAGS)/$(LINUX_ZNOW_FLAG) even on a Darwin host: %s", docker)
	}
	if !regexp.MustCompile(`(?m)^\s*OMP_LDFLAGS\s*:=\s*-lgomp\s*$`).MatchString(mk) {
		t.Errorf("Makefile: the Linux branch must define OMP_LDFLAGS := -lgomp")
	}
	if !strings.Contains(mk, "-lomp") {
		t.Errorf("Makefile: the Darwin branch must link libomp (-lomp)")
	}

	for _, want := range []string{
		"install_name_tool -add_rpath @executable_path",
		"install_name_tool -add_rpath @loader_path ",
		"install_name_tool -add_rpath @loader_path/../lib",
	} {
		if !strings.Contains(mk, want) {
			t.Errorf("Makefile: missing Darwin run-path rewrite %q", want)
		}
	}
	linuxGuard := strings.Index(mk, `if [ "$(UNAME_S)" != "Darwin" ]`)
	darwinGuard := strings.Index(mk, `if [ "$(UNAME_S)" = "Darwin" ]`)
	switch {
	case linuxGuard < 0:
		t.Errorf("Makefile: no `if [ \"$(UNAME_S)\" != \"Darwin\" ]` guard for the Linux .so bundling")
	case !strings.Contains(mk[linuxGuard:], "patchelf --set-rpath"):
		t.Errorf("Makefile: patchelf does not run inside the non-Darwin guard")
	case !strings.Contains(mk[linuxGuard:], "libopenblas.so.0"):
		t.Errorf("Makefile: the Linux-only .so copies must sit inside the non-Darwin guard")
	case darwinGuard < 0:
		t.Errorf("Makefile: no `if [ \"$(UNAME_S)\" = \"Darwin\" ]` guard for install_name_tool")
	case !strings.Contains(mk[darwinGuard:], "install_name_tool -add_rpath"):
		t.Errorf("Makefile: install_name_tool does not run inside the Darwin guard")
	}
}

// TestMakeDryRunFullDoesNotTouchTheWorktree guards the MINOR: GNU Make runs a
// recipe line that contains $(MAKE) even under -n, and the v1 prepare-assets
// shared that line with `rm -rf internal/server/dist`, so `make -n full`
// deleted tracked files and failed on the missing ui/dist.
func TestMakeDryRunFullDoesNotTouchTheWorktree(t *testing.T) {
	p := newProbe(t, "go", "cargo")
	before := p.gitStatus(t)
	if before != "" {
		t.Fatalf("probe repository is not clean before the dry run:\n%s", before)
	}

	out := p.make(t, "-n", "full")

	if after := p.gitStatus(t); after != "" {
		t.Errorf("`make -n full` mutated the worktree:\n%s\noutput:\n%s", after, out)
	}
	if _, err := os.Stat(filepath.Join(p.dir, "internal", "server", "dist", "index.html")); err != nil {
		t.Errorf("`make -n full` removed the tracked web assets: %v", err)
	}
	if !strings.Contains(out, "Building Web UI") {
		t.Errorf("dry run did not report the missing ui/dist (build-web); output:\n%s", out)
	}

	// The static half of the guard: the asset copy must not share a recipe
	// line with recursive make.
	mk := readFile(t, filepath.Join(repoRoot(t), "Makefile"))
	prep := makeTargetRecipe(mk, "prepare-assets")
	if prep == "" {
		t.Fatal("Makefile: no prepare-assets recipe")
	}
	if strings.Contains(prep, "$(MAKE)") {
		t.Errorf("prepare-assets invokes $(MAKE); GNU Make executes that line even under -n, so `make -n full` deletes internal/server/dist\n%s", prep)
	}
}

// TestFullBuildRunpathReachesTheLinkerAsOrigin feeds the Makefile's RUNPATH
// expression to a real cgo link and inspects the result with readelf. A
// variable that "looks right" in the file can still reach the linker as a PID
// expansion or with the quotes baked into RUNPATH; only the ELF header proves
// what a bundled binary will search.
func TestFullBuildRunpathReachesTheLinkerAsOrigin(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the RUNPATH inspection needs an ELF binary and readelf")
	}
	readelfBin := commandPath(t, "readelf")
	goBin := commandPath(t, "go")
	root := repoRoot(t)
	const want = "-Wl,-rpath,$ORIGIN -Wl,-rpath,$ORIGIN/../lib"

	// LINUX_RPATH_FLAGS is what the Docker branch passes, on every host.
	if got := makeVar(t, root, "$(LINUX_RPATH_FLAGS)"); got != want {
		t.Fatalf("LINUX_RPATH_FLAGS expands to %q, want %q", got, want)
	}
	// RPATH_FLAGS is the host equivalent on Linux.
	if got := makeVar(t, root, "$(RPATH_FLAGS)"); got != want {
		t.Fatalf("RPATH_FLAGS expands to %q, want %q", got, want)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

/*
int answer(void) { return 42; }
*/
import "C"

import "fmt"

func main() { fmt.Println(int(C.answer())) }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	prog := filepath.Join(dir, "prog")
	build := exec.Command(goBin, "build", "-o", prog, "main.go")
	build.Dir = dir
	build.Env = append(os.Environ(), "CGO_ENABLED=1", "CGO_LDFLAGS="+want)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("cgo link with CGO_LDFLAGS=%q: %v\n%s", want, err, out)
	}

	cmd := exec.Command(readelfBin, "-d", prog)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("readelf -d %s: %v", prog, err)
	}
	dynamic := string(out)
	if !strings.Contains(dynamic, "$ORIGIN:$ORIGIN/../lib") {
		t.Errorf("RUNPATH of a binary linked with %q does not contain $ORIGIN:$ORIGIN/../lib:\n%s", want, dynamic)
	}
	if bad := forbiddenRunpath(dynamic); len(bad) > 0 {
		t.Errorf("RUNPATH of the probe binary embeds %s", strings.Join(bad, ", "))
	}
}
