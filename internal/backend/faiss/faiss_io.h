#pragma once
/*
 * faiss_io.h — in-memory I/O helpers for FAISS C API.
 *
 * Exposed to the CGo layer in backend.go.
 */

#include <stdint.h>
#include <stddef.h>
#include <stdio.h>
#include <faiss/c_api/faiss_c.h>
#include <faiss/c_api/Index_c.h>
#include <faiss/c_api/index_io_c.h>
#include <faiss/c_api/impl/io_c.h>

#ifdef __cplusplus
extern "C" {
#endif

/*
 * Serialize a FAISS index into a heap-allocated buffer.
 * The caller must free(*out_buf) after use.
 * Returns 0 on success, non-zero on error.
 */
int gleann_faiss_write_buf(FaissIndex *idx, uint8_t **out_buf, size_t *out_size);

/*
 * Deserialize a FAISS index from an in-memory buffer.
 * Does not take ownership of buf.
 * Returns 0 on success, non-zero on error.
 */
int gleann_faiss_read_buf(const uint8_t *buf, size_t size, FaissIndex **out_idx);

#ifdef __cplusplus
}
#endif
