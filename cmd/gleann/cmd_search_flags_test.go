package main

import "testing"

func TestStripSearchFlags(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"auth", "validation"}, "auth validation"},
		{[]string{"--json", "handler"}, "handler"},
		{[]string{"--rerank", "auth", "validation"}, "auth validation"},
		{[]string{"auth", "--top-k", "3", "validation"}, "auth validation"},
		{[]string{"--top-k=3", "auth"}, "auth"},
		{[]string{"--", "--name", "test"}, "--name test"},
		{[]string{"auth", "--json"}, "auth"},
	}
	for _, c := range cases {
		if got := stripSearchFlags(c.in); got != c.want {
			t.Errorf("stripSearchFlags(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
