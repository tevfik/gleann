package llamacpp

import (
	"embed"
)

//go:embed bin/*
var embeddedBinaries embed.FS
