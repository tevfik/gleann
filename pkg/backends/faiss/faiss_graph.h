#ifndef GLEANN_FAISS_GRAPH_H
#define GLEANN_FAISS_GRAPH_H

/*
 * gleann FAISS graph extraction — C header.
 *
 * Provides a C-linkage function that extracts the HNSW topology
 * (neighbors, offsets, node levels) from a FAISS IndexHNSW.
 * The implementation is in faiss_graph.cpp (C++).
 */

#include <stdint.h>
#include <stddef.h>

/* Forward-declare the opaque FAISS index type (matches faiss/c_api/Index_c.h). */
typedef struct FaissIndex_H FaissIndex;

/* Raw HNSW graph topology extracted from FAISS. */
typedef struct {
    /* Flat neighbor array — widened to int64 from FAISS storage_idx_t (int32).
       Use offsets + cum_nneighbor to locate a node's neighbors at a given level.
       Empty slots are -1. */
    int64_t *neighbors;
    int64_t  neighbors_len;

    /* Per-node offset into the neighbors array (n+1 elements).
       offsets[i] = start of node i's neighbor block. */
    int64_t *offsets;
    int64_t  offsets_len;   /* = num_nodes + 1 */

    /* Level assigned to each node (n elements). */
    int32_t *levels;
    int64_t  levels_len;    /* = num_nodes */

    /* Cumulative neighbor slots per level (max_level+2 elements).
       cum_nneighbor[l] = start offset within a node's block for level l.
       cum_nneighbor[l+1] - cum_nneighbor[l] = neighbor capacity at level l. */
    int32_t *cum_nneighbor;
    int32_t  cum_nneighbor_len;

    int32_t  max_level;
    int64_t  entry_point;
} GleannFAISSRawHNSW;

#ifdef __cplusplus
extern "C" {
#endif

/*
 * Extract the HNSW graph topology from a FAISS index (must be IndexHNSW*).
 * All arrays are heap-allocated and must be freed with gleann_faiss_free_graph().
 * Returns 0 on success, -1 if the index is not an HNSW type.
 */
int gleann_faiss_extract_graph(FaissIndex *index, GleannFAISSRawHNSW *out);

/* Free resources allocated by gleann_faiss_extract_graph. */
void gleann_faiss_free_graph(GleannFAISSRawHNSW *g);

#ifdef __cplusplus
}
#endif

#endif /* GLEANN_FAISS_GRAPH_H */
