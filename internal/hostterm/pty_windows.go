//go:build windows

package hostterm

import (
	"os"
	"os/exec"
	"syscall"
)

// ptySupported is false on Windows: a usable terminal there means ConPTY, and
// creack/pty has no implementation of it. Rather than ship a pipe-backed shell
// that looks like a terminal but has no job control, no Ctrl-C and no cursor
// addressing, the panel says the feature is unavailable and the UI never
// offers it. Everything else in the panel works on Windows as before.
const ptySupported = false

var defaultShells = []string{"powershell.exe", "cmd.exe"}

const fallbackShell = "cmd.exe"

// Signal values exist only so the shared code compiles; nothing reaches the
// calls that use them while ptySupported is false.
const (
	hangup = syscall.Signal(0)
	kill   = syscall.Signal(0)
)

func startPTY(*exec.Cmd, uint16, uint16) (*os.File, error) { return nil, ErrUnsupported }

func resizePTY(*os.File, uint16, uint16) error { return ErrUnsupported }

func signalGroup(proc *os.Process, _ syscall.Signal) {
	if proc != nil {
		_ = proc.Kill()
	}
}
