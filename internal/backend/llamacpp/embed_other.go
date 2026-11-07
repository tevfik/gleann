//go:build !(linux && (amd64 || arm64))

package llamacpp

import "embed"

// No llama-server is embedded for this platform; extraction reports that.
var embeddedBinaries embed.FS

const embeddedDir = "bin/none"
