package main

import (
	"fmt"
	"os"

	"github.com/tevfik/gleann/internal/tui"
)

// configExemptCommands run even when config.json is malformed: they either
// repair the config, report on it, or never read it.
var configExemptCommands = map[string]bool{
	"version": true, "help": true, "--help": true, "-h": true,
	"config": true, // show/set are gated inside cmdConfig; path/edit/validate stay open
	"setup":  true, "doctor": true, "completion": true,
	"update": true, "upgrade": true, "self-update": true,
}

// requireValidConfig exits with status 1 and a message naming the file and
// the line:column of the JSON error when the saved config.json is malformed.
// A missing config is fine. The file is never modified.
func requireValidConfig() {
	if _, err := tui.LoadSavedConfigChecked(); err != nil {
		fmt.Fprintf(os.Stderr, "error: config: %v\n", err)
		os.Exit(1)
	}
}
