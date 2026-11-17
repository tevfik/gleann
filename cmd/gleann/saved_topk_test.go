package main

import (
	"testing"

	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/pkg/gleann"
)

func TestApplySavedConfig_TopK(t *testing.T) {
	cases := []struct {
		name  string
		saved int
		args  []string
		want  int
	}{
		{"no saved value keeps the default", 0, nil, gleann.DefaultConfig().SearchConfig.TopK},
		{"saved value applies", 4, nil, 4},
		{"flag wins over saved value", 4, []string{"--top-k", "7"}, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			if err := tui.SaveConfig(tui.OnboardResult{TopK: tc.saved, Completed: true}); err != nil {
				t.Fatal(err)
			}
			cfg := getConfig(tc.args)
			applySavedConfig(&cfg, tc.args)
			if cfg.SearchConfig.TopK != tc.want {
				t.Errorf("TopK = %d, want %d", cfg.SearchConfig.TopK, tc.want)
			}
		})
	}
}
