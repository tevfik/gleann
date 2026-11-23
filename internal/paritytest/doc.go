// Package paritytest holds the in-tree parity fixtures of RFC 008
// ("One retrieval service", decision D12): one HTTP fake for the embedding,
// chat and rerank APIs gleann calls, with per-endpoint request counters and
// a record of the last request body and headers, plus a small corpus of
// tiny indexes with no passage shared between them, built through the real
// builder.
//
// The fake's embeddings are quality.EmbedText, the same deterministic hash
// scheme the quality gate's HashEmbedder uses, so an index built through
// the fake and a search run with a direct HashEmbedder produce identical
// vectors and results are reproducible without a model, network or CGo.
//
// Steps 0b (goldens) and 0c (hole tests) build on these fixtures; nothing
// here changes gleann's behaviour.
package paritytest
