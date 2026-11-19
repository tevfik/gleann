// Package diskann wraps gleann's pure-Go DiskANN (Vamana) engine
// (modules/diskann). Importing this package registers the "diskann" store.
package diskann

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/tevfik/gleann/bench/vectordb"
	"github.com/tevfik/gleann/modules/diskann"
)

func init() {
	vectordb.Register("diskann", func() vectordb.Store { return &Store{} })
}

const filterPoolCap = 4096

// Store implements vectordb.Store for modules/diskann.
type Store struct {
	dir      string
	params   vectordb.Params
	distFunc diskann.DistanceFunc

	mu         sync.RWMutex
	idx        *diskann.DiskIndex
	idMap      []int64
	metas      map[int64]vectordb.Meta
	tombstones map[int64]struct{}

	// Delta for incremental upserts (RFC 005)
	deltaIDs   []int64
	deltaVecs  [][]float32
	deltaMetas map[int64]vectordb.Meta
}

type metaFile struct {
	IDMap      []int64                 `json:"id_map"`
	Metas      map[int64]vectordb.Meta `json:"metas"`
	Tombstones []int64                 `json:"tombstones"`
}

type deltaFile struct {
	IDs   []int64                 `json:"ids"`
	Vecs  [][]float32             `json:"vecs"`
	Metas map[int64]vectordb.Meta `json:"metas"`
}

func (s *Store) Name() string { return "diskann" }

func (s *Store) Caveats() []string {
	return []string{
		"No native metadata filtering; uses widen-until-k candidate over-fetching and post-filtering (gleann WP-2.5)",
		"No in-place upsert or delete; uses tombstones and delta vectors (gleann RFC 005)",
		"Built with gleann's DiskANN defaults (R=64, L=100, PQ dims/4); the swept ef is search L; exact rerank with gleann's default width (all L candidates)",
	}
}

func (s *Store) SetEfSearch(ef int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.params.EfSearch = ef
}

func (s *Store) Open(ctx context.Context, dir string, p vectordb.Params) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.dir = dir
	s.params = p
	s.distFunc = diskann.GetDistanceFunc(p.Metric)
	s.metas = make(map[int64]vectordb.Meta)
	s.tombstones = make(map[int64]struct{})
	s.deltaMetas = make(map[int64]vectordb.Meta)
	s.deltaIDs = nil
	s.deltaVecs = nil
	s.idMap = nil

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating store directory %s: %w", dir, err)
	}

	indexPath := filepath.Join(dir, "diskann.index")
	if f, err := os.Open(indexPath); err == nil {
		defer f.Close()
		idx, rErr := diskann.ReadDiskIndex(f)
		if rErr != nil {
			return fmt.Errorf("reading diskann index: %w", rErr)
		}
		s.idx = idx
	}

	metaPath := filepath.Join(dir, "diskann.meta")
	if data, err := os.ReadFile(metaPath); err == nil && len(data) > 0 {
		var mf metaFile
		if err := json.Unmarshal(data, &mf); err == nil {
			s.idMap = mf.IDMap
			s.metas = mf.Metas
			for _, id := range mf.Tombstones {
				s.tombstones[id] = struct{}{}
			}
		}
	}

	deltaPath := filepath.Join(dir, "diskann.delta")
	if data, err := os.ReadFile(deltaPath); err == nil && len(data) > 0 {
		var df deltaFile
		if err := json.Unmarshal(data, &df); err == nil {
			s.deltaIDs = df.IDs
			s.deltaVecs = df.Vecs
			s.deltaMetas = df.Metas
		}
	}

	return nil
}

func choosePQDim(dims int) int {
	target := dims / 4
	if target < 1 {
		return 1
	}
	for m := target; m >= 1; m-- {
		if dims%m == 0 {
			return m
		}
	}
	return 1
}

func (s *Store) Insert(ctx context.Context, ids []int64, vecs [][]float32, meta []vectordb.Meta) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(vecs) == 0 {
		return nil
	}

	dims := len(vecs[0])
	metric := diskann.DistanceCosine
	if s.params.Metric == "l2" {
		metric = diskann.DistanceL2
	}
	// Same configuration as gleann's builder (modules/diskann Builder.Build):
	// the shipped defaults (R=64, L=100, alpha 1.2, PQ dims/4, 256
	// centroids). Params.M / EfConstruction are HNSW settings and are not
	// mapped onto R / L.
	cfg := diskann.DiskANNConfig{DistanceMetric: metric}
	cfg.Defaults(dims)
	if dims%cfg.PQDim != 0 {
		// TrainPQ needs dims % PQDim == 0; dims/4 only divides dims that are
		// multiples of 4 (real embedding sizes are). Small test sets differ.
		cfg.PQDim = choosePQDim(dims)
	}
	cfg.PQCentroids = min(cfg.PQCentroids, len(vecs))

	idx, err := diskann.BuildDiskIndex(vecs, cfg)
	if err != nil {
		return fmt.Errorf("building diskann index: %w", err)
	}

	s.idx = idx
	s.idMap = make([]int64, len(ids))
	copy(s.idMap, ids)

	for i, id := range ids {
		if i < len(meta) {
			s.metas[id] = meta[i]
		}
	}

	return nil
}

func (s *Store) Upsert(ctx context.Context, ids []int64, vecs [][]float32, meta []vectordb.Meta) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, id := range ids {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		delete(s.tombstones, id)

		var m vectordb.Meta
		if i < len(meta) {
			m = meta[i]
		}
		s.metas[id] = m

		found := false
		for dIdx, dID := range s.deltaIDs {
			if dID == id {
				s.deltaVecs[dIdx] = vecs[i]
				s.deltaMetas[id] = m
				found = true
				break
			}
		}
		if !found {
			s.deltaIDs = append(s.deltaIDs, id)
			s.deltaVecs = append(s.deltaVecs, vecs[i])
			s.deltaMetas[id] = m
		}
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, ids []int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, id := range ids {
		s.tombstones[id] = struct{}{}
		for dIdx, dID := range s.deltaIDs {
			if dID == id {
				s.deltaIDs = append(s.deltaIDs[:dIdx], s.deltaIDs[dIdx+1:]...)
				s.deltaVecs = append(s.deltaVecs[:dIdx], s.deltaVecs[dIdx+1:]...)
				delete(s.deltaMetas, id)
				break
			}
		}
	}
	return nil
}

func (s *Store) Flush(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.dir == "" {
		return fmt.Errorf("flush called before open")
	}

	if s.idx != nil {
		var buf bytes.Buffer
		if _, err := s.idx.WriteTo(&buf); err != nil {
			return fmt.Errorf("writing diskann index: %w", err)
		}
		if err := os.WriteFile(filepath.Join(s.dir, "diskann.index"), buf.Bytes(), 0644); err != nil {
			return fmt.Errorf("saving diskann index: %w", err)
		}
	}

	var tombList []int64
	for id := range s.tombstones {
		tombList = append(tombList, id)
	}
	mf := metaFile{
		IDMap:      s.idMap,
		Metas:      s.metas,
		Tombstones: tombList,
	}
	mfBytes, err := json.Marshal(mf)
	if err != nil {
		return fmt.Errorf("marshaling diskann meta: %w", err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "diskann.meta"), mfBytes, 0644); err != nil {
		return fmt.Errorf("saving diskann meta: %w", err)
	}

	df := deltaFile{
		IDs:   s.deltaIDs,
		Vecs:  s.deltaVecs,
		Metas: s.deltaMetas,
	}
	dfBytes, err := json.Marshal(df)
	if err != nil {
		return fmt.Errorf("marshaling diskann delta: %w", err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "diskann.delta"), dfBytes, 0644); err != nil {
		return fmt.Errorf("saving diskann delta: %w", err)
	}

	return nil
}

type candidate struct {
	id   int64
	dist float32
}

func (s *Store) Search(ctx context.Context, q []float32, k int, f *vectordb.Filter) ([]int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if k <= 0 {
		return nil, nil
	}

	searchL := s.params.EfSearch
	if searchL <= 0 {
		searchL = 100
	}
	if searchL < k {
		searchL = k
	}

	deltaMatches := make(map[int64]candidate)
	for i, id := range s.deltaIDs {
		if _, dead := s.tombstones[id]; dead {
			continue
		}
		m, ok := s.deltaMetas[id]
		if !ok {
			m = s.metas[id]
		}
		if f != nil && !f.Matches(m) {
			continue
		}
		d := s.distFunc(q, s.deltaVecs[i])
		deltaMatches[id] = candidate{id: id, dist: d}
	}

	candMap := make(map[int64]candidate, len(deltaMatches))
	for id, c := range deltaMatches {
		candMap[id] = c
	}

	if s.idx != nil && s.idx.NumNodes > 0 {
		totalNodes := int(s.idx.NumNodes)
		pool := max(k*4, 50)
		if f == nil && len(s.tombstones) == 0 && len(s.deltaIDs) == 0 {
			pool = k
		}

		for {
			curL := max(searchL, pool)
			// gleann's searcher (modules/diskann Searcher.Search) calls
			// SearchExactRerank with the configured width; unset, as here,
			// that is the default (DefaultRerankK), which this mirrors.
			rawCands := diskann.SearchExact(s.idx, q, pool, curL, s.distFunc)

			validCount := len(candMap)
			for _, rc := range rawCands {
				if rc.ID < 0 || int(rc.ID) >= len(s.idMap) {
					continue
				}
				id := s.idMap[rc.ID]
				if _, dead := s.tombstones[id]; dead {
					continue
				}
				if _, inDelta := deltaMatches[id]; inDelta {
					continue
				}
				m := s.metas[id]
				if f != nil && !f.Matches(m) {
					continue
				}
				candMap[id] = candidate{id: id, dist: rc.Distance}
				validCount++
			}

			if validCount >= k || pool >= filterPoolCap || pool >= totalNodes {
				break
			}
			pool = min(pool*2, filterPoolCap)
		}
	}

	all := make([]candidate, 0, len(candMap))
	for _, c := range candMap {
		all = append(all, c)
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].dist == all[j].dist {
			return all[i].id < all[j].id
		}
		return all[i].dist < all[j].dist
	})

	if len(all) > k {
		all = all[:k]
	}

	out := make([]int64, len(all))
	for i, c := range all {
		out[i] = c.id
	}
	return out, nil
}

func (s *Store) Stats(ctx context.Context) (vectordb.Stats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int64
	seen := make(map[int64]bool)
	for _, id := range s.deltaIDs {
		if _, dead := s.tombstones[id]; !dead {
			seen[id] = true
			count++
		}
	}
	if s.idx != nil {
		for _, id := range s.idMap {
			if !seen[id] {
				if _, dead := s.tombstones[id]; !dead {
					seen[id] = true
					count++
				}
			}
		}
	}

	var diskBytes int64
	if s.dir != "" {
		_ = filepath.Walk(s.dir, func(_ string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				diskBytes += info.Size()
			}
			return nil
		})
	}

	return vectordb.Stats{
		Count:     count,
		DiskBytes: diskBytes,
		ServerRSS: 0,
	}, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.idx != nil {
		_ = s.idx.Close()
		s.idx = nil
	}
	s.idMap = nil
	s.metas = nil
	s.tombstones = nil
	s.deltaIDs = nil
	s.deltaVecs = nil
	s.deltaMetas = nil
	return nil
}
