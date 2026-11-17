package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// srcDir is the package directory; tests change into a temp directory.
var srcDir, _ = os.Getwd()

const bogusFlag = "--definitely-not-a-flag"

type exitCalled struct{ code int }

// runExit runs fn with exitFunc replaced and returns the status passed to it,
// or -1 when fn returned without exiting. Standard output and error are
// swallowed. The test runs in an empty directory so that a command that is
// not rejected cannot touch the repository.
func runExit(t *testing.T, fn func()) (code int) {
	t.Helper()
	t.Chdir(t.TempDir())
	origExit, origOut, origErr := exitFunc, os.Stdout, os.Stderr
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = devnull, devnull
	exitFunc = func(c int) { panic(exitCalled{c}) }
	defer func() {
		exitFunc, os.Stdout, os.Stderr = origExit, origOut, origErr
		devnull.Close()
		if r := recover(); r != nil {
			ec, ok := r.(exitCalled)
			if !ok {
				panic(r)
			}
			code = ec.code
		}
	}()
	fn()
	return -1
}

// captureHelp returns what fn writes to stdout and stderr; fn may exit
// through exitFunc.
func captureHelp(t *testing.T, fn func()) string {
	t.Helper()
	origExit, origOut, origErr := exitFunc, os.Stdout, os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = w, w
	exitFunc = func(c int) { panic(exitCalled{c}) }
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				if _, ok := rec.(exitCalled); !ok {
					panic(rec)
				}
			}
		}()
		fn()
	}()
	w.Close()
	out := <-done
	exitFunc, os.Stdout, os.Stderr = origExit, origOut, origErr
	return out
}

// mainWith runs main() as `gleann <args...>`.
func mainWith(args ...string) func() {
	return func() {
		orig := os.Args
		defer func() { os.Args = orig }()
		os.Args = append([]string{"gleann"}, args...)
		main()
	}
}

// caseStrings returns the string literals of the case clauses of function fn
// in file.
func caseStrings(t *testing.T, file, fn string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(srcDir, file), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != fn {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			cc, ok := n.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, e := range cc.List {
				if bl, ok := e.(*ast.BasicLit); ok && bl.Kind == token.STRING {
					s, _ := strconv.Unquote(bl.Value)
					out = append(out, s)
				}
			}
			return true
		})
	}
	if len(out) == 0 {
		t.Fatalf("no case clauses found in %s of %s", fn, file)
	}
	return out
}

// Commands of the main dispatch that take their flags in a subcommand table
// (indexFlagSets and the like) and are exercised by TestSubcommandsRejectUnknownFlags.
var subcommandTables = map[string]map[string]flagSet{
	"index":   indexFlagSets,
	"memory":  memoryFlagSets,
	"service": serviceFlagSets,
	"token":   tokenFlagSets,
	"plugin":  pluginFlagSets,
	"plugins": pluginFlagSets,
	"config":  configSubFlagSets,
}

// Commands of the main dispatch that are not checked for unknown flags, with
// the reason.
var exemptCommands = map[string]string{
	"help":       "prints the usage, takes no arguments",
	"--help":     "prints the usage, takes no arguments",
	"-h":         "prints the usage, takes no arguments",
	"quickstart": "deprecated, exits with a message before reading anything",
	"go":         "deprecated, exits with a message before reading anything",
	"graph":      "checked per subcommand in the treesitter build (graph_flags_test.go)",
	"multimodal": "checked through its analyze subcommand",
}

// Commands of the main dispatch checked with a single bogus flag.
var flatCommands = [][]string{
	{"search", "idx", "q"}, {"ask", "idx", "q"}, {"def", "Sym"}, {"chat"},
	{"serve"}, {"tasks"}, {"benchmark"}, {"bench"}, {"tokens", "."},
	{"models"}, {"completion", "bash"}, {"doctor"}, {"tui"}, {"version"},
	{"agents"}, {"install"}, {"uninstall"}, {"setup"}, {"update"},
	{"upgrade"}, {"self-update"}, {"mcp"}, {"mcp", "install"},
	{"multimodal", "analyze", "x.png"},
}

func TestMainDispatchRejectsUnknownFlags(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range flatCommands {
		covered[c[0]] = true
		args := append(append([]string{}, c...), bogusFlag)
		if got := runExit(t, mainWith(args...)); got != 2 {
			t.Errorf("gleann %s: exit status %d, want 2", strings.Join(args, " "), got)
		}
	}
	for _, name := range caseStrings(t, "main.go", "main") {
		if _, ok := exemptCommands[name]; ok {
			continue
		}
		if _, ok := subcommandTables[name]; ok {
			continue
		}
		if !covered[name] {
			t.Errorf("command %q is in the main dispatch but is neither checked nor listed in exemptCommands", name)
		}
	}
}

func TestSubcommandsRejectUnknownFlags(t *testing.T) {
	for cmd, table := range subcommandTables {
		for sub := range table {
			if got := runExit(t, mainWith(cmd, sub, bogusFlag)); got != 2 {
				t.Errorf("gleann %s %s %s: exit status %d, want 2", cmd, sub, bogusFlag, got)
			}
		}
	}
	// Every case of the dispatchers has a table entry, or is listed here.
	noFlags := map[string]bool{"help": true, "--help": true, "-h": true, "models": true}
	for _, d := range []struct{ file, fn, cmd string }{
		{"cmd_index.go", "cmdIndex", "index"},
		{"cmd_memory.go", "cmdMemory", "memory"},
		{"cmd_service.go", "cmdService", "service"},
		{"cmd_token.go", "cmdToken", "token"},
		{"cmd_plugin.go", "cmdPlugin", "plugin"},
		{"cmd_config.go", "cmdConfig", "config"},
	} {
		for _, sub := range caseStrings(t, d.file, d.fn) {
			if _, ok := subcommandTables[d.cmd][sub]; !ok && !noFlags[sub] {
				t.Errorf("gleann %s %s has no flag table", d.cmd, sub)
			}
		}
	}
	// plugin models forwards its flags to the plugin binary: nothing to reject.
	if _, ok := pluginFlagSets["models"]; ok {
		t.Error("plugin models forwards its arguments to the plugin and must stay unchecked")
	}
}

func TestFreeTextAndDoubleDash(t *testing.T) {
	if got := freeText([]string{"a", "--json", "b", "--tier", "long", "c"}, flagSet{"--json": false, "--tier": true}); got != "a b c" {
		t.Errorf("freeText = %q", got)
	}
	if got := freeText([]string{"a", "--", "--json", "b"}, flagSet{"--json": false}); got != "a --json b" {
		t.Errorf("freeText after -- = %q", got)
	}
	if got := stripDoubleDash([]string{"k", "--", "--v"}); strings.Join(got, " ") != "k --v" {
		t.Errorf("stripDoubleDash = %v", got)
	}
	// Free text with spaces is not a flag.
	if err := checkFlags([]string{"--force is dangerous"}, flagSet{}); err != nil {
		t.Errorf("text with spaces rejected: %v", err)
	}
	// --help is accepted everywhere.
	if err := checkFlags([]string{"--help"}, flagSet{}); err != nil {
		t.Errorf("--help rejected: %v", err)
	}
}

var helpFlagRE = regexp.MustCompile(`--[a-z][a-z0-9-]*`)

// helpFlags returns the sorted long flags mentioned in help.
func helpFlags(help string) []string {
	seen := map[string]bool{}
	for _, m := range helpFlagRE.FindAllString(help, -1) {
		seen[strings.TrimRight(m, "-")] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func unionAll(tables map[string]flagSet) flagSet {
	out := flagSet{}
	for _, t := range tables {
		out = out.union(t)
	}
	return out
}

// Every flag a command's help text mentions must be accepted by its flag
// table, so the help and the parser cannot drift apart.
func TestHelpFlagsAreAccepted(t *testing.T) {
	// Flags of other programs that help texts mention (git, llama-server).
	foreign := map[string]bool{"--show-toplevel": true, "--reranking": true}
	run := func(fn func(args []string), args ...string) func() {
		return func() { fn(args) }
	}
	cases := []struct {
		name  string
		known flagSet
		help  func()
	}{
		{"search", searchFlags, run(cmdSearch, "--help")},
		{"ask", askFlags, printCmdAskUsage},
		{"def", defFlags, printDefUsage},
		{"serve", serveFlags, run(cmdServe, "--help")},
		{"tasks", tasksFlags, printTasksUsage},
		{"benchmark", benchFlags, printBenchmarkUsage},
		{"tokens", flagSet{}, printTokensUsage},
		{"agents", agentsFlags, printAgentsUsage},
		{"install", installFlags, printInstallUsage},
		{"uninstall", uninstallFlags, printUninstallUsage},
		{"update", updateFlags, run(cmdUpdate, "--help")},
		{"mcp", mcpFlags.union(mcpInstallFlags), printMCPUsage},
		{"multimodal", configFlags, run(cmdMultimodal, "--help")},
		{"index", unionAll(indexFlagSets), printIndexUsage},
		{"index build", buildFlags, printBuildUsage},
		{"index rebuild", buildFlags, run(cmdRebuild, "--help")},
		{"index sync", indexFlagSets["sync"], run(cmdSync, "--help")},
		{"index watch", indexFlagSets["watch"], run(cmdWatch, "--help")},
		{"index list", indexFlagSets["list"], run(cmdList, "--help")},
		{"index info", indexFlagSets["info"], run(cmdInfo, "--help")},
		{"index remove", indexFlagSets["remove"], run(cmdRemove, "--help")},
		{"index tag", indexFlagSets["tag"], run(cmdTag, "--help")},
		{"index set", indexFlagSets["set"], run(cmdSet, "--help")},
		{"index storage", indexFlagSets["storage"], run(cmdStorage, "--help")},
		{"index repair", indexFlagSets["repair"], printRepairUsage},
		{"index migrate", indexFlagSets["migrate"], printMigrateUsage},
		// The memory help also shows the conversation flags of gleann chat.
		{"memory", unionAll(memoryFlagSets).union(chatFlags), printMemoryUsage},
		{"service", unionAll(serviceFlagSets), printServiceUsage},
		{"token", unionAll(tokenFlagSets), run(cmdToken, "--help")},
		{"plugin", pluginFlagSets["update"], pluginUsage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := captureHelp(t, tc.help)
			flags := helpFlags(out)
			if len(flags) == 0 && tc.name != "tokens" {
				t.Fatalf("no flags found in the help text %q", out)
			}
			for _, f := range flags {
				if foreign[f] || f == "--help" {
					continue
				}
				if _, ok := tc.known[f]; !ok {
					t.Errorf("help mentions %s but the flag table of %q does not accept it", f, tc.name)
				}
			}
		})
	}
}
