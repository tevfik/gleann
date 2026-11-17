package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/modelcatalog"
	"github.com/tevfik/gleann/modules/settings"
	"github.com/tevfik/gleann/pkg/gleann"
)

// h2bEmbeddingCatalogEntry is a catalog embedding model; with a fresh HOME
// it is never on disk.
func h2bEmbeddingCatalogEntry(t *testing.T) modelcatalog.Entry {
	t.Helper()
	entries := modelcatalog.OfKind(modelcatalog.Embedding)
	if len(entries) == 0 {
		t.Skip("the catalog has no embedding model")
	}
	return entries[0]
}

func h2bSettingsWith(t *testing.T, cfg OnboardResult) SettingsModel {
	t.Helper()
	h2bHome(t)
	ResetModelOptionsCache()
	t.Cleanup(ResetModelOptionsCache)
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	m := NewSettingsModel()
	if m.err != nil {
		t.Fatal(m.err)
	}
	return m
}

func TestH2bSettingsOffersToDownloadAMissingCatalogModel(t *testing.T) {
	e := h2bEmbeddingCatalogEntry(t)
	m := h2bSettingsWith(t, OnboardResult{EmbeddingProvider: "llamacpp", EmbeddingModel: e.File})
	m = cursorTo(t, m, "embedding_model")

	view := m.render()
	if !strings.Contains(view, "⬇ not downloaded (d)") || !strings.Contains(view, "d download") {
		t.Fatalf("a missing catalog model must be flagged with its download key:\n%s", view)
	}
	r, cmd := m.Update(h2bKey("d"))
	m = r.(SettingsModel)
	if cmd == nil || m.downloading != e.Name {
		t.Fatalf("d must start the download of %s: downloading=%q", e.Name, m.downloading)
	}
	if !strings.Contains(m.render(), "Downloading "+e.Name) {
		t.Error("the running download is shown")
	}
	// A second d while one runs starts nothing.
	if _, cmd := m.Update(h2bKey("d")); cmd != nil {
		t.Error("only one download at a time")
	}
}

func TestH2bSettingsBackRow(t *testing.T) {
	m := h2bSettingsWith(t, OnboardResult{})
	m.cursor = len(m.rows()) - 1
	m.height = 0 // show every line
	view := m.render()
	if !strings.Contains(view, "▸ ← Back") || !strings.Contains(view, "enter back") {
		t.Errorf("Back row selected:\n%s", view)
	}
	if r, cmd := m.Update(h2bKey("d")); cmd != nil || r.(SettingsModel).downloading != "" {
		t.Error("d on the Back row must do nothing")
	}
}

func TestH2bDownloadCatalogModel(t *testing.T) {
	h2bHome(t)
	body := []byte("GGUF fake model bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	sum := sha256.Sum256(body)

	bad := modelcatalog.Entry{Name: "Bad", File: "bad.gguf", URL: srv.URL, Bytes: int64(len(body)), SHA256: strings.Repeat("0", 64)}
	msg := downloadCatalogModel(bad)().(settingsDownloadedMsg)
	if msg.name != "Bad" || msg.err == nil || !strings.Contains(msg.err.Error(), "checksum") {
		t.Fatalf("a corrupted download must fail its checksum: %+v", msg)
	}
	m := NewSettingsModel()
	m.downloading = "Bad"
	r, _ := m.Update(msg)
	if m = r.(SettingsModel); m.err == nil || m.downloading != "" {
		t.Errorf("the failure is shown: err=%v downloading=%q", m.err, m.downloading)
	}

	good := modelcatalog.Entry{Name: "Good", File: "good.gguf", URL: srv.URL, Bytes: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}
	if msg := downloadCatalogModel(good)().(settingsDownloadedMsg); msg.err != nil {
		t.Fatalf("download: %v", msg.err)
	}
	if !good.Installed() {
		t.Error("the model must be on disk after the download")
	}
}

func TestH2bSettingsPickerMarksAndNotesACatalogChoice(t *testing.T) {
	e := h2bEmbeddingCatalogEntry(t)
	m := h2bSettingsWith(t, OnboardResult{EmbeddingProvider: "llamacpp", EmbeddingModel: e.File})
	m = cursorTo(t, m, "embedding_model")
	m = h2bPress(m, "enter").(SettingsModel)
	if !m.picking {
		t.Fatal("enter on a model field opens the picker")
	}
	picker := m.render()
	if !strings.Contains(picker, "● "+e.Name) || !strings.Contains(picker, "["+e.File+"]") {
		t.Errorf("the current catalog model is marked and shows its file name:\n%s", picker)
	}
	if len(m.filtered("embedding_model")) < 2 {
		t.Fatal("the picker needs at least two options")
	}
	m.pick = 0
	if m = h2bPress(m, "down").(SettingsModel); m.pick != 1 {
		t.Errorf("down moves the highlight, pick=%d", m.pick)
	}
	if m = h2bPress(m, "up").(SettingsModel); m.pick != 0 {
		t.Errorf("up moves the highlight back, pick=%d", m.pick)
	}
	if m = h2bPress(m, "up").(SettingsModel); m.pick != 0 {
		t.Errorf("up stops at the first option, pick=%d", m.pick)
	}

	// Choose the catalog model by name: it is not downloaded yet.
	for _, k := range strings.Split(e.File, "") {
		m = h2bPress(m, k).(SettingsModel)
	}
	m = h2bPress(m, "enter").(SettingsModel)
	if m.picking || m.err != nil {
		t.Fatalf("picking=%v err=%v", m.picking, m.err)
	}
	if !strings.Contains(m.notice, "is not downloaded yet: press d") {
		t.Errorf("notice = %q", m.notice)
	}
}

// A select whose options cannot be listed (the server is down) falls back to
// typing the value.
func TestH2bSettingsUnlistableModelIsTyped(t *testing.T) {
	down := httptest.NewServer(http.NotFoundHandler())
	url := down.URL
	down.Close()
	m := h2bSettingsWith(t, OnboardResult{LLMProvider: "openai", OpenAIBaseURL: url, LLMModel: "gpt-old"})
	m = cursorTo(t, m, "llm_model")
	if opts, err := m.field("llm_model").OptionsFrom(m.cfg); err == nil || !strings.Contains(err.Error(), "could not list openai models") || opts != nil {
		t.Fatalf("options = %v, err = %v", opts, err)
	}
	m = h2bPress(m, "enter").(SettingsModel)
	if m.picking || !m.editing || m.input != "gpt-old" {
		t.Fatalf("picking=%v editing=%v input=%q", m.picking, m.editing, m.input)
	}
}

func TestH2bModelOptionsFromAnOpenAIServer(t *testing.T) {
	h2bHome(t)
	ResetModelOptionsCache()
	t.Cleanup(ResetModelOptionsCache)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-t" {
			http.Error(w, "no key", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"text-embedding-3-small"},{"id":"gpt-4o"}]}`))
	}))
	defer srv.Close()
	c := &OnboardResult{OpenAIBaseURL: srv.URL, OpenAIKey: "sk-t"}
	values := func(opts []settings.Option) string {
		var v []string
		for _, o := range opts {
			v = append(v, o.Value)
		}
		return strings.Join(v, ",")
	}
	emb, err := modelOptions(c, "openai", modelcatalog.Embedding)
	if err != nil || values(emb) != "text-embedding-3-small" {
		t.Errorf("embedding options = %q, %v", values(emb), err)
	}
	llm, err := modelOptions(c, "openai", modelcatalog.LLM)
	if err != nil || values(llm) != "gpt-4o" {
		t.Errorf("LLM options = %q, %v", values(llm), err)
	}
	if opts, err := modelOptions(c, "anthropic", modelcatalog.LLM); opts != nil || err != nil {
		t.Errorf("a provider without a model list: %v, %v", opts, err)
	}
}

func TestH2bLocalModelOptionsListFilesOnDisk(t *testing.T) {
	home := h2bHome(t)
	ResetModelOptionsCache()
	t.Cleanup(ResetModelOptionsCache)
	modelsDir := DefaultModelsDir()
	other := filepath.Join(home, "models")
	for _, p := range []string{
		filepath.Join(modelsDir, "my-chat-q4.gguf"),
		filepath.Join(other, "my-chat-q4.gguf"), // same name elsewhere: listed once
		filepath.Join(other, "side-llm.gguf"),
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("gguf"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	opts, err := modelOptions(&OnboardResult{}, "llamacpp", modelcatalog.LLM)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, o := range opts {
		got[o.Value]++
	}
	if got["my-chat-q4.gguf"] != 1 {
		t.Errorf("a file in the models directory is stored by name, once: %v", got)
	}
	if got[filepath.Join(other, "side-llm.gguf")] != 1 {
		t.Errorf("a file elsewhere is stored by path: %v", got)
	}
	if got[filepath.Join(other, "my-chat-q4.gguf")] != 0 {
		t.Errorf("a duplicate name must not be listed twice: %v", got)
	}
}

func TestH2bRerankModelOptionsPerEngine(t *testing.T) {
	for _, tc := range []struct {
		provider gleann.RerankerProvider
		want     string
	}{
		{gleann.RerankerJina, "jina-reranker-v2-base-multilingual"},
		{gleann.RerankerVoyage, "rerank-2"},
		{gleann.RerankerCohere, "rerank-v3.5"},
	} {
		opts, err := rerankModelOptions(&OnboardResult{RerankProvider: string(tc.provider)})
		if err != nil || len(opts) == 0 || opts[0].Value != tc.want {
			t.Errorf("%s: options = %+v, err = %v", tc.provider, opts, err)
		}
	}
	// An engine with no known model list offers nothing (the value is typed).
	if opts, err := rerankModelOptions(&OnboardResult{RerankProvider: "bogus"}); opts != nil || err != nil {
		t.Errorf("unknown engine: %+v, %v", opts, err)
	}
}

func TestH2bSettingsChecks(t *testing.T) {
	reg := Settings()
	c := &OnboardResult{}

	// The built-in reranker engine is accepted only when this build has it.
	err := reg.Set(c, "rerank_provider", string(gleann.RerankerEIF))
	if gleann.EIFRerankAvailable() != (err == nil) {
		t.Errorf("rerank_provider=eif: err = %v, available = %v", err, gleann.EIFRerankAvailable())
	}
	if err := reg.Set(c, "rerank_provider", string(gleann.RerankerJina)); err != nil || c.RerankProvider != string(gleann.RerankerJina) {
		t.Errorf("rerank_provider=jina: %v (%q)", err, c.RerankProvider)
	}

	check := checkProvider("embedding")
	if _, err := check(c, "eif"); gleann.IsEIFSupported() != (err == nil) {
		t.Errorf("eif provider: err = %v", err)
	}
	if _, err := check(c, "native"); gleann.IsNativeSupported() != (err == nil) {
		t.Errorf("native provider: err = %v", err)
	}
	if v, err := check(c, "ollama"); err != nil || v != "ollama" {
		t.Errorf("ollama provider: %v, %v", v, err)
	}

	if err := reg.Set(c, "expand_model", "qwen3.5:9b"); err != nil || c.ExpandModel != "qwen3.5:9b" {
		t.Errorf("expand_model: %v (%q)", err, c.ExpandModel)
	}
}

func TestH2bValidateSettings(t *testing.T) {
	// Auth off with the default (loopback) address is allowed.
	if err := validateSettings(&OnboardResult{Auth: "off"}); err != nil {
		t.Errorf("auth off on the default address: %v", err)
	}
	err := validateSettings(&OnboardResult{RerankEnabled: true, RerankProvider: string(gleann.RerankerEIF)})
	if gleann.EIFRerankAvailable() != (err == nil) {
		t.Errorf("reranking with the built-in engine: err = %v", err)
	}
}

func TestH2bLoadSettingsUnreadableConfig(t *testing.T) {
	home := h2bHome(t)
	// config.json is a directory: it exists but cannot be read.
	if err := os.MkdirAll(filepath.Join(home, ".gleann", "config.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if c, err := loadSettings(); err == nil || c != nil {
		t.Errorf("loadSettings = %+v, %v; want an error", c, err)
	}
}

// A change re-reads the config from disk; when an edit made elsewhere hid
// rows, the cursor is kept inside the shorter list.
func TestH2bSettingsCursorStaysInsideAfterRowsDisappear(t *testing.T) {
	m := h2bSettingsWith(t, OnboardResult{})
	m.cfg.RerankEnabled = true // shown here, but not on disk
	m.cfg.QueryExpand = true
	rows := m.rows()
	m.cursor = len(rows) - 2 // the last setting
	key := rows[m.cursor]
	if !steppable(m.field(key)) {
		t.Fatalf("last row %s is not steppable", key)
	}
	m = h2bPress(m, "right").(SettingsModel)
	if m.err != nil {
		t.Fatal(m.err)
	}
	if n := len(m.rows()); m.cursor != n-1 || n >= len(rows) {
		t.Errorf("cursor = %d, rows %d -> %d", m.cursor, len(rows), n)
	}
}
