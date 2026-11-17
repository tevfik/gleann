package gleann

import "testing"

func TestHNSWDTypeSwitch(t *testing.T) {
	for _, tc := range []struct {
		env, cfg, want string
		wantErr        bool
	}{
		{"", "", "", false},
		{"", "F16", "f16", false},
		{"f16", "f32", "f16", false},
		{"fp16", "f32", "f32", false}, // a typo in the environment is ignored
		{"", "half", "", true},
	} {
		t.Setenv("GLEANN_HNSW_DTYPE", tc.env)
		got, err := hnswDTypeSwitch(tc.cfg)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("env %q cfg %q: got %q, %v; want %q (err %v)", tc.env, tc.cfg, got, err, tc.want, tc.wantErr)
		}
	}
}
