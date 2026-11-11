package modelcatalog

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
)

// Pooler names the BERT pooler (dense + tanh) of a Hugging Face
// cross-encoder checkpoint. Community GGUFs of cross-encoder/ms-marco-MiniLM
// keep only the classifier (cls.output.*) and drop bert.pooler.dense, so the
// model scores h[CLS] without the projection it was trained with and its
// scores are nearly constant. After downloading such a GGUF, gleann fetches
// just the two pooler tensors from the safetensors file (HTTP range
// requests, ~600 KB) and appends them as cls.weight / cls.bias, which
// bitllm.c and llama.cpp use as the pooler.
type Pooler struct {
	// URL of the model's .safetensors file.
	URL string `json:"url"`
	// Weight and Bias are the tensor names, e.g. "bert.pooler.dense.weight".
	Weight string `json:"weight"`
	Bias   string `json:"bias"`
	// Bytes is the size of the patched GGUF.
	Bytes int64 `json:"bytes"`
}

// stTensor is one entry of a safetensors header.
type stTensor struct {
	Dtype   string   `json:"dtype"`
	Shape   []int64  `json:"shape"`
	Offsets [2]int64 `json:"data_offsets"`
}

// fetchRange returns bytes [from, to) of url.
func fetchRange(ctx context.Context, url string, from, to int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "gleann")
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, to-1))
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusPartialContent:
		b, err := io.ReadAll(io.LimitReader(resp.Body, to-from+1))
		if err == nil && int64(len(b)) != to-from {
			err = fmt.Errorf("short range read: %d of %d bytes", len(b), to-from)
		}
		return b, err
	case http.StatusOK:
		// Server ignored Range: skip to the window.
		if _, err := io.CopyN(io.Discard, resp.Body, from); err != nil {
			return nil, err
		}
		b := make([]byte, to-from)
		_, err := io.ReadFull(resp.Body, b)
		return b, err
	default:
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
}

// fetchPooler reads the pooler weight [dim, dim] and bias [dim] from a
// remote safetensors file as float32.
func fetchPooler(ctx context.Context, p Pooler) (weight, bias []float32, err error) {
	head, err := fetchRange(ctx, p.URL, 0, 8)
	if err != nil {
		return nil, nil, fmt.Errorf("safetensors header: %w", err)
	}
	n := int64(binary.LittleEndian.Uint64(head))
	if n <= 0 || n > 100<<20 {
		return nil, nil, fmt.Errorf("safetensors header size %d", n)
	}
	hj, err := fetchRange(ctx, p.URL, 8, 8+n)
	if err != nil {
		return nil, nil, fmt.Errorf("safetensors header: %w", err)
	}
	var hdr map[string]json.RawMessage
	if err := json.Unmarshal(hj, &hdr); err != nil {
		return nil, nil, fmt.Errorf("safetensors header: %w", err)
	}
	load := func(name string) ([]float32, []int64, error) {
		raw, ok := hdr[name]
		if !ok {
			return nil, nil, fmt.Errorf("tensor %s not in %s", name, p.URL)
		}
		var t stTensor
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, nil, fmt.Errorf("tensor %s: %w", name, err)
		}
		if t.Offsets[0] < 0 || t.Offsets[1] <= t.Offsets[0] || t.Offsets[1]-t.Offsets[0] > 64<<20 {
			return nil, nil, fmt.Errorf("tensor %s: bad data offsets %v", name, t.Offsets)
		}
		b, err := fetchRange(ctx, p.URL, 8+n+t.Offsets[0], 8+n+t.Offsets[1])
		if err != nil {
			return nil, nil, fmt.Errorf("tensor %s: %w", name, err)
		}
		v, err := toFloat32(t.Dtype, b)
		return v, t.Shape, err
	}
	weight, ws, err := load(p.Weight)
	if err != nil {
		return nil, nil, err
	}
	bias, bs, err := load(p.Bias)
	if err != nil {
		return nil, nil, err
	}
	if len(bs) != 1 || len(ws) != 2 || ws[0] != bs[0] || ws[1] != bs[0] ||
		int64(len(bias)) != bs[0] || int64(len(weight)) != ws[0]*ws[1] {
		return nil, nil, fmt.Errorf("pooler shapes %v / %v (%d / %d values), want [d d] / [d]", ws, bs, len(weight), len(bias))
	}
	return weight, bias, nil
}

func toFloat32(dtype string, b []byte) ([]float32, error) {
	switch dtype {
	case "F32":
		out := make([]float32, len(b)/4)
		for i := range out {
			out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
		}
		return out, nil
	case "BF16":
		out := make([]float32, len(b)/2)
		for i := range out {
			out[i] = math.Float32frombits(uint32(binary.LittleEndian.Uint16(b[2*i:])) << 16)
		}
		return out, nil
	case "F16":
		out := make([]float32, len(b)/2)
		for i := range out {
			out[i] = f16ToF32(binary.LittleEndian.Uint16(b[2*i:]))
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported safetensors dtype %s", dtype)
}

func f16ToF32(h uint16) float32 {
	sign := uint32(h>>15) << 31
	exp := uint32(h>>10) & 0x1f
	frac := uint32(h) & 0x3ff
	switch {
	case exp == 0 && frac == 0:
		return math.Float32frombits(sign)
	case exp == 0: // subnormal
		for frac&0x400 == 0 {
			frac <<= 1
			exp--
		}
		exp++
		frac &= 0x3ff
	case exp == 0x1f:
		return math.Float32frombits(sign | 0xff<<23 | frac<<13)
	}
	return math.Float32frombits(sign | (exp+112)<<23 | frac<<13)
}

// gguf value type sizes (fixed-size types).
var ggufSize = map[uint32]int64{0: 1, 1: 1, 2: 2, 3: 2, 4: 4, 5: 4, 6: 4, 7: 1, 10: 8, 11: 8, 12: 8}

type ggufReader struct {
	r   *bufio.Reader
	pos int64
	err error
}

func (g *ggufReader) read(n int64) []byte {
	if g.err != nil {
		return nil
	}
	if n < 0 || n > 1<<30 {
		g.err = fmt.Errorf("gguf: bad length %d", n)
		return nil
	}
	b := make([]byte, n)
	_, g.err = io.ReadFull(g.r, b)
	g.pos += n
	return b
}
func (g *ggufReader) u32() uint32 {
	b := g.read(4)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}
func (g *ggufReader) u64() uint64 {
	b := g.read(8)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint64(b)
}
func (g *ggufReader) str() string { return string(g.read(int64(g.u64()))) }

// skipValue skips a metadata value of type t and returns its first four
// bytes for uint32 values (used for general.alignment).
func (g *ggufReader) skipValue(t uint32) uint32 {
	switch t {
	case 8:
		g.str()
	case 9:
		et, n := g.u32(), int64(g.u64())
		if et == 8 {
			for i := int64(0); i < n && g.err == nil; i++ {
				g.str()
			}
		} else if sz, ok := ggufSize[et]; ok {
			g.read(sz * n)
		} else {
			g.err = fmt.Errorf("gguf: unsupported array type %d", et)
		}
	case 4:
		return g.u32()
	default:
		sz, ok := ggufSize[t]
		if !ok {
			g.err = fmt.Errorf("gguf: unsupported value type %d", t)
			return 0
		}
		g.read(sz)
	}
	return 0
}

// AddPooler writes dst: the GGUF src with cls.weight [dim, dim] and cls.bias
// [dim] (F32) appended. weight is row-major [out][in], as in PyTorch. It
// fails if src already has cls.weight.
func AddPooler(src, dst string, weight, bias []float32) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	g := &ggufReader{r: bufio.NewReaderSize(in, 1<<20)}
	if string(g.read(4)) != "GGUF" {
		return fmt.Errorf("%s: not a GGUF file", src)
	}
	ver := g.u32()
	nt, nkv := g.u64(), g.u64()
	kvStart := g.pos
	align := int64(32)
	for i := uint64(0); i < nkv && g.err == nil; i++ {
		k := g.str()
		t := g.u32()
		v := g.skipValue(t)
		if k == "general.alignment" && t == 4 && v > 0 {
			align = int64(v)
		}
	}
	if g.err != nil {
		return fmt.Errorf("%s: %w", src, g.err)
	}
	kv := make([]byte, g.pos-kvStart)
	if _, err := in.ReadAt(kv, kvStart); err != nil {
		return err
	}
	type tinfo struct {
		name string
		dims []uint64
		typ  uint32
		off  uint64
	}
	var tensors []tinfo
	for i := uint64(0); i < nt && g.err == nil; i++ {
		ti := tinfo{name: g.str()}
		nd := g.u32()
		for d := uint32(0); d < nd; d++ {
			ti.dims = append(ti.dims, g.u64())
		}
		ti.typ, ti.off = g.u32(), g.u64()
		if ti.name == "cls.weight" {
			return errors.New("GGUF already has a pooler (cls.weight)")
		}
		tensors = append(tensors, ti)
	}
	if g.err != nil {
		return fmt.Errorf("%s: %w", src, g.err)
	}
	pad := func(n int64) int64 { return (n + align - 1) / align * align }
	dataStart := pad(g.pos)
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	blobLen := fi.Size() - dataStart
	if blobLen < 0 {
		return fmt.Errorf("%s: truncated GGUF (data starts at %d, file has %d bytes)", src, dataStart, fi.Size())
	}
	if len(weight) != len(bias)*len(bias) || len(bias) == 0 {
		return fmt.Errorf("pooler: weight has %d values, want %d", len(weight), len(bias)*len(bias))
	}
	dim := uint64(len(bias))
	offW := uint64(pad(blobLen))
	offB := uint64(pad(int64(offW) + int64(4*len(weight))))
	tensors = append(tensors, tinfo{"cls.weight", []uint64{dim, dim}, 0, offW}, tinfo{"cls.bias", []uint64{dim}, 0, offB})

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(out, 1<<20)
	var n int64
	put := func(b []byte) { m, _ := w.Write(b); n += int64(m) }
	le := func(v any) {
		var b [8]byte
		switch x := v.(type) {
		case uint32:
			binary.LittleEndian.PutUint32(b[:], x)
			put(b[:4])
		case uint64:
			binary.LittleEndian.PutUint64(b[:], x)
			put(b[:8])
		}
	}
	zeros := func(k int64) { put(make([]byte, k)) }
	put([]byte("GGUF"))
	le(ver)
	le(uint64(len(tensors)))
	le(nkv)
	put(kv)
	for _, t := range tensors {
		le(uint64(len(t.name)))
		put([]byte(t.name))
		le(uint32(len(t.dims)))
		for _, d := range t.dims {
			le(d)
		}
		le(t.typ)
		le(t.off)
	}
	zeros(pad(n) - n)
	if _, err := in.Seek(dataStart, io.SeekStart); err != nil {
		out.Close()
		return err
	}
	m, err := io.Copy(w, in)
	if err != nil {
		out.Close()
		return err
	}
	n += m
	base := n - blobLen // file offset of the data blob
	zeros(base + int64(offW) - n)
	f32 := make([]byte, 4)
	for _, v := range weight {
		binary.LittleEndian.PutUint32(f32, math.Float32bits(v))
		put(f32)
	}
	zeros(base + int64(offB) - n)
	for _, v := range bias {
		binary.LittleEndian.PutUint32(f32, math.Float32bits(v))
		put(f32)
	}
	if err := w.Flush(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
