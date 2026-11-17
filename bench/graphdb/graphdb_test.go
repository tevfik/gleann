package graphdb_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tevfik/gleann/bench/graphdb"
	_ "github.com/tevfik/gleann/bench/graphdb/adapters/sqlite"
)

// lossy wraps the reference and drops one caller from every Callers answer, so
// the runner must report parity mismatches for it.
type lossy struct{ graphdb.Backend }

func (l lossy) Name() string { return "lossy" }

func (l lossy) Callers(ctx context.Context, fqn string, depth int) ([]string, error) {
	r, err := l.Backend.Callers(ctx, fqn, depth)
	if len(r) > 0 {
		r = r[1:]
	}
	return r, err
}

func init() {
	graphdb.Register("lossy", func() graphdb.Backend {
		b, _ := graphdb.New("sqlite")
		return lossy{b}
	})
}

func cfg(backends ...string) graphdb.Config {
	return graphdb.Config{Sizes: []int{3000}, Queries: 30, Paths: 5, Backends: backends, WorkDir: ""}
}

func TestReferenceRunsClean(t *testing.T) {
	rep, err := graphdb.Run(context.Background(), cfg("sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Failed() {
		t.Fatalf("reference reported problems:\n%s", rep.Markdown())
	}
	res := rep.Sizes[0].Results[0]
	if res.DiskBytes == 0 || res.LoadMS == 0 || res.Latency["callers d3"].N == 0 {
		t.Errorf("missing measurements: %+v", res)
	}
	if md := rep.Markdown(); !strings.Contains(md, "callers d3") || !strings.Contains(md, "Parity vs sqlite") {
		t.Errorf("markdown lacks the expected tables:\n%s", md)
	}
	dir := t.TempDir()
	if _, _, err := rep.Write(dir); err != nil {
		t.Fatal(err)
	}
}

func TestParityMismatchIsDetected(t *testing.T) {
	rep, err := graphdb.Run(context.Background(), cfg("lossy"))
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Failed() {
		t.Fatal("a backend dropping callers was not reported")
	}
	var lossyRes *graphdb.Result
	for _, r := range rep.Sizes[0].Results {
		if r.Backend == "lossy" {
			lossyRes = r
		}
	}
	if lossyRes == nil || lossyRes.Mismatches["callers d1"] == 0 {
		t.Fatalf("callers d1 mismatches not recorded: %+v", lossyRes)
	}
	if lossyRes.Mismatches["callees d3"] != 0 {
		t.Errorf("callees were right but reported as mismatching")
	}
}

func TestUnknownBackend(t *testing.T) {
	if _, err := graphdb.Run(context.Background(), cfg("nope")); err == nil {
		t.Fatal("unknown backend accepted")
	}
}
