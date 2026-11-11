package modelcatalog

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tinyGGUF builds a GGUF v3 with one string kv, general.alignment and one
// F32 tensor "cls.output.weight" [dim].
func tinyGGUF(t *testing.T, dim int) []byte {
	t.Helper()
	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	str := func(s string) { w(uint64(len(s))); b.WriteString(s) }
	b.WriteString("GGUF")
	w(uint32(3))
	w(uint64(1)) // tensors
	w(uint64(2)) // kv
	str("general.architecture")
	w(uint32(8))
	str("bert")
	str("general.alignment")
	w(uint32(4))
	w(uint32(32))
	str("cls.output.weight")
	w(uint32(1))
	w(uint64(dim))
	w(uint32(0))
	w(uint64(0))
	for b.Len()%32 != 0 {
		b.WriteByte(0)
	}
	for i := 0; i < dim; i++ {
		w(float32(i))
	}
	return b.Bytes()
}

// safetensors builds a safetensors file with the given tensors.
func safetensors(t *testing.T, dtype string, tensors map[string][]float32, shapes map[string][]int64) []byte {
	t.Helper()
	hdr := map[string]any{}
	var data bytes.Buffer
	names := []string{"bert.pooler.dense.weight", "bert.pooler.dense.bias"}
	for _, n := range names {
		start := data.Len()
		for _, v := range tensors[n] {
			switch dtype {
			case "F32":
				_ = binary.Write(&data, binary.LittleEndian, math.Float32bits(v))
			case "BF16":
				_ = binary.Write(&data, binary.LittleEndian, uint16(math.Float32bits(v)>>16))
			}
		}
		hdr[n] = map[string]any{"dtype": dtype, "shape": shapes[n], "data_offsets": []int{start, data.Len()}}
	}
	hj, _ := json.Marshal(hdr)
	var out bytes.Buffer
	_ = binary.Write(&out, binary.LittleEndian, uint64(len(hj)))
	out.Write(hj)
	out.Write(data.Bytes())
	return out.Bytes()
}

// tensorData reads the named F32 tensor of a GGUF written by AddPooler.
func tensorData(t *testing.T, path, name string, n int) []float32 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, _ := f.Stat()
	g := &ggufReader{r: newBuf(f)}
	g.read(4)
	g.u32()
	nt, nkv := g.u64(), g.u64()
	align := int64(32)
	for i := uint64(0); i < nkv; i++ {
		k := g.str()
		ty := g.u32()
		if v := g.skipValue(ty); k == "general.alignment" && v > 0 {
			align = int64(v)
		}
	}
	var off uint64
	found := false
	for i := uint64(0); i < nt; i++ {
		tn := g.str()
		nd := g.u32()
		for d := uint32(0); d < nd; d++ {
			g.u64()
		}
		g.u32()
		o := g.u64()
		if tn == name {
			off, found = o, true
		}
	}
	if !found || g.err != nil {
		t.Fatalf("tensor %s not found (err %v)", name, g.err)
	}
	start := (g.pos+align-1)/align*align + int64(off)
	if start+int64(4*n) > fi.Size() {
		t.Fatalf("tensor %s out of range", name)
	}
	buf := make([]byte, 4*n)
	if _, err := f.ReadAt(buf, start); err != nil {
		t.Fatal(err)
	}
	out := make([]float32, n)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[4*i:]))
	}
	return out
}

func TestAddPoolerRoundTrip(t *testing.T) {
	const dim = 4
	for _, dtype := range []string{"F32", "BF16"} {
		t.Run(dtype, func(t *testing.T) {
			wt := make([]float32, dim*dim)
			for i := range wt {
				wt[i] = float32(i) / 4 // exact in BF16
			}
			bs := []float32{1, -2, 0.5, 8}
			st := safetensors(t, dtype,
				map[string][]float32{"bert.pooler.dense.weight": wt, "bert.pooler.dense.bias": bs},
				map[string][]int64{"bert.pooler.dense.weight": {dim, dim}, "bert.pooler.dense.bias": {dim}})
			var ranges int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Range") != "" {
					ranges++
				}
				http.ServeContent(w, r, "m.safetensors", testTime, bytes.NewReader(st))
			}))
			defer srv.Close()

			dir := t.TempDir()
			src := filepath.Join(dir, "in.gguf")
			if err := os.WriteFile(src, tinyGGUF(t, dim), 0o644); err != nil {
				t.Fatal(err)
			}
			p := Pooler{URL: srv.URL, Weight: "bert.pooler.dense.weight", Bias: "bert.pooler.dense.bias"}
			w, b, err := fetchPooler(context.Background(), p)
			if err != nil {
				t.Fatal(err)
			}
			if ranges != 4 {
				t.Errorf("range requests = %d, want 4 (no full download)", ranges)
			}
			dst := filepath.Join(dir, "out.gguf")
			if err := AddPooler(src, dst, w, b); err != nil {
				t.Fatal(err)
			}
			if got := tensorData(t, dst, "cls.weight", dim*dim); !equalF(got, wt) {
				t.Fatalf("cls.weight = %v", got)
			}
			if got := tensorData(t, dst, "cls.bias", dim); !equalF(got, bs) {
				t.Fatalf("cls.bias = %v", got)
			}
			// The original tensor is untouched.
			if got := tensorData(t, dst, "cls.output.weight", dim); !equalF(got, []float32{0, 1, 2, 3}) {
				t.Fatalf("cls.output.weight = %v", got)
			}
			// A second patch is refused.
			if err := AddPooler(dst, filepath.Join(dir, "again.gguf"), w, b); err == nil || !strings.Contains(err.Error(), "already") {
				t.Fatalf("second patch: %v", err)
			}
		})
	}
}

func TestF16ToF32(t *testing.T) {
	cases := map[uint16]float32{0x3c00: 1, 0xc000: -2, 0x3800: 0.5, 0x0000: 0, 0x0001: float32(math.Pow(2, -24)), 0x7bff: 65504}
	for h, want := range cases {
		if got := f16ToF32(h); got != want {
			t.Errorf("f16ToF32(%#x) = %v, want %v", h, got, want)
		}
	}
}

func TestAddPoolerRejectsNonGGUF(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "x.gguf")
	_ = os.WriteFile(src, []byte("nope"), 0o644)
	if err := AddPooler(src, filepath.Join(dir, "y"), []float32{1}, []float32{1}); err == nil {
		t.Fatal("want error for a non-GGUF file")
	}
}

// TestAddPoolerMatchesReference patches a real GGUF from Hugging Face and
// compares it with a reference file (opt-in: GLEANN_POOLER_REF_IN,
// GLEANN_POOLER_REF_OUT = the same patch made by another tool).
func TestAddPoolerMatchesReference(t *testing.T) {
	in, ref := os.Getenv("GLEANN_POOLER_REF_IN"), os.Getenv("GLEANN_POOLER_REF_OUT")
	if in == "" || ref == "" {
		t.Skip("GLEANN_POOLER_REF_IN / GLEANN_POOLER_REF_OUT not set")
	}
	e, ok := Find("ms-marco-minilm-l6")
	if !ok || e.Pooler == nil {
		t.Fatal("catalog entry ms-marco-minilm-l6 with a pooler not found")
	}
	w, b, err := fetchPooler(context.Background(), *e.Pooler)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out.gguf")
	if err := AddPooler(in, out, w, b); err != nil {
		t.Fatal(err)
	}
	got, want := fileSHA(t, out), fileSHA(t, ref)
	if got != want {
		t.Fatalf("sha256 %s, reference %s", got, want)
	}
	if fi, _ := os.Stat(out); fi.Size() != e.Pooler.Bytes {
		t.Fatalf("size %d, catalog says %d", fi.Size(), e.Pooler.Bytes)
	}
}

func fileSHA(t *testing.T, p string) string {
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	_, _ = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil))
}

func equalF(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

var testTime = time.Unix(0, 0)

func newBuf(r io.Reader) *bufio.Reader { return bufio.NewReader(r) }

func TestDownloadAddsPooler(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const dim = 4
	gg := tinyGGUF(t, dim)
	wt := make([]float32, dim*dim)
	for i := range wt {
		wt[i] = float32(i)
	}
	st := safetensors(t, "F32",
		map[string][]float32{"bert.pooler.dense.weight": wt, "bert.pooler.dense.bias": {1, 2, 3, 4}},
		map[string][]int64{"bert.pooler.dense.weight": {dim, dim}, "bert.pooler.dense.bias": {dim}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/m.gguf":
			http.ServeContent(w, r, "m.gguf", testTime, bytes.NewReader(gg))
		case "/m.safetensors":
			http.ServeContent(w, r, "m.safetensors", testTime, bytes.NewReader(st))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	sum := sha256.Sum256(gg)

	// The patched size: compute it once with AddPooler.
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	_ = os.WriteFile(src, gg, 0o644)
	if err := AddPooler(src, dst, wt, []float32{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(dst)

	e := Entry{ID: "x", Name: "x", Kind: Reranker, File: "x-pooler.gguf", URL: srv.URL + "/m.gguf",
		Bytes: int64(len(gg)), SHA256: hex.EncodeToString(sum[:]),
		Pooler: &Pooler{URL: srv.URL + "/m.safetensors", Weight: "bert.pooler.dense.weight", Bias: "bert.pooler.dense.bias", Bytes: fi.Size()}}
	if err := Download(context.Background(), e, nil); err != nil {
		t.Fatal(err)
	}
	if !e.Installed() {
		t.Fatal("patched model not reported installed")
	}
	if got := tensorData(t, e.Path(), "cls.bias", dim); !equalF(got, []float32{1, 2, 3, 4}) {
		t.Fatalf("cls.bias = %v", got)
	}
	// A wrong expected size is rejected and leaves nothing behind.
	_ = os.Remove(e.Path())
	e.Pooler.Bytes++
	if err := Download(context.Background(), e, nil); err == nil {
		t.Fatal("want a size error")
	}
	if _, err := os.Stat(e.Path()); !os.IsNotExist(err) {
		t.Fatal("a failed patch left a file under the real name")
	}
}
