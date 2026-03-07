# FAISS Backend (Optional)

gleann includes an optional FAISS backend via CGo for significantly faster HNSW operations. The FAISS backend uses the same `BackendFactory` interface — just change `config.Backend = "faiss"`.

## Prerequisites

```bash
# Ubuntu/Debian
sudo apt-get install cmake g++ libopenblas-dev libomp-dev swig

# Build FAISS from source with C API
git clone --branch v1.13.2 --depth 1 https://github.com/facebookresearch/faiss.git /tmp/faiss-src
cd /tmp/faiss-src && mkdir build && cd build
cmake .. -DFAISS_ENABLE_C_API=ON -DFAISS_ENABLE_GPU=OFF \
         -DBUILD_TESTING=OFF -DFAISS_ENABLE_PYTHON=OFF \
         -DCMAKE_BUILD_TYPE=Release
make -j$(nproc) faiss faiss_c

# Install
sudo cp -r c_api/libfaiss_c.a faiss/libfaiss.a /usr/local/lib/
sudo cp -r c_api/libfaiss_c.so faiss/libfaiss.so /usr/local/lib/
sudo mkdir -p /usr/local/include/faiss/c_api/impl
sudo cp ../c_api/*.h /usr/local/include/faiss/c_api/
sudo cp ../c_api/impl/*.h /usr/local/include/faiss/c_api/impl/
```

> [!TIP]
> **Pre-compiled Binary (gleann-full)**
> If you download the `gleann-full` release `.tar.gz`, the shared libraries (`libfaiss_c.so`, `libfaiss.so`) are automatically bundled with the binary. Thanks to dynamic `$ORIGIN` linking, you just extract the archive and run `./gleann-full` immediately in place. No `sudo` or `/usr/local/lib` installation required!

## Building with FAISS

```bash
# Build with FAISS support
go build -tags faiss -o gleann ./cmd/gleann/

# Run tests including FAISS
go test -tags faiss ./internal/backend/faiss/ -v

# Run FAISS vs Pure Go comparison
go test -tags faiss -run TestFAISSvsPureGo -timeout 300s ./internal/backend/faiss/ -v

# Standard benchmarks
go test -tags faiss -bench=BenchmarkFAISS -benchmem ./internal/backend/faiss/
```

Without `-tags faiss`, the FAISS backend is excluded and gleann builds as pure Go with zero C dependencies.

## FAISS vs Pure Go Performance

All benchmarks on Intel i9-13900H (20 threads), Linux. Both backends use M=32, efSearch=128.

| Config | Metric | FAISS (CGo) | Pure Go | Speedup |
|--------|--------|-------------|---------|---------|
| **1K×64d** | Build | 17ms | 314ms | **18.4x** |
| | Search/query | 292µs | 284µs | ~1x |
| | QPS | 3,424 | 3,522 | |
| | Recall@10 | 100% | 100% | |
| **1K×128d** | Build | 20ms | 332ms | **16.9x** |
| | Search/query | 86µs | 356µs | **4.1x** |
| | QPS | 11,590 | 2,812 | |
| | Recall@10 | 100% | 100% | |
| **5K×128d** | Build | 107ms | 4.9s | **45.3x** |
| | Search/query | 227µs | 1.5ms | **6.5x** |
| | QPS | 4,400 | 678 | |
| | Recall@10 | 98.8% | 99.0% | |
| **5K×384d** | Build | 436ms | 9.4s | **21.5x** |
| | Search/query | 279µs | 2.5ms | **9.0x** |
| | QPS | 3,588 | 398 | |
| | Recall@10 | 96.8% | 98.2% | |

## When to Use Each Backend

| | Pure Go (`hnsw`) | FAISS (`faiss`) |
|---|---|---|
| **Best for** | Simplicity, portability | Maximum throughput |
| **Dependencies** | None | libfaiss, OpenBLAS, libomp |
| **Cross-compile** | Yes | No (needs C toolchain) |
| **Binary size** | ~2.5 MB | ~15 MB |
| **Build speed** | Instant | Requires FAISS from source |
| **SIMD** | No | AVX2/SSE (auto-detected) |
| **Recall@10** | 98.8% (ef=128) | Tunable via efSearch |
| **Vector removal** | Supported | Not supported (rebuild needed) |
