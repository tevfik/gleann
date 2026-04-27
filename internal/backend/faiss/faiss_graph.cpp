/*
 * gleann FAISS graph extraction — C++ implementation.
 *
 * Extracts the HNSW graph topology from a FAISS IndexHNSW by
 * accessing internal C++ data structures (neighbors, offsets, levels).
 * Compiled by CGo's C++ compiler alongside the Go package.
 */

// Use local include path for C++ headers (they may not be in /usr/local/include).
// The CGo CXXFLAGS in extract.go adds -I${SRCDIR}/include.
#include <faiss/IndexHNSW.h>

#include <cstdlib>
#include <cstring>

extern "C" {
#include "faiss_graph.h"
}

int gleann_faiss_extract_graph(FaissIndex *index, GleannFAISSRawHNSW *out) {
    if (!index || !out) return -1;

    std::memset(out, 0, sizeof(*out));

    /* Cast the opaque C handle to the underlying C++ object.
       FAISS C API stores faiss::Index* behind FaissIndex*. */
    auto *base = reinterpret_cast<faiss::Index *>(index);
    auto *idx  = dynamic_cast<faiss::IndexHNSW *>(base);
    if (!idx) return -1;   /* Not an HNSW index */

    const auto &hnsw = idx->hnsw;

    /* ── Neighbors (storage_idx_t → int64) ────────────────────────── */
    out->neighbors_len = static_cast<int64_t>(hnsw.neighbors.size());
    out->neighbors = static_cast<int64_t *>(
        std::malloc(static_cast<size_t>(out->neighbors_len) * sizeof(int64_t)));
    if (!out->neighbors) goto fail;
    for (int64_t i = 0; i < out->neighbors_len; i++) {
        out->neighbors[i] = static_cast<int64_t>(hnsw.neighbors[i]);
    }

    /* ── Offsets (size_t → int64) ─────────────────────────────────── */
    out->offsets_len = static_cast<int64_t>(hnsw.offsets.size());
    out->offsets = static_cast<int64_t *>(
        std::malloc(static_cast<size_t>(out->offsets_len) * sizeof(int64_t)));
    if (!out->offsets) goto fail;
    for (int64_t i = 0; i < out->offsets_len; i++) {
        out->offsets[i] = static_cast<int64_t>(hnsw.offsets[i]);
    }

    /* ── Levels (int → int32) ─────────────────────────────────────── */
    out->levels_len = static_cast<int64_t>(hnsw.levels.size());
    out->levels = static_cast<int32_t *>(
        std::malloc(static_cast<size_t>(out->levels_len) * sizeof(int32_t)));
    if (!out->levels) goto fail;
    for (int64_t i = 0; i < out->levels_len; i++) {
        out->levels[i] = static_cast<int32_t>(hnsw.levels[i]);
    }

    /* ── Cumulative neighbor counts per level ─────────────────────── */
    out->cum_nneighbor_len = static_cast<int32_t>(hnsw.cum_nneighbor_per_level.size());
    out->cum_nneighbor = static_cast<int32_t *>(
        std::malloc(static_cast<size_t>(out->cum_nneighbor_len) * sizeof(int32_t)));
    if (!out->cum_nneighbor) goto fail;
    for (int32_t i = 0; i < out->cum_nneighbor_len; i++) {
        out->cum_nneighbor[i] = static_cast<int32_t>(hnsw.cum_nneighbor_per_level[i]);
    }

    /* ── Scalars ──────────────────────────────────────────────────── */
    out->max_level   = static_cast<int32_t>(hnsw.max_level);
    out->entry_point = static_cast<int64_t>(hnsw.entry_point);

    return 0;

fail:
    gleann_faiss_free_graph(out);
    return -1;
}

void gleann_faiss_free_graph(GleannFAISSRawHNSW *g) {
    if (!g) return;
    std::free(g->neighbors);
    std::free(g->offsets);
    std::free(g->levels);
    std::free(g->cum_nneighbor);
    std::memset(g, 0, sizeof(*g));
}
