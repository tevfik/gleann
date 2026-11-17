package gleann

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ggufBytes writes a GGUF header with nkv entries, then the raw tail.
func ggufBytes(nkv uint64, tail func(b *bytes.Buffer)) []byte {
	var b bytes.Buffer
	b.WriteString("GGUF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(3))
	_ = binary.Write(&b, binary.LittleEndian, uint64(0))
	_ = binary.Write(&b, binary.LittleEndian, nkv)
	if tail != nil {
		tail(&b)
	}
	return b.Bytes()
}

func ggufStr(b *bytes.Buffer, s string) {
	_ = binary.Write(b, binary.LittleEndian, uint64(len(s)))
	b.WriteString(s)
}

// A damaged or unusual GGUF header is an error, never a panic or a huge
// allocation; CheckEIFLLMModel leaves such files to the runtime.
func TestGGUFArchitectureRejectsDamagedHeaders(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"not gguf", []byte("GGML...."), "not a GGUF file"},
		{"short header", []byte("GGUF\x03\x00"), "EOF"},
		{"no architecture", ggufBytes(1, func(b *bytes.Buffer) {
			ggufStr(b, "general.name")
			_ = binary.Write(b, binary.LittleEndian, uint32(8))
			ggufStr(b, "tiny")
		}), "has no general.architecture"},
		{"huge string", ggufBytes(1, func(b *bytes.Buffer) {
			_ = binary.Write(b, binary.LittleEndian, uint64(1<<40))
		}), "implausible GGUF string length"},
		{"unknown value type", ggufBytes(1, func(b *bytes.Buffer) {
			ggufStr(b, "weird")
			_ = binary.Write(b, binary.LittleEndian, uint32(99))
		}), "unknown GGUF value type 99"},
		{"array of unknown type", ggufBytes(1, func(b *bytes.Buffer) {
			ggufStr(b, "arr")
			_ = binary.Write(b, binary.LittleEndian, uint32(9))
			_ = binary.Write(b, binary.LittleEndian, uint32(77))
			_ = binary.Write(b, binary.LittleEndian, uint64(1))
		}), "unknown GGUF value type 77"},
		{"truncated array header", ggufBytes(1, func(b *bytes.Buffer) {
			ggufStr(b, "arr")
			_ = binary.Write(b, binary.LittleEndian, uint32(9))
			_ = binary.Write(b, binary.LittleEndian, uint16(8))
		}), "EOF"},
		{"truncated value type", ggufBytes(1, func(b *bytes.Buffer) {
			ggufStr(b, "k")
		}), "EOF"},
		{"truncated key", ggufBytes(1, nil), "EOF"},
		{"truncated array count", ggufBytes(1, func(b *bytes.Buffer) {
			ggufStr(b, "arr")
			_ = binary.Write(b, binary.LittleEndian, uint32(9))
			_ = binary.Write(b, binary.LittleEndian, uint32(4))
		}), "EOF"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "m.gguf")
			if err := os.WriteFile(p, tc.data, 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := GGUFArchitecture(p)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if err := CheckEIFLLMModel(p); err != nil {
				t.Fatalf("an unreadable header must be left to the runtime: %v", err)
			}
		})
	}
	if _, err := GGUFArchitecture(filepath.Join(t.TempDir(), "absent.gguf")); err == nil {
		t.Fatal("a missing file was read")
	}
}

func TestChatContextWindowOpenAIAndOverrides(t *testing.T) {
	t.Setenv("GLEANN_CONTEXT_TOKENS", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models/direct":
			_, _ = io.WriteString(w, `{"id":"direct","max_model_len":32768}`)
		case "/v1/models/listed", "/v1/models/nowindow", "/v1/models/missing":
			http.NotFound(w, r)
		case "/v1/models":
			_, _ = io.WriteString(w, `{"data":[{"id":"other","n_ctx":1},{"id":"listed","context_window":8192},{"id":"nowindow"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cases := []struct {
		model  string
		base   string
		want   int
		source string
	}{
		{"direct", srv.URL, 32768, "provider /models"},
		{"listed", srv.URL + "/v1/", 8192, "provider /models"},
		{"nowindow", srv.URL, 0, ""},
		{"missing", srv.URL, 0, ""},
	}
	for _, tc := range cases {
		n, src := ChatContextWindow(context.Background(), ChatConfig{Provider: LLMOpenAI, Model: tc.model, BaseURL: tc.base})
		if n != tc.want || src != tc.source {
			t.Errorf("%s: window = %d (%q), want %d (%q)", tc.model, n, src, tc.want, tc.source)
		}
	}

	if n, src := ChatContextWindow(context.Background(), ChatConfig{Provider: LLMOpenAI}); n != 0 || src != "" {
		t.Fatalf("no model or URL: %d %q", n, src)
	}
	t.Setenv("GLEANN_CONTEXT_TOKENS", "12345")
	if n, src := ChatContextWindow(context.Background(), ChatConfig{}); n != 12345 || src != "GLEANN_CONTEXT_TOKENS" {
		t.Fatalf("override: %d %q", n, src)
	}
	t.Setenv("GLEANN_CONTEXT_TOKENS", "lots")
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	// An unreachable Ollama cannot lower the window: the num_ctx this chat
	// sends is the answer (a bad override is ignored).
	if n, src := ChatContextWindow(context.Background(), ChatConfig{Provider: LLMOllama, Model: "m", BaseURL: dead.URL}); n != ollamaContextWindow(0) || src != "num_ctx sent with each request" {
		t.Fatalf("unreachable ollama: %d %q", n, src)
	}
}
