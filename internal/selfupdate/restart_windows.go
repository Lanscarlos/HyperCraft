//go:build windows

package selfupdate

import (
	"os"
	"os/exec"
)

// Restart launches the binary at path and returns, leaving the caller to exit.
// Pass Staged.Target(): the path must be the one captured before the install,
// because os.Executable now resolves to the backup.
//
// Windows has no exec that replaces the running image, so the panel necessarily
// changes PID here; a service wrapper should be configured to treat the brief
// gap as a normal restart.
//
// Every managed server must already be stopped before this is called.
func Restart(path string) error {
	cmd := exec.Command(path, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir, _ = os.Getwd()
	return cmd.Start()
}
