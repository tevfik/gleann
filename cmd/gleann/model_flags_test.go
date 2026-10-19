package main

import "testing"

func TestWithModeModel(t *testing.T) {
	cases := []struct {
		name string
		args []string
		mode IndexMode
		want string
	}{
		{"code model for code index", []string{"idx", "--code-model", "granite"}, IndexModeCode, "granite"},
		{"doc model for docs index", []string{"idx", "--doc-model", "bge"}, IndexModeDocs, "bge"},
		{"code model ignored for docs index", []string{"idx", "--code-model", "granite"}, IndexModeDocs, ""},
		{"explicit --model wins", []string{"idx", "--model", "m", "--code-model", "granite"}, IndexModeCode, "m"},
		{"nothing given", []string{"idx"}, IndexModeCode, ""},
	}
	for _, c := range cases {
		args := withModeModel(c.args, c.mode)
		if got := getFlag(args, "--model"); got != c.want {
			t.Errorf("%s: --model = %q, want %q", c.name, got, c.want)
		}
		if modelExplicit(args) != (c.want != "") {
			t.Errorf("%s: modelExplicit = %v", c.name, modelExplicit(args))
		}
	}
}
