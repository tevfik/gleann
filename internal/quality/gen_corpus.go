//go:build ignore

// gen_corpus.go rebuilds testdata/corpus.tar.gz and testdata/tasks.json from a pinned
// commit of the gleann repository. It needs only git and Go, no network.
//
// Run it from the repository root:
//
//	go run internal/quality/gen_corpus.go -commit 7521e58
//
// The corpus holds every file named as a gold file by bench/tasks/gleann.json
// and bench/tasks/gleann_nl.json at that commit, plus a deterministic sample of
// the other Go and Markdown files as distractors (every third path in sorted
// order, files over 40 KB skipped) until the corpus reaches the size budget.
// The archive is deterministic: sorted entries, zero mtime, fixed owner and mode.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxFile  = 40 << 10
	maxTotal = 2800 << 10
	maxFiles = 320
	outRoot  = "internal/quality/testdata"
)

var taskFiles = []string{"bench/tasks/gleann.json", "bench/tasks/gleann_nl.json"}

func git(args ...string) []byte {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "git", strings.Join(args, " "), ":", err)
		os.Exit(1)
	}
	return out
}

type task struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Query       string   `json:"query"`
	GoldFiles   []string `json:"gold_files"`
}

func main() {
	commit := flag.String("commit", "", "commit to snapshot (required)")
	flag.Parse()
	if *commit == "" {
		fmt.Fprintln(os.Stderr, "-commit is required")
		os.Exit(2)
	}
	full := strings.TrimSpace(string(git("rev-parse", *commit)))

	all := strings.Split(strings.TrimSpace(string(git("ls-tree", "-r", "--name-only", full))), "\n")
	sort.Strings(all)
	exists := map[string]bool{}
	for _, f := range all {
		exists[f] = true
	}

	var tasks []task
	for _, tf := range taskFiles {
		var ts []task
		if err := json.Unmarshal(git("show", full+":"+tf), &ts); err != nil {
			fmt.Fprintln(os.Stderr, tf, err)
			os.Exit(1)
		}
		for _, t := range ts {
			ok := true
			for _, g := range t.GoldFiles {
				ok = ok && exists[g]
			}
			if ok {
				tasks = append(tasks, t)
			}
		}
	}

	chosen := map[string]bool{}
	total := 0
	size := func(f string) int { return len(git("cat-file", "blob", full+":"+f)) }
	for _, t := range tasks {
		for _, g := range t.GoldFiles {
			if !chosen[g] {
				chosen[g] = true
				total += size(g)
			}
		}
	}
	var cands []string
	for _, f := range all {
		if chosen[f] || !(strings.HasSuffix(f, ".go") || strings.HasSuffix(f, ".md")) {
			continue
		}
		if strings.HasPrefix(f, "bench/") || strings.HasPrefix(f, "ext/") || strings.HasPrefix(f, "deps/") || strings.Contains(f, "/testdata/") {
			continue
		}
		cands = append(cands, f)
	}
	// Stride through the sorted candidates so every directory is sampled.
	for i := 0; i < len(cands) && len(chosen) < maxFiles; i += 3 {
		f := cands[i]
		n := size(f)
		if n > maxFile || total+n > maxTotal {
			continue
		}
		chosen[f] = true
		total += n
	}

	var names []string
	for f := range chosen {
		names = append(names, f)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		panic(err)
	}
	tw := tar.NewWriter(gz)
	for _, f := range names {
		blob := git("cat-file", "blob", full+":"+f)
		hdr := &tar.Header{Name: f, Mode: 0o644, Size: int64(len(blob)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}
		if err := tw.WriteHeader(hdr); err != nil {
			panic(err)
		}
		if _, err := tw.Write(blob); err != nil {
			panic(err)
		}
	}
	if err := tw.Close(); err != nil {
		panic(err)
	}
	if err := gz.Close(); err != nil {
		panic(err)
	}
	if err := os.MkdirAll(outRoot, 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(outRoot, "corpus.tar.gz"), buf.Bytes(), 0o644); err != nil {
		panic(err)
	}
	data, _ := json.MarshalIndent(tasks, "", "  ")
	if err := os.WriteFile(filepath.Join(outRoot, "tasks.json"), append(data, '\n'), 0o644); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(outRoot, "COMMIT"), []byte(full+"\n"), 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("commit %s: %d files, %d bytes, %d tasks\n", full, len(names), total, len(tasks))
}
