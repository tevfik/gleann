package server

import (
	"testing"
	"time"
)

func TestHTTPWriteTimeoutFollowsBuildLimit(t *testing.T) {
	cases := []struct {
		build time.Duration
		want  time.Duration
	}{
		{10 * time.Minute, 30 * time.Minute}, // default build limit: the 30 min floor
		{25 * time.Minute, 30 * time.Minute}, // exactly the floor
		{26 * time.Minute, 31 * time.Minute}, // build limit + 5 min
		{2 * time.Hour, 2*time.Hour + 5*time.Minute},
	}
	for _, c := range cases {
		tc := timeoutConfig{build: c.build}
		if got := tc.httpWriteTimeout(); got != c.want {
			t.Errorf("build=%v: write timeout %v, want %v", c.build, got, c.want)
		}
	}
}
