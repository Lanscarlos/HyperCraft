//go:build windows

package instance

import (
	"os"
	"os/exec"
)

// ptySupported is false on Windows: a terminal there means ConPTY, and
// creack/pty has no implementation of it. An instance configured for TTY still
// starts — it falls back to pipes, exactly as every instance behaved before the
// option existed — and says so in the console.
const ptySupported = false

func startPTY(*exec.Cmd, uint16, uint16) (*os.File, error) { return nil, errPTYUnsupported }

func resizePTY(*os.File, uint16, uint16) error { return errPTYUnsupported }
