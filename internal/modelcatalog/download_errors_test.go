package modelcatalog

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/modules/settings"
)

func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GLEANN_CONFIG_DIR", t.TempDir())
}

func TestDownloadFailures(t *testing.T) {
	body := []byte("0123456789")
	tests := []struct {
		name    string
		handler http.HandlerFunc
		entry   Entry
		wantErr string
	}{
		{
			name:    "HTTP error status",
			handler: func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", http.StatusForbidden) },
			entry:   Entry{Name: "m", File: "m.gguf", Bytes: 10},
			wantErr: "HTTP 403",
		},
		{
			name:    "size mismatch",
			handler: func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) },
			entry:   Entry{Name: "m", File: "m.gguf", Bytes: 11},
			wantErr: "got 10 bytes, want 11",
		},
		{
			name: "connection cut mid-body",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", "100")
				_, _ = w.Write(body)
				// returning early with a short body makes the client see an unexpected EOF
			},
			entry:   Entry{Name: "m", File: "m.gguf"},
			wantErr: "unexpected EOF",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			e := tc.entry
			e.URL = srv.URL
			err := Download(context.Background(), e, nil)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
			if _, err := os.Stat(e.Path()); err == nil {
				t.Error("a failed download left the model under its real name")
			}
			if _, err := os.Stat(e.Path() + ".part"); err == nil {
				t.Error("a failed download left a .part file")
			}
		})
	}
}

func TestDownloadWithoutCatalogSizeUsesContentLength(t *testing.T) {
	isolate(t)
	body := []byte("abcdefgh")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	e := Entry{Name: "m", File: "m.gguf", URL: srv.URL} // Bytes == 0: size is not enforced
	var gotTotal int64
	if err := Download(context.Background(), e, func(_, total int64) { gotTotal = total }); err != nil {
		t.Fatal(err)
	}
	if gotTotal != int64(len(body)) {
		t.Errorf("progress total = %d, want Content-Length %d", gotTotal, len(body))
	}
	if !e.Installed() {
		t.Error("model not installed")
	}
}

func TestDownloadRequestErrors(t *testing.T) {
	isolate(t)
	if err := Download(context.Background(), Entry{Name: "m", File: "m.gguf", URL: "http://bad host/\x7f"}, nil); err == nil {
		t.Error("malformed URL: want an error")
	}

	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // connection refused
	err := Download(context.Background(), Entry{Name: "m", File: "m.gguf", URL: url}, nil)
	if err == nil || !strings.Contains(err.Error(), "download m") {
		t.Errorf("refused connection err = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("x")) }))
	defer live.Close()
	if err := Download(ctx, Entry{Name: "m", File: "m.gguf", URL: live.URL}, nil); err == nil {
		t.Error("cancelled context: want an error")
	}
}

func TestDownloadModelsDirNotCreatable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLEANN_CONFIG_DIR", blocker) // models dir would be <file>/models
	if err := Download(context.Background(), Entry{Name: "m", File: "m.gguf", URL: "http://127.0.0.1:1"}, nil); err == nil {
		t.Fatal("want an error when the models dir cannot be created")
	}
}

func TestDownloadCannotCreatePartFile(t *testing.T) {
	isolate(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("x")) }))
	defer srv.Close()
	e := Entry{Name: "m", File: "m.gguf", URL: srv.URL}
	// A directory under the .part name makes os.Create fail.
	if err := os.MkdirAll(e.Path()+".part", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Download(context.Background(), e, nil); err == nil {
		t.Fatal("want an error")
	}
}

// Catalog exposes the settings contract; a model already on disk with the
// catalog size is not downloaded again.
func TestCatalogContract(t *testing.T) {
	isolate(t)
	c := Catalog()
	list := c.List()
	if len(list) != len(All()) || len(list) == 0 {
		t.Fatalf("List has %d models, catalog %d", len(list), len(All()))
	}
	groups := map[string]bool{}
	for _, m := range list {
		if m.Installed {
			t.Errorf("%s reported installed in an empty models dir", m.ID)
		}
		if m.Group == "" || m.Value == "" || m.Name == "" {
			t.Errorf("incomplete model %+v", m)
		}
		groups[m.Group] = true
	}
	for _, g := range []string{"Embedding models", "LLMs", "Rerankers"} {
		if !groups[g] {
			t.Errorf("group %q missing from %v", g, groups)
		}
	}

	if err := c.Download(context.Background(), "no-such-model", func(settings.Progress) {}); err == nil || !strings.Contains(err.Error(), "unknown model") {
		t.Errorf("unknown id err = %v", err)
	}

	// Mark an entry installed with a sparse file of the exact size: Download is a no-op
	// (no network is touched) and List reflects the state.
	var target Entry
	for _, e := range All() {
		if e.finalBytes() > 0 {
			target = e
			break
		}
	}
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(target.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(target.finalBytes()); err != nil {
		t.Skipf("cannot create sparse file of %d bytes: %v", target.finalBytes(), err)
	}
	f.Close()
	reported := false
	if err := c.Download(context.Background(), target.ID, func(settings.Progress) { reported = true }); err != nil {
		t.Fatalf("Download of installed model: %v", err)
	}
	if reported {
		t.Error("progress reported for an already installed model")
	}
	installed := 0
	for _, m := range c.List() {
		if m.Installed {
			installed++
			if m.ID != target.ID {
				t.Errorf("unexpected installed model %s", m.ID)
			}
		}
	}
	if installed != 1 {
		t.Errorf("installed models = %d, want 1", installed)
	}
}

func TestDownloadReportsFinalProgress(t *testing.T) {
	isolate(t)
	e := Entry{Name: "m", File: "m.gguf"}
	body := []byte("payload")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	e.URL = srv.URL
	var last, total int64
	if err := Download(context.Background(), e, func(d, tot int64) { last, total = d, tot }); err != nil {
		t.Fatal(err)
	}
	if last != int64(len(body)) || total != int64(len(body)) {
		t.Errorf("final progress %d/%d", last, total)
	}
}

func TestAddPoolerToFailurePaths(t *testing.T) {
	const dim = 2
	gg := tinyGGUF(t, dim)
	wt := []float32{1, 2, 3, 4}
	bias := []float32{5, 6}
	st := safetensors(t, "F32",
		map[string][]float32{"bert.pooler.dense.weight": wt, "bert.pooler.dense.bias": bias},
		map[string][]int64{"bert.pooler.dense.weight": {dim, dim}, "bert.pooler.dense.bias": {dim}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/st" {
			http.ServeContent(w, r, "st", testTime, bytes.NewReader(st))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	pooler := func() *Pooler {
		return &Pooler{URL: srv.URL + "/st", Weight: "bert.pooler.dense.weight", Bias: "bert.pooler.dense.bias"}
	}

	t.Run("pooler tensors cannot be fetched", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "m.gguf")
		_ = os.WriteFile(path, gg, 0o644)
		p := pooler()
		p.URL = srv.URL + "/missing"
		err := addPoolerTo(context.Background(), Entry{Pooler: p}, path)
		if err == nil || !strings.Contains(err.Error(), "pooler:") {
			t.Fatalf("err = %v", err)
		}
		if got, _ := os.ReadFile(path); !bytes.Equal(got, gg) {
			t.Error("source GGUF modified by a failed fetch")
		}
	})

	t.Run("source already has a pooler", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "m.gguf")
		_ = os.WriteFile(path, gg, 0o644)
		if err := addPoolerTo(context.Background(), Entry{Pooler: pooler()}, path); err != nil {
			t.Fatal(err)
		}
		// Second patch of the patched file must be refused and leave no temp file.
		err := addPoolerTo(context.Background(), Entry{Pooler: pooler()}, path)
		if err == nil || !strings.Contains(err.Error(), "already has a pooler") {
			t.Fatalf("err = %v", err)
		}
		if _, err := os.Stat(path + ".pooler"); err == nil {
			t.Error(".pooler temp file left behind")
		}
	})

	t.Run("wrong patched size", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "m.gguf")
		_ = os.WriteFile(path, gg, 0o644)
		p := pooler()
		p.Bytes = 1
		err := addPoolerTo(context.Background(), Entry{Pooler: p}, path)
		if err == nil || !strings.Contains(err.Error(), "want 1") {
			t.Fatalf("err = %v", err)
		}
		if _, err := os.Stat(path + ".pooler"); err == nil {
			t.Error(".pooler temp file left behind")
		}
	})
}

func TestFetchRange(t *testing.T) {
	data := []byte("0123456789abcdef")
	tests := []struct {
		name     string
		handler  http.HandlerFunc
		from, to int64
		want     string
		wantErr  bool
	}{
		{
			name: "range honoured",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.ServeContent(w, r, "f", testTime, bytes.NewReader(data))
			},
			from: 4, to: 8, want: "4567",
		},
		{
			name:    "range ignored, full body returned",
			handler: func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(data) },
			from:    10, to: 14, want: "abcd",
		},
		{
			name:    "range ignored, body shorter than window",
			handler: func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(data[:6]) },
			from:    4, to: 12, wantErr: true,
		},
		{
			name:    "range ignored, body shorter than offset",
			handler: func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(data[:2]) },
			from:    4, to: 8, wantErr: true,
		},
		{
			name: "partial content shorter than requested",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write([]byte("45"))
			},
			from: 4, to: 8, wantErr: true,
		},
		{
			name:    "server error",
			handler: func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", 500) },
			from:    0, to: 4, wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			got, err := fetchRange(context.Background(), srv.URL, tc.from, tc.to)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("got %q, want an error", got)
				}
				return
			}
			if err != nil || string(got) != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	if _, err := fetchRange(context.Background(), "http://bad host/\x7f", 0, 1); err == nil {
		t.Error("malformed URL: want an error")
	}
}

func TestFetchPoolerHeaderErrors(t *testing.T) {
	serveBody := func(body []byte) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.ServeContent(w, r, "f", testTime, bytes.NewReader(body))
		}))
	}
	le64 := func(n uint64) []byte {
		b := make([]byte, 8)
		binary.LittleEndian.PutUint64(b, n)
		return b
	}
	hdrFile := func(hdr any, data []byte) []byte {
		hj, _ := json.Marshal(hdr)
		return append(append(le64(uint64(len(hj))), hj...), data...)
	}
	f32tensor := func(shape []int, from, to int) map[string]any {
		return map[string]any{"dtype": "F32", "shape": shape, "data_offsets": []int{from, to}}
	}
	tests := []struct {
		name string
		body []byte
		want string
	}{
		{"too short for a length prefix", []byte("abc"), "safetensors header"},
		{"zero header length", le64(0), "header size"},
		{"absurd header length", le64(1 << 40), "header size"},
		{"header longer than file", append(le64(500), []byte("{}")...), "safetensors header"},
		{"header is not JSON", append(le64(5), []byte("not j")...), "safetensors header"},
		{"weight tensor missing", hdrFile(map[string]any{"b": f32tensor([]int{2}, 0, 8)}, make([]byte, 8)), "tensor w not in"},
		{"bias tensor missing", hdrFile(map[string]any{"w": f32tensor([]int{2, 2}, 0, 16)}, make([]byte, 16)), "tensor b not in"},
		{"tensor entry malformed", hdrFile(map[string]any{"w": "oops"}, nil), "tensor w"},
		{"tensor data beyond file", hdrFile(map[string]any{
			"w": f32tensor([]int{2, 2}, 0, 16), "b": f32tensor([]int{2}, 16, 24)}, make([]byte, 4)), "tensor w"},
		{"bias shape mismatch", hdrFile(map[string]any{
			"w": f32tensor([]int{2, 2}, 0, 16), "b": f32tensor([]int{3}, 16, 28)}, make([]byte, 28)), "pooler shapes"},
		{"weight not square", hdrFile(map[string]any{
			"w": f32tensor([]int{2, 3}, 0, 24), "b": f32tensor([]int{2}, 24, 32)}, make([]byte, 32)), "pooler shapes"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := serveBody(tc.body)
			defer srv.Close()
			_, _, err := fetchPooler(context.Background(), Pooler{URL: srv.URL, Weight: "w", Bias: "b"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestToFloat32(t *testing.T) {
	// F16: 1.0 = 0x3c00, -2.0 = 0xc000, 0.5 = 0x3800.
	f16 := make([]byte, 6)
	binary.LittleEndian.PutUint16(f16[0:], 0x3c00)
	binary.LittleEndian.PutUint16(f16[2:], 0xc000)
	binary.LittleEndian.PutUint16(f16[4:], 0x3800)
	// BF16: upper half of the float32: 1.5 = 0x3fc0, -4 = 0xc080.
	bf16 := make([]byte, 4)
	binary.LittleEndian.PutUint16(bf16[0:], 0x3fc0)
	binary.LittleEndian.PutUint16(bf16[2:], 0xc080)

	tests := []struct {
		dtype string
		in    []byte
		want  []float32
	}{
		{"F16", f16, []float32{1, -2, 0.5}},
		{"BF16", bf16, []float32{1.5, -4}},
		{"F32", []byte{0, 0, 0x80, 0x3f}, []float32{1}},
		{"F32", []byte{0, 0, 0x80, 0x3f, 9}, []float32{1}}, // trailing partial value is dropped
	}
	for _, tc := range tests {
		got, err := toFloat32(tc.dtype, tc.in)
		if err != nil || !equalF(got, tc.want) {
			t.Errorf("%s: got %v, %v; want %v", tc.dtype, got, err, tc.want)
		}
	}
	for _, dt := range []string{"I8", "F64", ""} {
		if _, err := toFloat32(dt, []byte{1, 2, 3, 4}); err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Errorf("dtype %q: err = %v", dt, err)
		}
	}
}

// ggufBuilder assembles GGUF headers with arbitrary metadata for AddPooler.
type ggufBuilder struct{ b bytes.Buffer }

func (g *ggufBuilder) u32(v uint32) { _ = binary.Write(&g.b, binary.LittleEndian, v) }
func (g *ggufBuilder) u64(v uint64) { _ = binary.Write(&g.b, binary.LittleEndian, v) }
func (g *ggufBuilder) str(s string) { g.u64(uint64(len(s))); g.b.WriteString(s) }

// header writes magic, version, tensor and kv counts.
func (g *ggufBuilder) header(tensors, kvs uint64) {
	g.b.WriteString("GGUF")
	g.u32(3)
	g.u64(tensors)
	g.u64(kvs)
}

func TestAddPoolerSkipsAllMetadataValueTypes(t *testing.T) {
	var g ggufBuilder
	g.header(1, 6)
	g.str("k.str")
	g.u32(8)
	g.str("hello")
	g.str("k.u8")
	g.u32(0)
	g.b.WriteByte(7)
	g.str("k.f64")
	g.u32(12)
	g.u64(0x4000000000000000)
	g.str("k.arr.u32")
	g.u32(9)
	g.u32(4)
	g.u64(3)
	g.u32(1)
	g.u32(2)
	g.u32(3)
	g.str("k.arr.str")
	g.u32(9)
	g.u32(8)
	g.u64(2)
	g.str("ab")
	g.str("cde")
	g.str("general.alignment")
	g.u32(4)
	g.u32(16) // non-default alignment must be honoured
	// one tensor, F32 [2]
	g.str("cls.output.weight")
	g.u32(1)
	g.u64(2)
	g.u32(0)
	g.u64(0)
	for g.b.Len()%16 != 0 {
		g.b.WriteByte(0)
	}
	_ = binary.Write(&g.b, binary.LittleEndian, []float32{3, 4})

	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src.gguf"), filepath.Join(dir, "dst.gguf")
	if err := os.WriteFile(src, g.b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AddPooler(src, dst, []float32{1, 2, 3, 4}, []float32{5, 6}); err != nil {
		t.Fatal(err)
	}
	if got := tensorData(t, dst, "cls.weight", 4); !equalF(got, []float32{1, 2, 3, 4}) {
		t.Errorf("cls.weight = %v", got)
	}
	if got := tensorData(t, dst, "cls.bias", 2); !equalF(got, []float32{5, 6}) {
		t.Errorf("cls.bias = %v", got)
	}
	// The original tensor must survive unchanged.
	if got := tensorData(t, dst, "cls.output.weight", 2); !equalF(got, []float32{3, 4}) {
		t.Errorf("original tensor = %v", got)
	}
}

func TestAddPoolerMalformedGGUF(t *testing.T) {
	build := func(f func(g *ggufBuilder)) []byte {
		var g ggufBuilder
		f(&g)
		return g.b.Bytes()
	}
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"unsupported scalar type", build(func(g *ggufBuilder) {
			g.header(0, 1)
			g.str("k")
			g.u32(99)
		}), "unsupported value type 99"},
		{"unsupported array element type", build(func(g *ggufBuilder) {
			g.header(0, 1)
			g.str("k")
			g.u32(9)
			g.u32(99)
			g.u64(1)
		}), "unsupported array type 99"},
		{"absurd string length", build(func(g *ggufBuilder) {
			g.header(0, 1)
			g.str("k")
			g.u32(8)
			g.u64(1 << 40)
		}), "bad length"},
		{"metadata cut short", build(func(g *ggufBuilder) {
			g.header(0, 3)
			g.str("k")
			g.u32(4)
			g.u32(1)
		}), "EOF"},
		{"tensor table cut short", build(func(g *ggufBuilder) {
			g.header(2, 0)
			g.str("t")
		}), "EOF"},
		{"already patched", build(func(g *ggufBuilder) {
			g.header(1, 0)
			g.str("cls.weight")
			g.u32(0)
			g.u32(0)
			g.u64(0)
		}), "already has a pooler"},
		{"bad magic", []byte("GGML\x03\x00\x00\x00"), "not a GGUF"},
		{"empty file", nil, "not a GGUF"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "in.gguf")
			if err := os.WriteFile(src, tc.data, 0o644); err != nil {
				t.Fatal(err)
			}
			dst := filepath.Join(dir, "out.gguf")
			err := AddPooler(src, dst, []float32{1}, []float32{1})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
			if _, err := os.Stat(dst); err == nil {
				t.Error("output file created for a rejected input")
			}
		})
	}
}

func TestAddPoolerIOErrors(t *testing.T) {
	dir := t.TempDir()
	if err := AddPooler(filepath.Join(dir, "missing"), filepath.Join(dir, "o"), []float32{1}, []float32{1}); err == nil {
		t.Error("missing source: want an error")
	}
	src := filepath.Join(dir, "in.gguf")
	if err := os.WriteFile(src, tinyGGUF(t, 2), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AddPooler(src, filepath.Join(dir, "no-such-dir", "o"), []float32{1}, []float32{1}); err == nil {
		t.Error("uncreatable destination: want an error")
	}
	if err := AddPooler(src, filepath.Join(dir, "o"), nil, nil); err == nil {
		t.Error("empty pooler: want an error")
	}
}
