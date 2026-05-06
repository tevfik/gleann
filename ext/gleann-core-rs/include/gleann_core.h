#ifndef GLEANN_CORE_H
#define GLEANN_CORE_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct {
    const float* data;
    size_t rows;
    size_t cols;
} GleannEmbeddingResult;

// Opaque pointer to the Rust computer instance
typedef struct NativeComputer NativeComputer;

NativeComputer* gleann_native_init(void);
void gleann_native_free(NativeComputer* ptr);

GleannEmbeddingResult* gleann_embed_texts(NativeComputer* computer, const char** texts, size_t count);
void gleann_free_embedding_result(GleannEmbeddingResult* res);

#ifdef __cplusplus
}
#endif

#endif // GLEANN_CORE_H
