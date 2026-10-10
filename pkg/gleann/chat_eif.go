//go:build eif

package gleann

/*
#cgo CFLAGS: -I${SRCDIR}/../../ext/eif-runtime/include -O3
#cgo linux LDFLAGS: ${SRCDIR}/../../ext/eif-runtime/build/libeif_runtime.a -Wl,-Bstatic -lgomp -Wl,-Bdynamic -lpthread -lm
#cgo darwin LDFLAGS: ${SRCDIR}/../../ext/eif-runtime/build/libeif_runtime.a -lm
#cgo windows LDFLAGS: ${SRCDIR}/../../ext/eif-runtime/build/libeif_runtime.a -lm
#include "eif_llm.h"
#include <stdlib.h>
#include <stdint.h>

extern void goEIFChatTokenCallback(char *piece, int token_id, uintptr_t handle_val);

static inline void c_eif_chat_token_cb(const char *piece, int token_id, void *user_data) {
    goEIFChatTokenCallback((char *)piece, token_id, (uintptr_t)user_data);
}

static inline int c_eif_chat_generate_wrap(eif_llm_t *llm, const char *prompt, const eif_llm_gen_config_t *cfg, uintptr_t handle_val) {
    return eif_llm_generate(llm, prompt, cfg, c_eif_chat_token_cb, (void *)handle_val);
}
*/
import "C"

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/cgo"
	"strings"
	"sync"
	"unsafe"
)

//export goEIFChatTokenCallback
func goEIFChatTokenCallback(piece *C.char, tokenID C.int, handleVal C.uintptr_t) {
	if handleVal == 0 {
		return
	}
	h := cgo.Handle(handleVal)
	fn, ok := h.Value().(func(string))
	if ok && fn != nil {
		fn(C.GoString(piece))
	}
}

type eifLLMInstance struct {
	path string
	llm  C.eif_llm_t
	mu   sync.Mutex
}

var (
	eifLLMMu        sync.Mutex
	eifLLMInstances = make(map[string]*eifLLMInstance)
)

func resolveEIFLLMPath(model string) (string, error) {
	if model == "" {
		model = "qwen2.5-coder-1.5b-instruct-q4_k_m.gguf"
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
			filepath.Join(modelsDir, "qwen2.5-coder-1.5b-instruct-q4_k_m.gguf"),
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
		}

		// Look for any .gguf in ~/.gleann/models/
		entries, _ := os.ReadDir(modelsDir)
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".gguf") {
				name := strings.ToLower(e.Name())
				// prefer instruct / coder models
				if strings.Contains(name, "coder") || strings.Contains(name, "instruct") || strings.Contains(name, "qwen") {
					return filepath.Join(modelsDir, e.Name()), nil
				}
			}
		}
	}

	return "", fmt.Errorf("eif LLM model not found for %q; provide a valid .gguf path", model)
}

func getOrLoadEIFLLM(modelPath string) (*eifLLMInstance, error) {
	eifLLMMu.Lock()
	defer eifLLMMu.Unlock()

	if inst, exists := eifLLMInstances[modelPath]; exists {
		return inst, nil
	}

	inst := &eifLLMInstance{path: modelPath}
	cPath := C.CString(modelPath)
	defer C.free(unsafe.Pointer(cPath))

	rc := C.eif_llm_load(&inst.llm, cPath, nil, nil, 0)
	if rc != 0 {
		return nil, fmt.Errorf("failed to load EIF LLM model at %s (error code: %d)", modelPath, int(rc))
	}

	eifLLMInstances[modelPath] = inst
	return inst, nil
}

func formatEIFPrompt(messages []ChatMessage) string {
	var sb strings.Builder
	for _, m := range messages {
		if m.Content == "" {
			continue
		}
		role := m.Role
		if role == "" {
			role = "user"
		}
		sb.WriteString("<|im_start|>" + role + "\n" + m.Content + "<|im_end|>\n")
	}
	sb.WriteString("<|im_start|>assistant\n")
	return sb.String()
}

func (c *LeannChat) chatEIFStream(ctx context.Context, messages []ChatMessage, callback StreamCallback) error {
	resolvedPath, err := resolveEIFLLMPath(c.config.Model)
	if err != nil {
		return err
	}

	inst, err := getOrLoadEIFLLM(resolvedPath)
	if err != nil {
		return err
	}

	inst.mu.Lock()
	defer inst.mu.Unlock()

	C.eif_llm_reset(&inst.llm)

	prompt := formatEIFPrompt(messages)
	cPrompt := C.CString(prompt)
	defer C.free(unsafe.Pointer(cPrompt))

	var cfg C.eif_llm_gen_config_t
	maxTokens := c.config.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}
	cfg.max_new_tokens = C.int(maxTokens)

	temp := float32(c.config.Temperature)
	if temp <= 0 {
		temp = 0.7
	}
	cfg.temperature = C.float(temp)
	cfg.top_p = 0.9

	rep := float32(c.config.RepeatPenalty)
	if rep <= 0 {
		rep = 1.15
	}
	cfg.repetition_penalty = C.float(rep)
	cfg.eos_token_id = -1
	cfg.kv_type = 0
	cfg.use_gpu = false

	streamFn := func(piece string) {
		if callback != nil {
			callback(piece)
		}
	}

	h := cgo.NewHandle(streamFn)
	defer h.Delete()

	rc := C.c_eif_chat_generate_wrap(&inst.llm, cPrompt, &cfg, C.uintptr_t(h))
	if rc < 0 {
		return fmt.Errorf("eif_llm_generate failed with code %d", int(rc))
	}

	return nil
}

func (c *LeannChat) chatEIF(ctx context.Context, messages []ChatMessage) (string, error) {
	var sb strings.Builder
	err := c.chatEIFStream(ctx, messages, func(piece string) {
		sb.WriteString(piece)
	})
	if err != nil {
		return "", err
	}
	return sb.String(), nil
}
