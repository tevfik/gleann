//go:build faiss

package vector_db

import (
	"context"
	"math/rand"
	"os"
	"testing"

	"github.com/tevfik/gleann/internal/backend/faiss"
	"github.com/tevfik/gleann/modules/hnsw"
	"github.com/tevfik/gleann/pkg/gleann"
)

func generateRandomEmbeddings(count, dim int) [][]float32 {
	r := rand.New(rand.NewSource(42))
	embs := make([][]float32, count)
	for i := 0; i < count; i++ {
		embs[i] = make([]float32, dim)
		for j := 0; j < dim; j++ {
			embs[i][j] = r.Float32()
		}
	}
	return embs
}

func BenchmarkVectorDB(b *testing.B) {
	const (
		numVectors = 10000
		dimensions = 384 // Nomic-embed size usually
		topK       = 10
	)

	b.Logf("Generating %d vectors of dimension %d...", numVectors, dimensions)
	dataset := generateRandomEmbeddings(numVectors, dimensions)
	queries := generateRandomEmbeddings(100, dimensions)

	ctx := context.Background()
	config := gleann.Config{
		IndexDir: b.TempDir(),
	}

	b.Run("FAISS", func(b *testing.B) {
		factory := &faiss.Factory{}
		builder := factory.NewBuilder(config)

		var indexData []byte
		var err error

		b.Run("Build", func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				indexData, err = builder.Build(ctx, dataset)
				if err != nil {
					b.Fatalf("faiss build failed: %v", err)
				}
			}
		})

		searcher := factory.NewSearcher(config)
		err = searcher.Load(ctx, indexData, gleann.IndexMeta{})
		if err != nil {
			b.Fatalf("faiss load failed: %v", err)
		}
		defer searcher.Close()

		b.Run("Search", func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				q := queries[i%len(queries)]
				_, _, err := searcher.Search(ctx, q, topK)
				if err != nil {
					b.Fatalf("faiss search failed: %v", err)
				}
			}
		})
	})

	b.Run("HNSW", func(b *testing.B) {
		factory := &hnsw.Factory{}
		builder := factory.NewBuilder(config)

		var indexData []byte
		var err error

		b.Run("Build", func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				indexData, err = builder.Build(ctx, dataset)
				if err != nil {
					b.Fatalf("hnsw build failed: %v", err)
				}
			}
		})

		// HNSW Needs index saved to disk for MMAP
		idxPath := config.IndexDir + "/hnsw.bin"
		os.WriteFile(idxPath, indexData, 0644)

		searcher := factory.NewSearcher(config)
		err = searcher.Load(ctx, indexData, gleann.IndexMeta{}) // Load via bytes or it will handle it internall depending on your impl
		if err != nil {
			b.Fatalf("hnsw load failed: %v", err)
		}
		defer searcher.Close()

		b.Run("Search", func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				q := queries[i%len(queries)]
				_, _, err := searcher.Search(ctx, q, topK)
				if err != nil {
					b.Fatalf("hnsw search failed: %v", err)
				}
			}
		})
	})
}
