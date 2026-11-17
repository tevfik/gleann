package main

import (
	"fmt"
	"os"
	"testing"
)

// TestMain gives the package's tests a throwaway home directory, so they never
// read or write the developer's real ~/.gleann (indexes, config, memory) and
// never talk to a running `gleann serve`.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "gleann-cmd-test-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating test home: %v\n", err)
		os.Exit(1)
	}
	for k, v := range map[string]string{
		"HOME":               home,
		"USERPROFILE":        home,
		"GLEANN_REMOTE_ADDR": "off",
	} {
		if err := os.Setenv(k, v); err != nil {
			fmt.Fprintf(os.Stderr, "setting %s: %v\n", k, err)
			os.Exit(1)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
