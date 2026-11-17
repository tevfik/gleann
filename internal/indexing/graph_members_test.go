package indexing

import (
	"reflect"
	"testing"
)

func TestMemberIndexResolve(t *testing.T) {
	m := make(memberIndex)
	m.add("mod/p.A::add", "A::add")
	m.add("mod/p.B::add", "B::add")
	m.add("mod/p.A::get", "A::get")
	m.add("mod/p.helper", "helper") // not a member: ignored
	m.add("mod/p.A::get", "A::get") // duplicate: ignored

	cases := []struct {
		name, src, dst string
		want           []string
		conf           string
	}{
		{"unique member", "run", "mod/p.get", []string{"mod/p.A::get"}, "inferred"},
		{"ambiguous across classes", "run", "mod/p.add", []string{"mod/p.A::add", "mod/p.B::add"}, "ambiguous"},
		{"own class wins", "B::go", "mod/p.add", []string{"mod/p.B::add"}, "inferred"},
		{"no such member", "run", "mod/p.nope", nil, ""},
		{"plain function is not a member", "run", "mod/p.helper", nil, ""},
		{"other package", "run", "mod/q.add", nil, ""},
	}
	for _, c := range cases {
		got, conf := m.resolve(c.src, c.dst)
		if !reflect.DeepEqual(got, c.want) || conf != c.conf {
			t.Errorf("%s: resolve(%q, %q) = %v %q, want %v %q", c.name, c.src, c.dst, got, conf, c.want, c.conf)
		}
	}

	// Too many same-named members: the call stays unresolved.
	big := make(memberIndex)
	for i := 0; i <= maxMemberCandidates; i++ {
		fqn := "p.C" + string(rune('a'+i)) + "::run"
		big.add(fqn, "C"+string(rune('a'+i))+"::run")
	}
	if got, _ := big.resolve("x", "p.run"); got != nil {
		t.Errorf("resolved %d candidates, want none above the cap", len(got))
	}

	// A root-level member (no package prefix).
	root := make(memberIndex)
	root.add("A::m", "A::m")
	if got, _ := root.resolve("f", "m"); !reflect.DeepEqual(got, []string{"A::m"}) {
		t.Errorf("root member = %v", got)
	}
}
