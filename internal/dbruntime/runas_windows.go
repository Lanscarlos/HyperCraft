package dbruntime

import (
	"fmt"
	"os/exec"
)

// Windows has no equivalent of dropping to another account for a child process
// without a password for it, and none of the three engines refuses to run as
// Administrator the way PostgreSQL refuses to run as root. So the engine simply
// inherits the panel's account, and a requested run-as user is an error rather
// than something quietly ignored.

func resolveRunAs(_ string, requested string) (string, error) {
	if requested != "" {
		return "", fmt.Errorf("%w: Windows 上没法指定运行用户，数据库会用面板自己的账号运行", ErrInvalidConfig)
	}
	return "", nil
}

func applyCredential(_ *exec.Cmd, _ string) error { return nil }

func chownTree(_, _ string) error { return nil }

// terminateTree has no graceful counterpart here: a Windows console process
// cannot be sent the equivalent of SIGTERM from an unrelated parent. Every
// engine that has its own shutdown command uses it instead — see stopCommand —
// and MongoDB, which has none, is killed and recovers from its journal on the
// next start.
func terminateTree(cmd *exec.Cmd) error { return killTree(cmd) }

func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
