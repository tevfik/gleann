package main

import (
	"testing"

	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/pkg/gleann"
)

func TestSavedProxyContextIntroReachesConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := tui.SaveConfig(tui.OnboardResult{ProxyContextIntro: "Read the notes.", Completed: true}); err != nil {
		t.Fatal(err)
	}
	cfg := gleann.DefaultConfig()
	applySavedConfig(&cfg, nil)
	if cfg.ProxyContextIntro != "Read the notes." {
		t.Fatalf("ProxyContextIntro = %q, want the saved text", cfg.ProxyContextIntro)
	}
}
