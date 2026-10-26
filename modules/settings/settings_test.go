package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type cfg struct {
	Backend   string            `json:"backend,omitempty"`
	Threshold float64           `json:"threshold,omitempty"`
	DPI       int               `json:"dpi,omitempty"`
	Match     bool              `json:"match,omitempty"`
	Model     string            `json:"model,omitempty"`
	Token     string            `json:"token,omitempty"`
	Hotkey    string            `json:"hotkey,omitempty"`
	Installed bool              `json:"installed,omitempty"`
	Path      string            `json:"extra_path,omitempty"`
	Extra     map[string]string `json:"extra,omitempty"`
}

func registry(models func(*cfg) ([]Option, error)) *Registry[cfg] {
	return &Registry[cfg]{
		Title: "test",
		Fields: []Field[cfg]{
			{Key: "backend", Label: "Backend", Type: Select, Default: "whisper",
				Options: Opts("whisper", "Whisper", "onnx", "ONNX"),
				Bind:    func(c *cfg) any { return &c.Backend }},
			{Key: "threshold", Label: "Threshold", Type: Number, Default: 0.45,
				Min: F(0.30), Max: F(0.80), Step: F(0.05), Apply: Live,
				Bind: func(c *cfg) any { return &c.Threshold }, VisibleIf: &Cond{Key: "match", Equals: true}},
			{Key: "dpi", Label: "DPI", Type: Number, Default: 150, Min: F(72), Max: F(600),
				Options: Opts("150", "150", "200", "200", "300", "300"),
				Bind:    func(c *cfg) any { return &c.DPI }},
			{Key: "match", Label: "Only my voice", Type: Bool, Bind: func(c *cfg) any { return &c.Match }},
			{Key: "model", Label: "Model", Type: Select, AllowCustom: true, OptionsFrom: models,
				Options: Opts("small", "Small (suggested)"), Bind: func(c *cfg) any { return &c.Model }},
			{Key: "token", Label: "Token", Type: Secret, Bind: func(c *cfg) any { return &c.Token }},
			{Key: "extra_path", Label: "Library", Type: Path, Bind: func(c *cfg) any { return &c.Path },
				Placeholder: func(*cfg) string { return "/usr/lib/libx.so" }},
			{Key: "hotkey", Label: "Hotkey", Type: Text, Bind: func(c *cfg) any { return &c.Hotkey },
				Check: func(_ *cfg, v any) (any, error) {
					s := strings.ToLower(v.(string))
					if strings.Contains(s, " ") {
						return nil, errors.New("use keys joined by +")
					}
					return s, nil
				}},
		},
		Internal: []string{"installed"},
	}
}

func TestDefaultsAreStoredAsZero(t *testing.T) {
	r, c := registry(nil), &cfg{}
	if r.Value(c, "backend") != "whisper" || r.Value(c, "threshold") != 0.45 || !r.IsDefault(c, "dpi") {
		t.Fatalf("defaults: %v %v", r.Value(c, "backend"), r.Value(c, "threshold"))
	}
	if err := r.Set(c, "backend", "onnx"); err != nil || c.Backend != "onnx" {
		t.Fatalf("set onnx: %v %q", err, c.Backend)
	}
	if err := r.Set(c, "backend", "whisper"); err != nil || c.Backend != "" {
		t.Fatalf("setting the default must store zero: %v %q", err, c.Backend)
	}
	if err := r.Set(c, "dpi", "150"); err != nil || c.DPI != 0 {
		t.Fatalf("dpi default: %v %d", err, c.DPI)
	}
	if err := r.Set(c, "dpi", 300.0); err != nil || c.DPI != 300 {
		t.Fatalf("dpi 300: %v %d", err, c.DPI)
	}
	if err := r.Set(c, "dpi", 99.5); err == nil {
		t.Error("an int setting accepted 99.5")
	}
}

func TestValidationMessages(t *testing.T) {
	r, c := registry(nil), &cfg{}
	for key, raw := range map[string]any{
		"backend": "magic", "threshold": 0.9, "dpi": "lots", "match": "maybe",
		"hotkey": "ctrl + k", "nope": "x", "model": 3.0,
	} {
		if err := r.Set(c, key, raw); err == nil {
			t.Errorf("%s=%v accepted", key, raw)
		}
	}
	if err := r.Set(c, "backend", "magic"); err.Error() != "must be one of: whisper, onnx" {
		t.Errorf("message: %v", err)
	}
	if err := r.Set(c, "hotkey", "Ctrl+K"); err != nil || c.Hotkey != "ctrl+k" {
		t.Errorf("Check must normalise: %v %q", err, c.Hotkey)
	}
	if err := r.Set(c, "match", "on"); err != nil || !c.Match {
		t.Errorf("bool from text: %v", err)
	}
	if err := r.Set(c, "threshold", 0.0); err != nil || c.Threshold != 0 {
		t.Errorf("zero must reset to the default: %v", err)
	}
	c.Backend, c.DPI = "onnx", 300
	if r.Reset(c, "backend") != nil || r.Reset(c, "dpi") != nil || c.Backend != "" || c.DPI != 0 {
		t.Errorf("reset: %+v", c)
	}
	r.Fields = append(r.Fields, Field[cfg]{Key: "required", Type: Path, Bind: func(c *cfg) any { return &c.Model },
		Check: func(_ *cfg, v any) (any, error) {
			if v == "" {
				return nil, errors.New("required")
			}
			return v, nil
		}})
	c.Model = "/m"
	if err := r.Reset(c, "required"); err == nil || c.Model != "/m" {
		t.Errorf("reset must not clear a required value: %v %q", err, c.Model)
	}
}

func TestStep(t *testing.T) {
	r, c := registry(nil), &cfg{}
	steps := []struct {
		key  string
		dir  int
		want func() any
		is   any
	}{
		{"threshold", 1, func() any { return c.Threshold }, 0.5},
		{"threshold", 1, func() any { return c.Threshold }, 0.55},
		{"threshold", -1, func() any { return c.Threshold }, 0.5},
		{"threshold", -1, func() any { return c.Threshold }, 0.0}, // back on the default
		{"backend", 1, func() any { return c.Backend }, "onnx"},
		{"backend", 1, func() any { return c.Backend }, ""},
		{"backend", -1, func() any { return c.Backend }, "onnx"},
		{"dpi", 1, func() any { return c.DPI }, 200},
		{"dpi", -1, func() any { return c.DPI }, 0},
		{"dpi", -1, func() any { return c.DPI }, 300},
		{"match", 1, func() any { return c.Match }, true},
	}
	for i, s := range steps {
		if err := r.Step(c, s.key, s.dir); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if got := s.want(); got != s.is {
			t.Fatalf("step %d (%s %+d): %v, want %v", i, s.key, s.dir, got, s.is)
		}
	}
	c.Threshold = 0.80
	_ = r.Step(c, "threshold", 1)
	if c.Threshold != 0.80 {
		t.Errorf("step past Max: %v", c.Threshold)
	}
	if err := r.Step(c, "hotkey", 1); err == nil {
		t.Error("a text field must not step")
	}
}

func TestVisibleAndDisplay(t *testing.T) {
	r, c := registry(nil), &cfg{Backend: "whisper"}
	if !r.IsDefault(c, "backend") {
		t.Error("a default spelled out in the file is still the default")
	}
	if r.Visible(c, "threshold") {
		t.Error("threshold shown while match is off")
	}
	c.Match = true
	if !r.Visible(c, "threshold") || !r.Visible(c, "backend") {
		t.Error("visibility")
	}
	c.Backend, c.Token, c.Threshold = "onnx", "abc", 0.5
	for key, want := range map[string]string{"backend": "ONNX", "threshold": "0.50", "match": "on", "token": "••••••", "hotkey": "(not set)", "dpi": "150", "extra_path": "auto: /usr/lib/libx.so"} {
		if got := r.Display(c, key); got != want {
			t.Errorf("Display(%s) = %q, want %q", key, got, want)
		}
	}
}

func TestOptionsFromAndCustomValues(t *testing.T) {
	models := func(*cfg) ([]Option, error) { return Opts("large", "Large"), nil }
	r, c := registry(models), &cfg{}
	if err := r.Step(c, "model", 1); err != nil || c.Model != "small" {
		t.Fatalf("options: %v %q", err, c.Model)
	}
	_ = r.Step(c, "model", 1)
	if c.Model != "large" {
		t.Fatalf("run-time option not offered: %q", c.Model)
	}
	if err := r.Set(c, "model", "my-own:7b"); err != nil || c.Model != "my-own:7b" {
		t.Fatalf("custom value: %v", err)
	}

	failing := registry(func(*cfg) ([]Option, error) { return nil, errors.New("server down") })
	f := failing.Describe(c)
	for _, s := range f.Fields {
		if s.Key == "model" && (s.OptionsError != "server down" || len(s.Options) != 1) {
			t.Errorf("a failed listing must keep the suggestions and report the error: %+v", s)
		}
	}
}

func TestUpdateIsAllOrNothing(t *testing.T) {
	r := registry(nil)
	r.Validate = func(c *cfg) error {
		if c.Backend == "onnx" && c.DPI == 300 {
			return errors.New("onnx cannot use 300 dpi")
		}
		return nil
	}
	c := &cfg{Hotkey: "f9"}
	if _, errs := r.Update(c, map[string]any{"backend": "onnx", "dpi": 7.0}); errs["dpi"] == "" || c.Backend != "" {
		t.Fatalf("partial update: %v %+v", errs, c)
	}
	if _, errs := r.Update(c, map[string]any{"backend": "onnx", "dpi": 300.0}); errs[""] == "" || c.DPI != 0 {
		t.Fatalf("cross-field rule: %v %+v", errs, c)
	}
	changed, errs := r.Update(c, map[string]any{"dpi": 200.0, "backend": "onnx"})
	if len(errs) > 0 || !reflect.DeepEqual(changed, []string{"backend", "dpi"}) || c.Hotkey != "f9" {
		t.Fatalf("update: %v %v %+v", changed, errs, c)
	}
	if r.NeedsRestart([]string{"threshold"}) || !r.NeedsRestart([]string{"threshold", "dpi"}) {
		t.Error("NeedsRestart")
	}
}

func TestUncovered(t *testing.T) {
	if got := registry(nil).Uncovered(); !reflect.DeepEqual(got, []string{"extra"}) {
		t.Errorf("Uncovered = %v", got)
	}
}

func TestBindMustBeAPointer(t *testing.T) {
	r := &Registry[cfg]{Fields: []Field[cfg]{{Key: "x", Type: Text, Bind: func(c *cfg) any { return c.Model }}}}
	defer func() {
		if recover() == nil {
			t.Error("a non-pointer Bind must panic with an explanation")
		}
	}()
	r.Value(&cfg{}, "x")
}

// --- HTTP ---

func server(t *testing.T, r *Registry[cfg], c *cfg, auth func(*http.Request) bool) *httptest.Server {
	t.Helper()
	r.Load = func() (*cfg, error) { cp := *c; return &cp, nil }
	r.Save = func(n *cfg) error { *c = *n; return nil }
	ts := httptest.NewServer(r.Handler(auth))
	t.Cleanup(ts.Close)
	return ts
}

func putJSON(t *testing.T, url string, body any, ct string) (*http.Response, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", ct)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func getForm(t *testing.T, url string) Form {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET: %v %v", resp, err)
	}
	defer resp.Body.Close()
	var f Form
	_ = json.NewDecoder(resp.Body).Decode(&f)
	return f
}

func TestHandlerGetPut(t *testing.T) {
	c := &cfg{Token: "hunter2", Installed: true}
	r := registry(nil)
	r.BeforeSave = func(c *cfg) { c.Installed = true }
	ts := server(t, r, c, nil)

	f := getForm(t, ts.URL)
	if f.SchemaVersion != SchemaVersion || f.Revision == "" || len(f.Fields) != len(r.Fields) {
		t.Fatalf("form: %+v", f)
	}
	for _, s := range f.Fields {
		if s.Key == "extra_path" && s.Placeholder != "/usr/lib/libx.so" {
			t.Errorf("placeholder not sent: %+v", s)
		}
		if s.Key == "token" && (s.Value != "" || !s.IsSet) {
			t.Errorf("a secret leaked or lost its is_set: %+v", s)
		}
		if s.Key == "threshold" && (s.Apply != Live || s.VisibleIf == nil || *s.Step != 0.05) {
			t.Errorf("threshold spec: %+v", s)
		}
	}

	resp, out := putJSON(t, ts.URL, map[string]any{"values": map[string]any{"backend": "onnx", "threshold": 0.5}, "revision": f.Revision}, "application/json")
	if resp.StatusCode != 200 || out["restart_required"] != true || out["revision"] == f.Revision {
		t.Fatalf("PUT: %d %v", resp.StatusCode, out)
	}
	if c.Backend != "onnx" || c.Threshold != 0.5 || c.Token != "hunter2" {
		t.Errorf("saved config: %+v", c)
	}

	// The form is stale now: a PUT carrying its revision is refused.
	if resp, _ := putJSON(t, ts.URL, map[string]any{"values": map[string]any{"backend": "whisper"}, "revision": f.Revision}, "application/json"); resp.StatusCode != http.StatusConflict {
		t.Errorf("stale revision: %d", resp.StatusCode)
	}
	if resp, out := putJSON(t, ts.URL, map[string]any{"values": map[string]any{"backend": "magic", "match": true}}, "application/json"); resp.StatusCode != 422 || c.Match {
		t.Errorf("invalid PUT: %d %v %+v", resp.StatusCode, out, c)
	}
	if resp, _ := putJSON(t, ts.URL, map[string]any{"values": map[string]any{"match": true}}, "text/plain"); resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain: %d", resp.StatusCode)
	}
	if resp, _ := putJSON(t, ts.URL, map[string]any{"values": map[string]any{}}, "application/json"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty values: %d", resp.StatusCode)
	}
}

func TestHandlerAuth(t *testing.T) {
	c := &cfg{}
	ts := server(t, registry(nil), c, func(r *http.Request) bool { return r.URL.Query().Get("token") == "s" })
	if r, _ := http.Get(ts.URL); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET without token: %d", r.StatusCode)
	}
	if resp, _ := putJSON(t, ts.URL, map[string]any{"values": map[string]any{"match": true}}, "application/json"); resp.StatusCode != http.StatusUnauthorized || c.Match {
		t.Errorf("PUT without token: %d", resp.StatusCode)
	}
	if r, _ := http.Get(ts.URL + "?token=s"); r.StatusCode != 200 {
		t.Errorf("GET with token: %d", r.StatusCode)
	}
}
