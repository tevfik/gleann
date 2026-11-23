package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/benchmark"
)

// TestResolveComparePairKnownPlusUnknown pins the split of a --compare spec
// whose strategy names contain commas. When exactly one side names a
// registered strategy, the other side must be preserved verbatim so the
// runner can report `strategy "NonExistent" not found in run results` instead
// of a generic "two strategies required" error.
func TestResolveComparePairKnownPlusUnknown(t *testing.T) {
	const def = "Hybrid (default, α=0.7)"
	names := []string{"BM25", def, "Hybrid (α=0.5)", "Vector (HNSW)"}

	cases := []struct {
		name      string
		spec      string
		wantA     string
		wantB     string
		wantOK    bool
		wantError bool
	}{
		{
			name:   "known default then unknown",
			spec:   def + ",NonExistent",
			wantA:  def,
			wantB:  "NonExistent",
			wantOK: true,
		},
		{
			name:   "unknown then known default",
			spec:   "NonExistent," + def,
			wantA:  "NonExistent",
			wantB:  def,
			wantOK: true,
		},
		{
			name:   "plain known then unknown",
			spec:   "BM25,NonExistent",
			wantA:  "BM25",
			wantB:  "NonExistent",
			wantOK: true,
		},
		{
			name:   "both known, both comma names",
			spec:   "Hybrid (α=0.5)," + def,
			wantA:  "Hybrid (α=0.5)",
			wantB:  def,
			wantOK: true,
		},
		{
			name:   "both known, comma name first",
			spec:   def + ",Hybrid (α=0.5)",
			wantA:  def,
			wantB:  "Hybrid (α=0.5)",
			wantOK: true,
		},
		{
			name:   "whitespace around the separator",
			spec:   " " + def + " , NonExistent ",
			wantA:  def,
			wantB:  "NonExistent",
			wantOK: true,
		},
		{
			name:      "single strategy",
			spec:      "Hybrid",
			wantError: true,
		},
		{
			name:      "trailing comma",
			spec:      "Hybrid,",
			wantError: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, b, ok := resolveComparePair(tc.spec, names)
			if tc.wantError {
				if ok {
					t.Fatalf("resolveComparePair(%q) = (%q, %q, true), want rejection", tc.spec, a, b)
				}
				return
			}
			if !ok {
				t.Fatalf("resolveComparePair(%q) = (%q, %q, false), want (%q, %q, true)", tc.spec, a, b, tc.wantA, tc.wantB)
			}
			if a != tc.wantA || b != tc.wantB {
				t.Fatalf("resolveComparePair(%q) = (%q, %q), want (%q, %q)", tc.spec, a, b, tc.wantA, tc.wantB)
			}
		})
	}
}

// TestCLICBenchHybridNonFiniteAlpha: a saved or flagged NaN/Inf weight must be
// treated like any other out-of-range weight and fall back to the shipped
// default 0.7. Otherwise the label claims a hybrid measurement while the
// searcher silently skips the BM25 branch.
func TestCLICBenchHybridNonFiniteAlpha(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")

	tasks := cWriteTasks(t, filepath.Join(e.Home, "tasks.json"), cBenchTasks)

	for _, alpha := range []string{"NaN", "nan", "+Inf", "Inf", "-Inf"} {
		t.Run(alpha, func(t *testing.T) {
			r := e.run("bench", "--index", "demo", "--tasks", tasks, "--top-k", "3", "--json", "--hybrid", alpha)
			wantExit(t, r, 0)
			var summaries map[string]benchmark.SummaryResult
			if err := json.Unmarshal([]byte(r.Out), &summaries); err != nil {
				t.Fatalf("bench --json --hybrid %s: %v\n%s", alpha, err, r.Out)
			}
			if _, ok := summaries["Hybrid (default, α=0.7)"]; !ok {
				names := make([]string, 0, len(summaries))
				for name := range summaries {
					names = append(names, name)
				}
				t.Fatalf("--hybrid %s must fall back to the default α=0.7 row, got strategies %v", alpha, names)
			}
			for name := range summaries {
				if strings.Contains(name, "α=NaN") || strings.Contains(name, "α=+Inf") || strings.Contains(name, "α=-Inf") {
					t.Errorf("--hybrid %s registered a non-finite weight row %q", alpha, name)
				}
			}
		})
	}
}

// TestCLICBenchInRunCompareKnownPlusUnknown: the in-run compare must keep the
// unknown half of the spec, so the runner produces its precise not-found
// error rather than a generic parse error.
func TestCLICBenchInRunCompareKnownPlusUnknown(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo", "--graph")
	tasks := cWriteTasks(t, filepath.Join(e.Home, "tasks.json"), cBenchTasks)

	for _, spec := range []string{
		"Hybrid (default, α=0.7),NonExistent",
		"NonExistent,Hybrid (default, α=0.7)",
	} {
		t.Run(spec, func(t *testing.T) {
			r := e.run("bench", "--index", "demo", "--tasks", tasks, "--top-k", "3", "--compare", spec)
			wantExit(t, r, 1)
			mustContain(t, "unknown strategy", r.Err, `strategy "NonExistent" not found in run results`)
			if strings.Contains(r.Err, "--compare requires two strategies") {
				t.Errorf("must not report a generic compare parse error for %q: %s", spec, r.Err)
			}
		})
	}
}

// TestSplitStandaloneCompareSpec: the offline two-file form must keep a
// strategy qualifier that itself contains a comma intact.
func TestSplitStandaloneCompareSpec(t *testing.T) {
	const def = "Hybrid (default, α=0.7)"
	cases := []struct {
		name   string
		spec   string
		wantA  string
		wantB  string
		wantOK bool
	}{
		{
			name:   "comma strategy in first target",
			spec:   "a.jsonl:" + def + ",b.jsonl:BM25",
			wantA:  "a.jsonl:" + def,
			wantB:  "b.jsonl:BM25",
			wantOK: true,
		},
		{
			name:   "comma strategy in second target",
			spec:   "a.jsonl:BM25,b.jsonl:" + def,
			wantA:  "a.jsonl:BM25",
			wantB:  "b.jsonl:" + def,
			wantOK: true,
		},
		{
			name:   "comma strategies in both targets",
			spec:   "a.jsonl:" + def + ",b.jsonl:" + def,
			wantA:  "a.jsonl:" + def,
			wantB:  "b.jsonl:" + def,
			wantOK: true,
		},
		{
			name:   "plain two files",
			spec:   "a.jsonl,b.jsonl",
			wantA:  "a.jsonl",
			wantB:  "b.jsonl",
			wantOK: true,
		},
		{
			name:   "single target",
			spec:   "a.jsonl",
			wantOK: false,
		},
		{
			name:   "three plain files",
			spec:   "a.jsonl,b.jsonl,c.jsonl",
			wantOK: false,
		},
		{
			name:   "trailing comma",
			spec:   "a.jsonl,",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, b, ok := splitStandaloneCompareSpec(tc.spec)
			if ok != tc.wantOK {
				t.Fatalf("splitStandaloneCompareSpec(%q) = (%q, %q, %v), want ok=%v", tc.spec, a, b, ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if a != tc.wantA || b != tc.wantB {
				t.Fatalf("splitStandaloneCompareSpec(%q) = (%q, %q), want (%q, %q)", tc.spec, a, b, tc.wantA, tc.wantB)
			}
		})
	}
}

// TestCLICBenchCompareStandaloneDefaultStrategyName drives the offline
// comparison end to end with the default strategy name as one target.
func TestCLICBenchCompareStandaloneDefaultStrategyName(t *testing.T) {
	e := newCLIEnv(t)
	const def = "Hybrid (default, α=0.7)"

	fileA := filepath.Join(e.Home, "defaultA.jsonl")
	fileB := filepath.Join(e.Home, "defaultB.jsonl")
	contentA := `{"task":"t1","strategy":"` + def + `","r1":0.0,"r5":0.5,"r10":1.0,"mrr":0.3,"tokens":1500}
{"task":"t2","strategy":"` + def + `","r1":0.0,"r5":0.0,"r10":0.5,"mrr":0.2,"tokens":1600}
{"task":"t1","strategy":"BM25","r1":1.0,"r5":1.0,"r10":1.0,"mrr":1.0,"tokens":1200}
{"task":"t2","strategy":"BM25","r1":0.0,"r5":1.0,"r10":1.0,"mrr":0.5,"tokens":1300}
`
	contentB := `{"task":"t1","strategy":"` + def + `","r1":0.0,"r5":0.5,"r10":1.0,"mrr":0.3,"tokens":1500}
{"task":"t2","strategy":"` + def + `","r1":0.0,"r5":0.0,"r10":0.5,"mrr":0.2,"tokens":1600}
{"task":"t1","strategy":"BM25","r1":1.0,"r5":1.0,"r10":1.0,"mrr":1.0,"tokens":1200}
{"task":"t2","strategy":"BM25","r1":0.0,"r5":1.0,"r10":1.0,"mrr":0.5,"tokens":1300}
`
	if err := os.WriteFile(fileA, []byte(contentA), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte(contentB), 0o644); err != nil {
		t.Fatal(err)
	}

	r := e.run("bench", "--compare", fileA+":"+def+","+fileB+":BM25", "--json")
	wantExit(t, r, 0)
	var comp benchmark.ComparisonResult
	if err := json.Unmarshal([]byte(r.Out), &comp); err != nil {
		t.Fatalf("unmarshal compare json: %v\n%s", err, r.Out)
	}
	if comp.BaselineName != def || comp.CandidateName != "BM25" {
		t.Fatalf("unexpected strategies: baseline=%s candidate=%s", comp.BaselineName, comp.CandidateName)
	}
	if comp.TaskCount != 2 {
		t.Errorf("expected 2 compared tasks, got %d", comp.TaskCount)
	}

	// A missing target file still fails with the file-specific error, not a
	// generic spec parse error.
	missing := filepath.Join(e.Home, "does-not-exist.jsonl")
	r = e.run("bench", "--compare", missing+":BM25,"+fileB+":"+def)
	wantExit(t, r, 1)
	mustContain(t, "missing file", r.Err, "error reading")
}

func TestSplitStandaloneCompareSpecAcceptsParenthesesInPaths(t *testing.T) {
	spec := "bench (runs)/runA.jsonl:Hybrid (default, α=0.7),bench (runs)/runB.jsonl:BM25"
	a, b, ok := splitStandaloneCompareSpec(spec)
	if !ok {
		t.Fatalf("splitStandaloneCompareSpec(%q) rejected a path with parentheses", spec)
	}
	if a != "bench (runs)/runA.jsonl:Hybrid (default, α=0.7)" || b != "bench (runs)/runB.jsonl:BM25" {
		t.Fatalf("got %q and %q", a, b)
	}
}
