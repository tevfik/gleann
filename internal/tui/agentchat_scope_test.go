package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

var testInfos = []indexInfo{
	{Name: "api", Passages: 1200, Tags: []string{"work", "code"}, Graph: true},
	{Name: "docs", Passages: 340, Tags: []string{"Work"}},
	{Name: "notes", Passages: 15000},
}

func TestResolveScope(t *testing.T) {
	if got := strings.Join(resolveScope(testInfos, []string{"notes"}, []string{"work"}), ","); got != "api,docs,notes" {
		t.Errorf("tag must add every tagged index, case-insensitively, in disk order: %s", got)
	}
	if got := resolveScope(testInfos, []string{"ghost"}, nil); got != nil {
		t.Errorf("unknown names are dropped: %v", got)
	}
	if got := strings.Join(resolveScope(testInfos, nil, []string{"@code"}), ","); got != "api" {
		t.Errorf("@ prefix must be ignored: %s", got)
	}
}

func TestParseScopeArg(t *testing.T) {
	got, err := parseScopeArg(testInfos, "notes,@code")
	if err != nil || strings.Join(got, ",") != "api,notes" {
		t.Errorf("got %v %v", got, err)
	}
	for _, a := range []string{"#code", "tag:code", "@code", "TAG:Code"} {
		if got, err := parseScopeArg(testInfos, a); err != nil || strings.Join(got, ",") != "api" {
			t.Errorf("%s: %v %v", a, got, err)
		}
	}
	if _, err := parseScopeArg(testInfos, "ghost"); err == nil {
		t.Error("unknown index must error")
	}
	if _, err := parseScopeArg(testInfos, "@nope"); err == nil {
		t.Error("unknown tag must error")
	}
	if got := tagsOf(testInfos); strings.Join(got, ",") != "code,work" {
		t.Errorf("tags must be distinct and lower-cased: %v", got)
	}
}

func TestRetrievalPanelRender(t *testing.T) {
	st := &retrievalState{}
	st.setScope(testInfos, []string{"api"})
	st.setLast([]gleann.SearchResult{
		{Metadata: map[string]any{"_index": "api"}},
		{Metadata: map[string]any{"_index": "api"}},
		{Metadata: map[string]any{"_index": "docs"}},
	})
	out := retrievalPanel{st}.Render(40, 30)
	for _, want := range []string{"● api 1.2k ⛓", "#work #code", "○ docs 340", "○ notes 15k", "last: 3 passages", "api ×2", "docs ×1", "/index"} {
		if !strings.Contains(out, want) {
			t.Errorf("panel missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "#Work") && !strings.Contains(out, "● api") {
		t.Error("tags only show for active indexes")
	}
	short := retrievalPanel{st}.Render(40, 4)
	if n := len(strings.Split(short, "\n")); n != 4 || !strings.Contains(short, "more") {
		t.Errorf("must truncate to height with a hint (%d lines):\n%s", n, short)
	}
	if got := (retrievalPanel{&retrievalState{}}).Render(30, 10); !strings.Contains(got, "no indexes") {
		t.Errorf("empty state: %q", got)
	}
}

func TestAgentChat_SetScopeSwapsSearcher(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ac := newTestAgentChat(nil)
	ac.cm.indexName = "docs"

	old := SearcherLoader
	defer func() { SearcherLoader = old }()
	SearcherLoader = nil
	if _, err := ac.setScope(context.Background(), []string{"a"}); err == nil {
		t.Error("without a loader switching must fail clearly")
	}
	var asked []string
	SearcherLoader = func(_ context.Context, names []string) (gleann.Searcher, error) {
		asked = names
		if names[0] == "bad" {
			return nil, errors.New("boom")
		}
		return fakeSearcher{}, nil
	}
	if _, err := ac.setScope(context.Background(), nil); err == nil {
		t.Error("empty selection must be rejected")
	}
	if _, err := ac.setScope(context.Background(), []string{"bad"}); err == nil || ac.cm.indexName != "docs" {
		t.Errorf("a failed load must leave the scope untouched: %v %s", err, ac.cm.indexName)
	}
	msg, err := ac.setScope(context.Background(), []string{"api", "docs"})
	if err != nil || !strings.Contains(msg, "api, docs") {
		t.Fatalf("%q %v", msg, err)
	}
	if strings.Join(asked, ",") != "api,docs" || ac.cm.indexName != "api" || strings.Join(ac.cm.activeIndexes, ",") != "docs" {
		t.Errorf("state not updated: %v %s %v", asked, ac.cm.indexName, ac.cm.activeIndexes)
	}
	if got := indexLabel(ac.cm); got != "api+docs" {
		t.Errorf("label %q", got)
	}
	if msg, _ := ac.setScope(context.Background(), []string{"api", "docs"}); !strings.Contains(msg, "unchanged") {
		t.Errorf("same scope must be a no-op: %q", msg)
	}
}

func TestAgentChat_IndexCommandArgErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ac := newTestAgentChat(nil)
	out, local, err := ac.indexCommand(context.Background(), []string{"ghost"}, ac.app)
	if err != nil || !local || !strings.Contains(out, "not found") || !strings.Contains(out, "Usage") {
		t.Errorf("%q %v %v", out, local, err)
	}
}

func TestAgentChat_ForgetAsksFirst(t *testing.T) {
	ac := newTestAgentChat(nil)
	for _, c := range ac.commands() {
		if c.Name != "forget" {
			continue
		}
		// App is not running, so the confirmation cannot be answered: nothing is forgotten.
		out, _, _ := c.Handler(context.Background(), []string{"x"}, ac.app)
		if strings.Contains(out, "Forgot") {
			t.Errorf("must not delete without confirmation: %q", out)
		}
		return
	}
	t.Fatal("no /forget")
}
