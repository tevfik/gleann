package gleann

import "testing"

func TestDefaultChatConfigEnv(t *testing.T) {
	cases := []struct {
		name      string
		env       map[string]string
		wantThink bool
		wantPen   float64
	}{
		{"defaults", nil, false, 1.1},
		{"think numeric", map[string]string{"GLEANN_THINK": "1"}, true, 1.1},
		{"think word", map[string]string{"GLEANN_THINK": "TRUE"}, true, 1.1},
		{"think other value", map[string]string{"GLEANN_THINK": "no"}, false, 1.1},
		{"alias penalty", map[string]string{"GLEANN_REPETITION_PENALTY": "1.4"}, false, 1.4},
		{"alias penalty garbage", map[string]string{"GLEANN_REPETITION_PENALTY": "x"}, false, 1.1},
		{"alias penalty not positive", map[string]string{"GLEANN_REPETITION_PENALTY": "-2"}, false, 1.1},
		{"primary beats alias", map[string]string{"GLEANN_REPEAT_PENALTY": "1.2", "GLEANN_REPETITION_PENALTY": "1.9"}, false, 1.2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, k := range []string{"GLEANN_THINK", "GLEANN_REPEAT_PENALTY", "GLEANN_REPETITION_PENALTY"} {
				t.Setenv(k, "")
			}
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			cfg := DefaultChatConfig()
			if cfg.Think == nil || *cfg.Think != c.wantThink || cfg.RepeatPenalty != c.wantPen {
				t.Errorf("think=%v penalty=%v, want %v %v", cfg.Think, cfg.RepeatPenalty, c.wantThink, c.wantPen)
			}
		})
	}
}
