//go:build windows

package memory

import (
	"os/exec"
)

func spawnDaemonCmd(binPath, serveAddr string) *exec.Cmd {
	args := []string{"serve", "--addr", serveAddr, "--quiet"}
	return exec.Command(binPath, args...)
}
