package store

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// PassagePID computes the stable passage ID of RFC 003 section 3.4
// ("Stable passage IDs"):
//
//	pid = hex(sha256(source 0x00 sha256(text) 0x00 dup_index))[:32]
//
// sha256(text) is fed as the raw 32-byte digest and dup_index as its decimal
// text. dupIndex is the order of this text among identical texts of the same
// source (normally 0), so repeated chunks still get distinct IDs.
//
// The ordinal chunk_no is deliberately not part of the ID: an insertion at
// the top of a file shifts every ordinal but leaves unchanged chunks with
// the same pid, so their vectors and embedding-cache entries remain valid.
func PassagePID(source, text string, dupIndex int) string {
	textHash := sha256.Sum256([]byte(text))
	h := sha256.New()
	h.Write([]byte(source))
	h.Write([]byte{0})
	h.Write(textHash[:])
	h.Write([]byte{0})
	h.Write([]byte(strconv.Itoa(dupIndex)))
	return hex.EncodeToString(h.Sum(nil))[:32]
}
