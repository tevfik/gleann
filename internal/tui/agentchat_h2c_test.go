package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/agenttui"

	"github.com/tevfik/gleann/pkg/conversations"
	"github.com/tevfik/gleann/pkg/gleann"
)

// h2cPrompter answers the questions and forms of slash commands the way a
// user would, and records what was put to the user.
type h2cPrompter struct {
	ask       func(q agenttui.Question) (string, error)
	form      func(f agenttui.Form) ([]string, error)
	questions []agenttui.Question
	forms     []agenttui.Form
}

func (p *h2cPrompter) Ask(_ context.Context, q agenttui.Question) (string, error) {
	p.questions = append(p.questions, q)
	return p.ask(q)
}

func (p *h2cPrompter) Form(_ context.Context, f agenttui.Form) ([]string, error) {
	p.forms = append(p.forms, f)
	return p.form(f)
}

// h2cFormValues returns the current values of f with some fields replaced.
func h2cFormValues(f agenttui.Form, set map[string]string) []string {
	out := make([]string, len(f.Fields))
	for i, fld := range f.Fields {
		out[i] = fld.Value
		if v, ok := set[fld.Label]; ok {
			out[i] = v
		}
	}
	return out
}

func h2cRun(t *testing.T, ac *agentChat, name string, args ...string) (string, error) {
	t.Helper()
	out, local, err := agentCmd(t, ac, name).Handler(context.Background(), args, ac.app)
	if !local {
		t.Fatalf("/%s must be handled locally", name)
	}
	return out, err
}

func TestH2cAgentSettingsFormAppliesValues(t *testing.T) {
	h2cHome(t)
	ac := newTestAgentChat(nil)
	cm := ac.cm
	cm.llmModels = []string{"mock", "other"}
	cm.rerankModels = []string{"r1", "r2"}
	cm.roleNames = []string{"(none)", "reviewer"}
	cm.systemPrompt = "old prompt"
	cm.embeddingModel = "/models/bge-m3.gguf"

	p := &h2cPrompter{form: func(f agenttui.Form) ([]string, error) {
		return h2cFormValues(f, map[string]string{
			"Model": "other", "Temperature": "0.3", "Repeat penalty": "1.20", "Max tokens": "512",
			"Top-K": "5", "Reranker": "on", "Rerank model": "r2", "System prompt": "be brief",
		}), nil
	}}
	ac.prompt = p
	out, err := h2cRun(t, ac, "settings")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.forms) != 1 || !strings.Contains(p.forms[0].Note, "Embedding: /models/bge-m3.gguf") {
		t.Fatalf("form note must name the embedding model: %+v", p.forms)
	}
	if !strings.Contains(out, "Settings updated — model: other") {
		t.Errorf("confirmation: %q", out)
	}
	if cm.modelName != "other" || cm.temperature != 0.3 || cm.repeatPenalty != 1.2 || cm.maxTokens != 512 || cm.topK != 5 {
		t.Errorf("values not applied: %+v", cm)
	}
	if !cm.rerankEnabled || cm.rerankModelIdx != 1 || cm.systemPrompt != "be brief" || cm.roleIdx != 0 {
		t.Errorf("reranker/prompt not applied: rerank=%v idx=%d prompt=%q role=%d", cm.rerankEnabled, cm.rerankModelIdx, cm.systemPrompt, cm.roleIdx)
	}
	if ac.app.GetModelName() != "other" {
		t.Errorf("the app header must show the new model: %q", ac.app.GetModelName())
	}

	// Picking a role with the prompt untouched resolves the role's prompt.
	p.form = func(f agenttui.Form) ([]string, error) {
		return h2cFormValues(f, map[string]string{"Role": "reviewer"}), nil
	}
	if _, err := h2cRun(t, ac, "settings"); err != nil {
		t.Fatal(err)
	}
	if cm.roleIdx != 1 || !strings.Contains(cm.systemPrompt, "code reviewer") {
		t.Errorf("role must win when the prompt is unchanged: role=%d prompt=%q", cm.roleIdx, cm.systemPrompt)
	}
	// Unparsable numbers keep the old values.
	p.form = func(f agenttui.Form) ([]string, error) {
		return h2cFormValues(f, map[string]string{"Temperature": "warm", "Top-K": "many"}), nil
	}
	if _, err := h2cRun(t, ac, "settings"); err != nil {
		t.Fatal(err)
	}
	if cm.temperature != 0.3 || cm.topK != 5 {
		t.Errorf("bad numbers must be ignored: %v %d", cm.temperature, cm.topK)
	}
}

func TestH2cAgentSettingsCancelAndError(t *testing.T) {
	ac := newTestAgentChat(nil)
	before := ac.cm.temperature
	ac.prompt = &h2cPrompter{form: func(agenttui.Form) ([]string, error) { return nil, agenttui.ErrAskCancelled }}
	if out, err := h2cRun(t, ac, "settings"); err != nil || out != "Settings unchanged." {
		t.Errorf("cancel: %q %v", out, err)
	}
	boom := errors.New("terminal gone")
	ac.prompt = &h2cPrompter{form: func(agenttui.Form) ([]string, error) { return nil, boom }}
	if _, err := h2cRun(t, ac, "settings"); !errors.Is(err, boom) {
		t.Errorf("form error must be returned: %v", err)
	}
	if ac.cm.temperature != before {
		t.Error("a failed form must not change settings")
	}
}

func TestH2cAgentHistoryResume(t *testing.T) {
	home := h2cHome(t)
	ac := newTestAgentChat(nil)

	// No conversations: nothing is asked.
	p := &h2cPrompter{ask: func(agenttui.Question) (string, error) { t.Fatal("must not ask"); return "", nil }}
	ac.prompt = p
	if out, _ := h2cRun(t, ac, "history"); !strings.Contains(out, "No saved conversations") {
		t.Errorf("empty store: %q", out)
	}

	store := conversations.NewStore(filepath.Join(home, "convs"))
	conv := &conversations.Conversation{Title: "about alpha", Messages: []conversations.Message{
		{Role: "system", Content: "saved prompt"},
		{Role: "user", Content: "what is alpha?"},
		{Role: "assistant", Content: "a letter"},
	}}
	if err := store.Save(conv); err != nil {
		t.Fatal(err)
	}
	ac.cm.tokensSent = 99

	p.ask = func(q agenttui.Question) (string, error) { return q.Options[0], nil }
	out, err := h2cRun(t, ac, "history")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Loaded conversation: about alpha (3 messages)") {
		t.Errorf("resume: %q", out)
	}
	if !strings.Contains(p.questions[0].Options[0], "about alpha") || !strings.Contains(p.questions[0].Options[0], "3 msgs") {
		t.Errorf("option label: %q", p.questions[0].Options[0])
	}
	h := ac.cm.chat.History()
	if len(h) != 2 || h[0].Content != "what is alpha?" || ac.cm.systemPrompt != "saved prompt" || ac.cm.chat.Config().SystemPrompt != "saved prompt" {
		t.Errorf("history/prompt not restored: %+v %q", h, ac.cm.systemPrompt)
	}
	if ac.cm.tokensSent != 0 {
		t.Error("the token budget restarts with a resumed conversation")
	}

	for _, tc := range []struct {
		name   string
		answer func(q agenttui.Question) (string, error)
		check  func(out string, err error) bool
	}{
		{"cancel", func(agenttui.Question) (string, error) { return "", agenttui.ErrAskCancelled },
			func(out string, err error) bool { return err == nil && out == "Cancelled." }},
		{"error", func(agenttui.Question) (string, error) { return "", errors.New("closed") },
			func(_ string, err error) bool { return err != nil && err.Error() == "closed" }},
		{"unknown answer", func(agenttui.Question) (string, error) { return "not an option", nil },
			func(_ string, err error) bool { return err != nil && strings.Contains(err.Error(), "not found") }},
		{"deleted meanwhile", func(q agenttui.Question) (string, error) {
			_ = os.RemoveAll(filepath.Join(home, "convs"))
			return q.Options[0], nil
		}, func(_ string, err error) bool {
			return err != nil && strings.Contains(err.Error(), "load conversation")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p.ask = tc.answer
			if out, err := h2cRun(t, ac, "history"); !tc.check(out, err) {
				t.Errorf("got %q %v", out, err)
			}
		})
	}
}

func TestH2cAgentForgetAfterConfirmation(t *testing.T) {
	h2cHome(t)
	ac := newTestAgentChat(nil)
	mgr := withMemory(t, ac)
	if _, err := mgr.Remember("the sky is blue"); err != nil {
		t.Fatal(err)
	}
	p := &h2cPrompter{ask: func(q agenttui.Question) (string, error) { return "Yes", nil }}
	ac.prompt = p
	out, err := h2cRun(t, ac, "forget", "sky")
	if err != nil || !strings.Contains(out, "Forgot 1 memory block") {
		t.Fatalf("forget: %q %v", out, err)
	}
	if q := p.questions[0]; q.Default != "No" || !strings.Contains(q.Text, `"sky"`) {
		t.Errorf("confirmation must default to No and name the query: %+v", q)
	}
	mgr.Close()
	if out, _ := h2cRun(t, ac, "forget", "sky"); !strings.HasPrefix(out, "⚠ ") {
		t.Errorf("forget on a closed store: %q", out)
	}
}

// h2cScopeEnv builds two indexes (one with a code graph and a tag) in a temp
// dir the picker lists, and a loader that records what it was asked to open.
func h2cScopeEnv(t *testing.T) (dir string, loaded *[]string) {
	t.Helper()
	h2cHome(t)
	dir = t.TempDir()
	h2cGraphIndex(t, dir, "code")
	h2bBuildIndexes(t, dir, "docs")
	if err := gleann.UpdateIndexMeta(dir, "code", func(m *gleann.IndexMeta) {
		m.Tags = []string{"work"}
		m.Description = "the calc module"
	}); err != nil {
		t.Fatal(err)
	}
	oldDir, oldLoader := searchIndexDir, SearcherLoader
	t.Cleanup(func() { searchIndexDir, SearcherLoader = oldDir, oldLoader })
	searchIndexDir = dir
	var names []string
	SearcherLoader = func(_ context.Context, n []string) (gleann.Searcher, error) {
		names = n
		if n[0] == "fail" {
			return nil, errors.New("cannot open")
		}
		return fakeSearcher{}, nil
	}
	return dir, &names
}

func TestH2cAgentScopePicker(t *testing.T) {
	_, loaded := h2cScopeEnv(t)
	ac := newTestAgentChat(nil)
	ac.cm.indexName = "docs"

	var form agenttui.Form
	ac.prompt = &h2cPrompter{form: func(f agenttui.Form) ([]string, error) {
		form = f
		// Tick the #work tag, untick docs.
		return h2cFormValues(f, map[string]string{"#work": "on", "docs": "off"}), nil
	}}
	out, err := h2cRun(t, ac, "index")
	if err != nil {
		t.Fatal(err)
	}
	labels := make([]string, len(form.Fields))
	for i, f := range form.Fields {
		labels[i] = f.Label + "=" + f.Value
	}
	if got := strings.Join(labels, ","); got != "#work=off,code=off,docs=on" {
		t.Errorf("fields: %s", got)
	}
	if h := form.Fields[1].Hint; !strings.Contains(h, "code graph (@symbol works)") || !strings.Contains(h, "#work") || !strings.Contains(h, "the calc module") {
		t.Errorf("index hint: %q", h)
	}
	if !strings.Contains(form.Fields[0].Hint, "tagged work: code") {
		t.Errorf("tag hint must list its members: %q", form.Fields[0].Hint)
	}
	if strings.Join(*loaded, ",") != "code" || ac.cm.indexName != "code" || !strings.Contains(out, "code") {
		t.Errorf("ticking the tag must switch to its index: %v %q %q", *loaded, ac.cm.indexName, out)
	}

	// /index info lists the graph flag and tags.
	info, _ := h2cRun(t, ac, "index", "info")
	if !strings.Contains(info, "● code") || !strings.Contains(info, "· graph") || !strings.Contains(info, "#work") {
		t.Errorf("/index info:\n%s", info)
	}

	// Nothing ticked: the switch is refused and reported, not returned as an error.
	ac.prompt = &h2cPrompter{form: func(f agenttui.Form) ([]string, error) {
		return h2cFormValues(f, map[string]string{"#work": "off", "code": "off", "docs": "off"}), nil
	}}
	if out, err := h2cRun(t, ac, "index"); err != nil || !strings.HasPrefix(out, "⚠ ") {
		t.Errorf("empty selection: %q %v", out, err)
	}
	ac.prompt = &h2cPrompter{form: func(agenttui.Form) ([]string, error) { return nil, agenttui.ErrAskCancelled }}
	if out, _ := h2cRun(t, ac, "index"); out != "Index selection unchanged." {
		t.Errorf("cancel: %q", out)
	}
	boom := errors.New("closed")
	ac.prompt = &h2cPrompter{form: func(agenttui.Form) ([]string, error) { return nil, boom }}
	if _, err := h2cRun(t, ac, "index"); !errors.Is(err, boom) {
		t.Errorf("form error: %v", err)
	}
}

func TestH2cListIndexInfoSources(t *testing.T) {
	home := h2cHome(t)
	old := searchIndexDir
	t.Cleanup(func() { searchIndexDir = old })
	searchIndexDir = ""

	// Without a loader the saved config's IndexDir is listed.
	dir := t.TempDir()
	h2bBuildIndexes(t, dir, "notes")
	if err := SaveConfig(OnboardResult{IndexDir: dir}); err != nil {
		t.Fatal(err)
	}
	if got := listIndexInfo(); len(got) != 1 || got[0].Name != "notes" || got[0].Graph {
		t.Errorf("config index dir: %+v", got)
	}
	// An index dir that cannot be read lists nothing.
	blocker := filepath.Join(home, "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	searchIndexDir = filepath.Join(blocker, "indexes")
	if got := listIndexInfo(); len(got) != 0 {
		t.Errorf("unreadable dir: %+v", got)
	}
}

func TestH2cNewSearcherLoaderOpensOneOrMany(t *testing.T) {
	dir := t.TempDir()
	h2bBuildIndexes(t, dir, "a", "b")
	old := searchIndexDir
	t.Cleanup(func() { searchIndexDir = old })

	load := NewSearcherLoader(gleann.Config{IndexDir: dir, Backend: "hnsw"}, nil)
	if searchIndexDir != dir {
		t.Errorf("the picker must list the loader's dir: %q", searchIndexDir)
	}
	one, err := load(context.Background(), []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	if _, ok := one.(*gleann.LeannSearcher); !ok {
		t.Errorf("one index opens a single searcher: %T", one)
	}
	many, err := load(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	defer many.Close()
	if _, ok := many.(*gleann.MultiSearcher); !ok {
		t.Errorf("several indexes open a multi-searcher: %T", many)
	}
	if _, err := load(context.Background(), []string{"missing"}); err == nil {
		t.Error("a missing index must fail")
	}
}

func TestH2cAgentMentionsUseCodeGraph(t *testing.T) {
	s := h2cGraphSearcher(t)
	ac := newTestAgentChat(nil)
	ac.cm.chat.SetSearcher(s)

	got := ac.mentionComplete("Add")
	if len(got) == 0 || got[0] != "example.com/calc.Add" {
		t.Errorf("completion: %v", got)
	}
	if ac.mentionComplete("A") != nil {
		t.Error("one-character queries are not completed")
	}
	def, ok := ac.mentionResolve("example.com/calc.Add")
	if !ok || !strings.Contains(def, "calc.go") {
		t.Errorf("resolve: %v %q", ok, def)
	}
	if _, ok := ac.mentionResolve("NoSuchSymbolAnywhere"); ok {
		t.Error("an unknown symbol must not resolve")
	}
	if got := ac.mentionComplete("NoSuchSymbolAnywhere"); len(got) != 0 {
		t.Errorf("unknown symbol completion: %v", got)
	}
}

func TestH2cAgentGraphSearcherInMultiIndex(t *testing.T) {
	dir := t.TempDir()
	h2bBuildIndexes(t, dir, "docs")
	h2cGraphIndex(t, dir, "code")
	ms, err := gleann.LoadMultiSearcher(context.Background(), gleann.Config{IndexDir: dir, Backend: "hnsw"}, nil, []string{"docs", "code"})
	if err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	ac := newTestAgentChat(nil)
	ac.cm.chat.SetSearcher(ms)
	ls := ac.graphSearcher()
	if ls == nil || ls.GraphDB() == nil {
		t.Fatal("the index with a code graph must be found in a multi-index scope")
	}
	if got := ac.mentionComplete("Twice"); len(got) == 0 || got[0] != "example.com/calc.Twice" {
		t.Errorf("completion through the multi-searcher: %v", got)
	}
}

func TestH2cAgentHeaderAndInfoRows(t *testing.T) {
	ac := newTestAgentChat(nil)
	cm := ac.cm
	cm.indexName = "(no index)"
	cm.maxTokens = 0
	cm.roleNames = []string{"(none)", "reviewer"}
	cm.roleIdx = 1
	cm.pendingImages = []string{"x"}
	cm.attachedFiles = []string{"a", "b"}
	cm.embeddingModel = filepath.Join("models", "bge-m3.gguf")

	wide := ac.headerInfoWide()
	for _, want := range []string{"LLM only", "max=∞", "role:reviewer", "📷×1", "📎×2"} {
		if !strings.Contains(wide, want) {
			t.Errorf("wide header lacks %q: %q", want, wide)
		}
	}
	if strings.Contains(wide, "k=") {
		t.Errorf("no top-k without retrieval: %q", wide)
	}
	if h := ac.headerInfo(); !strings.Contains(h, "📎×2") {
		t.Errorf("header: %q", h)
	}
	rows := map[string]string{}
	for _, r := range ac.infoRows() {
		rows[r[0]] = r[1]
	}
	if rows["role"] != "reviewer" || rows["embedding"] != "bge-m3.gguf" || rows["mode"] != "LLM only (no retrieval)" {
		t.Errorf("info rows: %v", rows)
	}

	cm.indexName = "docs"
	cm.rerankEnabled = true
	if wide := ac.headerInfoWide(); !strings.Contains(wide, "RAG") || !strings.Contains(wide, "rerank") {
		t.Errorf("RAG header: %q", wide)
	}
	// While a command holds the state the last rendering is served.
	last := ac.lastWide
	ac.mu.Lock()
	cm.temperature = 1.9
	got := ac.headerInfoWide()
	ac.mu.Unlock()
	if got != last {
		t.Errorf("locked header must reuse %q, got %q", last, got)
	}
}

func TestH2cAgentStreamWithMemoryAndImages(t *testing.T) {
	ac := newTestAgentChat([]gleann.SearchResult{{Text: "alpha", Metadata: map[string]any{"source": "a.md", "_index": "docs"}}})
	t.Cleanup(ac.close)
	mgr := withMemory(t, ac)
	if _, err := mgr.Remember("user prefers short answers"); err != nil {
		t.Fatal(err)
	}
	ac.cm.pendingImages = []string{"aW1n"}
	var sources []agenttui.Source
	if _, err := ac.stream(context.Background(), "describe", func(e agenttui.Event) {
		if e.Kind == agenttui.EventSources {
			sources = e.Sources
		}
	}); err != nil {
		t.Fatal(err)
	}
	if ac.cm.pendingImages != nil {
		t.Error("queued images must be sent with the question")
	}
	if len(sources) != 1 || sources[0].Location != "docs" {
		t.Errorf("source card must carry the index name: %+v", sources)
	}
}
