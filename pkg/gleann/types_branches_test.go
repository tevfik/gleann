package gleann

import (
	"runtime/debug"
	"testing"
)

func TestParseByteSizeFaults(t *testing.T) {
	const (
		ki = int64(1024)
		mi = ki * 1024
		gi = mi * 1024
	)
	ok := []struct {
		in   string
		want int64
	}{
		{"", 0}, {"0", 0}, {"  ", 0},
		{"1048576", mi},
		{"512B", 512}, {"512b", 512},
		{"4K", 4 * ki}, {"4KB", 4 * ki}, {"4KiB", 4 * ki},
		{"500MB", 500 * mi}, {"500m", 500 * mi}, {"512MiB", 512 * mi},
		{"2GB", 2 * gi}, {"2G", 2 * gi}, {"1GiB", gi},
		{" 3 MB ", 3 * mi},
	}
	for _, c := range ok {
		got, err := ParseByteSize(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseByteSize(%q) = %d, %v; want %d", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"abc", "12XB", "-5MB", "MB", "1.5GB"} {
		if got, err := ParseByteSize(bad); err == nil {
			t.Errorf("ParseByteSize(%q) = %d, want an error", bad, got)
		}
	}
}

func TestGetGleannVersion(t *testing.T) {
	oldVersion, oldRead := CurrentVersion, readBuildInfo
	t.Cleanup(func() { CurrentVersion, readBuildInfo = oldVersion, oldRead })

	info := func(mainVersion, revision string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			bi := &debug.BuildInfo{Main: debug.Module{Version: mainVersion}}
			if revision != "" {
				bi.Settings = []debug.BuildSetting{{Key: "vcs.time", Value: "t"}, {Key: "vcs.revision", Value: revision}}
			}
			return bi, true
		}
	}

	cases := []struct {
		name    string
		current string
		read    func() (*debug.BuildInfo, bool)
		want    string
	}{
		{"explicit version wins", "v1.2.3", info("v9.9.9", ""), "v1.2.3"},
		{"module version", "dev", info("v0.5.0", ""), "v0.5.0"},
		{"devel module falls back to the revision", "", info("(devel)", "0123456789abcdef"), "git-0123456"},
		{"short revision is ignored", "", info("", "abc"), "dev"},
		{"no build info, dev marker kept", "dev", func() (*debug.BuildInfo, bool) { return nil, false }, "dev"},
		{"no build info, nothing set", "", func() (*debug.BuildInfo, bool) { return nil, false }, "dev"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			CurrentVersion, readBuildInfo = c.current, c.read
			if got := GetGleannVersion(); got != c.want {
				t.Errorf("GetGleannVersion() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAttributesToJSONFaults(t *testing.T) {
	if got, err := AttributesToJSON(nil); err != nil || got != "{}" {
		t.Errorf("nil map: %q, %v", got, err)
	}
	if got, err := AttributesToJSON(map[string]any{"a": 1}); err != nil || got != `{"a":1}` {
		t.Errorf("map: %q, %v", got, err)
	}
	if _, err := AttributesToJSON(map[string]any{"bad": make(chan int)}); err == nil {
		t.Error("an unmarshalable value must be reported")
	}
}
