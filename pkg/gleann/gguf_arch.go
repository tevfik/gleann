package gleann

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
)

// eifLLMArchitectures are the GGUF "general.architecture" values the bitllm.c
// runtime can load from a .gguf file: the decoder-only LLaMA family, Qwen2/2.5
// and text-only Qwen3.5 (24 layers, tied head; bitllm.c repacks it to INT4 on
// load). Plain "qwen3" is not listed: the runtime has no GGUF path for it. The
// runtime also maps every unknown architecture to its LLaMA path, which for
// models such as LFM2 (hybrid conv) runs forever or emits garbage, so gleann
// refuses them up front instead.
var eifLLMArchitectures = map[string]bool{
	"llama": true, "smollm": true, "smollm2": true, "granite": true,
	"qwen2": true, "qwen2.5": true, "qwen35": true,
}

// GGUFArchitecture returns the general.architecture string of a GGUF file.
func GGUFArchitecture(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	r := bufio.NewReader(f)

	var magic [4]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil || string(magic[:]) != "GGUF" {
		return "", fmt.Errorf("%s is not a GGUF file", path)
	}
	var hdr struct {
		Version  uint32
		NTensors uint64
		NKV      uint64
	}
	if err := binary.Read(r, binary.LittleEndian, &hdr); err != nil {
		return "", err
	}
	for i := uint64(0); i < hdr.NKV; i++ {
		key, err := ggufString(r)
		if err != nil {
			return "", err
		}
		var vt uint32
		if err := binary.Read(r, binary.LittleEndian, &vt); err != nil {
			return "", err
		}
		if key == "general.architecture" && vt == 8 {
			return ggufString(r)
		}
		if err := ggufSkip(r, vt); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("%s has no general.architecture", path)
}

func ggufString(r *bufio.Reader) (string, error) {
	var n uint64
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return "", err
	}
	if n > 1<<20 {
		return "", fmt.Errorf("implausible GGUF string length %d", n)
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return string(b), err
}

func ggufSkip(r *bufio.Reader, vt uint32) error {
	fixed := map[uint32]int64{0: 1, 1: 1, 7: 1, 2: 2, 3: 2, 4: 4, 5: 4, 6: 4, 10: 8, 11: 8, 12: 8}
	if n, ok := fixed[vt]; ok {
		_, err := r.Discard(int(n))
		return err
	}
	switch vt {
	case 8:
		_, err := ggufString(r)
		return err
	case 9:
		var et uint32
		var cnt uint64
		if err := binary.Read(r, binary.LittleEndian, &et); err != nil {
			return err
		}
		if err := binary.Read(r, binary.LittleEndian, &cnt); err != nil {
			return err
		}
		for i := uint64(0); i < cnt; i++ {
			if err := ggufSkip(r, et); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("unknown GGUF value type %d", vt)
}

// CheckEIFLLMModel reports an error when the GGUF model's architecture is not
// one the EIF runtime can run.
func CheckEIFLLMModel(path string) error {
	arch, err := GGUFArchitecture(path)
	if err != nil {
		return nil // unreadable header: let the runtime produce its own error
	}
	if !eifLLMArchitectures[strings.ToLower(arch)] {
		return fmt.Errorf("model %s uses architecture %q, which the built-in EIF runtime (bitllm.c) does not support; "+
			"use a llama, qwen2 or qwen3.5 GGUF or switch the LLM provider (e.g. --llm-provider ollama)", path, arch)
	}
	return nil
}
