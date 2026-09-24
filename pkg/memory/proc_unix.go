//go:build !windows

package memory

import (
	"os/exec"
	"syscall"
)

func spawnDaemonCmd(binPath, serveAddr string) *exec.Cmd {
	args := []string{"serve", "--addr", serveAddr, "--quiet"}
	cmd := exec.Command(binPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	return cmd
}
