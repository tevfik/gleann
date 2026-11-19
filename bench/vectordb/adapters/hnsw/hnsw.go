// Package hnsw is the reference adapter wrapping gleann's pure-Go HNSW engine
// (modules/hnsw). Importing this package registers the "hnsw" store.
package hnsw

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
	"github.com/tevfik/gleann/modules/hnsw"
)

func init() {
	vectordb.Register("hnsw", func() vectordb.Store { return &Store{} })
}

const filterPoolCap = 4096

// Store implements vectordb.Store for modules/hnsw.
type Store struct {
	dir      string
	params   vectordb.Params
	distFunc hnsw.DistanceFunc

	mu         sync.RWMutex
	graph      *hnsw.Graph
	metas      map[int64]vectordb.Meta
	tombstones map[int64]struct{}

	// Delta for incremental upserts (RFC 005)
	deltaIDs   []int64
	deltaVecs  [][]float32
	deltaMetas map[int64]vectordb.Meta
}

type metaFile struct {
	Metas      map[int64]vectordb.Meta `json:"metas"`
	Tombstones []int64                 `json:"tombstones"`
}

type deltaFile struct {
	IDs   []int64                 `json:"ids"`
	Vecs  [][]float32             `json:"vecs"`
	Metas map[int64]vectordb.Meta `json:"metas"`
}

func (s *Store) Name() string { return "hnsw" }

func (s *Store) Caveats() []string {
	return []string{
		"No native metadata filtering; uses widen-until-k candidate over-fetching and post-filtering (gleann WP-2.5)",
		"No in-place delete; uses tombstones and delta vectors (gleann RFC 005)",
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
	s.distFunc = hnsw.GetDistanceFunc(p.Metric)
	s.metas = make(map[int64]vectordb.Meta)
	s.tombstones = make(map[int64]struct{})
	s.deltaMetas = make(map[int64]vectordb.Meta)
	s.deltaIDs = nil
	s.deltaVecs = nil

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating store directory %s: %w", dir, err)
	}

	indexPath := filepath.Join(dir, "hnsw.index")
	if data, err := os.ReadFile(indexPath); err == nil && len(data) > 0 {
		csr, err := hnsw.ReadCSR(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("reading hnsw csr: %w", err)
		}
		s.graph = csr.ToGraphWithDistance(s.distFunc)
	}

	metaPath := filepath.Join(dir, "hnsw.meta")
	if data, err := os.ReadFile(metaPath); err == nil && len(data) > 0 {
		var mf metaFile
		if err := json.Unmarshal(data, &mf); err == nil {
			s.metas = mf.Metas
			for _, id := range mf.Tombstones {
				s.tombstones[id] = struct{}{}
			}
		}
	}

	deltaPath := filepath.Join(dir, "hnsw.delta")
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

func (s *Store) Insert(ctx context.Context, ids []int64, vecs [][]float32, meta []vectordb.Meta) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(vecs) == 0 {
		return nil
	}

	m := s.params.M
	if m <= 0 {
		m = 32
	}
	efConstruction := s.params.EfConstruction
	if efConstruction <= 0 {
		efConstruction = 200
	}

	dims := len(vecs[0])
	s.graph = hnsw.NewGraphWithOptions(m, efConstruction, dims, s.distFunc, true)

	for i, vec := range vecs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		id := ids[i]
		s.graph.Insert(id, vec)
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

		// Check if already in delta
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

	if s.graph != nil {
		csr := hnsw.ConvertToCSR(s.graph)
		csr.VectorDType = hnsw.VectorDTypeF32
		hnsw.PruneEmbeddings(s.graph, csr, 1.0) // keep full embeddings

		var buf bytes.Buffer
		if _, err := csr.WriteTo(&buf); err != nil {
			return fmt.Errorf("writing csr index: %w", err)
		}
		if err := os.WriteFile(filepath.Join(s.dir, "hnsw.index"), buf.Bytes(), 0644); err != nil {
			return fmt.Errorf("saving hnsw index: %w", err)
		}
	}

	// Write metadata and tombstones
	var tombList []int64
	for id := range s.tombstones {
		tombList = append(tombList, id)
	}
	mf := metaFile{
		Metas:      s.metas,
		Tombstones: tombList,
	}
	mfBytes, err := json.Marshal(mf)
	if err != nil {
		return fmt.Errorf("marshaling meta file: %w", err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "hnsw.meta"), mfBytes, 0644); err != nil {
		return fmt.Errorf("saving hnsw meta: %w", err)
	}

	// Write delta file
	df := deltaFile{
		IDs:   s.deltaIDs,
		Vecs:  s.deltaVecs,
		Metas: s.deltaMetas,
	}
	dfBytes, err := json.Marshal(df)
	if err != nil {
		return fmt.Errorf("marshaling delta file: %w", err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "hnsw.delta"), dfBytes, 0644); err != nil {
		return fmt.Errorf("saving hnsw delta: %w", err)
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

	ef := s.params.EfSearch
	if ef <= 0 {
		ef = 128
	}
	if ef < k {
		ef = k
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

	if s.graph != nil && s.graph.Size() > 0 {
		totalNodes := int(s.graph.Size())
		pool := max(k*4, 50)
		if f == nil && len(s.tombstones) == 0 && len(s.deltaIDs) == 0 {
			pool = k
		}

		for {
			searchEf := max(ef, pool)
			rawCands := s.graph.Search(q, pool, searchEf)

			validCount := len(candMap)
			for _, rc := range rawCands {
				id := rc.ID
				if _, dead := s.tombstones[id]; dead {
					continue
				}
				// If superseded by delta, delta candidate takes precedence
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
	if s.graph != nil {
		for _, id := range s.graph.AllNodeIDs() {
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

	s.graph = nil
	s.metas = nil
	s.tombstones = nil
	s.deltaIDs = nil
	s.deltaVecs = nil
	s.deltaMetas = nil
	return nil
}
