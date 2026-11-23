package gate

// Gates: recall@10 on 10,000 x 384 synthetic vectors, 300 queries. Each
// comment gives the value measured when the gate was set (2026-11-20) and the
// regression it guards against. Lower a gate only with a measured reason in
// the commit message.
const (
	// 0.9993.
	gateHNSW = 0.99
	// 0.9763; 0.92 before the neighbour top-up fix (selectNeighborsHeuristic
	// left nodes with fewer than M neighbours on near-duplicate data).
	gateHNSWDup = 0.95
	// 0.9993: the real search path without a filter.
	gateSearchAll = 0.99
	// 1.0000.
	gateSearch1 = 0.99
	// 0.9997; 0.942 before filters with few matches were scored exactly.
	gateSearch10 = 0.99
	// 0.9967.
	gateSearch50 = 0.98
	// 0.9977 in each of three builds (the build is multi-threaded and not
	// deterministic). 0.919 before the rerank width fix. A 10K index does
	// not show the build's quality at scale: a best-first build search scored
	// the same here and 0.512 instead of 0.920 at 100K x 768, search L 64.
	gateDiskANN = 0.98
	// DiskANN through the real search path (gleann-search-diskann rows),
	// measured post-fix in three runs each (2026-11-21, 10K x 384, 300
	// queries). Before VectorDistances existed on the DiskANN searcher the
	// filtered rows scored 0.9057 (1%) and 0.8480 (10%): the widen loop's
	// approximate matches. Exact scoring of the pre-selected set lifts them
	// to the hnsw rows' level.
	// 0.9997 / 0.9997 / 1.0000.
	gateDiskSearchAll = 0.99
	// 1.0000 in all three runs; 0.9057 before the exact path.
	gateDiskSearch1 = 0.99
	// 0.9993 / 0.9997 / 0.9997; 0.8480 before the exact path.
	gateDiskSearch10 = 0.99
	// 0.9977 / 0.9963 / 0.9967 (the build is multi-threaded and not
	// deterministic); 0.9980 before the fix — the loop already handles a
	// dense filter well, the gate guards the build quality.
	gateDiskSearch50 = 0.98
)
