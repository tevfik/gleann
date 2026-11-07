//go:build linux && amd64

package llamacpp

import "embed"

// embeddedBinaries holds llama-server and its libraries for this platform
// (see download_binaries.sh). Each platform embeds only its own files.
//
//go:embed bin/linux-amd64
var embeddedBinaries embed.FS

const embeddedDir = "bin/linux-amd64"
