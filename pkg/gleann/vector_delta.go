package gleann

import (
	"container/heap"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/tevfik/gleann/modules/hnsw"
)

// The delta file (RFC 005 section 3.3): the vectors of passages added since
// the main vector file was last built, as a flat list searched exactly.
//
//	magic   "GLDL"
//	version u32 (1)
//	dims    u32
//	dtype   u8   (0 f32, 1 f16)
//	metric  u8   (0 l2, 1 cosine, 2 ip)
//	pad     u16
//	count   u64
//	slots   count x int64
//	vectors count x dims x (4 or 2 bytes)
//
// It is rewritten whole at each publication; a passage that died (its source
// changed or was removed) loses its entry then, so the file stays small by
// construction. Slots are never reused, so an entry can never be resolved to
// a passage it was not computed for.
const (
	deltaMagic   = "GLDL"
	deltaVersion = 1
	deltaHeader  = 4 + 4 + 4 + 1 + 1 + 2 + 8

	deltaDTypeF32 = "f32"
	deltaDTypeF16 = "f16"
)

var errDeltaFormat = errors.New("invalid delta file")

// vectorDelta is the in-memory form of a delta file. Vectors are held widened
// to f32; for an f16 delta they were rounded through f16 first, so they equal
// what the file holds and distances match the main file's f16 distances.
type vectorDelta struct {
	dims   int
	dtype  string
	metric string
	slots  []int64
	vecs   []float32 // len(slots) x dims

	pos atomic.Pointer[map[int64]int] // slot -> entry, built on first lookup
}

// newVectorDelta returns an empty delta. dtype "" means f32.
func newVectorDelta(dims int, dtype, metric string) *vectorDelta {
	if dtype != deltaDTypeF16 {
		dtype = deltaDTypeF32
	}
	return &vectorDelta{dims: dims, dtype: dtype, metric: normalizeMetric(metric)}
}

func normalizeMetric(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "cosine":
		return "cosine"
	case "ip", "inner_product":
		return "ip"
	default:
		return "l2"
	}
}

// Len is the number of entries.
func (d *vectorDelta) Len() int {
	if d == nil {
		return 0
	}
	return len(d.slots)
}

// Has reports whether the delta holds an entry for slot.
func (d *vectorDelta) Has(slot int64) bool {
	_, ok := d.position(slot)
	return ok
}

// position returns the entry index of slot.
func (d *vectorDelta) position(slot int64) (int, bool) {
	if d == nil {
		return 0, false
	}
	m := d.pos.Load()
	if m == nil {
		built := make(map[int64]int, len(d.slots))
		for i, s := range d.slots {
			built[s] = i
		}
		d.pos.Store(&built)
		m = &built
	}
	i, ok := (*m)[slot]
	return i, ok
}

// Append adds an entry. The vector is rounded through f16 first in an f16
// delta.
func (d *vectorDelta) Append(slot int64, vec []float32) error {
	if len(vec) != d.dims {
		return fmt.Errorf("delta entry for slot %d has %d dims, expected %d", slot, len(vec), d.dims)
	}
	d.slots = append(d.slots, slot)
	d.pos.Store(nil)
	if d.dtype == deltaDTypeF16 {
		for _, v := range vec {
			d.vecs = append(d.vecs, hnsw.Float16ToFloat32(hnsw.Float32ToFloat16(v)))
		}
	} else {
		d.vecs = append(d.vecs, vec...)
	}
	return nil
}

// Vector returns the i-th entry's vector (a view, not a copy).
func (d *vectorDelta) Vector(i int) []float32 { return d.vecs[i*d.dims : (i+1)*d.dims] }

// Vector32 returns a copy of the vector stored for slot.
func (d *vectorDelta) Vector32(slot int64) ([]float32, bool) {
	i, ok := d.position(slot)
	if !ok {
		return nil, false
	}
	return append([]float32(nil), d.Vector(i)...), true
}

// Without returns a copy that drops every entry for which dead reports true.
func (d *vectorDelta) Without(dead func(slot int64) bool) *vectorDelta {
	out := &vectorDelta{dims: d.dims, dtype: d.dtype, metric: d.metric}
	for i, s := range d.slots {
		if dead(s) {
			continue
		}
		out.slots = append(out.slots, s)
		out.vecs = append(out.vecs, d.Vector(i)...)
	}
	return out
}

// Marshal encodes the delta.
func (d *vectorDelta) Marshal() []byte {
	elem := 4
	if d.dtype == deltaDTypeF16 {
		elem = 2
	}
	buf := make([]byte, deltaHeader+len(d.slots)*8+len(d.vecs)*elem)
	copy(buf, deltaMagic)
	binary.LittleEndian.PutUint32(buf[4:], deltaVersion)
	binary.LittleEndian.PutUint32(buf[8:], uint32(d.dims))
	if d.dtype == deltaDTypeF16 {
		buf[12] = 1
	}
	switch d.metric {
	case "cosine":
		buf[13] = 1
	case "ip":
		buf[13] = 2
	}
	binary.LittleEndian.PutUint64(buf[16:], uint64(len(d.slots)))
	off := deltaHeader
	for _, s := range d.slots {
		binary.LittleEndian.PutUint64(buf[off:], uint64(s))
		off += 8
	}
	if d.dtype == deltaDTypeF16 {
		for _, v := range d.vecs {
			binary.LittleEndian.PutUint16(buf[off:], hnsw.Float32ToFloat16(v))
			off += 2
		}
	} else {
		for _, v := range d.vecs {
			binary.LittleEndian.PutUint32(buf[off:], math.Float32bits(v))
			off += 4
		}
	}
	return buf
}

// parseVectorDelta decodes a delta file.
func parseVectorDelta(b []byte) (*vectorDelta, error) {
	if len(b) < deltaHeader || string(b[:4]) != deltaMagic {
		return nil, fmt.Errorf("%w: bad header", errDeltaFormat)
	}
	if v := binary.LittleEndian.Uint32(b[4:]); v != deltaVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", errDeltaFormat, v)
	}
	dims := int(binary.LittleEndian.Uint32(b[8:]))
	count := binary.LittleEndian.Uint64(b[16:])
	d := &vectorDelta{dims: dims, dtype: deltaDTypeF32}
	elem := 4
	if b[12] == 1 {
		d.dtype, elem = deltaDTypeF16, 2
	}
	switch b[13] {
	case 1:
		d.metric = "cosine"
	case 2:
		d.metric = "ip"
	default:
		d.metric = "l2"
	}
	if dims <= 0 && count > 0 {
		return nil, fmt.Errorf("%w: zero dimensions", errDeltaFormat)
	}
	// Compare in uint64 so a hostile count cannot overflow the size check.
	if count > uint64(len(b)) || uint64(len(b)) != uint64(deltaHeader)+count*8+count*uint64(dims)*uint64(elem) {
		return nil, fmt.Errorf("%w: size %d does not match %d entries of %d dims", errDeltaFormat, len(b), count, dims)
	}
	n := int(count)
	d.slots = make([]int64, n)
	off := deltaHeader
	for i := range d.slots {
		d.slots[i] = int64(binary.LittleEndian.Uint64(b[off:]))
		off += 8
	}
	d.vecs = make([]float32, n*dims)
	if d.dtype == deltaDTypeF16 {
		for i := range d.vecs {
			d.vecs[i] = hnsw.Float16ToFloat32(binary.LittleEndian.Uint16(b[off:]))
			off += 2
		}
	} else {
		for i := range d.vecs {
			d.vecs[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[off:]))
			off += 4
		}
	}
	return d, nil
}

// readVectorDelta loads <path>. A missing file is an empty delta (nil, nil):
// an index built before RFC 005 has none.
func readVectorDelta(path string) (*vectorDelta, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading delta file: %w", err)
	}
	return parseVectorDelta(b)
}

// deltaDistance returns the distance function of a metric, matching the
// backends' conventions: squared L2, 1 - cosine, negated inner product.
func deltaDistance(metric string) func(a, b []float32) float32 {
	switch normalizeMetric(metric) {
	case "cosine":
		return func(a, b []float32) float32 {
			var dot, na, nb float32
			for i := range a {
				dot += a[i] * b[i]
				na += a[i] * a[i]
				nb += b[i] * b[i]
			}
			if na == 0 || nb == 0 {
				return 1
			}
			return 1 - dot/float32(math.Sqrt(float64(na)*float64(nb)))
		}
	case "ip":
		return func(a, b []float32) float32 {
			var dot float32
			for i := range a {
				dot += a[i] * b[i]
			}
			return -dot
		}
	default:
		return func(a, b []float32) float32 {
			var s float32
			for i := range a {
				x := a[i] - b[i]
				s += x * x
			}
			return s
		}
	}
}

// Distance returns the distance from query to the entry of slot; an absent
// slot reports ok=false.
func (d *vectorDelta) Distance(query []float32, slot int64) (float32, bool) {
	if d == nil || len(query) != d.dims {
		return 0, false
	}
	i, ok := d.position(slot)
	if !ok {
		return 0, false
	}
	return deltaDistance(d.metric)(query, d.Vector(i)), true
}

type deltaHit struct {
	slot int64
	dist float32
}

// maxHeap keeps the k closest hits: the root is the farthest of them.
type deltaHeap []deltaHit

func (h deltaHeap) Len() int { return len(h) }
func (h deltaHeap) Less(i, j int) bool {
	if h[i].dist != h[j].dist {
		return h[i].dist > h[j].dist
	}
	return h[i].slot > h[j].slot
}
func (h deltaHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *deltaHeap) Push(x any)   { *h = append(*h, x.(deltaHit)) }
func (h *deltaHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// Search returns the k entries closest to query, nearest first (exact).
func (d *vectorDelta) Search(query []float32, k int) ([]int64, []float32) {
	if d == nil || len(d.slots) == 0 || k <= 0 || len(query) != d.dims {
		return nil, nil
	}
	dist := deltaDistance(d.metric)
	h := make(deltaHeap, 0, k+1)
	for i, s := range d.slots {
		hit := deltaHit{slot: s, dist: dist(query, d.Vector(i))}
		if len(h) < k {
			heap.Push(&h, hit)
		} else if hit.dist < h[0].dist || (hit.dist == h[0].dist && hit.slot < h[0].slot) {
			h[0] = hit
			heap.Fix(&h, 0)
		}
	}
	hits := []deltaHit(h)
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].dist != hits[j].dist {
			return hits[i].dist < hits[j].dist
		}
		return hits[i].slot < hits[j].slot
	})
	ids := make([]int64, len(hits))
	ds := make([]float32, len(hits))
	for i, hit := range hits {
		ids[i], ds[i] = hit.slot, hit.dist
	}
	return ids, ds
}

// mergeVectorHits merges two nearest-first hit lists into one of at most k
// entries. A slot present in both keeps its smaller distance (RFC 005
// section 4: a mixed main/delta pair may hold a slot in both files).
func mergeVectorHits(aIDs []int64, aDist []float32, bIDs []int64, bDist []float32, k int) ([]int64, []float32) {
	best := make(map[int64]float32, len(aIDs)+len(bIDs))
	for i, id := range aIDs {
		best[id] = aDist[i]
	}
	for i, id := range bIDs {
		if cur, ok := best[id]; !ok || bDist[i] < cur {
			best[id] = bDist[i]
		}
	}
	ids := make([]int64, 0, len(best))
	for id := range best {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if best[ids[i]] != best[ids[j]] {
			return best[ids[i]] < best[ids[j]]
		}
		return ids[i] < ids[j]
	})
	if k > 0 && len(ids) > k {
		ids = ids[:k]
	}
	ds := make([]float32, len(ids))
	for i, id := range ids {
		ds[i] = best[id]
	}
	return ids, ds
}
