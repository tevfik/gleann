package main

import (
	"encoding/json"
	"reflect"

	"github.com/tevfik/gleann/internal/server"
	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/pkg/gleann"
)

// mountSettings serves gleann's settings registry (the fields the TUI
// settings screen edits) at /api/settings and applies every save to the
// running server.
//
// args are serve's own flags: a flag (e.g. --host) keeps winning after a
// save, as it did at start-up. asLoaded is the config read from disk at
// start-up and overrides the keys start-up changed in memory only (the
// embedded llama-server URL, a provider fallback); those keep their running
// value unless the save changed them on disk.
func mountSettings(srv *server.Server, args []string, asLoaded gleann.Config, overrides []string) {
	reg := tui.Settings()
	save := reg.Save
	reg.Save = func(c *tui.OnboardResult) error {
		if err := save(c); err != nil {
			return err
		}
		fresh := getConfig(args)
		applySavedConfig(&fresh, args)
		srv.ApplyConfig(keepRuntimeOverrides(fresh, srv.CurrentConfig(), asLoaded, overrides))
		return nil
	}
	srv.SetSettingsHandler(reg.Handler(nil)) // the server's auth middleware guards /api/
}

// keepRuntimeOverrides returns fresh with each overridden key set back to
// its running value when the disk value did not change since start-up.
func keepRuntimeOverrides(fresh, running, asLoaded gleann.Config, overrides []string) gleann.Config {
	if len(overrides) == 0 {
		fresh.EmbedderResolver = defaultEmbedderResolver(fresh)
		return fresh
	}
	fm, rm, lm := asMap(fresh), asMap(running), asMap(asLoaded)
	for _, k := range overrides {
		if reflect.DeepEqual(fm[k], lm[k]) {
			if v, ok := rm[k]; ok {
				fm[k] = v
			} else {
				delete(fm, k)
			}
		}
	}
	raw, _ := json.Marshal(fm)
	var out gleann.Config
	if err := json.Unmarshal(raw, &out); err != nil {
		out = fresh
	}
	out.EmbedderResolver = defaultEmbedderResolver(out)
	return out
}

func asMap(c gleann.Config) map[string]any {
	raw, _ := json.Marshal(c)
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	return m
}
