package indexing

import (
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestNewMultimodalProcessor_AudioPluginWithoutModel(t *testing.T) {
	h := t.TempDir() // no plugin registry
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	t.Setenv("GLEANN_MULTIMODAL_MODEL", "")
	t.Setenv("GLEANN_AUDIO_PLUGIN_URL", "")
	if p := NewMultimodalProcessor("", "", "", nil); p != nil {
		t.Fatal("no model and no plugin must stay disabled")
	}
	t.Setenv("GLEANN_AUDIO_PLUGIN_URL", "http://127.0.0.1:1")
	p := NewMultimodalProcessor("", "", "", nil)
	if p == nil || !p.PluginOnly {
		t.Fatalf("audio plugin alone should enable a plugin-only processor, got %+v", p)
	}
	if NewMultimodalProcessor("", "", "off", nil) != nil {
		t.Fatal("explicit off must still disable")
	}
}

func TestEnsureAudioPlugin_NoOpCases(t *testing.T) {
	p := newPrinter(nil, nil)
	ensureAudioPlugin(nil, "a.wav", p) // nil manager

	pm := &gleann.PluginManager{Registry: &gleann.PluginRegistry{Plugins: []gleann.Plugin{{
		Name: "snd", URL: "http://127.0.0.1:1", Capabilities: []string{gleann.CapAudioTranscription},
		Command: []string{"/nonexistent-binary"},
	}}}}
	t.Setenv("GLEANN_AUDIO_PLUGIN_URL", "http://127.0.0.1:1")
	ensureAudioPlugin(pm, "pic.png", p) // not audio: must not try to start the (broken) command

	t.Setenv("GLEANN_AUDIO_PLUGIN_URL", "http://other:9")
	ensureAudioPlugin(pm, "a.wav", p) // env points elsewhere: leave the registry plugin alone
}
