package packs

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
)

type etagHasher struct{ h hash.Hash }

func newETagHasher() *etagHasher { return &etagHasher{h: sha256.New()} }

func (e *etagHasher) Write(p []byte) (int, error) { return e.h.Write(p) }

// String returns a quoted strong ETag of the form `"<12-hex>"`. Twelve hex
// characters (48 bits) are sufficient to detect any practical update; the
// quote characters satisfy the HTTP spec.
func (e *etagHasher) String() string {
	sum := e.h.Sum(nil)
	return `"` + hex.EncodeToString(sum)[:12] + `"`
}
