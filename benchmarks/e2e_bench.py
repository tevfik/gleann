#!/usr/bin/env python3
"""
e2e_bench.py — Gleann vs Python LEANN end-to-end benchmark

Karşılaştır:
  1. Embedding (Ollama, aynı model, aynı batch/chunk sayısı)
  2. HNSW/FAISS index build
  3. Search latency

Gereksinimler:
  pip install requests numpy faiss-cpu (opsiyonel)

Kullanım:
  python3 benchmarks/e2e_bench.py --model bge-m3 --n 1000 --ollama http://localhost:11434
  python3 benchmarks/e2e_bench.py --gleann-bin ./gleann-full --docs /path/to/dir
"""

import argparse
import json
import os
import subprocess
import sys
import time
import tempfile
import shutil
import random
import string

import requests

# ─── Yardımcı Fonksiyonlar ────────────────────────────────────────────────────

def random_texts(n: int, words_per_chunk: int = 80, seed: int = 42) -> list[str]:
    """Rastgele lorem-ipsum benzeri metin üret."""
    random.seed(seed)
    vocab = ("algorithm vector graph embedding neural search index query "
             "database network model training inference retrieval chunk passage "
             "document similarity distance metric ranking relevance score").split()
    texts = []
    for _ in range(n):
        texts.append(" ".join(random.choices(vocab, k=words_per_chunk)))
    return texts

def measure_ollama_embed(texts: list[str], model: str, base_url: str,
                          batch_size: int = 256) -> dict:
    """Python'dan Ollama embedding ölçümü."""
    url = f"{base_url}/api/embed"
    results = []
    total_tokens = 0

    start = time.perf_counter()
    for i in range(0, len(texts), batch_size):
        batch = texts[i:i+batch_size]
        payload = {"model": model, "input": batch}
        resp = requests.post(url, json=payload, timeout=600)
        resp.raise_for_status()
        data = resp.json()
        results.extend(data.get("embeddings", []))
        total_tokens += data.get("prompt_eval_count", len(batch) * 80)
    elapsed = time.perf_counter() - start

    dim = len(results[0]) if results else 0
    return {
        "n": len(texts),
        "dim": dim,
        "elapsed_s": elapsed,
        "throughput_texts_per_s": len(texts) / elapsed,
        "ms_per_text": elapsed / len(texts) * 1000,
    }

def phase(label: str):
    print(f"\n{'─'*55}")
    print(f"  {label}")
    print(f"{'─'*55}")

# ─── Gleann CLI benchmark ──────────────────────────────────────────────────────

def bench_gleann_build(gleann_bin: str, docs_dir: str, index_dir: str,
                        model: str, batch_size: int, concurrency: int,
                        index_name: str = "bench_e2e") -> dict:
    """gleann build komutunu zaman ölçümüyle çalıştır."""
    cmd = [
        gleann_bin, "build", index_name,
        "--docs", docs_dir,
        "--index-dir", index_dir,
        "--model", model,
        "--batch-size", str(batch_size),
        "--concurrency", str(concurrency),
    ]
    t0 = time.perf_counter()
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=3600)
    elapsed = time.perf_counter() - t0

    if result.returncode != 0:
        print(f"  STDERR: {result.stderr[:500]}")
        return {"error": result.stderr[:200], "elapsed_s": elapsed}

    lines = result.stdout.strip().split("\n")
    passages = 0
    for line in lines:
        if "passages" in line:
            # "✅ Index "bench_e2e" built: 1234 passages in 45.2s"
            try:
                passages = int(line.split("passages")[0].split()[-1])
            except Exception:
                pass

    return {
        "elapsed_s": elapsed,
        "passages": passages,
        "stdout_tail": lines[-1] if lines else "",
    }

def bench_gleann_search(gleann_bin: str, index_dir: str, model: str,
                         index_name: str = "bench_e2e",
                         n_queries: int = 20) -> dict:
    """n_queries arama yaparak ortalama latency ölç."""
    queries = [
        "vector similarity search algorithm",
        "neural network embedding model",
        "graph traversal index structure",
        "semantic similarity retrieval",
        "approximate nearest neighbor search",
    ]
    times = []
    for i in range(n_queries):
        q = queries[i % len(queries)]
        cmd = [gleann_bin, "search", index_name, q,
               "--index-dir", index_dir, "--model", model, "--top-k", "10"]
        t0 = time.perf_counter()
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=60)
        elapsed = time.perf_counter() - t0
        if r.returncode == 0:
            times.append(elapsed)

    if not times:
        return {"error": "no successful queries"}

    return {
        "n_queries": len(times),
        "avg_latency_ms": sum(times)/len(times)*1000,
        "min_latency_ms": min(times)*1000,
        "max_latency_ms": max(times)*1000,
        "qps": len(times) / sum(times),
    }

# ─── Synthetic doc generator ──────────────────────────────────────────────────

def generate_synthetic_docs(target_dir: str, n_files: int = 50,
                              chunks_per_file: int = 20) -> int:
    """Test için rastgele kaynak dosyaları oluştur."""
    os.makedirs(target_dir, exist_ok=True)
    vocab = ("algorithm vector graph embedding neural search index query "
             "database network model training inference retrieval chunk passage").split()
    total_chunks = 0
    for i in range(n_files):
        path = os.path.join(target_dir, f"doc_{i:04d}.txt")
        with open(path, "w") as f:
            for _ in range(chunks_per_file):
                sentence = " ".join(random.choices(vocab, k=random.randint(30, 100)))
                f.write(sentence + "\n\n")
        total_chunks += chunks_per_file
    return n_files * chunks_per_file

# ─── Ana Karşılaştırma ────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(description="Gleann e2e benchmark")
    parser.add_argument("--model", default="bge-m3", help="Ollama model adı")
    parser.add_argument("--ollama", default="http://localhost:11434", help="Ollama base URL")
    parser.add_argument("--gleann-bin", default="./gleann-full", help="Gleann binary yolu")
    parser.add_argument("--docs", default=None, help="Gerçek docs dizini (opsiyonel)")
    parser.add_argument("--n-files", type=int, default=50, help="Synthetic dosya sayısı")
    parser.add_argument("--batch-size", type=int, default=256, help="Embedding batch size")
    parser.add_argument("--concurrency", type=int, default=4, help="Embedding concurrency")
    parser.add_argument("--n-embed", type=int, default=500,
                        help="Doğrudan embedding ölçümü için chunk sayısı")
    parser.add_argument("--skip-build", action="store_true", help="Index build'i atla")
    parser.add_argument("--output", default="/tmp/gleann_bench.json", help="JSON çıktı dosyası")
    args = parser.parse_args()

    all_results = {"config": vars(args)}

    print("=" * 55)
    print(f"  Gleann End-to-End Benchmark")
    print(f"  Model: {args.model}  Batch: {args.batch_size}  Concurrency: {args.concurrency}")
    print("=" * 55)

    # ── 1. Ollama bağlantı kontrolü ──────────────────────────────────────────
    phase("1. Ollama Bağlantı Kontrolü")
    try:
        r = requests.get(f"{args.ollama}/api/tags", timeout=5)
        models = [m["name"] for m in r.json().get("models", [])]
        model_loaded = any(args.model in m for m in models)
        print(f"  ✅ Ollama aktif. Model yüklü: {model_loaded}")
        if not model_loaded:
            print(f"  ⚠️  Model {args.model!r} yüklü değil. Pull edin:")
            print(f"      ollama pull {args.model}")
    except Exception as e:
        print(f"  ❌ Ollama bağlanamıyor: {e}")
        sys.exit(1)

    # ── 2. Doğrudan embedding hız testi (Python → Ollama) ──────────────────
    phase(f"2. Python→Ollama Embedding Hızı ({args.n_embed} chunks)")
    texts = random_texts(args.n_embed)
    print(f"  Ölçülüyor (batch_size={args.batch_size})... ", end="", flush=True)
    try:
        embed_result = measure_ollama_embed(texts, args.model, args.ollama, args.batch_size)
        print(f"✅")
        print(f"  Toplam süre:  {embed_result['elapsed_s']:.1f}s")
        print(f"  Throughput:   {embed_result['throughput_texts_per_s']:.1f} chunks/s")
        print(f"  Ortalama:     {embed_result['ms_per_text']:.1f} ms/chunk")
        print(f"  Vektör boyutu: {embed_result['dim']}")
        all_results["python_embed_direct"] = embed_result
    except Exception as e:
        print(f"\n  ❌ Hata: {e}")
        all_results["python_embed_direct"] = {"error": str(e)}

    # Bu, Ollama'nın maksimum teorik hızı.
    # Gleann'in build süresi bu sayıdan daha uzunsa overhead var demektir.

    # ── 3. Gleann index build ────────────────────────────────────────────────
    if not args.skip_build:
        phase("3. Gleann Index Build")

        tmpdir = tempfile.mkdtemp(prefix="gleann_bench_")
        index_dir = os.path.join(tmpdir, "indexes")
        docs_dir = args.docs

        if docs_dir is None:
            docs_dir = os.path.join(tmpdir, "docs")
            n = generate_synthetic_docs(docs_dir, args.n_files, chunks_per_file=20)
            print(f"  Synthetic docs: {args.n_files} dosya oluşturuldu (~{n} chunk)")
        else:
            print(f"  Gerçek docs: {docs_dir}")

        if not os.path.exists(args.gleann_bin):
            print(f"  ⚠️  Binary bulunamadı: {args.gleann_bin}")
        else:
            print(f"  Binary: {args.gleann_bin}")
            print(f"  Index dir: {index_dir}")
            print(f"  Build başlıyor... ", end="", flush=True)

            build_result = bench_gleann_build(
                args.gleann_bin, docs_dir, index_dir,
                args.model, args.batch_size, args.concurrency)

            print("✅" if "error" not in build_result else "❌")
            if "error" in build_result:
                print(f"  Hata: {build_result['error']}")
            else:
                elapsed = build_result["elapsed_s"]
                passages = build_result.get("passages", "?")
                print(f"  Geçen süre:  {elapsed:.1f}s")
                print(f"  Passage'lar: {passages}")
                if passages and isinstance(passages, int) and passages > 0:
                    print(f"  Throughput:  {passages/elapsed:.1f} passages/s")

                # Embedding overhead hesapla
                if "python_embed_direct" in all_results and "dim" in all_results["python_embed_direct"]:
                    raw_embed_speed = all_results["python_embed_direct"]["throughput_texts_per_s"]
                    if isinstance(passages, int):
                        theoretical_min = passages / raw_embed_speed
                        overhead_pct = (elapsed - theoretical_min) / theoretical_min * 100
                        print(f"\n  Teorik minimum (sadece embedding): {theoretical_min:.1f}s")
                        print(f"  Gerçek süre:                       {elapsed:.1f}s")
                        print(f"  Overhead (chunking+index build):   {overhead_pct:.1f}%")

            all_results["gleann_build"] = build_result

            # ── 4. Arama latency ──────────────────────────────────────────────
            if "error" not in build_result:
                phase("4. Gleann Search Latency (20 sorgu)")
                print("  Ölçülüyor... ", end="", flush=True)
                search_result = bench_gleann_search(
                    args.gleann_bin, index_dir, args.model, n_queries=20)
                if "error" in search_result:
                    print(f"❌ {search_result['error']}")
                else:
                    print("✅")
                    print(f"  Ortalama latency: {search_result['avg_latency_ms']:.0f}ms")
                    print(f"  Min/Max:          {search_result['min_latency_ms']:.0f}ms / {search_result['max_latency_ms']:.0f}ms")
                    print(f"  QPS:              {search_result['qps']:.2f}")
                all_results["gleann_search"] = search_result

        try:
            shutil.rmtree(tmpdir)
        except Exception:
            pass

    # ── 5. Özet ──────────────────────────────────────────────────────────────
    phase("5. Özet")
    if "python_embed_direct" in all_results and "throughput_texts_per_s" in all_results["python_embed_direct"]:
        t = all_results["python_embed_direct"]["throughput_texts_per_s"]
        ms = all_results["python_embed_direct"]["ms_per_text"]
        print(f"  Ollama embedding (Python doğrudan): {t:.1f} chunk/s  ({ms:.0f}ms/chunk)")
    if "gleann_build" in all_results and "error" not in all_results["gleann_build"]:
        b = all_results["gleann_build"]
        elapsed = b["elapsed_s"]
        passages = b.get("passages", 0)
        if passages:
            print(f"  Gleann build throughput:            {passages/elapsed:.1f} chunk/s")
    if "gleann_search" in all_results and "avg_latency_ms" in all_results["gleann_search"]:
        s = all_results["gleann_search"]
        print(f"  Sorgu latency:                      {s['avg_latency_ms']:.0f}ms avg  ({s['qps']:.2f} QPS)")

    print(f"\n  💡 Gleann build hızı ≈ Ollama embedding hızı olmalı.")
    print(f"     Büyük fark varsa: chunk/index overhead veya")
    print(f"     düşük batch-size nedeniyle GPU boşta kalıyor olabilir.")

    # JSON kaydet
    with open(args.output, "w") as f:
        json.dump(all_results, f, indent=2, default=str)
    print(f"\n  📊 Sonuçlar: {args.output}")
    print()

if __name__ == "__main__":
    main()
