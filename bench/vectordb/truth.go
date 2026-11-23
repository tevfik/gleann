package vectordb

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
)

// GroundTruth holds exact top-100 nearest neighbor IDs computed by brute force
// for unfiltered queries and standard filtered queries.
type GroundTruth struct {
	K             int       `json:"k"`
	Metric        string    `json:"metric"`
	ExactTop100   [][]int64 `json:"exact_top100"`
	Filtered1Pct  [][]int64 `json:"filtered_1pct"`  // TagMax: 1
	Filtered10Pct [][]int64 `json:"filtered_10pct"` // TagMax: 10
	Filtered50Pct [][]int64 `json:"filtered_50pct"` // TagMax: 50
}

type distItem struct {
	id   int64
	dist float32
}

// ComputeDistance computes the distance between vectors a and b using metric.
// Lower distance means higher similarity.
func ComputeDistance(a, b []float32, metric string) float32 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}

	switch metric {
	case "l2":
		var sum float32
		for i := 0; i < n; i++ {
			d := a[i] - b[i]
			sum += d * d
		}
		return sum

	case "ip":
		var dot float32
		for i := 0; i < n; i++ {
			dot += a[i] * b[i]
		}
		return -dot

	case "cosine":
		fallthrough
	default:
		var dot, normA, normB float32
		for i := 0; i < n; i++ {
			dot += a[i] * b[i]
		}
		for i := 0; i < n; i++ {
			normA += a[i] * a[i]
			normB += b[i] * b[i]
		}
		if normA <= 0 || normB <= 0 {
			return 1.0
		}
		cos := dot / float32(math.Sqrt(float64(normA)*float64(normB)))
		return 1.0 - cos
	}
}

// BruteForceTopK computes the exact top-k IDs for query q against dataset ds
// matching optional filter f.
func BruteForceTopK(ds *Dataset, q []float32, k int, f *Filter, metric string) []int64 {
	items := make([]distItem, 0, len(ds.IDs))
	for i, vec := range ds.Vectors {
		if f != nil && !f.Matches(ds.Metas[i]) {
			continue
		}
		d := ComputeDistance(q, vec, metric)
		items = append(items, distItem{id: ds.IDs[i], dist: d})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].dist == items[j].dist {
			return items[i].id < items[j].id
		}
		return items[i].dist < items[j].dist
	})

	if len(items) > k {
		items = items[:k]
	}
	out := make([]int64, len(items))
	for i, it := range items {
		out[i] = it.id
	}
	return out
}

// ComputeGroundTruth generates exact top-100 nearest neighbors for all queries
// in ds, both unfiltered and for standard 1%, 10%, and 50% selectivity filters.
func ComputeGroundTruth(ds *Dataset, metric string) *GroundTruth {
	if metric == "" {
		metric = "cosine"
	}
	k := 100
	gt := &GroundTruth{
		K:             k,
		Metric:        metric,
		ExactTop100:   make([][]int64, len(ds.Queries)),
		Filtered1Pct:  make([][]int64, len(ds.Queries)),
		Filtered10Pct: make([][]int64, len(ds.Queries)),
		Filtered50Pct: make([][]int64, len(ds.Queries)),
	}

	f1 := &Filter{TagMax: 1}
	f10 := &Filter{TagMax: 10}
	f50 := &Filter{TagMax: 50}

	for qIdx, q := range ds.Queries {
		gt.ExactTop100[qIdx] = BruteForceTopK(ds, q, k, nil, metric)
		gt.Filtered1Pct[qIdx] = BruteForceTopK(ds, q, k, f1, metric)
		gt.Filtered10Pct[qIdx] = BruteForceTopK(ds, q, k, f10, metric)
		gt.Filtered50Pct[qIdx] = BruteForceTopK(ds, q, k, f50, metric)
	}

	return gt
}

// LoadOrComputeGroundTruth loads cached ground truth from disk if present,
// or computes it by brute force and saves it to cacheDir.
func LoadOrComputeGroundTruth(cacheDir string, ds *Dataset, metric string) (*GroundTruth, error) {
	if cacheDir == "" {
		cacheDir = os.TempDir()
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("creating truth cache dir %s: %w", cacheDir, err)
	}

	// Deterministic cache key from dataset size, dims, queries count, and first/last sample
	h := sha256.New()
	fmt.Fprintf(h, "n=%d,dims=%d,q=%d,metric=%s,dup=%g,src=%s", len(ds.IDs), ds.Dims, len(ds.Queries), metric, ds.DupFraction, ds.Source)
	if len(ds.Vectors) > 0 {
		for _, v := range ds.Vectors[0] {
			fmt.Fprintf(h, "%.4f,", v)
		}
	}
	key := fmt.Sprintf("%x", h.Sum(nil))[:16]
	cachePath := filepath.Join(cacheDir, fmt.Sprintf("truth_%d_%d_%s.json", len(ds.IDs), ds.Dims, key))

	if b, err := os.ReadFile(cachePath); err == nil {
		var gt GroundTruth
		if err := json.Unmarshal(b, &gt); err == nil && len(gt.ExactTop100) == len(ds.Queries) {
			return &gt, nil
		}
	}

	gt := ComputeGroundTruth(ds, metric)
	if data, err := json.Marshal(gt); err == nil {
		_ = os.WriteFile(cachePath, data, 0644)
	}
	return gt, nil
}

// RecallAtK computes recall@k between exact ground truth and store result.
// Recall is |groundTruth[:k] ∩ results[:k]| / min(k, |groundTruth|).
func RecallAtK(groundTruth, results []int64, k int) float64 {
	if k <= 0 {
		return 0
	}
	gtK := groundTruth
	if len(gtK) > k {
		gtK = gtK[:k]
	}
	resK := results
	if len(resK) > k {
		resK = resK[:k]
	}
	if len(gtK) == 0 {
		return 1.0
	}
	set := make(map[int64]struct{}, len(gtK))
	for _, id := range gtK {
		set[id] = struct{}{}
	}
	matched := 0
	for _, id := range resK {
		if _, ok := set[id]; ok {
			matched++
		}
	}
	return float64(matched) / float64(len(gtK))
}

// AverageRecallAtK calculates the mean recall@k across all queries.
func AverageRecallAtK(groundTruth, results [][]int64, k int) float64 {
	if len(groundTruth) == 0 || len(results) == 0 {
		return 0
	}
	n := len(groundTruth)
	if len(results) < n {
		n = len(results)
	}
	if n == 0 {
		return 0
	}
	total := 0.0
	for i := 0; i < n; i++ {
		total += RecallAtK(groundTruth[i], results[i], k)
	}
	return total / float64(n)
}
