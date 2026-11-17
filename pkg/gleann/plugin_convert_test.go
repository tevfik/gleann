package gleann

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// convertPlugin serves /health and answers /convert with body (status code
// status), recording the uploaded file.
func convertPlugin(t *testing.T, status int, body string) (*Plugin, *string) {
	t.Helper()
	var uploaded string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = io.WriteString(w, `{"status":"ok"}`)
		case "/convert":
			f, hdr, err := r.FormFile("file")
			if err == nil {
				data, _ := io.ReadAll(f)
				uploaded = hdr.Filename + ":" + string(data)
			}
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &Plugin{Name: "conv", URL: srv.URL}, &uploaded
}

func TestProcessStructuredParsesNodesAndEdges(t *testing.T) {
	file := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(file, []byte("%PDF-fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, uploaded := convertPlugin(t, http.StatusOK, `{
		"markdown": "# Report",
		"nodes": [
			{"_type": "Document", "path": "report.pdf"},
			{"_type": "Section", "heading": "Intro"},
			{"heading": "no type: dropped"},
			"not an object"
		],
		"edges": [
			{"_type": "HAS_SECTION", "from": "report.pdf", "to": "Intro"},
			{"_type": "HAS_SECTION", "from": "", "to": "x"},
			{"from": "a", "to": "b"},
			42
		]
	}`)
	m := &PluginManager{Registry: &PluginRegistry{}}
	res, err := m.ProcessStructured(p, file)
	if err != nil {
		t.Fatal(err)
	}
	if *uploaded != "report.pdf:%PDF-fake" {
		t.Fatalf("uploaded %q", *uploaded)
	}
	if res.Markdown != "# Report" || len(res.Nodes) != 2 || len(res.Edges) != 1 {
		t.Fatalf("result = %+v", res)
	}
	if res.Nodes[1].Type != "Section" || res.Nodes[1].Data["heading"] != "Intro" {
		t.Fatalf("node = %+v", res.Nodes[1])
	}
	if e := res.Edges[0]; e.Type != "HAS_SECTION" || e.From != "report.pdf" || e.To != "Intro" {
		t.Fatalf("edge = %+v", e)
	}
}

func TestProcessStructuredErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "doc.docx")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &PluginManager{Registry: &PluginRegistry{}}
	cases := []struct {
		name   string
		status int
		body   string
		path   string
		want   string
	}{
		{"plugin reports an error", http.StatusOK, `{"error":"unsupported layout"}`, file, "plugin logic error: unsupported layout"},
		{"non-200 status", http.StatusBadGateway, "upstream down", file, "status 502: upstream down"},
		{"not JSON", http.StatusOK, "<html>", file, "decode plugin response"},
		{"missing input file", http.StatusOK, `{}`, filepath.Join(t.TempDir(), "gone.pdf"), "open file for plugin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := convertPlugin(t, tc.status, tc.body)
			_, err := m.ProcessStructured(p, tc.path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestPluginInfoAndPingErrors(t *testing.T) {
	m := &PluginManager{Registry: &PluginRegistry{}}
	if _, err := m.FetchPluginInfo(nil); err == nil {
		t.Fatal("FetchPluginInfo(nil) succeeded")
	}
	if _, err := m.PingPlugin(&Plugin{Name: "x"}); err == nil {
		t.Fatal("PingPlugin without URL succeeded")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/info":
			_, _ = io.WriteString(w, "{broken")
		case "/health":
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	p := &Plugin{Name: "x", URL: srv.URL + "/"}
	if _, err := m.FetchPluginInfo(p); err == nil || !strings.Contains(err.Error(), "decode /info") {
		t.Fatalf("broken /info: %v", err)
	}
	if _, err := m.PingPlugin(p); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("unhealthy plugin: %v", err)
	}
	notFound := httptest.NewServer(http.NotFoundHandler())
	defer notFound.Close()
	if _, err := m.FetchPluginInfo(&Plugin{Name: "y", URL: notFound.URL}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("missing /info: %v", err)
	}
}
