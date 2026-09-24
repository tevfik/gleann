//go:build treesitter

package kuzu

import (
	"errors"
	"testing"
)

func TestIsLockError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("some random corruption error"), false},
		{errors.New("failed to acquire lock on file"), true},
		{errors.New("Resource temporarily unavailable"), true},
		{errors.New("database already open by another process"), true},
		{errors.New("permission denied"), true},
		{errors.New("access is denied"), true},
		{errors.New("database is busy"), true},
	}

	for _, tc := range cases {
		got := isLockError(tc.err)
		if got != tc.want {
			t.Errorf("isLockError(%v) = %v; want %v", tc.err, got, tc.want)
		}
	}
}
