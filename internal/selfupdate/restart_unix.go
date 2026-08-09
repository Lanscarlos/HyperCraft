//go:build !windows

package selfupdate

import (
	"os"
	"syscall"
)

// Restart replaces this process image with the binary at path, keeping the same
// PID, arguments and environment. Pass Staged.Target(): the path must be the one
// captured before the install, because os.Executable now resolves to the backup.
//
// Keeping the PID matters under a supervisor: systemd is watching this process,
// and a fresh PID would look like a crash. It also means the unit does not need
// Restart=always for in-panel updates to work.
//
// Every managed server must already be stopped — exec does not run deferred
// functions and the new image inherits no knowledge of the old one's children.
// On success this call does not return.
func Restart(path string) error {
	return syscall.Exec(path, os.Args, os.Environ())
}
