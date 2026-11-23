package vectordb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

func fmtQPS(qps float64) string {
	if qps >= 1000 {
		return fmt.Sprintf("%.0f", qps)
	}
	return fmt.Sprintf("%.1f", qps)
}

// SweepPoint is the measurement of search at one ef setting.
type SweepPoint struct {
	EfSearch int     `json:"ef_search"`
	Recall   float64 `json:"recall"`
	P50us    int64   `json:"p50_us"`
	P95us    int64   `json:"p95_us"`
	P99us    int64   `json:"p99_us"`
	QPSC1    float64 `json:"qps_c1"`
	QPSC8    float64 `json:"qps_c8"`
}

// RecallMatchedRow holds search metrics at the cheapest ef achieving target recall.
type RecallMatchedRow struct {
	TargetRecall   float64 `json:"target_recall"`
	AchievedRecall float64 `json:"achieved_recall"`
	EfSearch       int     `json:"ef_search"`
	P50us          int64   `json:"p50_us"`
	P95us          int64   `json:"p95_us"`
	P99us          int64   `json:"p99_us"`
	QPSC1          float64 `json:"qps_c1"`
	QPSC8          float64 `json:"qps_c8"`
	Reached        bool    `json:"reached"`
}

// FilteredRow holds latency and recall metrics for filtered queries.
type FilteredRow struct {
	Selectivity string  `json:"selectivity"`
	Recall      float64 `json:"recall"`
	P50us       int64   `json:"p50_us"`
	P95us       int64   `json:"p95_us"`
}

// StoreResult is the complete measurement of one vector store at one size.
type StoreResult struct {
	Store          string                      `json:"store"`
	LoadMS         float64                     `json:"load_ms"`
	ColdStartMS    float64                     `json:"cold_start_ms"`
	FileUpdateMS   float64                     `json:"file_update_ms"`
	DiskBytes      int64                       `json:"disk_bytes"`
	PeakRSSDeltaKB int64                       `json:"peak_rss_delta_kb"`
	ServerRSSKB    int64                       `json:"server_rss_kb"`
	Caveats        []string                    `json:"caveats,omitempty"`
	Sweep          []SweepPoint                `json:"sweep"`
	RecallMatched  map[string]RecallMatchedRow `json:"recall_matched"`
	Filtered       map[string]FilteredRow      `json:"filtered"`
	Error          string                      `json:"error,omitempty"`
}

// SizeRun groups results across stores for a given vector count.
type SizeRun struct {
	Size    int `json:"size"`
	Dims    int `json:"dims"`
	Queries int `json:"queries"`
	K       int `json:"k"`
	// DupFraction is the share of near-copy vectors (0 = plain dataset).
	DupFraction float64 `json:"dup_fraction,omitempty"`
	// Source names the real-vector file the run used ("" = synthetic).
	Source  string         `json:"source,omitempty"`
	Results []*StoreResult `json:"results"`
}

// Report records the entire benchmark run.
type Report struct {
	Time    string    `json:"time"`
	Go      string    `json:"go"`
	Platf   string    `json:"platform"`
	CPUs    int       `json:"cpus"`
	Command string    `json:"command"`
	Dims    int       `json:"dims"`
	K       int       `json:"k"`
	Queries int       `json:"queries"`
	Sizes   []SizeRun `json:"sizes"`
}

// Failed reports whether any store errored or had a critical recall failure.
func (r *Report) Failed() bool {
	for _, sz := range r.Sizes {
		for _, res := range sz.Results {
			if res.Error != "" {
				return true
			}
			// Search must reach at least 0.90 recall at its maximum ef point
			var maxRecall float64
			for _, sp := range res.Sweep {
				if sp.Recall > maxRecall {
					maxRecall = sp.Recall
				}
			}
			if maxRecall < 0.90 && len(res.Sweep) > 0 {
				return true
			}
		}
	}
	return false
}

// Markdown formats the benchmark report as Markdown tables.
func (r *Report) Markdown() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Vector database benchmark — %s\n\n", r.Time)
	fmt.Fprintf(&sb, "go %s %s, %d CPUs visible; %d queries, k=%d, dims=%d.\n", r.Go, r.Platf, r.CPUs, r.Queries, r.K, r.Dims)
	if r.Command != "" {
		fmt.Fprintf(&sb, "\n`%s`\n", r.Command)
	}

	for _, sz := range r.Sizes {
		dup := ""
		if sz.DupFraction > 0 {
			dup = fmt.Sprintf(" · %.0f%% near-copies", sz.DupFraction*100)
		}
		if sz.Source != "" {
			dup += " · real vectors (" + sz.Source + ")"
		}
		fmt.Fprintf(&sb, "\n## %d vectors · %d dims · %d queries · k=%d%s\n", sz.Size, sz.Dims, sz.Queries, sz.K, dup)

		cols := func(cell func(*StoreResult) string) string {
			var s strings.Builder
			for _, res := range sz.Results {
				s.WriteString(" " + cell(res) + " |")
			}
			return s.String()
		}

		header := "| |" + cols(func(sr *StoreResult) string { return sr.Store })
		sep := "|---|" + strings.Repeat("---:|", len(sz.Results))

		// Summary Phase Table
		fmt.Fprintf(&sb, "\n**Phases & Footprint**\n\n%s\n%s\n", header, sep)
		fmt.Fprintf(&sb, "| load (bulk) |%s\n", cols(func(sr *StoreResult) string {
			if sr.Error != "" {
				return "err"
			}
			return fmtMS(sr.LoadMS)
		}))
		fmt.Fprintf(&sb, "| cold start (first query) |%s\n", cols(func(sr *StoreResult) string {
			if sr.Error != "" {
				return "err"
			}
			return fmtMS(sr.ColdStartMS)
		}))
		fmt.Fprintf(&sb, "| update one file (~24 passages) |%s\n", cols(func(sr *StoreResult) string {
			if sr.Error != "" {
				return "err"
			}
			return fmtMS(sr.FileUpdateMS)
		}))
		fmt.Fprintf(&sb, "| disk bytes |%s\n", cols(func(sr *StoreResult) string {
			if sr.Error != "" {
				return "err"
			}
			return fmtMiB(sr.DiskBytes)
		}))
		fmt.Fprintf(&sb, "| peak RSS delta |%s\n", cols(func(sr *StoreResult) string {
			if sr.PeakRSSDeltaKB == 0 {
				return "n/a"
			}
			return fmtMiB(sr.PeakRSSDeltaKB << 10)
		}))
		if hasServerRSS(sz.Results) {
			fmt.Fprintf(&sb, "| server RSS (cgroup) |%s\n", cols(func(sr *StoreResult) string {
				if sr.ServerRSSKB == 0 {
					return "-"
				}
				return fmtMiB(sr.ServerRSSKB << 10)
			}))
		}

		// Recall-Matched Search Rows (RFC 4.4: >=0.90, >=0.95, >=0.99)
		fmt.Fprintf(&sb, "\n**Search latency & throughput matched by recall@10**\n\n")
		fmt.Fprintf(&sb, "Cheapest ef setting that achieves recall@10 ≥ threshold (concurrency 1: p50/p95, QPS; concurrency 8: QPS):\n\n")
		fmt.Fprintf(&sb, "%s\n%s\n", header, sep)
		for _, target := range []string{"0.90", "0.95", "0.99"} {
			fmt.Fprintf(&sb, "| recall ≥ %s |%s\n", target, cols(func(sr *StoreResult) string {
				rm, ok := sr.RecallMatched[target]
				if !ok || !rm.Reached {
					return "-"
				}
				return fmt.Sprintf("ef=%d (%.3f) %s/%s, %s qps (c8: %s)",
					rm.EfSearch, rm.AchievedRecall, fmtUS(rm.P50us), fmtUS(rm.P95us), fmtQPS(rm.QPSC1), fmtQPS(rm.QPSC8))
			}))
		}

		// Filtered Search Table
		fmt.Fprintf(&sb, "\n**Filtered search (k=10, p50 / p95 latency, recall@10 vs exact filtered truth)**\n\n")
		fmt.Fprintf(&sb, "%s\n%s\n", header, sep)
		for _, sel := range []string{"1%", "10%", "50%"} {
			fmt.Fprintf(&sb, "| selectivity %s |%s\n", sel, cols(func(sr *StoreResult) string {
				fr, ok := sr.Filtered[sel]
				if !ok {
					return "-"
				}
				return fmt.Sprintf("%s / %s (rec: %.3f)", fmtUS(fr.P50us), fmtUS(fr.P95us), fr.Recall)
			}))
		}

		// ef sweep curve details
		fmt.Fprintf(&sb, "\n**ef search sweep detail**\n\n")
		sweepHeader := "| Store | ef | recall@10 | p50 | p95 | p99 | QPS (c=1) | QPS (c=8) |\n|---|---:|---:|---:|---:|---:|---:|---:|"
		fmt.Fprintf(&sb, "%s\n", sweepHeader)
		for _, sr := range sz.Results {
			for _, sp := range sr.Sweep {
				fmt.Fprintf(&sb, "| %s | %d | %.3f | %s | %s | %s | %s | %s |\n",
					sr.Store, sp.EfSearch, sp.Recall, fmtUS(sp.P50us), fmtUS(sp.P95us), fmtUS(sp.P99us), fmtQPS(sp.QPSC1), fmtQPS(sp.QPSC8))
			}
		}

		// Caveats
		var caveatsList []string
		for _, sr := range sz.Results {
			if len(sr.Caveats) > 0 {
				caveatsList = append(caveatsList, fmt.Sprintf("**%s caveats:**\n- %s", sr.Store, strings.Join(sr.Caveats, "\n- ")))
			}
		}
		if len(caveatsList) > 0 {
			fmt.Fprintf(&sb, "\n### Caveats & Differences\n\n%s\n", strings.Join(caveatsList, "\n\n"))
		}
	}

	return sb.String()
}

func hasServerRSS(results []*StoreResult) bool {
	for _, r := range results {
		if r.ServerRSSKB > 0 {
			return true
		}
	}
	return false
}

// JSON encodes the report to indented JSON.
func (r *Report) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// Write writes the JSON and Markdown reports to dir.
func (r *Report) Write(dir string) (string, string, error) {
	if dir == "" {
		dir = "results"
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", "", fmt.Errorf("creating results dir %s: %w", dir, err)
	}

	ts := time.Now().Format("20060102-150405")
	jsonPath := filepath.Join(dir, fmt.Sprintf("vectordb-%s.json", ts))
	mdPath := filepath.Join(dir, fmt.Sprintf("vectordb-%s.md", ts))

	data, err := r.JSON()
	if err != nil {
		return "", "", fmt.Errorf("marshaling report JSON: %w", err)
	}
	if err := os.WriteFile(jsonPath, data, 0644); err != nil {
		return "", "", fmt.Errorf("writing report JSON: %w", err)
	}
	if err := os.WriteFile(mdPath, []byte(r.Markdown()), 0644); err != nil {
		return "", "", fmt.Errorf("writing report Markdown: %w", err)
	}
	return jsonPath, mdPath, nil
}
