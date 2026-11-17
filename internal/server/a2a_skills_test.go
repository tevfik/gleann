package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/a2a"
	"github.com/tevfik/gleann/pkg/gleann"
)

func TestA2ASearch_TargetsIndexAndFormatsHits(t *testing.T) {
	s, _, _ := newAskServer(t)
	for _, md := range []map[string]any{{"target": "idx"}, {"index": "idx"}, {"project": "idx"}} {
		out, err := s.a2aSearchHandler(a2a.SkillContext{Query: "alpha beta", Metadata: md})
		if err != nil {
			t.Fatalf("%v: %v", md, err)
		}
		if !strings.HasPrefix(out, "[idx] (score: ") {
			t.Errorf("%v: unexpected output %q", md, out)
		}
	}
}

func TestA2ASearch_NoTargetSearchesAccessibleIndexes(t *testing.T) {
	s, _, _ := newAskServer(t)
	out, err := s.a2aSearchHandler(a2a.SkillContext{Query: "alpha"})
	if err != nil || !strings.Contains(out, "[idx]") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	// Hits are capped: results stop once 10 are collected and long text is cut.
	if n := strings.Count(out, "[idx]"); n > 10 {
		t.Errorf("more than 10 hits returned: %d", n)
	}
}

func TestA2ASearch_Errors(t *testing.T) {
	s, _, _ := newAskServer(t)
	if _, err := s.a2aSearchHandler(a2a.SkillContext{Query: "q", Metadata: map[string]any{"target": "missing"}}); err == nil ||
		!strings.Contains(err.Error(), "not accessible") {
		t.Errorf("unknown index: %v", err)
	}
	if _, err := s.a2aSearchHandler(a2a.SkillContext{Query: "q", Metadata: map[string]any{"target": "../etc"}}); err == nil {
		t.Error("invalid index name must be refused")
	}

	empty := &Server{config: gleann.Config{IndexDir: t.TempDir()}, searchers: map[string]*gleann.LeannSearcher{}}
	if _, err := empty.a2aSearchHandler(a2a.SkillContext{Query: "q"}); err == nil ||
		!strings.Contains(err.Error(), "no indexes available") {
		t.Errorf("no indexes: %v", err)
	}
}

func TestA2ASearch_TagFilterHidesIndexes(t *testing.T) {
	s, _, _ := newAskServer(t)
	t.Setenv("GLEANN_TAGS", "nothing-has-this")
	out, err := s.a2aSearchHandler(a2a.SkillContext{Query: "alpha"})
	if err != nil || out != "No accessible indexes found" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestA2AAsk_AnswersFromLLM(t *testing.T) {
	s, _, rec := newAskServer(t)
	out, err := s.a2aAskHandler(a2a.SkillContext{Query: "what is alpha?", Metadata: map[string]any{"index": "idx", "llm_model": "m2"}})
	if err != nil || out != "the answer" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if rec.last(t)["model"] != "m2" {
		t.Errorf("llm_model metadata ignored: %v", rec.last(t)["model"])
	}
	// Without a target the first accessible index is used.
	if out, err := s.a2aAskHandler(a2a.SkillContext{Query: "q"}); err != nil || out != "the answer" {
		t.Fatalf("default index: out=%q err=%v", out, err)
	}
}

func TestA2AAsk_AttachmentsGoToTheVisionPath(t *testing.T) {
	s, _, rec := newAskServer(t)
	_, err := s.a2aAskHandler(a2a.SkillContext{
		Query:    "describe",
		Metadata: map[string]any{"target": "idx"},
		Files:    []a2a.FilePart{{Name: "empty"}, {Name: "pic.png", Bytes: "QUJD"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	msgs := rec.last(t)["messages"].([]any)
	imgs, _ := msgs[len(msgs)-1].(map[string]any)["images"].([]any)
	if len(imgs) != 1 || imgs[0] != "QUJD" {
		t.Errorf("only attachments with bytes must be forwarded: %v", imgs)
	}
}

func TestA2AAsk_Errors(t *testing.T) {
	s, _, rec := newAskServer(t)
	if _, err := s.a2aAskHandler(a2a.SkillContext{Query: "q", Metadata: map[string]any{"target": "nope"}}); err == nil ||
		!strings.Contains(err.Error(), "not available or access denied") {
		t.Errorf("unknown index: %v", err)
	}
	rec.status = http.StatusInternalServerError
	if _, err := s.a2aAskHandler(a2a.SkillContext{Query: "q", Metadata: map[string]any{"target": "idx"}}); err == nil ||
		!strings.Contains(err.Error(), "RAG ask failed") {
		t.Errorf("LLM failure: %v", err)
	}
	if _, err := s.a2aAskHandler(a2a.SkillContext{Query: "q", Metadata: map[string]any{"target": "idx"},
		Files: []a2a.FilePart{{Bytes: "QUJD"}}}); err == nil || !strings.Contains(err.Error(), "multimodal RAG ask failed") {
		t.Errorf("LLM failure on vision path: %v", err)
	}

	empty := &Server{config: gleann.Config{IndexDir: t.TempDir()}, searchers: map[string]*gleann.LeannSearcher{}}
	if _, err := empty.a2aAskHandler(a2a.SkillContext{Query: "q"}); err == nil || !strings.Contains(err.Error(), "no indexes available") {
		t.Errorf("no indexes: %v", err)
	}
	t.Setenv("GLEANN_TAGS", "nothing-has-this")
	if _, err := s.a2aAskHandler(a2a.SkillContext{Query: "q"}); err == nil || !strings.Contains(err.Error(), "no accessible index") {
		t.Errorf("tag filter: %v", err)
	}
}

func TestA2AMultimodal_Validation(t *testing.T) {
	s, _, _ := newAskServer(t)
	if _, err := s.a2aMultimodalHandler(a2a.SkillContext{}); err == nil {
		t.Error("empty request must be refused")
	}
	out, err := s.a2aMultimodalHandler(a2a.SkillContext{Files: []a2a.FilePart{{Name: "a.png"}, {Name: "b.wav"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(out, "\n"); len(got) != 2 || !strings.HasPrefix(got[1], "[b.wav]") {
		t.Errorf("attachment summary = %q", out)
	}
}

func TestA2AMultimodal_PathMustBeInAllowedRoots(t *testing.T) {
	s, _, _ := newAskServer(t)
	allowed, outside := t.TempDir(), t.TempDir()
	t.Setenv("GLEANN_ALLOWED_ROOTS", allowed)
	f := filepath.Join(outside, "pic.png")
	if err := os.WriteFile(f, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := s.a2aMultimodalHandler(a2a.SkillContext{Query: f})
	if err == nil || !strings.Contains(err.Error(), "allowed_roots") {
		t.Fatalf("want allowed_roots refusal, got %v", err)
	}
}

func TestA2AMultimodal_DescribesImageThroughVisionModel(t *testing.T) {
	s, _, rec := newAskServer(t)
	dir := t.TempDir()
	t.Setenv("GLEANN_ALLOWED_ROOTS", dir)
	img := filepath.Join(dir, "pic.png")
	if err := os.WriteFile(img, []byte("\x89PNG fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.cfgMu.Lock()
	s.config.MultimodalModel = "vision-x"
	s.cfgMu.Unlock()

	out, err := s.a2aMultimodalHandler(a2a.SkillContext{Query: "  " + img + " "})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "the answer") || !strings.Contains(out, "Analysis of ") {
		t.Errorf("out = %q", out)
	}
	if rec.last(t)["model"] != "vision-x" {
		t.Errorf("model = %v", rec.last(t)["model"])
	}

	rec.status = http.StatusInternalServerError
	img2 := filepath.Join(dir, "other.png")
	_ = os.WriteFile(img2, []byte("different bytes"), 0o644)
	if _, err := s.a2aMultimodalHandler(a2a.SkillContext{Query: img2}); err == nil || !strings.Contains(err.Error(), "analysis failed") {
		t.Errorf("vision model failure: %v", err)
	}
}
