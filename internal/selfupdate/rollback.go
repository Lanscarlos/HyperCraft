package selfupdate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// versionProbeTimeout bounds the "what version are you" question put to the
// binary next door. It only prints a string and exits, so a second is already
// generous; the bound exists because a corrupt file could do anything.
const versionProbeTimeout = 5 * time.Second

// OldBinary is where Commit leaves the binary an update replaced.
func (u *Updater) OldBinary() (string, error) {
	exe, err := u.executable()
	if err != nil {
		return "", err
	}
	return exe + ".old", nil
}

// InspectRollback reports whether the panel can be put back on previous — the
// version recorded when the last update replaced it — and where that binary
// is. An empty why means yes.
//
// The record alone is not enough to act on. It lives in panel.json while the
// binary lives beside the executable, and the two drift: a data directory gets
// cleaned up, an operator swaps a binary by hand, a full disk truncates the
// copy. So the file is asked what it is, by running it, and is only trusted
// when it answers with the version that was recorded. Swapping the panel for a
// file that cannot start is the one outcome a rollback must never produce —
// there would be no panel left to fix it with.
func (u *Updater) InspectRollback(previous string) (path string, why string) {
	if strings.TrimSpace(previous) == "" {
		return "", "没有记录到上一版：这台面板要么是全新安装，要么二进制是手动换上去的（只有面板自己装的更新才会留下上一版）"
	}

	old, err := u.OldBinary()
	if err != nil {
		return "", "找不到面板自己的二进制：" + err.Error()
	}
	if _, err := os.Stat(old); err != nil {
		return "", fmt.Sprintf("上一版的二进制已经不在了（%s）", old)
	}

	reported, err := probeVersion(old)
	if err != nil {
		return "", fmt.Sprintf("上一版的二进制跑不起来（%s）：%v", old, err)
	}
	if NormalizeVersion(reported) != NormalizeVersion(previous) {
		return "", fmt.Sprintf("旁边那个二进制报的是 %s，不是记录里的 %s，已经被人换过了", reported, previous)
	}
	return old, ""
}

// probeVersion runs a binary's -version flag and returns the version it prints.
// The output is "hypercraft <version>", as main prints it.
func probeVersion(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), versionProbeTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, path, "-version").Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 || fields[0] != "hypercraft" {
		return "", fmt.Errorf("unexpected output %q", strings.TrimSpace(string(out)))
	}
	return fields[1], nil
}
