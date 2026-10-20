package main

import (
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestKnownPluginReposPointAtOwnRepository(t *testing.T) {
	for _, name := range []string{"docs", "marker", "sound"} {
		want := "https://github.com/tevfik/gleann-plugin-" + name
		if got := knownPluginRepos[name]; got != want {
			t.Errorf("%s -> %s, want %s", name, got, want)
		}
		if got := knownPluginRepos["gleann-plugin-"+name]; got != want {
			t.Errorf("gleann-plugin-%s -> %s, want %s", name, got, want)
		}
	}
}

func TestCatalogPluginForResolvesAliases(t *testing.T) {
	catalog := []gleann.PluginMeta{{Name: "gleann-plugin-docs"}, {Name: "gleann-plugin-sound"}}
	for _, in := range []string{"docs", "gleann-docs", "gleann-plugin-docs"} {
		m, ok := catalogPluginFor(in, catalog)
		if !ok || m.Name != "gleann-plugin-docs" {
			t.Errorf("%q -> %q %v", in, m.Name, ok)
		}
	}
	if _, ok := catalogPluginFor("nope", catalog); ok {
		t.Error("unknown plugin resolved")
	}
	if _, ok := catalogPluginFor("https://github.com/x/gleann-plugin-docs", catalog); ok {
		t.Error("a git URL must keep the clone path")
	}
}
