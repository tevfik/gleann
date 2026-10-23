//go:build eif

package embedding

/*
#cgo CFLAGS: -I${SRCDIR}/../../ext/bitllm.c/include -O3
#cgo linux LDFLAGS: ${SRCDIR}/../../ext/bitllm.c/build/libbitllm.a -Wl,-Bstatic -lgomp -Wl,-Bdynamic -lpthread -lm
#cgo darwin LDFLAGS: ${SRCDIR}/../../ext/bitllm.c/build/libbitllm.a -lm
#cgo windows LDFLAGS: ${SRCDIR}/../../ext/bitllm.c/build/libbitllm.a -lm
#include "eif_bert.h"
#include <stdlib.h>
#if defined(__linux__) && defined(__has_include)
#if __has_include(<omp.h>)
#include <omp.h>
#define GLEANN_HAVE_OMP 1
#endif
#endif
#ifndef GLEANN_HAVE_OMP
// macOS/Windows builds link bitllm.c without libomp (see release.yml).
static inline void omp_set_num_threads(int n) { (void)n; }
#endif

// omp_set_num_threads only affects the calling thread, so it has to happen in
// the same cgo call that runs the embedding.
static inline int eif_embed_with_threads(eif_bert_t *bert, const char *text, float *out, int threads) {
    omp_set_num_threads(threads);
    return eif_bert_embed(bert, text, out);
}

static inline int eif_host_has_avx2(void) {
#if (defined(__x86_64__) || defined(_M_X64)) && defined(__GNUC__)
    return __builtin_cpu_supports("avx2") && __builtin_cpu_supports("fma");
#else
    return 1;
#endif
}
*/
import "C"
import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"
)

// EIFComputer executes in-process high-performance CPU embeddings using bitllm.c.
//
// bert is the primary instance. Compute spreads a batch over several
// independent instances (extra, grown on demand) that each run single-threaded
// and pull passages from a shared queue. Splitting one short passage across
// OpenMP threads scales badly (a fork/join per small GEMM, and a static split
// waits for the slowest E-core on hybrid CPUs); one passage per thread keeps
// every core busy. The model file is mapped read-only, so extra instances only
// add their activation scratch.
type EIFComputer struct {
	modelPath string
	bert      *C.eif_bert_t // C memory, see growWorkers
	extra     []*C.eif_bert_t
	dim       int
	mu        sync.Mutex
	closed    bool
}

var (
	eifCacheMu   sync.Mutex
	eifInstances = make(map[string]*EIFComputer)
)

func resolveEIFModelPath(model string) (string, error) {
	if model == "" {
		model = "minilm.gguf"
	}

	// 1. Direct path check
	if _, err := os.Stat(model); err == nil {
		return model, nil
	}

	// 2. Check ~/.gleann/models/
	home, err := os.UserHomeDir()
	if err == nil {
		modelsDir := filepath.Join(home, ".gleann", "models")
		base := filepath.Base(model)
		candidates := []string{
			filepath.Join(modelsDir, model),
			filepath.Join(modelsDir, base),
			filepath.Join(modelsDir, model+".gguf"),
			filepath.Join(modelsDir, base+".gguf"),
			filepath.Join(modelsDir, model+"-q8_0.gguf"),
			filepath.Join(modelsDir, base+"-q8_0.gguf"),
			filepath.Join(modelsDir, model+"_q8_0.gguf"),
			filepath.Join(modelsDir, base+"_q8_0.gguf"),
			filepath.Join(modelsDir, model+".eifm"),
			filepath.Join(modelsDir, base+".eifm"),
			filepath.Join(modelsDir, model+"_bert.eifm"),
			filepath.Join(modelsDir, base+"_bert.eifm"),
			filepath.Join(modelsDir, "minilm_bert.eifm"),
			filepath.Join(modelsDir, "multilingual-e5-small-q8_0.gguf"),
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
		}
	}

	// 3. Check common workspace build locations
	workspaceCandidates := []string{
		"/home/tevfik/workspace_old/edge-intelligence/models/dist/minilm_bert.eifm",
		"/home/tevfik/workspace_old/edge-intelligence/models/dist/granite107m.eifm",
	}
	for _, c := range workspaceCandidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}

	return "", fmt.Errorf("EIF model not found for %q (checked direct path, ~/.gleann/models/, and workspace)", model)
}

// GetOrLoadEIF retrieves or initializes a cached in-process EIF engine instance.
func GetOrLoadEIF(model string) (*EIFComputer, error) {
	eifCacheMu.Lock()
	defer eifCacheMu.Unlock()

	if C.eif_host_has_avx2() == 0 {
		return nil, fmt.Errorf("in-process EIF engine requires AVX2+FMA instruction support on x86_64 hosts; current CPU does not support these instructions")
	}

	resolved, err := resolveEIFModelPath(model)
	if err != nil {
		return nil, err
	}

	if inst, ok := eifInstances[resolved]; ok && !inst.closed {
		return inst, nil
	}

	cPath := C.CString(resolved)
	defer C.free(unsafe.Pointer(cPath))

	inst := &EIFComputer{
		modelPath: resolved,
		bert:      (*C.eif_bert_t)(C.calloc(1, C.sizeof_eif_bert_t)),
	}

	rc := C.eif_bert_load(inst.bert, cPath)
	if rc != 0 {
		C.free(unsafe.Pointer(inst.bert))
		return nil, fmt.Errorf("failed to load EIF model %q (code %d)", resolved, int(rc))
	}

	inst.dim = int(inst.bert.config.dim)
	runtime.SetFinalizer(inst, func(e *EIFComputer) {
		e.Close()
	})

	eifInstances[resolved] = inst
	return inst, nil
}

func (e *EIFComputer) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.closed {
		for _, b := range e.extra {
			C.eif_bert_free(b)
			C.free(unsafe.Pointer(b))
		}
		e.extra = nil
		C.eif_bert_free(e.bert)
		C.free(unsafe.Pointer(e.bert))
		e.closed = true
	}
}

// growWorkers makes sure n worker instances exist (the primary counts as one)
// and returns them. Called with e.mu held.
func (e *EIFComputer) growWorkers(n int) []*C.eif_bert_t {
	cPath := C.CString(e.modelPath)
	defer C.free(unsafe.Pointer(cPath))
	for len(e.extra)+1 < n {
		// C memory: the struct points into itself, which cgo rejects for
		// a whole Go-allocated struct.
		b := (*C.eif_bert_t)(C.calloc(1, C.sizeof_eif_bert_t))
		if C.eif_bert_load(b, cPath) != 0 {
			C.free(unsafe.Pointer(b))
			break // run with what we have
		}
		e.extra = append(e.extra, b)
	}
	workers := make([]*C.eif_bert_t, 0, 1+len(e.extra))
	workers = append(workers, e.bert)
	workers = append(workers, e.extra...)
	if len(workers) > n {
		workers = workers[:n]
	}
	return workers
}

// embedOne embeds one text on a single OpenMP thread.
func embedOne(b *C.eif_bert_t, txt string, dim int) ([]float32, C.int) {
	cText := C.CString(txt)
	defer C.free(unsafe.Pointer(cText))
	vec := make([]float32, dim)
	rc := C.eif_embed_with_threads(b, cText, (*C.float)(unsafe.Pointer(&vec[0])), 1)
	return vec, rc
}

// Dim returns the embedding dimensionality.
func (e *EIFComputer) Dim() int {
	return e.dim
}

// Compute computes embeddings for an array of input texts.
func (e *EIFComputer) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return nil, fmt.Errorf("EIF engine is closed")
	}

	dim := e.dim
	results := make([][]float32, len(texts))

	n := embedThreads()
	if n > len(texts) {
		n = len(texts)
	}
	workers := e.growWorkers(n)

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
				if i >= len(texts) {
					return
				}
				if err := ctx.Err(); err != nil {
					errOnce.Do(func() { firstErr = err; failed.Store(true) })
					return
				}
				vec, rc := embedOne(b, texts[i], dim)
				if rc != 0 {
					errOnce.Do(func() {
						firstErr = fmt.Errorf("eif_bert_embed failed on text %d with code %d", i, int(rc))
						failed.Store(true)
					})
					return
				}
				results[i] = vec
			}
		}(w)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

func (c *Computer) computeEIF(ctx context.Context, texts []string) ([][]float32, error) {
	engine, err := GetOrLoadEIF(c.model)
	if err != nil {
		return nil, err
	}
	return engine.Compute(ctx, texts)
}

func (c *Computer) getEIFDim() int {
	if engine, err := GetOrLoadEIF(c.model); err == nil && engine != nil {
		return engine.Dim()
	}
	return 0
}
