// Package competitive compares gleann's HNSW and BM25 modules with chromem-go,
// an embeddable Go vector store that searches by brute force. It lives in the
// bench/vectordb module so chromem-go is not a dependency of gleann itself.
//
//	cd bench/vectordb && go test -v -run TestCompetitive ./competitive/
//	cd bench/vectordb && go test -bench Competitive -run '^$' ./competitive/
package competitive
