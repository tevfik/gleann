package embedding

import "testing"

// The index builder finds the progress hook through this interface; it must be
// satisfied, or embedding progress is silently never reported.
var (
	_ interface{ SetProgressCallback(func(int, int)) } = (*Computer)(nil)
	_ interface{ SetProgressCallback(func(int, int)) } = (*CachedComputer)(nil)
)

func TestProgressCallbackIsAnAliasOfAPlainFunc(t *testing.T) {
	var plain func(completed, total int)
	var named ProgressCallback = plain
	plain = named // assignable both ways only because it is an alias
	_ = plain
}
