package embedding

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// errBatchFake is the sentinel failure returned by batchFakeComputer.
var errBatchFake = errors.New("fake embedder failure")

// batchFakeComputer records the texts it receives per call and can fail on
// a chosen call. It also checks that every earlier batch is already on disk
// when a new call starts, which is the per-batch write timing RFC 003 asks
// for.
type batchFakeComputer struct {
	model         string
	dim           int
	dir           string // cache dir, used to check earlier batches are on disk
	failOnCall    int    // 1-based call number that fails; 0 = never fail
	calls         int
	received      [][]string // texts per call, in call order
	priorTexts    []string
	writesInOrder bool // false if a previous batch was not on disk yet
}

func (f *batchFakeComputer) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	f.calls++
	// Every earlier batch must already be persisted before this call starts.
	for _, text := range f.priorTexts {
		path := filepath.Join(f.dir, cacheKey(f.model, text)+".bin")
		if _, err := os.Stat(path); err != nil {
			f.writesInOrder = false
		}
	}
	f.priorTexts = append(f.priorTexts, texts...)
	f.received = append(f.received, append([]string(nil), texts...))
	if f.failOnCall > 0 && f.calls == f.failOnCall {
		return nil, errBatchFake
	}
	result := make([][]float32, len(texts))
	for i, text := range texts {
		result[i] = batchFakeVector(text, f.dim)
	}
	return result, nil
}

func (f *batchFakeComputer) ComputeSingle(ctx context.Context, text string) ([]float32, error) {
	vecs, err := f.Compute(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

func (f *batchFakeComputer) Dimensions() int   { return f.dim }
func (f *batchFakeComputer) ModelName() string { return f.model }

// batchFakeVector builds a deterministic vector so expected results can be
// recomputed independently of the cache.
func batchFakeVector(text string, dim int) []float32 {
	h := 0
	for _, b := range []byte(text) {
		h = h*31 + int(b)
	}
	vec := make([]float32, dim)
	for j := range vec {
		vec[j] = float32(h%1000+j) * 0.01
	}
	return vec
}

func assertVectorsEqual(t *testing.T, label string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: length mismatch: %d != %d", label, len(got), len(want))
		return
	}
	for j := range want {
		if got[j] != want[j] {
			t.Errorf("%s: value mismatch at [%d]: %f != %f", label, j, got[j], want[j])
		}
	}
}

func assertReceivedEqual(t *testing.T, got, want [][]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %d inner calls, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("call %d: expected texts %v, got %v", i+1, want[i], got[i])
		}
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				t.Fatalf("call %d: expected texts %v, got %v", i+1, want[i], got[i])
			}
		}
	}
}

// A failure on the 3rd batch must leave the first 2 batches on disk, and a
// second Compute (fresh cache over the same dir, like a restarted build)
// must only ask the inner computer for the remaining texts.
func TestCachedComputer_PerBatchPersistenceOnError(t *testing.T) {
	dir := t.TempDir()
	texts := []string{"t0", "t1", "t2", "t3", "t4", "t5"}
	fake := &batchFakeComputer{model: "batch-model", dim: 4, dir: dir, failOnCall: 3, writesInOrder: true}
	cached := NewCachedComputer(fake, CacheOptions{Dir: dir, BatchSize: 2})

	_, err := cached.Compute(context.Background(), texts)
	if err == nil {
		t.Fatal("expected error from the 3rd batch")
	}
	if !errors.Is(err, errBatchFake) {
		t.Errorf("expected wrapped fake error, got: %v", err)
	}
	if fake.calls != 3 {
		t.Fatalf("expected 3 inner calls, got %d", fake.calls)
	}
	if !fake.writesInOrder {
		t.Error("a batch was not written to disk before the next inner call started")
	}

	// First two batches are on disk; the failed batch is not.
	for i, text := range texts {
		path := filepath.Join(dir, cacheKey(fake.model, text)+".bin")
		_, statErr := os.Stat(path)
		if i < 4 && statErr != nil {
			t.Errorf("text %q (batch %d) should be persisted: %v", text, i/2+1, statErr)
		}
		if i >= 4 && statErr == nil {
			t.Errorf("text %q should not be persisted after its batch failed", text)
		}
	}
	// No half-written temp files may be left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == sweepStampName {
			continue // the hourly sweep's stamp, not a leftover
		}
		if !strings.HasSuffix(e.Name(), ".bin") {
			t.Errorf("unexpected leftover file in cache dir: %s", e.Name())
		}
	}

	// Restart: a fresh cache over the same dir only computes the remainder.
	fake2 := &batchFakeComputer{model: "batch-model", dim: 4, dir: dir, writesInOrder: true}
	cached2 := NewCachedComputer(fake2, CacheOptions{Dir: dir, BatchSize: 2})
	vecs, err := cached2.Compute(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	assertReceivedEqual(t, fake2.received, [][]string{{"t4", "t5"}})
	if len(vecs) != len(texts) {
		t.Fatalf("expected %d vectors, got %d", len(texts), len(vecs))
	}
	for i, text := range texts {
		assertVectorsEqual(t, text, vecs[i], batchFakeVector(text, 4))
	}
	// Stats: 6 lookups, 4 hits from the persisted batches.
	hits, total := cached2.Stats()
	if hits != 4 || total != 6 {
		t.Errorf("expected stats 4/6, got %d/%d", hits, total)
	}
}

// Mixed cached/uncached input must come back in input order, and the inner
// computer must only see the uncached texts, in order, split into batches.
func TestCachedComputer_OrderPreservedMixedCache(t *testing.T) {
	dir := t.TempDir()
	fake := &batchFakeComputer{model: "batch-model", dim: 4, dir: dir, writesInOrder: true}
	cached := NewCachedComputer(fake, CacheOptions{Dir: dir, BatchSize: 2})
	ctx := context.Background()

	// Pre-cache two texts that will sit between uncached ones.
	if _, err := cached.Compute(ctx, []string{"b", "d"}); err != nil {
		t.Fatal(err)
	}

	texts := []string{"a", "b", "c", "d", "e", "f", "g"}
	vecs, err := cached.Compute(ctx, texts)
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != len(texts) {
		t.Fatalf("expected %d vectors, got %d", len(texts), len(vecs))
	}
	for i, text := range texts {
		assertVectorsEqual(t, text, vecs[i], batchFakeVector(text, 4))
	}
	// First call cached b,d; then uncached a,c,e,f,g in batches of 2.
	assertReceivedEqual(t, fake.received, [][]string{{"b", "d"}, {"a", "c"}, {"e", "f"}, {"g"}})
	if !fake.writesInOrder {
		t.Error("a batch was not written to disk before the next inner call started")
	}
	// Stats: 2 + 7 lookups, 2 hits (b and d).
	hits, total := cached.Stats()
	if hits != 2 || total != 9 {
		t.Errorf("expected stats 2/9, got %d/%d", hits, total)
	}
}

// A truncated .bin must not be used: the loader rejects it and Compute
// recomputes the text, rewriting the file at full length.
func TestCachedComputer_TruncatedFileNotUsed(t *testing.T) {
	cases := []struct {
		name string
		// bytes to keep from the full 16-byte (dim 4) file
		keep int
	}{
		// Still float-aligned but the wrong dimension: only the dimension
		// check catches this, the %4 check alone would load 2 floats.
		{"wrong dimension", 8},
		// Cut mid-float, like a writer killed partway through.
		{"partial float", 14},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			fake := &batchFakeComputer{model: "batch-model", dim: 4, dir: dir, writesInOrder: true}
			cached := NewCachedComputer(fake, CacheOptions{Dir: dir})

			key := cacheKey(fake.model, "hello")
			vec := batchFakeVector("hello", 4)
			if err := cached.saveToDisk(key, vec); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, key+".bin")
			full, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, full[:tc.keep], 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := cached.loadFromDisk(key); err == nil {
				t.Fatal("expected truncated cache entry to be rejected")
			}

			vecs, err := cached.Compute(context.Background(), []string{"hello"})
			if err != nil {
				t.Fatal(err)
			}
			assertReceivedEqual(t, fake.received, [][]string{{"hello"}})
			assertVectorsEqual(t, "hello", vecs[0], vec)

			// The file was rewritten at full length.
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() != int64(4*4) {
				t.Errorf("expected rewritten file of %d bytes, got %d", 4*4, info.Size())
			}
		})
	}
}

// With an unknown dimension (0) the loader cannot check the length and must
// still accept any float-aligned file.
func TestCachedComputer_UnknownDimensionSkipsLengthCheck(t *testing.T) {
	dir := t.TempDir()
	mock := newMockEmbedder(0) // Dimensions() == 0: not known yet
	cached := NewCachedComputer(mock, CacheOptions{Dir: dir})

	key := cacheKey(mock.ModelName(), "hello")
	path := filepath.Join(dir, key+".bin")
	if err := os.WriteFile(path, make([]byte, 12), 0o644); err != nil {
		t.Fatal(err)
	}
	vec, err := cached.loadFromDisk(key)
	if err != nil {
		t.Fatalf("expected float-aligned file to load with unknown dimension: %v", err)
	}
	if len(vec) != 3 {
		t.Errorf("expected 3 floats, got %d", len(vec))
	}
}

// Batch size selection: explicit option wins, otherwise one parallel wave of
// the inner *Computer (batch size times concurrency, so the cache does not
// serialise its requests), otherwise the default 64.
func TestCachedComputer_BatchSizeSelection(t *testing.T) {
	c := NewCachedComputer(newMockEmbedder(4), CacheOptions{Dir: t.TempDir(), BatchSize: 5})
	if c.batchSize != 5 {
		t.Errorf("explicit option: expected batch size 5, got %d", c.batchSize)
	}

	inner := NewComputer(Options{Provider: ProviderMock, Model: "m", BatchSize: 3, Concurrency: 4})
	c = NewCachedComputer(inner, CacheOptions{Dir: t.TempDir()})
	if c.batchSize != 12 {
		t.Errorf("inner computer wave: expected 3x4 = 12, got %d", c.batchSize)
	}

	c = NewCachedComputer(newMockEmbedder(4), CacheOptions{Dir: t.TempDir()})
	if c.batchSize != 64 {
		t.Errorf("default: expected batch size 64, got %d", c.batchSize)
	}
}

// End-to-end through a real *Computer (mock provider): per-batch caching
// works with the provider's own batching and the dimension check engages
// once the dimension is known.
func TestCachedComputer_BatchedWithInnerComputer(t *testing.T) {
	dir := t.TempDir()
	inner := NewComputer(Options{Provider: ProviderMock, Model: "mock-model", BatchSize: 2})
	cached := NewCachedComputer(inner, CacheOptions{Dir: dir})
	ctx := context.Background()

	texts := []string{"a", "b", "c", "d", "e"}
	vecs1, err := cached.Compute(ctx, texts)
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs1) != len(texts) {
		t.Fatalf("expected %d vectors, got %d", len(texts), len(vecs1))
	}

	// Everything is cached now (L1 and disk); a fresh cache over the same
	// dir must load all vectors from disk without calling the provider.
	cached2 := NewCachedComputer(inner, CacheOptions{Dir: dir})
	vecs2, err := cached2.Compute(ctx, texts)
	if err != nil {
		t.Fatal(err)
	}
	for i := range texts {
		assertVectorsEqual(t, texts[i], vecs2[i], vecs1[i])
	}
	hits, total := cached2.Stats()
	if hits != len(texts) || total != len(texts) {
		t.Errorf("expected stats %d/%d, got %d/%d", len(texts), len(texts), hits, total)
	}
}

// shortFake answers one vector fewer than asked, and reports dimension 0
// until its first Compute, like the real providers.
type shortFake struct {
	batchFakeComputer
	short   bool
	learned bool
}

func (f *shortFake) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	out, err := f.batchFakeComputer.Compute(ctx, texts)
	f.learned = true
	if f.short && len(out) > 0 {
		out = out[:len(out)-1]
	}
	return out, err
}

func (f *shortFake) Dimensions() int {
	if !f.learned {
		return 0
	}
	return f.dim
}

// An embedder that answers fewer vectors than texts is an error, not a
// result with nil vectors in it.
func TestCachedComputer_ShortAnswerIsError(t *testing.T) {
	f := &shortFake{batchFakeComputer: batchFakeComputer{model: "m", dim: 4}, short: true}
	c := NewCachedComputer(f, CacheOptions{Dir: t.TempDir(), BatchSize: 8})
	if _, err := c.Compute(context.Background(), []string{"a", "b", "c"}); err == nil {
		t.Fatal("expected an error for a short embedder answer")
	}
}

// A new process whose texts are all cached never calls the embedder, which
// then still reports dimension 0; the cache reports the size it loaded.
func TestCachedComputer_DimensionsFromColdCache(t *testing.T) {
	dir := t.TempDir()
	warm := NewCachedComputer(&shortFake{batchFakeComputer: batchFakeComputer{model: "m", dim: 4}}, CacheOptions{Dir: dir})
	if _, err := warm.Compute(context.Background(), []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	cold := NewCachedComputer(&shortFake{batchFakeComputer: batchFakeComputer{model: "m", dim: 4}}, CacheOptions{Dir: dir})
	if _, err := cold.Compute(context.Background(), []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if d := cold.Dimensions(); d != 4 {
		t.Errorf("Dimensions after an all-cached Compute = %d, want 4", d)
	}
}

// A cancelled context stops before the next batch is sent.
func TestCachedComputer_StopsWhenCancelled(t *testing.T) {
	f := &batchFakeComputer{model: "m", dim: 4}
	c := NewCachedComputer(f, CacheOptions{Dir: t.TempDir(), BatchSize: 1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Compute(ctx, []string{"a", "b"}); err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
	if f.calls != 0 {
		t.Errorf("embedder called %d times after cancel", f.calls)
	}
}

// The cache key covers the text as embedded, prefix included: a vector cached for an unprefixed text is never
// served for the prefixed one (EmbeddingGemma prompts, E5 prefixes).
func TestCacheKeyIncludesPrefix(t *testing.T) {
	const m, text, prefix = "embeddinggemma-2-Q8_0.gguf", "hello", "title: none | text: "
	if cacheKey(m, text) == cacheKey(m, prefix+text) {
		t.Fatal("prefixed and unprefixed text share a cache key")
	}
	prefixed := prefix + text
	if cacheKey(m, prefixed) != cacheKey(m, prefix+"hello") {
		t.Fatal("key not deterministic")
	}
}
