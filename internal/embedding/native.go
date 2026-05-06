//go:build native

package embedding

/*
#cgo LDFLAGS: -L../../ext/gleann-core-rs/target/release -lgleann_core_rs
#include "../../ext/gleann-core-rs/include/gleann_core.h"
#include <stdlib.h>
*/
import "C"
import (
	"context"
	"fmt"
	"runtime"
	"unsafe"
)

// NativeComputer computes embeddings natively using the embedded Rust core.
type NativeComputer struct {
	model string
	ptr   *C.NativeComputer
}

// NewNativeComputer creates a new NativeComputer.
func NewNativeComputer(model string) *NativeComputer {
	c := &NativeComputer{
		model: model,
		ptr:   C.gleann_native_init(),
	}
	
	// Ensure we free the Rust memory when this Go object is garbage collected
	runtime.SetFinalizer(c, func(nc *NativeComputer) {
		if nc.ptr != nil {
			C.gleann_native_free(nc.ptr)
			nc.ptr = nil
		}
	})
	
	return c
}

// Compute computes embeddings for a list of texts natively.
func (c *NativeComputer) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if c.ptr == nil {
		return nil, fmt.Errorf("native computer is not initialized")
	}

	// Prepare C array of C strings
	cStrings := make([]*C.char, len(texts))
	for i, t := range texts {
		cStrings[i] = C.CString(t)
	}
	// Free the strings after call
	defer func() {
		for _, cs := range cStrings {
			C.free(unsafe.Pointer(cs))
		}
	}()

	res := C.gleann_embed_texts(c.ptr, (**C.char)(unsafe.Pointer(&cStrings[0])), C.size_t(len(texts)))
	if res == nil {
		return nil, fmt.Errorf("native rust engine returned null or failed to compute")
	}
	defer C.gleann_free_embedding_result(res)

	rows := int(res.rows)
	cols := int(res.cols)
	total := rows * cols

	if total == 0 {
		return nil, fmt.Errorf("returned 0 embeddings")
	}

	// Convert flat C array to [][]float32
	flatSlice := unsafe.Slice((*float32)(unsafe.Pointer(res.data)), total)
	
	embeddings := make([][]float32, rows)
	for i := 0; i < rows; i++ {
		embeddings[i] = make([]float32, cols)
		copy(embeddings[i], flatSlice[i*cols:(i+1)*cols])
	}

	return embeddings, nil
}

// ComputeSingle computes an embedding for a single text natively.
func (c *NativeComputer) ComputeSingle(ctx context.Context, text string) ([]float32, error) {
	embs, err := c.Compute(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embs) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return embs[0], nil
}

func (c *NativeComputer) Dimensions() int {
	return 384 // all-MiniLM-L6-v2 size
}

func (c *NativeComputer) ModelName() string {
	return c.model
}
