#!/usr/bin/env python3
"""
Fair comparison benchmark: Python (FAISS HNSW + NumPy) vs gleann-go (Pure Go HNSW)

Tests identical operations:
1. HNSW Build (insert N vectors)
2. HNSW Search (k-NN query)
3. Brute-force search baseline
4. Memory usage
5. Startup time (import overhead)
"""

import json
import os
import sys
import time

import numpy as np
import psutil

# ── Helpers ──────────────────────────────────────────────────────────────────

def get_mem_mb():
    return psutil.Process().memory_info().rss / 1024 / 1024

def random_vectors(n, dim, seed=42):
    rng = np.random.default_rng(seed)
    vecs = rng.standard_normal((n, dim)).astype(np.float32)
    norms = np.linalg.norm(vecs, axis=1, keepdims=True)
    return vecs / norms

def brute_force_knn(query, vectors, k):
    dists = np.sum((vectors - query) ** 2, axis=1)
    idx = np.argpartition(dists, k)[:k]
    idx = idx[np.argsort(dists[idx])]
    return idx, dists[idx]

def recall_at_k(predicted, true_nn, k):
    true_set = set(true_nn[:k].tolist())
    pred_set = set(predicted[:k].tolist())
    return len(true_set & pred_set) / k

# ── Benchmark Routines ───────────────────────────────────────────────────────

def bench_faiss_hnsw(n, dim, num_queries=100, k=10, ef_search=128):
    import faiss

    results = {}
    rng = np.random.default_rng(42)
    vectors = random_vectors(n, dim)
    queries = random_vectors(num_queries, dim, seed=99)

    # Build
    mem_before = get_mem_mb()
    t0 = time.perf_counter()
    index = faiss.IndexHNSWFlat(dim, 32)        # M=32
    index.hnsw.efConstruction = 200
    index.add(vectors)
    build_time = time.perf_counter() - t0
    mem_after = get_mem_mb()
    results["build_time_s"] = build_time
    results["build_mem_mb"] = mem_after - mem_before

    # Search
    index.hnsw.efSearch = ef_search
    t0 = time.perf_counter()
    for _ in range(3):  # warmup
        D, I = index.search(queries, k)
    t0 = time.perf_counter()
    D, I = index.search(queries, k)
    search_time = time.perf_counter() - t0
    results["search_total_s"] = search_time
    results["search_per_query_us"] = search_time / num_queries * 1e6
    results["qps"] = num_queries / search_time

    # Recall
    total_recall = 0.0
    for q in range(num_queries):
        true_idx, _ = brute_force_knn(queries[q], vectors, k)
        total_recall += recall_at_k(I[q], true_idx, k)
    results["recall_at_10"] = total_recall / num_queries

    # Brute force baseline
    t0 = time.perf_counter()
    for q in range(num_queries):
        brute_force_knn(queries[q], vectors, k)
    brute_time = time.perf_counter() - t0
    results["brute_force_total_s"] = brute_time
    results["brute_force_per_query_us"] = brute_time / num_queries * 1e6

    results["speedup_vs_brute"] = brute_time / search_time

    return results

def bench_numpy_brute(n, dim, num_queries=100, k=10):
    vectors = random_vectors(n, dim)
    queries = random_vectors(num_queries, dim, seed=99)

    t0 = time.perf_counter()
    for q in range(num_queries):
        brute_force_knn(queries[q], vectors, k)
    elapsed = time.perf_counter() - t0

    return {
        "brute_total_s": elapsed,
        "brute_per_query_us": elapsed / num_queries * 1e6,
        "qps": num_queries / elapsed,
    }

# ── Import / Startup Overhead ────────────────────────────────────────────────

def bench_import_overhead():
    """Measure import time of heavy Python deps."""
    results = {}

    t0 = time.perf_counter()
    import numpy
    results["numpy_import_s"] = time.perf_counter() - t0

    t0 = time.perf_counter()
    import faiss
    results["faiss_import_s"] = time.perf_counter() - t0

    return results

# ── Main ─────────────────────────────────────────────────────────────────────

def main():
    print("=" * 65)
    print("  Python FAISS HNSW Benchmark")
    print(f"  NumPy {np.__version__}, CPU: {os.cpu_count()} cores")
    print(f"  PID: {os.getpid()}, Memory baseline: {get_mem_mb():.1f} MB")
    print("=" * 65)

    # Import overhead
    import_times = bench_import_overhead()
    print(f"\nImport overhead: numpy={import_times['numpy_import_s']*1000:.1f}ms "
          f"faiss={import_times['faiss_import_s']*1000:.1f}ms")

    all_results = {"import": import_times}

    configs = [
        (1000, 128),
        (5000, 128),
        (1000, 384),
        (5000, 384),
        (1000, 768),
        (5000, 768),
    ]

    for n, dim in configs:
        label = f"N={n}, dim={dim}"
        print(f"\n{'─' * 50}")
        print(f"  {label}")
        print(f"{'─' * 50}")

        res = bench_faiss_hnsw(n, dim)
        all_results[label] = res

        print(f"  Build:        {res['build_time_s']*1000:>8.1f} ms")
        print(f"  Search (100q): {res['search_per_query_us']:>7.0f} µs/q   ({res['qps']:.0f} QPS)")
        print(f"  Brute force:   {res['brute_force_per_query_us']:>7.0f} µs/q")
        print(f"  Speedup:       {res['speedup_vs_brute']:>7.1f}x")
        print(f"  Recall@10:     {res['recall_at_10']*100:>7.1f}%")
        print(f"  Build memory:  {res['build_mem_mb']:>7.1f} MB")

    # Save JSON
    with open("/tmp/python_bench_results.json", "w") as f:
        json.dump(all_results, f, indent=2)
    print(f"\nResults saved to /tmp/python_bench_results.json")

if __name__ == "__main__":
    main()
