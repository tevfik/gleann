// Package quality holds the retrieval-quality regression gate.
//
// It indexes a pinned snapshot of gleann's own source and docs
// (testdata/corpus) through the real indexing pipeline, runs the benchmark
// tasks against BM25, dense and hybrid search, and compares file-level
// R@1, R@10 and MRR with testdata/baseline.json. See testdata/README.md.
package quality
