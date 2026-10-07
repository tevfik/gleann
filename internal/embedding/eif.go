//go:build eif

package embedding

/*
#cgo CFLAGS: -I${SRCDIR}/../../ext/eif-runtime/include -O3
#cgo LDFLAGS: ${SRCDIR}/../../ext/eif-runtime/build/libeif_runtime.a -lm -fopenmp
#include "eif_bert.h"
#include <stdlib.h>
*/
import "C"
import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"
)

// EIFComputer executes in-process high-performance CPU embeddings using eif-runtime.
type EIFComputer struct {
	modelPath string
	bert      C.eif_bert_t
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
		model = "minilm_bert.eifm"
	}

	// 1. Direct path check
	if _, err := os.Stat(model); err == nil {
		return model, nil
	}

	// If given a .gguf path, check for sibling .eifm
	if strings.HasSuffix(model, ".gguf") {
		candidate := strings.TrimSuffix(model, ".gguf") + "_bert.eifm"
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		candidate2 := strings.TrimSuffix(model, ".gguf") + ".eifm"
		if _, err := os.Stat(candidate2); err == nil {
			return candidate2, nil
		}
	}

	// 2. Check ~/.gleann/models/
	home, err := os.UserHomeDir()
	if err == nil {
		modelsDir := filepath.Join(home, ".gleann", "models")
		candidates := []string{
			filepath.Join(modelsDir, model),
			filepath.Join(modelsDir, model+".eifm"),
			filepath.Join(modelsDir, model+"_bert.eifm"),
			filepath.Join(modelsDir, "minilm_bert.eifm"),
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
	}

	rc := C.eif_bert_load(&inst.bert, cPath)
	if rc != 0 {
		return nil, fmt.Errorf("failed to load EIF model %q (code %d)", resolved, int(rc))
	}

	inst.dim = int(inst.bert.config.dim)
	runtime.SetFinalizer(inst, func(e *EIFComputer) {
		e.Close()
	})

	eifInstances[resolved] = inst
	return inst, nil
}

// Close unmaps and frees native EIF model memory.
func (e *EIFComputer) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.closed {
		C.eif_bert_free(&e.bert)
		e.closed = true
	}
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

	for i, txt := range texts {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		cText := C.CString(txt)
		vec := make([]float32, dim)
		rc := C.eif_bert_embed(&e.bert, cText, (*C.float)(unsafe.Pointer(&vec[0])))
		C.free(unsafe.Pointer(cText))

		if rc != 0 {
			return nil, fmt.Errorf("eif_bert_embed failed on text %d with code %d", i, int(rc))
		}
		results[i] = vec
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
