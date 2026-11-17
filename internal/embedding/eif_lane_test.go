//go:build eif

package embedding

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// laneTestModel returns the model the lane tests load: GLEANN_TEST_EIF_MODEL (a path or a model name), else the
// name the other eif tests use. The tests skip when no model can be found.
func laneTestModel() string {
	if m := os.Getenv("GLEANN_TEST_EIF_MODEL"); m != "" {
		return m
	}
	return "granite-30m-q8_0"
}

// freshEIF loads a model instance that no other test shares (a private engine, closed by the caller).
func freshEIF(t testing.TB) *EIFComputer {
	t.Helper()
	shared, err := GetOrLoadEIF(laneTestModel())
	if err != nil {
		t.Skipf("model not available: %v", err)
	}
	return newEIFForTest(t, shared.modelPath)
}

func newEIFForTest(t testing.TB, path string) *EIFComputer {
	t.Helper()
	e := &EIFComputer{modelPath: path}
	b, err := e.loadInstance()
	if err != nil {
		t.Skipf("model not loadable: %v", err)
	}
	e.bert = b
	e.dim = int(b.config.dim)
	return e
}

func longTexts(n int) []string {
	texts := make([]string, n)
	for i := range texts {
		texts[i] = strings.Repeat("func Example() { return value } // a longer passage of code ", 12) + string(rune('a'+i%26))
	}
	return texts
}

// A short call runs on the query lane, loaded on first use, and returns the vectors the batch path computes.
func TestEIFQueryLaneMatchesBatchPath(t *testing.T) {
	e := freshEIF(t)
	defer e.Close()
	texts := []string{"how do I sort an array", "where is the config loaded"}

	if e.query != nil {
		t.Fatal("the query lane must load lazily")
	}
	got, err := e.Compute(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	if e.query == nil {
		t.Fatal("a short call did not use the query lane")
	}
	want, err := e.computeBatch(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	for i := range texts {
		if cs := cosineSimilarity(got[i], want[i]); cs < 0.99999 {
			t.Errorf("text %d: lane vs batch cosine %.6f", i, cs)
		}
	}
	// A call above the limit never loads the lane of a fresh engine.
	f := freshEIF(t)
	defer f.Close()
	if _, err := f.Compute(context.Background(), longTexts(queryLaneMax+1)); err != nil {
		t.Fatal(err)
	}
	if f.query != nil {
		t.Error("a batch above the lane limit loaded the query lane")
	}
}

// A search query is answered while a build's batch is still running.
func TestEIFQueryDoesNotWaitForBatch(t *testing.T) {
	e := freshEIF(t)
	defer e.Close()
	if _, err := e.Compute(context.Background(), []string{"warm the lane up"}); err != nil {
		t.Fatal(err)
	}

	batchDone := make(chan time.Duration, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		t0 := time.Now()
		_, _ = e.Compute(context.Background(), longTexts(200))
		batchDone <- time.Since(t0)
	}()
	<-started
	time.Sleep(50 * time.Millisecond) // let the batch take its lock

	t0 := time.Now()
	if _, err := e.Compute(context.Background(), []string{"a short search query"}); err != nil {
		t.Fatal(err)
	}
	query := time.Since(t0)
	select {
	case <-batchDone:
		t.Skip("the batch finished before the query: nothing to compare (model too fast)")
	default:
	}
	t.Logf("query took %v while the batch was still running", query)
	batch := <-batchDone
	if query >= batch/2 {
		t.Errorf("query (%v) was held up by the batch (%v)", query, batch)
	}
}

// When the lane cannot be loaded, short calls use the batch path.
func TestEIFQueryLaneFallsBackToBatch(t *testing.T) {
	e := freshEIF(t)
	defer e.Close()
	e.queryFailed = true // as after a failed load
	got, err := e.Compute(context.Background(), []string{"fallback query"})
	if err != nil || len(got) != 1 || len(got[0]) != e.Dim() {
		t.Fatalf("fallback: %v %d", err, len(got))
	}
	if e.query != nil {
		t.Error("the lane was loaded although it is marked failed")
	}
}

// Close frees both instances, and a closed engine refuses further work.
func TestEIFCloseFreesBothLanes(t *testing.T) {
	e := freshEIF(t)
	if _, err := e.Compute(context.Background(), []string{"load the lane"}); err != nil {
		t.Fatal(err)
	}
	if e.query == nil {
		t.Fatal("lane not loaded")
	}
	e.Close()
	if e.query != nil || !e.closed {
		t.Fatalf("after Close: query=%v closed=%v", e.query, e.closed)
	}
	if _, err := e.Compute(context.Background(), []string{"after close"}); err == nil {
		t.Error("a closed engine computed a short call")
	}
	e.Close() // idempotent
}

// BenchmarkEIFQueryDuringBuild measures the latency of a one-text call while a build's batch runs: "lane" is
// the shipped behaviour, "batch-path" forces short calls through the batch lock like before. It needs a model
// (GLEANN_TEST_EIF_MODEL) and skips without one.
func BenchmarkEIFQueryDuringBuild(b *testing.B) {
	for _, mode := range []string{"lane", "batch-path"} {
		b.Run(mode, func(b *testing.B) {
			e := freshEIF(b)
			defer e.Close()
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				defer close(done)
				for ctx.Err() == nil {
					_, _ = e.computeBatch(ctx, longTexts(64))
				}
			}()
			time.Sleep(100 * time.Millisecond)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var err error
				if mode == "lane" {
					_, err = e.Compute(ctx, []string{"a short search query"})
				} else {
					_, err = e.computeBatch(ctx, []string{"a short search query"})
				}
				if err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			cancel()
			<-done
		})
	}
}
