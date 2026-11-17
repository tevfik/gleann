package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
)

// mockChatModel is a ChatModel wired to a LeannChat that answers with the mock
// provider (it echoes the prompt), so slash commands that talk to the LLM run
// without a network.
func mockChatModel(t *testing.T, results ...gleann.SearchResult) ChatModel {
	t.Helper()
	m := newTestChatModel(t)
	m.chat = gleann.NewChat(fakeSearcher{results}, gleann.ChatConfig{Provider: gleann.LLMMock, Model: "mock"})
	return m
}

// ollamaChatServer answers /api/chat with content, both streamed and not.
func ollamaChatServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		body := map[string]any{"message": map[string]string{"role": "assistant", "content": content}, "done": true}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func lastSystemMessage(t *testing.T, m ChatModel) string {
	t.Helper()
	last := m.messages[len(m.messages)-1]
	if last.role != "system" {
		t.Fatalf("last message role = %q, want system", last.role)
	}
	return last.content
}

// ── /script ────────────────────────────────────────────────────

func TestHandleScriptCommand_SavesExtractedScript(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("TMP", os.Getenv("TMPDIR"))
	t.Setenv("TEMP", os.Getenv("TMPDIR"))
	srv := ollamaChatServer(t, "Here you go:\n```bash\n#!/bin/bash\nset -euo pipefail\necho hello\n```\nbye")
	m := newTestChatModel(t)
	m.chat = gleann.NewChat(fakeSearcher{}, gleann.ChatConfig{Provider: gleann.LLMOllama, Model: "x", BaseURL: srv.URL})

	out := m.handleScriptCommand("say hello")
	if !strings.Contains(out, "Generated script") || !strings.Contains(out, "echo hello") {
		t.Fatalf("unexpected output: %s", out)
	}
	path := filepath.Join(os.TempDir(), "gleann-script-"+strconv.Itoa(os.Getpid())+".sh")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("script not saved: %v", err)
	}
	if !strings.HasPrefix(string(data), "#!/bin/bash") || strings.Contains(string(data), "```") {
		t.Fatalf("saved script must be the bare fenced content, got %q", data)
	}
}

func TestHandleScriptCommand_NoScriptInAnswer(t *testing.T) {
	m := mockChatModel(t) // the mock echoes the prompt, which holds no fenced script
	out := m.handleScriptCommand("count lines")
	if !strings.Contains(out, "Could not extract") {
		t.Fatalf("got %q", out)
	}
}

func TestHandleScriptCommand_LLMFailureIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	m := newTestChatModel(t)
	m.chat = gleann.NewChat(fakeSearcher{}, gleann.ChatConfig{Provider: gleann.LLMOllama, Model: "x", BaseURL: srv.URL})
	if out := m.handleScriptCommand("anything"); !strings.Contains(out, "Script generation failed") {
		t.Fatalf("got %q", out)
	}
}

func TestHandleScriptCommand_UnwritableTempDir(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("TMP", os.Getenv("TMPDIR"))
	t.Setenv("TEMP", os.Getenv("TMPDIR"))
	srv := ollamaChatServer(t, "#!/bin/bash\necho x")
	m := newTestChatModel(t)
	m.chat = gleann.NewChat(fakeSearcher{}, gleann.ChatConfig{Provider: gleann.LLMOllama, Model: "x", BaseURL: srv.URL})
	if out := m.handleScriptCommand("t"); !strings.Contains(out, "Failed to save script") {
		t.Fatalf("got %q", out)
	}
}

func TestHandleScriptCommand_Usage(t *testing.T) {
	m := newTestChatModel(t)
	if out := m.handleScriptCommand(""); !strings.Contains(out, "Script-first mode") {
		t.Fatalf("got %q", out)
	}
}

func TestExtractScript_Variants(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"sh fence", "x\n```sh\necho a\n```", "echo a"},
		{"plain fence with shebang", "```\n#!/bin/sh\necho a\n```", "#!/bin/sh\necho a"},
		{"plain fence with set", "```\nset -e\nls\n```", "set -e\nls"},
		{"plain fence of prose is not a script", "```\nhello there\n```", ""},
		{"unterminated fence falls through", "```bash\n#!/bin/bash\necho a", ""},
		{"bare shebang", "  #!/bin/bash\necho a  ", "#!/bin/bash\necho a"},
		{"prose", "I cannot do that", ""},
	}
	for _, tc := range tests {
		if got := extractScript(tc.in); got != tc.want {
			t.Errorf("%s: extractScript = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// ── attached files / streaming with media ──────────────────────

func TestAttachedFilesContext(t *testing.T) {
	dir := t.TempDir()
	one := filepath.Join(dir, "one.txt")
	if err := os.WriteFile(one, []byte("first file"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(dir, "tree")
	if err := os.MkdirAll(filepath.Join(tree, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "sub", "inner.go"), []byte("package inner"), 0o644); err != nil {
		t.Fatal(err)
	}
	huge := filepath.Join(tree, "huge.bin")
	if err := os.WriteFile(huge, make([]byte, 512*1024+1), 0o644); err != nil {
		t.Fatal(err)
	}
	hugeTop := filepath.Join(dir, "huge-top.bin")
	if err := os.WriteFile(hugeTop, make([]byte, 512*1024+1), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := attachedFilesContext(nil); got != "" {
		t.Fatalf("nothing attached must add nothing, got %q", got)
	}
	got := attachedFilesContext([]string{one, tree, filepath.Join(dir, "gone"), hugeTop})
	for _, want := range []string{"Attached Files Context", "=== one.txt ===\nfirst file", "=== " + filepath.Join("sub", "inner.go") + " ===\npackage inner"} {
		if !strings.Contains(got, want) {
			t.Errorf("context missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "huge") {
		t.Error("files over 512KB must be skipped")
	}
}

func TestAskLLMStreamWithMedia_PlainAndWithImages(t *testing.T) {
	// Regression: pending images are base64 data (see handleImageCommand); the
	// stream must send them as such instead of reading them as file paths.
	dir := t.TempDir()
	att := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(att, []byte("ATTACHED-MARKER"), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr, err := memory.OpenStore(filepath.Join(dir, "mem"))
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	for name, images := range map[string][]string{"plain": nil, "with image": {"aGVsbG8="}} {
		t.Run(name, func(t *testing.T) {
			m := mockChatModel(t, gleann.SearchResult{Text: "doc passage", Score: 1})
			m.memoryMgr = memory.NewManager(mgr)
			m.attachedFiles = []string{att}
			m.topK = 3
			msg := m.askLLMStreamWithMedia("what is it?", images)()
			start, ok := msg.(streamStartMsg)
			if !ok {
				t.Fatalf("got %T, want streamStartMsg", msg)
			}
			var answer strings.Builder
			for tok := range start.tokenChan {
				if strings.HasPrefix(tok, "\x00ERROR:") {
					t.Fatalf("stream failed: %s", tok)
				}
				answer.WriteString(tok)
			}
			if !strings.Contains(answer.String(), "ATTACHED-MARKER") || !strings.Contains(answer.String(), "doc passage") {
				t.Fatalf("answer should echo attached files and retrieved context, got %q", answer.String())
			}
		})
	}
}

func TestAskLLMStreamWithMedia_ErrorTokenOnFailure(t *testing.T) {
	m := newTestChatModel(t)
	m.chat = gleann.NewChat(fakeSearcher{}, gleann.ChatConfig{Provider: "no-such-provider", Model: "x"})
	start := m.askLLMStreamWithMedia("q", nil)().(streamStartMsg)
	var last string
	for tok := range start.tokenChan {
		last = tok
	}
	if !strings.HasPrefix(last, "\x00ERROR:") {
		t.Fatalf("a failing chat must end the stream with an error token, got %q", last)
	}
	if _, ok := waitForToken(chanOf(last))().(streamErrorMsg); !ok {
		t.Fatal("the error token must become a streamErrorMsg")
	}
}

func chanOf(s string) <-chan string {
	ch := make(chan string, 1)
	ch <- s
	close(ch)
	return ch
}

// ── /screenshot ────────────────────────────────────────────────

func TestHandleScreenshotCommand_NoToolsGivesTip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("PATH", t.TempDir()) // none of the capture tools exist
	m := newTestChatModel(t)
	out := m.handleScreenshotCommand("")
	if !strings.Contains(out, "Could not capture") || !strings.Contains(out, "/image <filepath>") {
		t.Fatalf("got %q", out)
	}
	if len(m.pendingImages) != 0 {
		t.Fatal("nothing must be queued")
	}
}

func TestHandleScreenshotCommand_ClipboardImageIsQueued(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("exercises the wl-paste clipboard path")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf 'PNGBYTES'\n"
	if err := os.WriteFile(filepath.Join(bin, "wl-paste"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	m := newTestChatModel(t)
	out := m.handleScreenshotCommand("")
	if !strings.Contains(out, "Queued") || len(m.pendingImages) != 1 {
		t.Fatalf("clipboard image must be queued: %q (pending=%d)", out, len(m.pendingImages))
	}
	shots, _ := filepath.Glob(filepath.Join(home, ".gleann", "screenshots", "screenshot_*.png"))
	if len(shots) != 1 {
		t.Fatalf("screenshot must be saved under ~/.gleann/screenshots, got %v", shots)
	}
}

func TestHandleScreenshotCommand_PathDelegatesToImage(t *testing.T) {
	m := newTestChatModel(t)
	if out := m.handleScreenshotCommand(filepath.Join(t.TempDir(), "none.png")); !strings.Contains(out, "not found") {
		t.Fatalf("got %q", out)
	}
}

// ── /pdf ───────────────────────────────────────────────────────

func TestHandlePDFCommand_Validation(t *testing.T) {
	dir := t.TempDir()
	txt := filepath.Join(dir, "a.txt")
	_ = os.WriteFile(txt, []byte("x"), 0o644)
	bad := filepath.Join(dir, "bad.pdf")
	_ = os.WriteFile(bad, []byte("not a pdf"), 0o644)
	big := filepath.Join(dir, "big.pdf")
	f, _ := os.Create(big)
	_ = f.Truncate(50*1024*1024 + 1)
	f.Close()

	tests := []struct{ name, arg, want string }{
		{"usage", "", "/pdf <filepath>"},
		{"missing", filepath.Join(dir, "nope.pdf"), "PDF not found"},
		{"directory", dir, "directory"},
		{"wrong extension", txt, "Not a PDF"},
		{"too large", big, "too large"},
		{"unrenderable", bad, "PDF render error"},
	}
	for _, tc := range tests {
		m := newTestChatModel(t)
		if out := m.handlePDFCommand(tc.arg); !strings.Contains(out, tc.want) {
			t.Errorf("%s: got %q, want it to contain %q", tc.name, out, tc.want)
		}
		if len(m.pendingImages) != 0 {
			t.Errorf("%s: nothing may be queued", tc.name)
		}
	}
}

// ── /video ─────────────────────────────────────────────────────

func TestHandleVideoCommand(t *testing.T) {
	dir := t.TempDir()
	mp4 := filepath.Join(dir, "clip.mp4")
	_ = os.WriteFile(mp4, []byte("video"), 0o644)
	txt := filepath.Join(dir, "clip.txt")
	_ = os.WriteFile(txt, []byte("x"), 0o644)
	big := filepath.Join(dir, "big.mkv")
	f, _ := os.Create(big)
	_ = f.Truncate(100*1024*1024 + 1)
	f.Close()

	tests := []struct {
		name, arg, want string
		queued          int
	}{
		{"usage", "", "/video <filepath>", 0},
		{"missing", filepath.Join(dir, "none.mp4"), "not found", 0},
		{"directory", dir, "directory", 0},
		{"bad ext", txt, "Unsupported video format", 0},
		{"too large", big, "too large", 0},
		{"ok", mp4, "Queued `clip.mp4`", 1},
	}
	for _, tc := range tests {
		m := newTestChatModel(t)
		if out := m.handleVideoCommand(tc.arg); !strings.Contains(out, tc.want) {
			t.Errorf("%s: got %q", tc.name, out)
		}
		if len(m.pendingImages) != tc.queued {
			t.Errorf("%s: pending = %d, want %d", tc.name, len(m.pendingImages), tc.queued)
		}
	}
}

// ── /graph ─────────────────────────────────────────────────────

func TestHandleGraphCommand_ErrorPaths(t *testing.T) {
	m := newTestChatModel(t)
	if out := m.handleGraphCommand(""); !strings.Contains(out, "/graph <symbol>") {
		t.Fatalf("usage: %q", out)
	}
	m.chat = gleann.NewChat(fakeSearcher{}, gleann.ChatConfig{Provider: gleann.LLMMock})
	if out := m.handleGraphCommand("pkg.Fn"); !strings.Contains(out, "single index") {
		t.Fatalf("a non-LeannSearcher means multi-index mode: %q", out)
	}
	m.chat = gleann.NewChat(&gleann.LeannSearcher{}, gleann.ChatConfig{Provider: gleann.LLMMock})
	if out := m.handleGraphCommand("pkg.Fn"); !strings.Contains(out, "No graph database") {
		t.Fatalf("an index without a graph: %q", out)
	}
}

// ── /index with real index metadata ────────────────────────────

func writeIndexMeta(t *testing.T, indexDir string, metas ...gleann.IndexMeta) {
	t.Helper()
	for _, meta := range metas {
		d := filepath.Join(indexDir, meta.Name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(meta)
		if err := os.WriteFile(filepath.Join(d, meta.Name+".meta.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func indexHome(t *testing.T, metas ...gleann.IndexMeta) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	idxDir := filepath.Join(home, "indexes")
	writeIndexMeta(t, idxDir, metas...)
	if err := os.MkdirAll(filepath.Join(home, ".gleann"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(map[string]string{"index_dir": idxDir})
	if err := os.WriteFile(filepath.Join(home, ".gleann", "config.json"), cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	return idxDir
}

func TestHandleIndexCommand_ListInfoSwitchAndMulti(t *testing.T) {
	indexHome(t,
		gleann.IndexMeta{Name: "test-index", Backend: "hnsw", NumPassages: 42, Dimensions: 384},
		gleann.IndexMeta{Name: "other", Backend: "hnsw", NumPassages: 7, Dimensions: 384},
	)
	m := newTestChatModel(t)
	m.embeddingProvider, m.embeddingModel = "ollama", "bge-m3"

	list := m.handleIndexCommand("")
	if !strings.Contains(list, "▸ test-index (42 vectors)") || !strings.Contains(list, "  other (7 vectors)") {
		t.Fatalf("list must mark the current index: %s", list)
	}
	info := m.handleIndexCommand("info")
	if !strings.Contains(info, "**Vectors:** 42") || !strings.Contains(info, "**Dimensions:** 384") || !strings.Contains(info, "hnsw") {
		t.Fatalf("info: %s", info)
	}

	if out := m.handleIndexCommand("nope"); !strings.Contains(out, `"nope" not found`) {
		t.Fatalf("unknown index: %s", out)
	}
	if out := m.handleIndexCommand("other"); !strings.Contains(out, "Switched") || m.indexName != "other" {
		t.Fatalf("switch: %s (now %s)", out, m.indexName)
	}

	// Multi-index: add / duplicate / unknown / list shows active ones / remove.
	if out := m.handleIndexCommand("add test-index"); !strings.Contains(out, "Added index") {
		t.Fatalf("add: %s", out)
	}
	if out := m.handleIndexCommand("add ghost"); !strings.Contains(out, "not found") {
		t.Fatalf("add unknown: %s", out)
	}
	if out := m.handleIndexCommand(""); !strings.Contains(out, "Active indexes:** other + test-index") {
		t.Fatalf("list with active indexes: %s", out)
	}
	if out := m.handleIndexCommand("remove test-index"); !strings.Contains(out, "Querying only: other") {
		t.Fatalf("remove: %s", out)
	}
}

func TestHandleIndexCommand_DirectoryListingError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// index_dir is a file, so reading it as a directory fails.
	file := filepath.Join(home, "afile")
	_ = os.WriteFile(file, []byte("x"), 0o644)
	_ = os.MkdirAll(filepath.Join(home, ".gleann"), 0o700)
	cfg, _ := json.Marshal(map[string]string{"index_dir": file})
	_ = os.WriteFile(filepath.Join(home, ".gleann", "config.json"), cfg, 0o600)

	m := newTestChatModel(t)
	if out := m.handleIndexCommand("x"); !strings.Contains(out, "Error listing indexes") {
		t.Fatalf("switch: %s", out)
	}
	if out := m.handleIndexAdd("x"); !strings.Contains(out, "Error listing indexes") {
		t.Fatalf("add: %s", out)
	}
}

// ── reranker wiring ────────────────────────────────────────────

func TestApplyReranker_EnabledAndDisabled(t *testing.T) {
	// No LeannSearcher state is observable from outside, so check the paths do
	// not panic on both searcher kinds and that disabling leaves the model usable.
	m := newTestChatModel(t)
	m.chat = gleann.NewChat(&gleann.LeannSearcher{}, gleann.ChatConfig{Provider: gleann.LLMMock})
	m.rerankModels = []string{"rerank-v3.5"}
	m.rerankEnabled = true
	m.applyReranker()
	m.rerankModelIdx = 5 // out of range: falls back to no model
	m.applyReranker()
	m.rerankEnabled = false
	m.applyReranker()
}

// ── slash command dispatcher ───────────────────────────────────

func TestHandleSlashCommand_Dispatch(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "p.png")
	_ = os.WriteFile(img, []byte("png"), 0o644)
	aud := filepath.Join(dir, "a.mp3")
	_ = os.WriteFile(aud, []byte("mp3"), 0o644)
	vid := filepath.Join(dir, "v.mp4")
	_ = os.WriteFile(vid, []byte("mp4"), 0o644)

	tests := []struct {
		input, want string
	}{
		{"/help", "Commands:"},
		{"/image", "/image <filepath>"},
		{"/image " + img, "Queued"},
		{"/screenshot " + img, "Queued"},
		{"/audio " + aud, "Queued"},
		{"/video " + vid, "Queued"},
		{"/attach " + img, "Attached"},
		{"/attach", "attached"},
		{"/detach " + img, "Not found in attached files"},
		{"/index", "Index"},
		{"/index add", "No index directory"}, // "add" without a name is a switch to an index called add
		{"/budget", "Token Budget"},
		{"/script", "Script-first mode"},
		{"/pdf", "/pdf <filepath>"},
		{"/pdf " + filepath.Join(dir, "no.pdf"), "PDF not found"},
		{"/graph", "/graph <symbol>"},
		{"/graph x", "single index"},
		{"/repeat abc", "Usage: /repeat"},
		{"/repeat 9", "Usage: /repeat"},
		{"/repeat-penalty 1.3", "1.30"},
		{"/penalty 1.2", "1.20"},
		{"/remember", ""},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("USERPROFILE", os.Getenv("HOME"))
			m := mockChatModel(t)
			res, cmd, handled := m.handleSlashCommand(tc.input)
			if tc.input == "/remember" {
				if handled || cmd != nil {
					t.Fatal("a bare /remember is not a command (it is sent as a question)")
				}
				return
			}
			if !handled {
				t.Fatalf("%s must be handled", tc.input)
			}
			cm := res.(ChatModel)
			got := cm.messages[len(cm.messages)-1].content
			if !strings.Contains(got, tc.want) {
				t.Fatalf("%s: message %q does not contain %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestHandleSlashCommand_QuitAndClearAndNew(t *testing.T) {
	m := mockChatModel(t)
	m.messages = append(m.messages, chatMsg{role: "user", content: "hi"})
	m.pendingImages = []string{"x"}
	m.attachedFiles = []string{"y"}
	m.tokensSent, m.tokensReceived = 10, 20

	res, cmd, handled := m.handleSlashCommand("/quit")
	if !handled || !res.(ChatModel).quitting || cmd == nil {
		t.Fatal("/quit must set quitting and return tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("expected QuitMsg")
	}
	res, _, _ = m.handleSlashCommand("/clear")
	if n := len(res.(ChatModel).messages); n != 1 {
		t.Fatalf("/clear keeps only the welcome message, got %d", n)
	}
	res, _, _ = m.handleSlashCommand("/new")
	cm := res.(ChatModel)
	if len(cm.messages) != 1 || cm.pendingImages != nil || cm.attachedFiles != nil || cm.tokensSent != 0 || cm.tokensReceived != 0 {
		t.Fatalf("/new must reset the session: %+v", cm.messages)
	}
	if !strings.Contains(cm.messages[0].content, "New conversation") {
		t.Fatalf("got %q", cm.messages[0].content)
	}
	res, _, _ = m.handleSlashCommand("/settings")
	if !res.(ChatModel).showSettings {
		t.Fatal("/settings opens the panel")
	}
	if _, _, handled = m.handleSlashCommand("hello"); handled {
		t.Fatal("plain text is not a command")
	}
}

func TestHandleSlashCommand_MemoryCommands(t *testing.T) {
	store, err := memory.OpenStore(filepath.Join(t.TempDir(), "mem"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	m := mockChatModel(t)
	// Without a manager every memory command says so.
	for _, in := range []string{"/remember a fact", "/forget a", "/memories"} {
		res, _, _ := m.handleSlashCommand(in)
		cm := res.(ChatModel)
		if got := lastSystemMessage(t, cm); !strings.Contains(got, "Memory system unavailable") {
			t.Fatalf("%s: %q", in, got)
		}
	}

	m.memoryMgr = memory.NewManager(store)
	res, _, _ := m.handleSlashCommand("/memories")
	if got := lastSystemMessage(t, res.(ChatModel)); got != "No memories stored." {
		t.Fatalf("empty store: %q", got)
	}
	res, _, _ = m.handleSlashCommand("/remember the build uses go 1.25")
	if got := lastSystemMessage(t, res.(ChatModel)); !strings.Contains(got, "Remembered") {
		t.Fatalf("remember: %q", got)
	}
	res, _, _ = m.handleSlashCommand("/memories")
	if got := lastSystemMessage(t, res.(ChatModel)); !strings.Contains(got, "Memories (1)") || !strings.Contains(got, "go 1.25") {
		t.Fatalf("list: %q", got)
	}
	res, _, _ = m.handleSlashCommand("/forget go 1.25")
	if got := lastSystemMessage(t, res.(ChatModel)); !strings.Contains(got, "Forgot 1 memory block(s)") {
		t.Fatalf("forget: %q", got)
	}
	res, _, _ = m.handleSlashCommand("/memories")
	if got := lastSystemMessage(t, res.(ChatModel)); got != "No memories stored." {
		t.Fatalf("after forget: %q", got)
	}
}

func TestHandleSlashCommand_RepeatPenaltyIsPersisted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	m := mockChatModel(t)
	res, _, _ := m.handleSlashCommand("/repeat 1.35")
	if cm := res.(ChatModel); cm.repeatPenalty != 1.35 {
		t.Fatalf("repeatPenalty = %v", cm.repeatPenalty)
	}
	if saved := LoadSavedConfig(); saved == nil || saved.RepeatPenalty != 1.35 {
		t.Fatalf("the penalty must be saved to config.json, got %+v", saved)
	}
}

func TestHandleSlashCommand_HistoryEmptyAndListed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	m := mockChatModel(t)
	res, _, _ := m.handleSlashCommand("/history")
	if got := lastSystemMessage(t, res.(ChatModel)); !strings.Contains(got, "No saved conversations") {
		t.Fatalf("got %q", got)
	}
}

func TestLookupContextLimit_FromEnvironment(t *testing.T) {
	t.Setenv("GLEANN_CONTEXT_TOKENS", "12345")
	if got := lookupContextLimit(gleann.ChatConfig{Provider: gleann.LLMMock}); got != 12345 {
		t.Fatalf("got %d", got)
	}
	t.Setenv("GLEANN_CONTEXT_TOKENS", "")
	if got := lookupContextLimit(gleann.ChatConfig{}); got != 0 {
		t.Fatalf("unknown model must report 0, got %d", got)
	}
}
