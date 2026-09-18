// Package main — CLI remote memory routing.
//
// When `gleann serve` is running it holds an exclusive bbolt lock on
// ~/.gleann/memory/memory.db. Any local CLI call that tries to open the
// same database deadlocks for 5 s and then fails with `open memory db:
// timeout`. To make the CLI robust in that situation we probe a running
// server at the configured address and — when reachable — route the
// affected memory commands through the REST surface that is already
// exposed at /api/blocks.
package main

import (
	"github.com/tevfik/gleann/pkg/memory"
)

// remoteMemoryClient returns a REST-backed shim if a gleann server is
// reachable, otherwise nil. The result is cached for the lifetime of the
// process.
func remoteMemoryClient() *memory.RemoteClient {
	return memory.Remote()
}
