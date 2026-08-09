//go:build windows

package instance

import (
	"os/exec"
	"strconv"
	"syscall"
)

// createNewProcessGroup is Windows' CREATE_NEW_PROCESS_GROUP. It keeps a
// Ctrl+C in the panel's own console from propagating into managed servers.
const createNewProcessGroup = 0x00000200

func configureProcAttr(cmd *exec.Cmd, _ bool) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

// Windows has no SIGTERM, so both escalation steps go through taskkill.
// /t walks the child tree; /f is the non-negotiable variant.
func terminateTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return exec.Command("taskkill", "/pid", strconv.Itoa(cmd.Process.Pid), "/t").Run()
}

func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return exec.Command("taskkill", "/pid", strconv.Itoa(cmd.Process.Pid), "/t", "/f").Run()
}
