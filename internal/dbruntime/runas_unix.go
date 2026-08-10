//go:build !windows

package dbruntime

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

// Running a database as root is a problem the panel cannot ignore.
//
// PostgreSQL refuses outright — initdb and the server both exit with "cannot be
// run as root" — because a bug in a process that reads arbitrary files as root
// is a very different bug. MySQL refuses too unless it is told explicitly, and
// MongoDB does not care. A panel installed from a systemd unit is normally
// root, so "just run the panel as a normal user" is not an answer that can be
// given at the point the operator is trying to create a database.
//
// So when the panel is root it hands the engine to an unprivileged account and
// gives that account the service directory. When the panel is not root there is
// nothing to do: the engine inherits the panel's own account, which is already
// the unprivileged one.

// resolveRunAs picks the account an engine should run as, or "" for the panel's
// own account.
func resolveRunAs(engine, requested string) (string, error) {
	if os.Geteuid() != 0 {
		if requested != "" {
			return "", fmt.Errorf("%w: 面板不是以 root 运行，没法切换到用户 %q", ErrInvalidConfig, requested)
		}
		return "", nil
	}

	if requested != "" {
		if _, err := user.Lookup(requested); err != nil {
			return "", fmt.Errorf("%w: 系统里没有用户 %q", ErrInvalidConfig, requested)
		}
		return requested, nil
	}
	// The engine's own conventional account first. It is usually already there
	// from a package the operator installed and later removed, its home and
	// shell are already set up for a database, and — the practical reason — a
	// MySQL service reported as running as "postgres" reads like a bug even
	// when it works perfectly.
	for _, candidate := range append(conventionalAccounts(engine), "daemon", "nobody") {
		if account, err := user.Lookup(candidate); err == nil && account.Uid != "0" {
			return candidate, nil
		}
	}
	if engine == EnginePostgreSQL {
		return "", fmt.Errorf("%w: PostgreSQL 不能以 root 运行，"+
			"但这台机器上找不到可用的普通用户。请先建一个（例如 useradd -r -s /sbin/nologin postgres），"+
			"或者在创建时手动指定运行用户", ErrInvalidConfig)
	}
	// MySQL and MongoDB will run as root if told to, and on a single-purpose
	// box with no other account that is better than refusing to work at all.
	return "", nil
}

// conventionalAccounts are the system accounts each engine's own packages
// create, best match first.
func conventionalAccounts(engine string) []string {
	switch engine {
	case EngineMySQL:
		return []string{"mysql", "mariadb"}
	case EnginePostgreSQL:
		return []string{"postgres", "postgresql"}
	case EngineMongoDB:
		return []string{"mongodb", "mongod"}
	}
	return nil
}

// applyCredential puts the engine in its own process group and, when a run-as
// account was resolved, drops it to that account.
//
// The process group is what makes stopping reliable: anything the engine forks
// lands in the same group, so one signal takes the tree down rather than
// orphaning a child that still holds the port.
func applyCredential(cmd *exec.Cmd, username string) error {
	attr := &syscall.SysProcAttr{Setpgid: true}
	cmd.SysProcAttr = attr
	if username == "" {
		return nil
	}

	account, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("%w: 系统里没有用户 %q", ErrInvalidConfig, username)
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		return fmt.Errorf("%w: 用户 %q 的 uid 不是数字", ErrInvalidConfig, username)
	}
	gid, err := strconv.ParseUint(account.Gid, 10, 32)
	if err != nil {
		return fmt.Errorf("%w: 用户 %q 的 gid 不是数字", ErrInvalidConfig, username)
	}
	attr.Credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
	return nil
}

// chownTree hands a directory to the run-as account. Without it the engine
// drops privileges and then cannot write to its own data directory, which shows
// up as a permission error from a process that looks like it should have every
// right to be there.
func chownTree(dir, username string) error {
	if username == "" {
		return nil
	}
	account, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("%w: 系统里没有用户 %q", ErrInvalidConfig, username)
	}
	uid, _ := strconv.Atoi(account.Uid)
	gid, _ := strconv.Atoi(account.Gid)

	return filepath.WalkDir(dir, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, uid, gid)
	})
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
