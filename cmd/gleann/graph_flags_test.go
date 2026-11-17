package main

import "testing"

func TestGraphSubcommandsRejectUnknownFlags(t *testing.T) {
	for sub := range graphFlagSets {
		if got := runExit(t, mainWith("graph", sub, bogusFlag)); got != 2 {
			t.Errorf("gleann graph %s %s: exit status %d, want 2", sub, bogusFlag, got)
		}
	}
	for _, sub := range caseStrings(t, "graph_shared.go", "cmdGraph") {
		if _, ok := graphFlagSets[sub]; !ok && sub != "help" && sub != "--help" {
			t.Errorf("gleann graph %s has no flag table", sub)
		}
	}
}

func TestGraphHelpFlagsAreAccepted(t *testing.T) {
	known := unionAll(graphFlagSets)
	for _, f := range helpFlags(captureHelp(t, printGraphUsage)) {
		// The usage ends with "gleann index build <name> --docs <dir> --graph".
		if f == "--graph" || f == "--help" {
			continue
		}
		if _, ok := known[f]; !ok {
			t.Errorf("graph help mentions %s but no graph flag table accepts it", f)
		}
	}
}
