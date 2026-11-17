package graphdb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func fmtMS(ms float64) string {
	switch {
	case ms >= 1000:
		return fmt.Sprintf("%.2f s", ms/1000)
	case ms >= 10:
		return fmt.Sprintf("%.0f ms", ms)
	default:
		return fmt.Sprintf("%.1f ms", ms)
	}
}

func fmtUS(us int64) string { return fmtMS(float64(us) / 1000) }

func fmtMiB(b int64) string { return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20)) }

// Markdown renders the report: per size a summary table, a latency table and
// a parity table, with one column per backend (the reference first).
func (r *Report) Markdown() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Graph database benchmark — %s\n\n", r.Time)
	fmt.Fprintf(&sb, "go %s %s, %d CPUs visible; %d queries per type, %d path pairs.\n", r.Go, r.Platf, r.CPUs, r.Queries, r.Paths)
	if r.Command != "" {
		fmt.Fprintf(&sb, "\n`%s`\n", r.Command)
	}
	for _, sz := range r.Sizes {
		fmt.Fprintf(&sb, "\n## %d edges requested · %d stored · %d symbols · %d files\n", sz.EdgesRequested, sz.EdgesStored, sz.Symbols, sz.Files)
		cols := func(cell func(*Result) string) string {
			var s strings.Builder
			for _, res := range sz.Results {
				s.WriteString(" " + cell(res) + " |")
			}
			return s.String()
		}
		header := "| |" + cols(func(r *Result) string { return r.Backend })
		sep := "|---|" + strings.Repeat("---:|", len(sz.Results))
		fmt.Fprintf(&sb, "\n%s\n%s\n", header, sep)
		fmt.Fprintf(&sb, "| load (bulk) |%s\n", cols(func(r *Result) string { return fmtMS(r.LoadMS) }))
		fmt.Fprintf(&sb, "| update one file |%s\n", cols(func(r *Result) string { return fmtMS(r.UpdateMS) }))
		fmt.Fprintf(&sb, "| remove one file |%s\n", cols(func(r *Result) string { return fmtMS(r.RemoveMS) }))
		fmt.Fprintf(&sb, "| disk |%s\n", cols(func(r *Result) string { return fmtMiB(r.DiskBytes) }))
		fmt.Fprintf(&sb, "| peak RSS delta |%s\n", cols(func(r *Result) string {
			if r.PeakRSSDeltaKB == 0 {
				return "n/a"
			}
			return fmtMiB(r.PeakRSSDeltaKB << 10)
		}))

		var opNames []string
		seen := map[string]bool{}
		for _, res := range sz.Results {
			for n := range res.Latency {
				if !seen[n] {
					seen[n] = true
					opNames = append(opNames, n)
				}
			}
		}
		sort.Slice(opNames, func(i, j int) bool { return opOrder(opNames[i]) < opOrder(opNames[j]) })
		fmt.Fprintf(&sb, "\n**Query latency p50 / p95**\n\n%s\n%s\n", header, sep)
		for _, n := range opNames {
			fmt.Fprintf(&sb, "| %s |%s\n", n, cols(func(r *Result) string {
				l, ok := r.Latency[n]
				if !ok {
					return "-"
				}
				if n == "stats" {
					return fmtUS(l.P50us)
				}
				return fmtUS(l.P50us) + " / " + fmtUS(l.P95us)
			}))
		}

		var checks []string
		seen = map[string]bool{}
		for _, res := range sz.Results {
			for n := range res.Compared {
				if !seen[n] {
					seen[n] = true
					checks = append(checks, n)
				}
			}
		}
		sort.Slice(checks, func(i, j int) bool { return opOrder(checks[i]) < opOrder(checks[j]) })
		fmt.Fprintf(&sb, "\n**Parity vs %s** (mismatches / compared; skipped = search results at the cap)\n\n%s\n%s\n", Reference, header, sep)
		for _, n := range checks {
			fmt.Fprintf(&sb, "| %s |%s\n", n, cols(func(r *Result) string {
				if r.Backend == Reference {
					return "reference"
				}
				s := fmt.Sprintf("%d / %d", r.Mismatches[n], r.Compared[n])
				if k := r.Skipped[n]; k > 0 {
					s += fmt.Sprintf(" (%d skipped)", k)
				}
				return s
			}))
		}
		for _, res := range sz.Results {
			if res.Error != "" {
				fmt.Fprintf(&sb, "\n**%s failed:** %s\n", res.Backend, res.Error)
			}
			if len(res.Examples) > 0 {
				fmt.Fprintf(&sb, "\n%s mismatch examples:\n\n", res.Backend)
				for _, e := range res.Examples {
					fmt.Fprintf(&sb, "- %s\n", e)
				}
			}
			if len(res.Caveats) > 0 {
				fmt.Fprintf(&sb, "\n%s caveats:\n\n", res.Backend)
				for _, c := range res.Caveats {
					fmt.Fprintf(&sb, "- %s\n", c)
				}
			}
		}
	}
	return sb.String()
}

var opRank = map[string]int{
	"callers d1": 1, "callers d3": 2, "callees d1": 3, "callees d3": 4, "impact d1": 5, "impact d3": 6,
	"search": 7, "path": 8, "stats": 9, "update": 10, "remove": 11,
}

func opOrder(n string) int { return opRank[n] }

// Failed reports whether any candidate disagreed with the reference or failed.
func (r *Report) Failed() bool {
	for _, sz := range r.Sizes {
		for _, res := range sz.Results {
			if res.TotalMismatches() > 0 {
				return true
			}
		}
	}
	return false
}

// Write stores the report as <dir>/graphdb-<timestamp>.json and .md and
// returns the paths.
func (r *Report) Write(dir string) (jsonPath, mdPath string, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("creating results dir: %w", err)
	}
	t, perr := time.Parse(time.RFC3339, r.Time)
	if perr != nil {
		t = time.Now()
	}
	base := filepath.Join(dir, "graphdb-"+t.Format("20060102T150405"))
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("encoding report: %w", err)
	}
	if err := os.WriteFile(base+".json", append(b, '\n'), 0o644); err != nil {
		return "", "", fmt.Errorf("writing report: %w", err)
	}
	if err := os.WriteFile(base+".md", []byte(r.Markdown()), 0o644); err != nil {
		return "", "", fmt.Errorf("writing report: %w", err)
	}
	return base + ".json", base + ".md", nil
}
