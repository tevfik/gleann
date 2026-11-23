package main

import (
	"runtime/debug"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestResolveVersion(t *testing.T) {
	info := func(mainVersion string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			if mainVersion == "" {
				return nil, false
			}
			return &debug.BuildInfo{Main: debug.Module{Version: mainVersion}}, true
		}
	}

	cases := []struct {
		name    string
		initial string
		read    func() (*debug.BuildInfo, bool)
		want    string
	}{
		{"explicit ldflags version kept", "v1.20.0", info("v1.20.1"), "v1.20.0"},
		{"go install tagged release", "dev", info("v1.20.1"), "v1.20.1"},
		{"go install pseudo-version shown as is", "dev", info("v0.0.0-20261120000000-abcdef123456"), "v0.0.0-20261120000000-abcdef123456"},
		{"local devel build kept dev", "dev", info("(devel)"), "dev"},
		{"empty build info kept dev", "dev", func() (*debug.BuildInfo, bool) { return &debug.BuildInfo{}, true }, "dev"},
		{"nil build info kept dev", "dev", func() (*debug.BuildInfo, bool) { return nil, false }, "dev"},
		{"nil read function kept dev", "dev", nil, "dev"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveVersion(c.initial, c.read)
			if got != c.want {
				t.Errorf("resolveVersion(%q) = %q, want %q", c.initial, got, c.want)
			}
		})
	}
}

func TestCLIUpdatePseudoVersion(t *testing.T) {
	e := newCLIEnv(t)
	d1StubRelease(t, &gleann.Release{Tag: "v1.9.0"}, nil)
	d1SetVersion(t, "v0.0.0-20261120000000-abcdef123456")
	r := e.run("update")
	wantExit(t, r, 0)
	mustContain(t, "pseudo-version", r.Out, "installed v0.0.0-20261120000000-abcdef123456, latest v1.9.0", "This is a development build; use --force")
}

func TestCLIUpdateGoInstallRelease(t *testing.T) {
	e := newCLIEnv(t)
	d1StubRelease(t, &gleann.Release{Tag: "v1.9.0"}, nil)

	// Simulate go install tagged version resolved and up to date
	d1SetVersion(t, "v1.9.0")
	r := e.run("update")
	wantExit(t, r, 0)
	mustContain(t, "up-to-date", r.Out, "installed v1.9.0, latest v1.9.0", "gleann is up to date.")

	// Simulate go install tagged version with newer release available
	d1SetVersion(t, "v1.8.0")
	r = e.run("update", "--check")
	wantExit(t, r, 0)
	mustContain(t, "newer", r.Out, "installed v1.8.0, latest v1.9.0", "A newer release is available: run `gleann update`.")
}
