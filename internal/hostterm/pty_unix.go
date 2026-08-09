//go:build !windows

package hostterm

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

// ptySupported is true wherever creack/pty can open a master/slave pair, which
// is every unix the panel is built for.
const ptySupported = true

// defaultShells is tried in order when $SHELL is unset or not on PATH.
var defaultShells = []string{"bash", "sh"}

const fallbackShell = "/bin/sh"

const (
	hangup = syscall.SIGHUP
	kill   = syscall.SIGKILL
)

// startPTY runs cmd on a new pseudo-terminal. creack/pty puts the child in its
// own session with the tty as its controlling terminal, which is what makes
// job control, Ctrl-C and `stty` behave the way they do over SSH.
func startPTY(cmd *exec.Cmd, cols, rows uint16) (*os.File, error) {
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if errors.Is(err, pty.ErrUnsupported) {
		return nil, ErrUnsupported
	}
	return f, err
}

func resizePTY(f *os.File, cols, rows uint16) error {
	return pty.Setsize(f, &pty.Winsize{Cols: cols, Rows: rows})
}

// signalGroup signals the shell and everything it started. The child is a
// session leader, so its pid doubles as the process group id and the negative
// pid reaches the group.
func signalGroup(proc *os.Process, sig syscall.Signal) {
	if proc == nil {
		return
	}
	if err := syscall.Kill(-proc.Pid, sig); err != nil {
		// The group may already be gone, or we may be on a system that put the
		// child somewhere unexpected; either way the shell itself is worth a
		// second try on its own.
		_ = proc.Signal(sig)
	}
}
