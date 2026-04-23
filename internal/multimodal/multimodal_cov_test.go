package multimodal

import (
	"testing"
)

// ── applyModelHeuristics ──────────────────────────────────────

func TestApplyModelHeuristics_Llava(t *testing.T) {
	caps := &ModelCapabilities{Name: "llava:latest"}
	applyModelHeuristics(caps)
	if !caps.Vision {
		t.Error("expected Vision=true for llava")
	}
	if !caps.Multimodal {
		t.Error("expected Multimodal=true for llava")
	}
}

func TestApplyModelHeuristics_Gemma4(t *testing.T) {
	caps := &ModelCapabilities{Name: "gemma4:27b"}
	applyModelHeuristics(caps)
	if !caps.Vision {
		t.Error("expected Vision=true for gemma4")
	}
	if !caps.Audio {
		t.Error("expected Audio=true for gemma4")
	}
	if !caps.Multimodal {
		t.Error("expected Multimodal=true for gemma4")
	}
}

func TestApplyModelHeuristics_QwenVL(t *testing.T) {
	caps := &ModelCapabilities{Name: "qwen2.5-vl:7b"}
	applyModelHeuristics(caps)
	if !caps.Vision {
		t.Error("expected Vision=true for qwen2.5-vl")
	}
	if !caps.Multimodal {
		t.Error("expected Multimodal=true for qwen2.5-vl")
	}
}

func TestApplyModelHeuristics_Qwen3VL(t *testing.T) {
	caps := &ModelCapabilities{Name: "qwen3-vl:4b"}
	applyModelHeuristics(caps)
	if !caps.Vision {
		t.Error("expected Vision=true for qwen3-vl")
	}
}

func TestApplyModelHeuristics_MiniCPMV(t *testing.T) {
	caps := &ModelCapabilities{Name: "minicpm-v:latest"}
	applyModelHeuristics(caps)
	if !caps.Vision {
		t.Error("expected Vision=true for minicpm-v")
	}
}

func TestApplyModelHeuristics_Moondream(t *testing.T) {
	caps := &ModelCapabilities{Name: "moondream:1.8b"}
	applyModelHeuristics(caps)
	if !caps.Vision {
		t.Error("expected Vision=true for moondream")
	}
}

func TestApplyModelHeuristics_Bakllava(t *testing.T) {
	caps := &ModelCapabilities{Name: "bakllava:7b"}
	applyModelHeuristics(caps)
	if !caps.Vision {
		t.Error("expected Vision=true for bakllava")
	}
}

func TestApplyModelHeuristics_NonMultimodal(t *testing.T) {
	caps := &ModelCapabilities{Name: "llama3:8b"}
	applyModelHeuristics(caps)
	if caps.Vision {
		t.Error("expected Vision=false for llama3")
	}
	if caps.Audio {
		t.Error("expected Audio=false for llama3")
	}
	if caps.Multimodal {
		t.Error("expected Multimodal=false for llama3")
	}
}

func TestApplyModelHeuristics_AlreadyHasVision(t *testing.T) {
	caps := &ModelCapabilities{Name: "custom-model", Vision: true}
	applyModelHeuristics(caps)
	if !caps.Multimodal {
		t.Error("expected Multimodal=true when Vision is already true")
	}
}

// ── CanProcess ────────────────────────────────────────────────

func TestCanProcess_NoModel(t *testing.T) {
	p := &Processor{Model: ""}
	if p.CanProcess("photo.jpg") {
		t.Error("expected false when model is empty")
	}
}

func TestCanProcess_NonMultimodal(t *testing.T) {
	p := &Processor{Model: "llava"}
	if p.CanProcess("readme.txt") {
		t.Error("expected false for non-multimodal file")
	}
}

func TestCanProcess_Valid(t *testing.T) {
	p := &Processor{Model: "llava"}
	if !p.CanProcess("photo.jpg") {
		t.Error("expected true for image file with model set")
	}
}

func TestCanProcess_AudioValid(t *testing.T) {
	p := &Processor{Model: "gemma4"}
	if !p.CanProcess("speech.mp3") {
		t.Error("expected true for audio file with model set")
	}
}
