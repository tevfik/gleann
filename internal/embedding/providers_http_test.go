package embedding

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Error responses below use statuses and bodies that retry.IsRetryable treats as
// permanent (400/401/404), so Compute fails fast instead of backing off.

func newProviderComputer(t *testing.T, p Provider, h http.HandlerFunc) *Computer {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewComputer(Options{Provider: p, Model: "m", BaseURL: srv.URL, APIKey: "k", BatchSize: 2, Concurrency: 1})
}

func TestComputeGemini(t *testing.T) {
	var gotPath, gotKey string
	var gotReq geminiEmbedRequest
	c := newProviderComputer(t, ProviderGemini, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.Path, r.URL.Query().Get("key")
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		_, _ = w.Write([]byte(`{"embeddings":[{"values":[1,2]},{"values":[3,4]}]}`))
	})
	got, err := c.computeGemini(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1][0] != 3 {
		t.Errorf("embeddings = %v", got)
	}
	if gotPath != "/v1beta/models/m:batchEmbedContents" || gotKey != "k" {
		t.Errorf("path=%q key=%q", gotPath, gotKey)
	}
	if len(gotReq.Requests) != 2 || gotReq.Requests[0].Model != "models/m" || gotReq.Requests[1].Content.Parts[0].Text != "b" {
		t.Errorf("request = %+v", gotReq)
	}
}

func TestComputeGeminiDefaultModel(t *testing.T) {
	var gotPath string
	c := newProviderComputer(t, ProviderGemini, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"embeddings":[{"values":[1]}]}`))
	})
	c.model = ""
	if _, err := c.computeGemini(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotPath, "text-embedding-004") {
		t.Errorf("empty model did not fall back to text-embedding-004: %q", gotPath)
	}
}

func TestComputeGeminiErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"status", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "API key not valid", 400) }, "gemini returned 400: API key not valid"},
		{"malformed JSON", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>")) }, "decode response"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newProviderComputer(t, ProviderGemini, tc.handler)
			_, err := c.computeGemini(context.Background(), []string{"a"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
	// Unreachable server: the transport error is wrapped.
	c := newProviderComputer(t, ProviderGemini, func(http.ResponseWriter, *http.Request) {})
	c.baseURL = "http://127.0.0.1:1"
	if _, err := c.computeGemini(context.Background(), []string{"a"}); err == nil || !strings.Contains(err.Error(), "POST gemini") {
		t.Errorf("unreachable err = %v", err)
	}
}

func TestComputeOllamaErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"model missing", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":"model \"m\" not found, try pulling it first"}`, 404)
		}, "ollama pull m"},
		{"404 for another reason", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no such route", 404) }, "returned 404: no such route"},
		{"bad request", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "input too long", 400) }, "returned 400: input too long"},
		{"malformed JSON", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("{nope")) }, "decode response"},
		{"wrong embedding count", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"embeddings":[[1,2]]}`))
		}, "expected 2 embeddings, got 1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newProviderComputer(t, ProviderOllama, tc.handler)
			_, err := c.computeOllama(context.Background(), []string{"a", "b"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestComputeOllamaConnectionRefusedIsActionable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c := NewComputer(Options{Provider: ProviderOllama, BaseURL: url})
	_, err := c.computeOllama(context.Background(), []string{"a"})
	if err == nil || !strings.Contains(err.Error(), "is it running?") || !strings.Contains(err.Error(), url) {
		t.Fatalf("err = %v", err)
	}
}

func TestComputeOllamaSendsAuthAndUnescapedJSON(t *testing.T) {
	var auth string
	var raw []byte
	c := newProviderComputer(t, ProviderOllama, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		raw, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"embeddings":[[1]]}`))
	})
	if _, err := c.computeOllama(context.Background(), []string{"<a> & b"}); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer k" {
		t.Errorf("Authorization = %q", auth)
	}
	if !strings.Contains(string(raw), "<a> & b") {
		t.Errorf("HTML characters were escaped in the request: %s", raw)
	}
}

func TestComputeOpenAIOrderingAndErrors(t *testing.T) {
	serve := func(body string, status int) *Computer {
		return newProviderComputer(t, ProviderOpenAI, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer k" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		})
	}
	ctx := context.Background()

	t.Run("indexes reorder the response", func(t *testing.T) {
		c := serve(`{"data":[{"index":1,"embedding":[2]},{"index":0,"embedding":[1]}]}`, 200)
		got, err := c.computeOpenAI(ctx, []string{"a", "b"})
		if err != nil || got[0][0] != 1 || got[1][0] != 2 {
			t.Fatalf("got %v, %v", got, err)
		}
	})
	t.Run("missing indexes keep server order", func(t *testing.T) {
		c := serve(`{"data":[{"embedding":[7]},{"embedding":[8]}]}`, 200)
		got, err := c.computeOpenAI(ctx, []string{"a", "b"})
		if err != nil || got[0][0] != 7 || got[1][0] != 8 {
			t.Fatalf("got %v, %v", got, err)
		}
	})
	t.Run("missing embedding", func(t *testing.T) {
		c := serve(`{"data":[{"index":0,"embedding":[1]}]}`, 200)
		_, err := c.computeOpenAI(ctx, []string{"a", "b"})
		if err == nil || !strings.Contains(err.Error(), "missing embedding at index 1") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("out-of-range index is ignored, leaving a gap", func(t *testing.T) {
		c := serve(`{"data":[{"index":5,"embedding":[1]}]}`, 200)
		if _, err := c.computeOpenAI(ctx, []string{"a"}); err == nil {
			t.Fatal("want a missing-embedding error")
		}
	})
	t.Run("status error", func(t *testing.T) {
		c := serve(`{"error":"bad key"}`, 401)
		_, err := c.computeOpenAI(ctx, []string{"a"})
		if err == nil || !strings.Contains(err.Error(), "returned 401") || !strings.Contains(err.Error(), "bad key") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("malformed JSON", func(t *testing.T) {
		c := serve(`not json`, 200)
		if _, err := c.computeOpenAI(ctx, []string{"a"}); err == nil || !strings.Contains(err.Error(), "decode response") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unauthorised without key", func(t *testing.T) {
		c := serve(`{}`, 200)
		c.apiKey = ""
		if _, err := c.computeOpenAI(ctx, []string{"a"}); err == nil || !strings.Contains(err.Error(), "401") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		c := serve(`{}`, 200)
		c.baseURL = "http://127.0.0.1:1"
		if _, err := c.computeOpenAI(ctx, []string{"a"}); err == nil || !strings.Contains(err.Error(), "POST http://127.0.0.1:1/v1/embeddings") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestComputeCancelledContext(t *testing.T) {
	c := newProviderComputer(t, ProviderOllama, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"embeddings":[[1]]}`))
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Compute(ctx, []string{"a"}); err == nil {
		t.Fatal("want an error for a cancelled context")
	}
}

// Compute with a working provider: batches go out in parallel, results keep
// input order, progress reports the running total and the prompt template
// reaches the server.
func TestComputeBatchesProgressAndTemplate(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	c := newProviderComputer(t, ProviderOllama, func(w http.ResponseWriter, r *http.Request) {
		var req ollamaEmbedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		inputs := req.Input.([]any)
		out := make([][]float32, len(inputs))
		mu.Lock()
		for i, in := range inputs {
			s := in.(string)
			seen = append(seen, s)
			out[i] = []float32{float32(len(s))}
		}
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	})
	c.SetPromptTemplate("q: ")
	var progress []int
	var total int
	var pmu sync.Mutex
	c.SetProgressCallback(func(done, tot int) {
		pmu.Lock()
		progress = append(progress, done)
		total = tot
		pmu.Unlock()
	})
	texts := []string{"a", "bb", "ccc", "dddd", "eeeee"}
	got, err := c.Compute(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	for i, tx := range texts {
		if got[i][0] != float32(len("q: "+tx)) {
			t.Errorf("result %d = %v, want length of %q", i, got[i], "q: "+tx)
		}
	}
	if len(seen) != 5 || total != 5 || len(progress) != 3 || progress[len(progress)-1] != 5 {
		t.Errorf("seen=%d total=%d progress=%v", len(seen), total, progress)
	}
	if c.Dimensions() != 1 {
		t.Errorf("Dimensions = %d, want 1 learned from the first result", c.Dimensions())
	}
}

// A batch the server rejects is retried one text at a time (with a shorter
// text limit), so one bad text no longer fails its neighbours.
func TestComputeFallsBackToOneByOne(t *testing.T) {
	var batchCalls, singleCalls atomic.Int32
	c := newProviderComputer(t, ProviderOllama, func(w http.ResponseWriter, r *http.Request) {
		var req ollamaEmbedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		inputs := req.Input.([]any)
		if len(inputs) > 1 {
			batchCalls.Add(1)
			http.Error(w, "batch too large", 400)
			return
		}
		singleCalls.Add(1)
		_, _ = w.Write([]byte(`{"embeddings":[[` + strings.Repeat("1", 1) + `]]}`))
	})
	got, err := c.Compute(context.Background(), []string{"x", "y"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] == nil || got[1] == nil {
		t.Fatalf("got %v", got)
	}
	if batchCalls.Load() == 0 || singleCalls.Load() != 2 {
		t.Errorf("batch calls %d, single calls %d (want 2)", batchCalls.Load(), singleCalls.Load())
	}
}

func TestComputeOneByOneFailureReportsTheText(t *testing.T) {
	c := newProviderComputer(t, ProviderOllama, func(w http.ResponseWriter, r *http.Request) {
		var req ollamaEmbedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		inputs := req.Input.([]any)
		if len(inputs) == 1 && inputs[0].(string) == "poison" {
			http.Error(w, "cannot embed", 400)
			return
		}
		if len(inputs) > 1 {
			http.Error(w, "batch rejected", 400)
			return
		}
		_, _ = w.Write([]byte(`{"embeddings":[[1]]}`))
	})
	_, err := c.Compute(context.Background(), []string{"fine", "poison"})
	if err == nil || !strings.Contains(err.Error(), "compute text 1 (retry)") {
		t.Fatalf("err = %v", err)
	}
}

func TestComputeSingleTextBatchFailure(t *testing.T) {
	c := newProviderComputer(t, ProviderOllama, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "denied", 400)
	})
	_, err := c.ComputeSingle(context.Background(), "only")
	if err == nil || !strings.Contains(err.Error(), "compute batch 0-1") {
		t.Fatalf("err = %v", err)
	}
}

// A provider that answers a one-by-one retry with no vectors is an error, not a
// silent hole in the result.
func TestComputeOneByOneEmptyAnswer(t *testing.T) {
	c := newProviderComputer(t, ProviderGemini, func(w http.ResponseWriter, r *http.Request) {
		var req geminiEmbedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Requests) > 1 {
			http.Error(w, "too many", 400)
			return
		}
		_, _ = w.Write([]byte(`{"embeddings":[]}`))
	})
	_, err := c.Compute(context.Background(), []string{"a", "b"})
	if err == nil || !strings.Contains(err.Error(), "returned 0 embeddings") {
		t.Fatalf("err = %v", err)
	}
}

func TestComputeOneByOneUnsupportedProvider(t *testing.T) {
	c := NewComputer(Options{Provider: Provider("bogus"), Model: "m", BatchSize: 4})
	_, err := c.Compute(context.Background(), []string{"a", "b"})
	if err == nil || !strings.Contains(err.Error(), "unsupported provider: bogus") {
		t.Fatalf("err = %v", err)
	}
}

func TestComputeGeminiThroughCompute(t *testing.T) {
	c := newProviderComputer(t, ProviderGemini, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"embeddings":[{"values":[1,1,1]},{"values":[2,2,2]}]}`))
	})
	got, err := c.Compute(context.Background(), []string{"a", "b"})
	if err != nil || len(got) != 2 || got[1][0] != 2 || c.Dimensions() != 3 {
		t.Fatalf("got %v, dim %d, err %v", got, c.Dimensions(), err)
	}
}

func TestComputeMockProgress(t *testing.T) {
	c := NewComputer(Options{Provider: ProviderMock, BatchSize: 2, Concurrency: 1})
	var last int
	c.SetProgressCallback(func(done, total int) { last = done })
	got, err := c.Compute(context.Background(), []string{"a", "b", "c"})
	if err != nil || len(got) != 3 || last != 3 {
		t.Fatalf("got %d vectors, last progress %d, err %v", len(got), last, err)
	}
	one, err := c.ComputeSingle(context.Background(), "a")
	if err != nil || len(one) != mockDims {
		t.Fatalf("ComputeSingle = %v, %v", one, err)
	}
}

func TestGetModelTokenLimitTagsAndCase(t *testing.T) {
	tests := []struct {
		model string
		want  int
	}{
		{"bge-m3", 2048},
		{"bge-m3:latest", 2048},
		{"BGE-M3:567m", 2048},
		{"nomic-embed-text:v1.5", 2048},
		{"mxbai-embed-large:335m", 512},
		{"some-unknown-model:7b", modelTokenLimits["default"]},
		{"", modelTokenLimits["default"]},
	}
	for _, tc := range tests {
		if got := GetModelTokenLimit(tc.model); got != tc.want {
			t.Errorf("GetModelTokenLimit(%q) = %d, want %d", tc.model, got, tc.want)
		}
	}
}
