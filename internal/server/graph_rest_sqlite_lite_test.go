//go:build !treesitter

package server

import (
	"testing"

	"github.com/tevfik/gleann/internal/graph/analysisgolden"
)

// checkSQLiteGolden compares with the frozen lite-build golden (sqlite_lite/).
func checkSQLiteGolden(t *testing.T, name, got string) {
	t.Helper()
	analysisgolden.Check(t, "sqlite_lite/"+name, got)
}
