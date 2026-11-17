package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// h2bPluginServer is a running plugin: /health answers ready, /settings
// returns a small form and requires the plugin token.
func h2bPluginServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"ok","ready":true}`))
		case "/settings":
			if r.Header.Get("Authorization") != "Bearer tok" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"fields":[
				{"key":"ocr","label":"OCR","type":"bool","value":true},
				{"key":"lang","label":"Language","type":"text","value":"tr"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// h2bRegisterPlugins writes ~/.gleann/plugins.json.
func h2bRegisterPlugins(t *testing.T, home string, plugins ...gleann.Plugin) {
	t.Helper()
	data, err := json.Marshal(gleann.PluginRegistry{Plugins: plugins})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".gleann"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gleann", "plugins.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// h2bPluginModel is a plugin screen over the given catalog entries with
// statuses computed the way NewPluginModel does.
func h2bPluginModel(plugins ...gleann.PluginMeta) PluginModel {
	m := PluginModel{plugins: plugins, statuses: make([]pluginStatus, len(plugins)), width: 100, height: 40}
	m.refreshStatuses()
	return m
}

func h2bEmptyPath(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	// Windows also looks for pip-installed scripts under %APPDATA%.
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
}

func TestH2bPluginRunningShowsItsSettings(t *testing.T) {
	home := h2bHome(t)
	srv := h2bPluginServer(t)
	h2bRegisterPlugins(t, home, gleann.Plugin{Name: "gleann-plugin-docs", URL: srv.URL})

	m := h2bPluginModel(gleann.PluginMeta{Name: "gleann-plugin-docs", Description: "docs", HasSettings: true})
	if m.statuses[0] != statusRunning {
		t.Fatalf("a registered plugin answering /health ready is running, got %v", m.statuses[0])
	}
	registered(m.registry, m.plugins[0]).Token = "tok"

	r, cmd := m.Update(h2bKey("enter"))
	pm := r.(PluginModel)
	if pm.state != psDetail || cmd == nil {
		t.Fatalf("enter on a running plugin opens the detail view and reads its settings: state=%v cmd=%v", pm.state, cmd)
	}
	msg := cmd().(settingsSummaryMsg)
	r, _ = pm.Update(msg)
	pm = r.(PluginModel)
	want := []settingRow{{"OCR", "on"}, {"Language", "tr"}}
	if len(pm.settings) != 2 || pm.settings[0] != want[0] || pm.settings[1] != want[1] {
		t.Fatalf("settings = %+v, want %+v", pm.settings, want)
	}
	view := pm.View().Content
	for _, s := range []string{"Settings", "OCR:", "Language:", "Press c to configure", "c configure"} {
		if !strings.Contains(view, s) {
			t.Errorf("detail view lacks %q:\n%s", s, view)
		}
	}

	// A late summary for another plugin, or outside the detail view, is dropped.
	r, _ = pm.Update(settingsSummaryMsg{plugin: "other", rows: []settingRow{{"x", "y"}}})
	if got := r.(PluginModel).settings; len(got) != 2 {
		t.Errorf("summary of another plugin replaced the rows: %+v", got)
	}
	pm.state = psMain
	r, _ = pm.Update(settingsSummaryMsg{plugin: "gleann-plugin-docs"})
	if got := r.(PluginModel).settings; len(got) != 2 {
		t.Errorf("summary outside the detail view must be ignored: %+v", got)
	}
}

func TestH2bPluginRegisteredButDownIsInstalled(t *testing.T) {
	home := h2bHome(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens any more
	h2bRegisterPlugins(t, home, gleann.Plugin{Name: "gleann-docs", URL: url})
	m := h2bPluginModel(gleann.PluginMeta{Name: "gleann-plugin-docs"}) // legacy alias registered
	if m.statuses[0] != statusInstalled {
		t.Fatalf("status = %v, want installed", m.statuses[0])
	}
	r, cmd := m.Update(h2bKey("enter"))
	if r.(PluginModel).state != psDetail || cmd != nil {
		t.Error("an installed but stopped plugin opens the detail view without reading settings")
	}
}

func TestH2bPluginBrokenRegistryCountsAsEmpty(t *testing.T) {
	home := h2bHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".gleann"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gleann", "plugins.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := h2bPluginModel(gleann.PluginMeta{Name: "p"})
	if m.registry == nil || len(m.registry.Plugins) != 0 || m.statuses[0] != statusNotInstalled {
		t.Errorf("registry=%+v status=%v", m.registry, m.statuses[0])
	}
}

func TestH2bPluginMainKeys(t *testing.T) {
	h2bHome(t)
	h2bEmptyPath(t) // no markitdown, pip or pipx: installs fail at once, offline

	t.Run("u uninstalls only installed plugins", func(t *testing.T) {
		m := actionTestModel(statusNotInstalled, statusInstalled)
		m.state = psMain
		if r, cmd := m.Update(h2bKey("u")); cmd != nil || r.(PluginModel).state != psMain {
			t.Error("u on a missing plugin must do nothing")
		}
		m.cursor = 1
		r, cmd := m.Update(h2bKey("u"))
		if pm := r.(PluginModel); pm.state != psAction || cmd == nil || !strings.Contains(pm.actionMsg, "Uninstalling plug-b") {
			t.Fatalf("state=%v msg=%q", pm.state, pm.actionMsg)
		}
		if msg := cmd().(pluginActionMsg); msg.action != "uninstall" || msg.plugin != "plug-b" {
			t.Errorf("uninstall result %+v", msg)
		}
	})

	t.Run("i installs only missing plugins", func(t *testing.T) {
		m := actionTestModel(statusInstalled)
		m.state = psMain
		if _, cmd := m.Update(h2bKey("i")); cmd != nil {
			t.Error("i on an installed plugin must do nothing")
		}
		m = actionTestModel(statusNotInstalled)
		m.state = psMain
		m.plugins[0].RepoURL = filepath.Join(t.TempDir(), "no-such-repo")
		r, cmd := m.Update(h2bKey("i"))
		if pm := r.(PluginModel); pm.state != psAction || cmd == nil || !strings.Contains(pm.actionMsg, "Installing plug-a") {
			t.Fatalf("state=%v msg=%q", pm.state, pm.actionMsg)
		}
		for i := 0; i < 100; i++ {
			switch msg := cmd().(type) {
			case pluginInstallProgressMsg:
				cmd = msg.continueCmd
				continue
			case pluginActionMsg:
				if msg.err == nil {
					t.Errorf("install from a missing repository succeeded: %+v", msg)
				}
			}
			break
		}
	})

	t.Run("m installs markitdown only when needed", func(t *testing.T) {
		m := actionTestModel(statusInstalled)
		m.state = psMain
		if _, cmd := m.Update(h2bKey("m")); cmd != nil {
			t.Error("m without a markitdown dependency must do nothing")
		}
		m.plugins[0].RequiresMarkitdown = true
		r, cmd := m.Update(h2bKey("m"))
		pm := r.(PluginModel)
		if pm.state != psAction || cmd == nil {
			t.Fatalf("m must start the markitdown install: state=%v", pm.state)
		}
		msg := cmd().(pluginActionMsg)
		if msg.plugin != "markitdown" || msg.action != "install" || msg.err == nil {
			t.Fatalf("with no pip or pipx the install must fail: %+v", msg)
		}
		r, _ = pm.Update(msg)
		if pm := r.(PluginModel); pm.state != psResult || !strings.Contains(pm.View().Content, "✗") {
			t.Errorf("the failure must be shown: %q", pm.status)
		}
	})

	t.Run("keys are ignored while an action runs", func(t *testing.T) {
		m := actionTestModel(statusInstalled)
		m.state = psAction
		r, cmd := m.Update(h2bKey("q"))
		if cmd != nil || r.(PluginModel).state != psAction || r.(PluginModel).quitting {
			t.Error("q during an action must be ignored")
		}
	})
}

func TestH2bDetectMarkitdownMissing(t *testing.T) {
	h2bHome(t)
	h2bEmptyPath(t)
	if st := detectMarkitdown(); st.available || st.path != "" {
		t.Errorf("markitdown found with an empty PATH: %+v", st)
	}
}

func TestH2bPluginViewsShowTheMarkitdownDependency(t *testing.T) {
	h2bHome(t)
	m := actionTestModel(statusNotInstalled)
	m.state = psMain
	m.plugins[0].RequiresMarkitdown = true
	m.plugins[0].Extensions = []string{".pdf", ".docx"}
	m.status = "↻ Refreshed"

	main := m.viewMain()
	for _, s := range []string{"markitdown not installed", "m markitdown", ".pdf .docx", "↻ Refreshed"} {
		if !strings.Contains(main, s) {
			t.Errorf("main view lacks %q:\n%s", s, main)
		}
	}
	detail := m.viewDetail()
	for _, s := range []string{"Dependencies", "Not installed", "press m to install", "Press i to install", "esc back • i install"} {
		if !strings.Contains(detail, s) {
			t.Errorf("detail view lacks %q:\n%s", s, detail)
		}
	}

	m.markitdown = markitdownStatus{available: true, path: filepath.Join("bin", "markitdown")}
	m.statuses[0] = statusInstalled
	m.plugins[0].HasSettings = false
	detail = m.viewDetail()
	if !strings.Contains(detail, filepath.Join("bin", "markitdown")) || !strings.Contains(detail, "Press u to uninstall") {
		t.Errorf("detail view with markitdown present:\n%s", detail)
	}
	if strings.Contains(detail, "configure") {
		t.Errorf("a plugin without settings offers configure:\n%s", detail)
	}
	if main := m.viewMain(); strings.Contains(main, "markitdown not installed") {
		t.Errorf("warning shown although markitdown is present:\n%s", main)
	}
}

func TestH2bFetchSettingsSummaryFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bad/settings":
			_, _ = w.Write([]byte("{not json"))
		case "/auth/settings":
			if r.Header.Get("Authorization") != "Bearer secret" {
				http.Error(w, "no", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"fields":[{"key":"a","label":"A","type":"text","value":"x"},
				{"key":"b","label":"B","type":"text","value":null}]}`))
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	cases := []struct {
		name, url, token string
		rows             []settingRow
	}{
		{"invalid url", "http://bad\x7fhost", "", nil},
		{"connection refused", closedURL, "", nil},
		{"server error", srv.URL + "/err", "", nil},
		{"invalid json", srv.URL + "/bad", "", nil},
		{"token sent, empty values skipped", srv.URL + "/auth/", "secret", []settingRow{{"A", "x"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := fetchSettingsSummary("p", tc.url, tc.token)().(settingsSummaryMsg)
			if msg.plugin != "p" || len(msg.rows) != len(tc.rows) {
				t.Fatalf("msg = %+v, want rows %+v", msg, tc.rows)
			}
			for i := range tc.rows {
				if msg.rows[i] != tc.rows[i] {
					t.Errorf("row %d = %+v, want %+v", i, msg.rows[i], tc.rows[i])
				}
			}
		})
	}
}
