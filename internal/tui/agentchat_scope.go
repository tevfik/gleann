package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/tevfik/agenttui"

	"github.com/tevfik/gleann/pkg/gleann"
)

// SearcherLoader opens a searcher over the named indexes (one name: a
// *LeannSearcher, several: a *MultiSearcher). The CLI sets it; without it the
// chat cannot change which indexes it retrieves from.
var SearcherLoader func(ctx context.Context, names []string) (gleann.Searcher, error)

// NewSearcherLoader returns a SearcherLoader that opens indexes with the given
// config and embedder.
func NewSearcherLoader(cfg gleann.Config, embedder gleann.EmbeddingComputer) func(context.Context, []string) (gleann.Searcher, error) {
	searchIndexDir = cfg.IndexDir
	return func(ctx context.Context, names []string) (gleann.Searcher, error) {
		if len(names) == 1 {
			s := gleann.NewSearcher(cfg, embedder)
			if err := s.Load(ctx, names[0]); err != nil {
				return nil, err
			}
			return s, nil
		}
		return gleann.LoadMultiSearcher(ctx, cfg, embedder, names)
	}
}

// indexInfo is what the retrieval panel and the scope picker show about an index.
type indexInfo struct {
	Name        string
	Passages    int
	Tags        []string
	Description string
	Graph       bool
}

// searchIndexDir is the directory the loader opens indexes from; the picker
// must list the same one.
var searchIndexDir string

// listIndexInfo reads index metadata from disk.
func listIndexInfo() []indexInfo {
	dir := searchIndexDir
	if dir == "" {
		dir = DefaultIndexDir()
		if cfg := LoadSavedConfig(); cfg != nil && cfg.IndexDir != "" {
			dir = cfg.IndexDir
		}
	}
	metas, err := gleann.ListIndexes(dir)
	if err != nil {
		return nil
	}
	out := make([]indexInfo, 0, len(metas))
	for _, m := range metas {
		_, gerr := os.Stat(filepath.Join(dir, m.Name+"_graph"))
		out = append(out, indexInfo{Name: m.Name, Passages: m.NumPassages, Tags: m.Tags, Description: m.Description, Graph: gerr == nil})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// tagsOf returns the sorted distinct tags across indexes (lower-cased).
func tagsOf(infos []indexInfo) []string {
	seen := map[string]bool{}
	var tags []string
	for _, in := range infos {
		for _, t := range in.Tags {
			t = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(t, "@")))
			if t != "" && !seen[t] {
				seen[t] = true
				tags = append(tags, t)
			}
		}
	}
	sort.Strings(tags)
	return tags
}

// resolveScope turns picked index names and tags into the final ordered,
// de-duplicated index list: the named indexes plus every index carrying a picked tag.
func resolveScope(infos []indexInfo, names, tags []string) []string {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	for _, in := range infos {
		for _, t := range tags {
			for _, it := range in.Tags {
				if strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(it), "@"), strings.TrimPrefix(t, "@")) {
					want[in.Name] = true
				}
			}
		}
	}
	var out []string
	for _, in := range infos { // keep disk order, drop unknown names
		if want[in.Name] {
			out = append(out, in.Name)
		}
	}
	return out
}

// parseScopeArg understands "a,b", "a b", "#tag", "tag:x", "@tag" and mixes of
// them. #tag is the one to type in the chat: "@" opens the file-completion popup there.
func parseScopeArg(infos []indexInfo, arg string) (names []string, err error) {
	var idx, tags []string
	known := map[string]bool{}
	for _, in := range infos {
		known[in.Name] = true
	}
	tagSet := map[string]bool{}
	for _, t := range tagsOf(infos) {
		tagSet[t] = true
	}
	for _, f := range strings.FieldsFunc(arg, func(r rune) bool { return r == ',' || r == ' ' }) {
		if strings.HasPrefix(f, "@") || strings.HasPrefix(f, "#") || strings.HasPrefix(strings.ToLower(f), "tag:") {
			t := strings.ToLower(strings.TrimLeft(f, "@#"))
			t = strings.TrimPrefix(t, "tag:")
			if !tagSet[t] {
				return nil, fmt.Errorf("no index has tag %q", t)
			}
			tags = append(tags, t)
			continue
		}
		if !known[f] {
			return nil, fmt.Errorf("index %q not found", f)
		}
		idx = append(idx, f)
	}
	return resolveScope(infos, idx, tags), nil
}

// retrievalState is the retrieval panel's data, readable without the chat lock.
type retrievalState struct {
	mu       sync.Mutex
	infos    []indexInfo
	active   []string
	lastN    int
	lastFrom map[string]int // per-index hit counts of the last answer
}

func (r *retrievalState) setScope(infos []indexInfo, active []string) {
	r.mu.Lock()
	r.infos, r.active = infos, append([]string(nil), active...)
	r.mu.Unlock()
}

func (r *retrievalState) setLast(results []gleann.SearchResult) {
	from := map[string]int{}
	for _, res := range results {
		if n, ok := res.Metadata["_index"].(string); ok {
			from[n]++
		}
	}
	r.mu.Lock()
	r.lastN, r.lastFrom = len(results), from
	r.mu.Unlock()
}

// retrievalPanel is the sidebar widget listing the indexes, which of them the
// chat retrieves from, and what the last answer was grounded in.
type retrievalPanel struct{ st *retrievalState }

func (p retrievalPanel) Title() string                             { return "retrieval" }
func (p retrievalPanel) Visible() bool                             { return true }
func (p retrievalPanel) Update(tea.Msg) (agenttui.Widget, tea.Cmd) { return p, nil }

func (p retrievalPanel) Render(w, h int) string {
	p.st.mu.Lock()
	defer p.st.mu.Unlock()
	if len(p.st.infos) == 0 {
		return "(no indexes found)"
	}
	active := map[string]bool{}
	for _, n := range p.st.active {
		active[n] = true
	}
	fit := func(s string) string {
		r := []rune(s)
		if w > 5 && len(r) > w-2 {
			return string(r[:w-3]) + "…"
		}
		return s
	}
	var lines []string
	for _, in := range p.st.infos {
		mark := "○"
		if active[in.Name] {
			mark = "●"
		}
		line := fmt.Sprintf("%s %s %s", mark, in.Name, compactCount(in.Passages))
		if in.Graph {
			line += " ⛓"
		}
		lines = append(lines, fit(line))
		if active[in.Name] && len(in.Tags) > 0 {
			lines = append(lines, fit("    #"+strings.Join(in.Tags, " #")))
		}
	}
	if p.st.lastN > 0 {
		lines = append(lines, "", fit(fmt.Sprintf("last: %d passages", p.st.lastN)))
		var names []string
		for n := range p.st.lastFrom {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			lines = append(lines, fit(fmt.Sprintf("  %s ×%d", n, p.st.lastFrom[n])))
		}
	}
	lines = append(lines, "", fit("/index to change"))
	if h > 0 && len(lines) > h {
		lines = append(lines[:h-1], fit(fmt.Sprintf("… +%d more (/index)", len(lines)-h+1)))
	}
	return strings.Join(lines, "\n")
}

// compactCount renders passage counts as 340, 1.2k, 12k.
func compactCount(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 10000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%dk", n/1000)
}

// currentScope returns the indexes the chat retrieves from.
func (ac *agentChat) currentScope() []string {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	return ac.scopeLocked()
}

func (ac *agentChat) scopeLocked() []string {
	if ac.cm.indexName == "" || ac.cm.indexName == "(no index)" {
		return nil
	}
	return append([]string{ac.cm.indexName}, ac.cm.activeIndexes...)
}

// refreshRetrieval re-reads index metadata for the sidebar panel.
func (ac *agentChat) refreshRetrieval() {
	ac.retr.setScope(listIndexInfo(), ac.currentScope())
}

// setScope makes the chat retrieve from exactly names: it opens a new searcher,
// swaps it in, restores the reranker and closes the old one.
func (ac *agentChat) setScope(ctx context.Context, names []string) (string, error) {
	if len(names) == 0 {
		return "", errors.New("select at least one index")
	}
	if SearcherLoader == nil {
		return "", errors.New("switching indexes is not available in this session")
	}
	cur := ac.currentScope()
	if strings.Join(cur, ",") == strings.Join(names, ",") {
		return "Index selection unchanged.", nil
	}
	s, err := SearcherLoader(ctx, names)
	if err != nil {
		return "", fmt.Errorf("load %s: %w", strings.Join(names, ", "), err)
	}
	ac.mu.Lock()
	old := ac.cm.chat.GetSearcher()
	ac.cm.chat.SetSearcher(s)
	ac.cm.indexName, ac.cm.activeIndexes = names[0], append([]string(nil), names[1:]...)
	ac.cm.applyReranker()
	ac.lastHeader, ac.lastWide, ac.lastRows = "", "", nil
	ac.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	ac.refreshRetrieval()
	return "✅ Retrieving from: " + strings.Join(names, ", "), nil
}

// confirm asks a yes/no question; the default answer is No.
func confirm(ctx context.Context, a *agenttui.App, text string) bool {
	ans, err := a.Ask(ctx, agenttui.Question{Text: text, Options: []string{"Yes", "No"}, Default: "No"})
	return err == nil && ans == "Yes"
}

// editScope shows the picker: one switch per tag and per index. A ticked tag
// selects every index carrying it.
func (ac *agentChat) editScope(ctx context.Context, a *agenttui.App) (string, bool, error) {
	infos := listIndexInfo()
	if len(infos) == 0 {
		return "No indexes found. Build one with `gleann index build <name> --docs <dir>`.", true, nil
	}
	activeSet := map[string]bool{}
	for _, n := range ac.currentScope() {
		activeSet[n] = true
	}
	onoff := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}
	tags := tagsOf(infos)
	var fields []agenttui.Field
	for _, t := range tags {
		var members []string
		for _, in := range infos {
			if resolveScope([]indexInfo{in}, nil, []string{t}) != nil {
				members = append(members, in.Name)
			}
		}
		fields = append(fields, agenttui.Field{Label: "#" + t, Kind: agenttui.FieldBool, Value: "off",
			Hint: "all indexes tagged " + t + ": " + strings.Join(members, ", ")})
	}
	for _, in := range infos {
		hint := fmt.Sprintf("%d passages", in.Passages)
		if in.Graph {
			hint += " · code graph (@symbol works)"
		}
		if len(in.Tags) > 0 {
			hint += " · #" + strings.Join(in.Tags, " #")
		}
		if in.Description != "" {
			hint += " · " + in.Description
		}
		fields = append(fields, agenttui.Field{Label: in.Name, Kind: agenttui.FieldBool, Value: onoff(activeSet[in.Name]), Hint: hint})
	}
	vals, err := a.Form(ctx, agenttui.Form{
		Title:  "Retrieval scope",
		Fields: fields,
		Note:   "Tick one or more indexes. Ticking a #tag also selects every index that carries it.",
	})
	if errors.Is(err, agenttui.ErrAskCancelled) {
		return "Index selection unchanged.", true, nil
	}
	if err != nil {
		return "", true, err
	}
	var pickedTags, pickedIdx []string
	for i, t := range tags {
		if vals[i] == "on" {
			pickedTags = append(pickedTags, t)
		}
	}
	for i, in := range infos {
		if vals[len(tags)+i] == "on" {
			pickedIdx = append(pickedIdx, in.Name)
		}
	}
	msg, err := ac.setScope(ctx, resolveScope(infos, pickedIdx, pickedTags))
	if err != nil {
		return "⚠ " + err.Error(), true, nil
	}
	return msg, true, nil
}

// indexCommand implements /index: no argument opens the picker.
func (ac *agentChat) indexCommand(ctx context.Context, args []string, a *agenttui.App) (string, bool, error) {
	arg := strings.TrimSpace(strings.Join(args, " "))
	switch {
	case arg == "":
		return ac.editScope(ctx, a)
	case arg == "info" || arg == "list":
		return ac.withState(func(cm *ChatModel) string { return cm.handleIndexCommand("info") }) + ac.scopeSummary(), true, nil
	}
	infos := listIndexInfo()
	cur := ac.currentScope()
	var target []string
	var err error
	switch {
	case strings.HasPrefix(arg, "add "):
		var add []string
		if add, err = parseScopeArg(infos, strings.TrimPrefix(arg, "add ")); err == nil {
			target = resolveScope(infos, append(append([]string(nil), cur...), add...), nil)
		}
	case strings.HasPrefix(arg, "remove "):
		var rm []string
		if rm, err = parseScopeArg(infos, strings.TrimPrefix(arg, "remove ")); err == nil {
			drop := map[string]bool{}
			for _, n := range rm {
				drop[n] = true
			}
			var keep []string
			for _, n := range cur {
				if !drop[n] {
					keep = append(keep, n)
				}
			}
			target = keep
		}
	default:
		target, err = parseScopeArg(infos, arg)
	}
	if err != nil {
		return "⚠ " + err.Error() + ". Usage: /index [name|#tag|a,b | add X | remove X | info]", true, nil
	}
	msg, err := ac.setScope(ctx, target)
	if err != nil {
		return "⚠ " + err.Error(), true, nil
	}
	return msg, true, nil
}

// scopeSummary lists every index with its tags, marking the active ones.
func (ac *agentChat) scopeSummary() string {
	infos := listIndexInfo()
	if len(infos) == 0 {
		return ""
	}
	active := map[string]bool{}
	for _, n := range ac.currentScope() {
		active[n] = true
	}
	var sb strings.Builder
	sb.WriteString("\n\n**Indexes** (● = searched):\n")
	for _, in := range infos {
		mark := "○"
		if active[in.Name] {
			mark = "●"
		}
		fmt.Fprintf(&sb, "\n%s %s — %d passages", mark, in.Name, in.Passages)
		if in.Graph {
			sb.WriteString(" · graph")
		}
		if len(in.Tags) > 0 {
			sb.WriteString(" · #" + strings.Join(in.Tags, " #"))
		}
	}
	sb.WriteString("\n\n`/index` pick · `/index #tag` · `/index a,b` · `/index add|remove <name>`")
	return sb.String()
}
