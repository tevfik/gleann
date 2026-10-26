package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func typeModel(t *testing.T, m OnboardModel, s string) OnboardModel {
	t.Helper()
	for _, r := range s {
		res, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = res.(OnboardModel)
	}
	return m
}

func pressKey(m OnboardModel, k tea.KeyPressMsg) OnboardModel {
	res, _ := m.Update(k)
	return res.(OnboardModel)
}

var (
	kDown  = tea.KeyPressMsg{Code: tea.KeyDown}
	kUp    = tea.KeyPressMsg{Code: tea.KeyUp}
	kEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	kTab   = tea.KeyPressMsg{Code: tea.KeyTab}
)

// toCustom moves down until the Custom… row is selected.
func toCustom(t *testing.T, m OnboardModel) OnboardModel {
	t.Helper()
	for i := 0; i < 50 && !m.onCustom; i++ {
		m = pressKey(m, kDown)
	}
	if !m.onCustom {
		t.Fatal("Custom… row never selected")
	}
	return m
}

// The LLM list could not be fetched (e.g. a server without /api/tags): a
// model typed on the Custom… row is saved, not a fallback suggestion.
func TestOnboardCustomLLMModelWhenFetchFails(t *testing.T) {
	m := NewOnboardModel()
	m = pressKey(m, kEnter) // leave the quick/advanced screen untouched below
	m.phase = phaseLLMFetching
	res, _ := m.Update(modelsFetchedMsg{err: errors.New("404"), forLLM: true})
	m = res.(OnboardModel)
	if m.phase != phaseLLMModel {
		t.Fatalf("phase = %v", m.phase)
	}
	m = toCustom(t, m)
	m = typeModel(t, m, "jamba-gpt-oss-20b") // j/k type on this row
	if got := m.modelInput.Value(); got != "jamba-gpt-oss-20b" {
		t.Fatalf("typed %q", got)
	}
	m = pressKey(m, kEnter)
	if m.phase != phaseReranker {
		t.Fatalf("enter did not confirm: phase %v", m.phase)
	}
	m.buildResult()
	if m.result.LLMModel != "jamba-gpt-oss-20b" {
		t.Errorf("saved LLM model %q", m.result.LLMModel)
	}
}

// j/k and tab keep their list meaning; the field only takes keys on its row.
func TestOnboardCustomRowLeavesListKeysAlone(t *testing.T) {
	m := NewOnboardModel()
	m.phase = phaseEmbModel
	m.embAllModels = []ModelInfo{{Name: "bge-m3"}, {Name: "llama3"}, {Name: "nomic-embed-text"}}
	m.embModels = filterEmbeddingModels(m.embAllModels)
	m = typeModel(t, m, "j")
	if m.embModelIdx != 1 || m.onCustom || m.modelInput.Value() != "" {
		t.Fatalf("j on the list: idx %d custom %v input %q", m.embModelIdx, m.onCustom, m.modelInput.Value())
	}
	m = pressKey(m, kTab)
	if !m.embShowAll || m.onCustom {
		t.Fatal("tab must toggle the filter, not open the field")
	}
	m = toCustom(t, m)
	m = typeModel(t, m, "x")
	m = pressKey(m, kUp)
	if m.onCustom {
		t.Fatal("up must return to the list")
	}
	// Enter on a list entry ignores the text left in the field.
	m = pressKey(m, kEnter)
	m.buildResult()
	if m.result.EmbeddingModel == "x" {
		t.Error("text in the field was used although a list entry was selected")
	}
}

func TestOnboardCustomRowEmptyEnterStays(t *testing.T) {
	m := NewOnboardModel()
	m.phase = phaseLLMModel
	m.llmModels = []ModelInfo{{Name: "a"}}
	m.llmAllModels = m.llmModels
	m = toCustom(t, m)
	m = pressKey(m, kEnter)
	if m.phase != phaseLLMModel || !m.onCustom {
		t.Fatalf("empty Custom… must not confirm: phase %v custom %v", m.phase, m.onCustom)
	}
	if !strings.Contains(m.View().Content, "Custom…") {
		t.Error("view lacks the Custom… row")
	}
}

// Reopening a setting keeps the saved model selected even when the service
// does not list it, instead of saving the first default.
func TestOnboardKeepsCurrentModelNotInList(t *testing.T) {
	cfg := &OnboardResult{EmbeddingProvider: "ollama", EmbeddingModel: "bge-m3", LLMProvider: "ollama", LLMModel: "gpt-oss-20b"}
	m := NewOnboardModelWithConfig(cfg)
	res, _ := m.Update(modelsFetchedMsg{err: errors.New("404"), forLLM: true})
	m = res.(OnboardModel)
	if m.llmModels[m.llmModelIdx].Name != "gpt-oss-20b" {
		t.Fatalf("selected %q, want the saved gpt-oss-20b", m.llmModels[m.llmModelIdx].Name)
	}
	m = pressKey(m, kEnter)
	m.buildResult()
	if m.result.LLMModel != "gpt-oss-20b" {
		t.Errorf("saved %q", m.result.LLMModel)
	}

	m = NewOnboardModelWithConfig(cfg)
	res, _ = m.Update(modelsFetchedMsg{models: []ModelInfo{{Name: "llama3:8b"}}, forLLM: true})
	m = res.(OnboardModel)
	if m.llmModels[m.llmModelIdx].Name != "gpt-oss-20b" {
		t.Errorf("fetched list: selected %q", m.llmModels[m.llmModelIdx].Name)
	}
}

// Local providers save Tag (a file path); a custom path must land there.
func TestOnboardCustomEmbeddingPathForLocalProvider(t *testing.T) {
	m := NewOnboardModel()
	for i, p := range m.embProviders {
		if p == "llamacpp" {
			m.embProviderIdx = i
		}
	}
	m.phase = phaseEmbModel
	m.embModels = []ModelInfo{{Name: "(Download) BGE-M3", Tag: "bge-m3-q4_k_m.gguf"}}
	m.embAllModels = m.embModels
	m = toCustom(t, m)
	m = typeModel(t, m, "/models/e5.gguf")
	m = pressKey(m, kEnter)
	m.buildResult()
	if m.result.EmbeddingModel != "/models/e5.gguf" {
		t.Errorf("saved %q", m.result.EmbeddingModel)
	}
}

func TestOnboardCustomCodeEmbeddingModel(t *testing.T) {
	m := NewOnboardModel()
	m.phase = phaseCodeEmbModel
	m.embModels = []ModelInfo{{Name: "bge-m3"}}
	m.embAllModels = m.embModels
	m = toCustom(t, m)
	m = typeModel(t, m, "jina-embeddings-v2-base-code")
	m = pressKey(m, kEnter)
	if m.phase != phaseDocEmbModel {
		t.Fatalf("phase %v", m.phase)
	}
	if m.codeEmbeddingModel != "jina-embeddings-v2-base-code" {
		t.Errorf("code model %q", m.codeEmbeddingModel)
	}
}

func TestOnboardCustomRerankModel(t *testing.T) {
	m := NewOnboardModel()
	m.phase = phaseRerankModel
	m.rerankEnabled = true
	m.rerankAllModels = []ModelInfo{{Name: "bge-reranker-v2-m3"}, {Name: "llama3"}}
	m.rerankModels = filterRerankerModels(m.rerankAllModels)
	m = toCustom(t, m)
	m = typeModel(t, m, "jina-reranker-v2-base-multilingual")
	m = pressKey(m, kEnter)
	m.buildResult()
	if m.result.RerankModel != "jina-reranker-v2-base-multilingual" {
		t.Errorf("rerank model %q", m.result.RerankModel)
	}
}
