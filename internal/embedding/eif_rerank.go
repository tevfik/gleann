//go:build eif

package embedding

/*
#include "eif_bert.h"
#include <stdlib.h>
#if defined(__linux__) && defined(__has_include)
#if __has_include(<omp.h>)
#include <omp.h>
#define GLEANN_RANK_HAVE_OMP 1
#endif
#endif

// One pair per call on one OpenMP thread: the batch is spread over several
// engine instances instead (see EIFRanker.Rank).
static inline int eif_rank_one_thread(eif_bert_t *bert, const char *q, const char *d, float *score) {
#ifdef GLEANN_RANK_HAVE_OMP
    omp_set_num_threads(1);
#endif
    return eif_bert_rank(bert, q, d, score);
}
*/
import "C"

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/tevfik/gleann/internal/modelcatalog"
	"github.com/tevfik/gleann/pkg/gleann"
)

func init() {
	gleann.RegisterEIFRanker(func(ctx context.Context, model, query string, docs []string) ([]float32, error) {
		r, err := GetOrLoadEIFRanker(model)
		if err != nil {
			return nil, err
		}
		return r.Rank(ctx, query, docs)
	})
}

// EIFRanker scores (query, document) pairs with a GGUF cross-encoder in
// process. Like EIFComputer it keeps several engine instances (the weights are
// mapped read-only and shared) and gives each one pair at a time.
type EIFRanker struct {
	modelPath string
	mu        sync.Mutex
	workers   []*C.eif_bert_t
}

var (
	eifRankerMu    sync.Mutex
	eifRankerCache = make(map[string]*EIFRanker)
)

// resolveRerankModelPath finds a reranker GGUF: a path, or a file in
// ~/.gleann/models named model, model.gguf, or model-<quant>.gguf (Q8_0 is
// preferred), or a catalog id such as "ms-marco-minilm-l6". Unlike the embedding resolver it never falls back to another
// model: an embedding model loaded as a reranker would give meaningless scores.
func resolveRerankModelPath(model string) (string, error) {
	if model == "" {
		model = gleann.DefaultEIFRerankModel
	}
	if fi, err := os.Stat(model); err == nil && !fi.IsDir() {
		return model, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".gleann", "models")
	base := filepath.Base(model)
	for _, c := range []string{base, base + ".gguf"} {
		if fi, err := os.Stat(filepath.Join(dir, c)); err == nil && !fi.IsDir() {
			return filepath.Join(dir, c), nil
		}
	}
	// A catalog id or file name (gleann models download <id>).
	if e, ok := modelcatalog.Find(model); ok && e.Kind == modelcatalog.Reranker {
		if e.Installed() {
			return e.Path(), nil
		}
		return "", fmt.Errorf("reranker model %q is not downloaded (run: gleann models download %s)", model, e.ID)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, base+"[-_.]*.gguf"))
	if len(matches) > 0 {
		// Q8_0 first (the type bitllm.c validates rerankers with), then by name.
		sort.SliceStable(matches, func(i, j int) bool {
			qi := strings.Contains(strings.ToLower(matches[i]), "q8_0")
			qj := strings.Contains(strings.ToLower(matches[j]), "q8_0")
			if qi != qj {
				return qi
			}
			return matches[i] < matches[j]
		})
		return matches[0], nil
	}
	return "", fmt.Errorf("reranker model %q not found (give a path, or put a Q8_0 GGUF such as %s-Q8_0.gguf in %s)", model, base, dir)
}

func loadRankEngine(path string) (*C.eif_bert_t, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	b := (*C.eif_bert_t)(C.calloc(1, C.sizeof_eif_bert_t))
	if rc := C.eif_bert_load(b, cPath); rc != 0 {
		C.free(unsafe.Pointer(b))
		return nil, fmt.Errorf("failed to load reranker %q (code %d; F16 and K-quant files are not supported, use Q8_0)", path, int(rc))
	}
	if C.eif_bert_is_ranker(b) == 0 {
		C.eif_bert_free(b)
		C.free(unsafe.Pointer(b))
		return nil, fmt.Errorf("%q has no cross-encoder head: it is an embedding model, not a reranker", path)
	}
	return b, nil
}

// GetOrLoadEIFRanker returns the cached ranker for model, loading it once.
func GetOrLoadEIFRanker(model string) (*EIFRanker, error) {
	path, err := resolveRerankModelPath(model)
	if err != nil {
		return nil, err
	}
	eifRankerMu.Lock()
	defer eifRankerMu.Unlock()
	if r, ok := eifRankerCache[path]; ok {
		return r, nil
	}
	b, err := loadRankEngine(path)
	if err != nil {
		return nil, err
	}
	r := &EIFRanker{modelPath: path, workers: []*C.eif_bert_t{b}}
	eifRankerCache[path] = r
	return r, nil
}

// Rank returns one relevance logit per doc.
func (r *EIFRanker) Rank(ctx context.Context, query string, docs []string) ([]float32, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	n := embedThreads()
	if n > len(docs) {
		n = len(docs)
	}
	for len(r.workers) < n {
		b, err := loadRankEngine(r.modelPath)
		if err != nil {
			break // run with the instances we have
		}
		r.workers = append(r.workers, b)
	}
	workers := r.workers
	if len(workers) > n {
		workers = workers[:n]
	}

	cQuery := C.CString(query)
	defer C.free(unsafe.Pointer(cQuery))

	scores := make([]float32, len(docs))
	var (
		next     atomic.Int64
		wg       sync.WaitGroup
		errOnce  sync.Once
		firstErr error
		failed   atomic.Bool
	)
	for _, w := range workers {
		wg.Add(1)
		go func(b *C.eif_bert_t) {
			defer wg.Done()
			for !failed.Load() {
				i := int(next.Add(1) - 1)
				if i >= len(docs) {
					return
				}
				if err := ctx.Err(); err != nil {
					errOnce.Do(func() { firstErr = err; failed.Store(true) })
					return
				}
				cDoc := C.CString(docs[i])
				var s C.float
				rc := C.eif_rank_one_thread(b, cQuery, cDoc, &s)
				C.free(unsafe.Pointer(cDoc))
				if rc != 0 {
					errOnce.Do(func() {
						firstErr = fmt.Errorf("eif_bert_rank failed on document %d with code %d", i, int(rc))
						failed.Store(true)
					})
					return
				}
				scores[i] = float32(s)
			}
		}(w)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return scores, nil
}
