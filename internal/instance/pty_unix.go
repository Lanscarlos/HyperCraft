//go:build !windows

package instance

import (
	"errors"
	"os"
	"os/exec"

	"github.com/creack/pty"
)

// ptySupported is true wherever creack/pty can open a master/slave pair, which
// is every unix the panel is built for.
const ptySupported = true

// startPTY runs cmd on a new pseudo-terminal and returns the master side.
//
// creack/pty makes the child a session leader with the tty as its controlling
// terminal. That is precisely what a server jar looks for: JLine only offers
// completion and a persistent prompt line when it can see a terminal, and
// TerminalConsoleAppender only colours its output for the same reason.
func startPTY(cmd *exec.Cmd, cols, rows uint16) (*os.File, error) {
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if errors.Is(err, pty.ErrUnsupported) {
		return nil, errPTYUnsupported
	}
	return f, err
}

func resizePTY(f *os.File, cols, rows uint16) error {
	return pty.Setsize(f, &pty.Winsize{Cols: cols, Rows: rows})
}
