//go:build !windows

package instance

import (
	"os/exec"
	"syscall"
)

// configureProcAttr puts the server in its own process group. Everything the
// jar forks (wrapper scripts, Forge's relauncher) lands in the same group, so
// a single signal takes the whole tree down instead of orphaning children.
//
// On a pseudo-terminal the grouping is left to creack/pty, which needs the
// child to be a *session* leader so the tty can become its controlling
// terminal. setsid() already puts it in a new process group of its own, and
// asking for Setpgid on top of that is not merely redundant: the kernel
// refuses setpgid() from a session leader outright, so the child would fail to
// exec with EPERM. terminateTree looks the group up by pid either way.
func configureProcAttr(cmd *exec.Cmd, tty bool) {
	if tty {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
		return
	}
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
