//go:build !windows

package instance

import (
	"os/exec"
	"syscall"
)

// configureProcAttr puts the server in its own process group. Everything the
// jar forks (wrapper scripts, Forge's relauncher) lands in the same group, so
// a single signal takes the whole tree down instead of orphaning children.
func configureProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
		return syscall.Kill(-pgid, syscall.SIGTERM)
	}
	return cmd.Process.Signal(syscall.SIGTERM)
}

func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
		return syscall.Kill(-pgid, syscall.SIGKILL)
	}
	return cmd.Process.Kill()
}
