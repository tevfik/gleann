/*
 * faiss_io.c — in-memory I/O helpers for FAISS C API.
 *
 * Uses POSIX open_memstream / fmemopen to serialize/deserialize
 * FAISS indexes entirely in memory, eliminating all disk I/O.
 *
 * Go side calls:
 *   gleann_faiss_write_buf() → serialise index → heap buffer (caller frees)
 *   gleann_faiss_read_buf()  → deserialise index ← byte slice
 */

#include <stdlib.h>
#include <string.h>
#include "faiss_io.h"

/* ─── write: index → heap buffer ─── */

int gleann_faiss_write_buf(FaissIndex *idx, uint8_t **out_buf, size_t *out_size) {
    char   *mem    = NULL;
    size_t  memlen = 0;

    /* open_memstream grows the buffer dynamically; no fixed allocation needed. */
    FILE *mf = open_memstream(&mem, &memlen);
    if (!mf) return -1;

    int rc = faiss_write_index(idx, mf);
    fclose(mf); /* flush + finalise mem/memlen */

    if (rc != 0) {
        free(mem);
        return rc;
    }

    *out_buf  = (uint8_t *)mem; /* caller must free() */
    *out_size = memlen;
    return 0;
}

/* ─── read: byte buffer → index ─── */

int gleann_faiss_read_buf(const uint8_t *buf, size_t size, FaissIndex **out_idx) {
    /* fmemopen wraps the caller's buffer; zero-copy on the read side. */
    FILE *mf = fmemopen((void *)buf, size, "rb");
    if (!mf) return -1;
    int rc = faiss_read_index(mf, 0, out_idx);
    fclose(mf);
    return rc;
}
