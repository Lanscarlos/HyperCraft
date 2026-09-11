package selfupdate

import (
	"context"
	"errors"
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

// SwapToOld exchanges the running binary with the one at <exe>.old and returns
// the path a restart must exec.
//
// It is Staged.Commit's move in both directions, and for the same reason it is
// allowed: renaming the executable of a running process is fine on Unix and on
// Windows, because the running image is already mapped and only the directory
// entry moves.
//
// The exchange goes through a third name so that the build being left is kept
// rather than overwritten — a rollback made by mistake has to be undoable, and
// what undoes it is this same function finding the newer build at <exe>.old.
// Every step that can fail puts back what it moved.
func (u *Updater) SwapToOld() (string, error) {
	exe, err := u.executable()
	if err != nil {
		return "", err
	}
	old := exe + ".old"
	aside := exe + ".rollback-tmp"
	_ = os.Remove(aside)

	if err := os.Rename(exe, aside); err != nil {
		return "", fmt.Errorf("move the running binary aside: %w", err)
	}
	if err := os.Rename(old, exe); err != nil {
		if restoreErr := os.Rename(aside, exe); restoreErr != nil {
			return "", fmt.Errorf("install the previous binary: %w (and putting the running one back failed: %v; it is at %s)", err, restoreErr, aside)
		}
		return "", fmt.Errorf("install the previous binary: %w", err)
	}
	if err := os.Rename(aside, old); err != nil {
		// The panel binary is already the older build. Rather than restart into
		// a state where the way forward is at a name nothing looks for, undo the
		// whole exchange and report it.
		if backErr := os.Rename(exe, old); backErr == nil {
			if backErr = os.Rename(aside, exe); backErr == nil {
				return "", fmt.Errorf("keep the build being left: %w", err)
			}
		}
		return "", fmt.Errorf("keep the build being left: %w (and the exchange could not be undone; the running build is at %s and the panel binary is the older one)", err, aside)
	}
	return exe, nil
}

// Rollback puts the panel back on the build at <exe>.old: the one the last
// update replaced, or — after a rollback — the one that rollback left.
//
// It is the update path with the download cut out, and it takes the same care
// in the same order: refuse before touching anything if the target is not
// usable, copy the panel's own state while this build's fields are still in it,
// stop every server before the binary moves, and put the servers back if any of
// that fails.
func (s *Service) Rollback(ctx context.Context) error {
	s.mu.Lock()
	if s.phase != PhaseIdle {
		s.mu.Unlock()
		return ErrBusy
	}
	target, why, previous := s.rollbackPath, s.rollbackWhy, s.previous
	if target == "" {
		s.mu.Unlock()
		if why == "" {
			why = "现在没有可以退回的版本"
		}
		return errors.New(why)
	}
	current := s.up.CurrentVersion()
	// Nothing is downloaded, so the bar would otherwise sit at zero for the
	// whole shutdown. The work that remains is the stopping.
	s.phase = PhaseStopping
	s.progress = 100
	s.lastErr = ""
	s.shutdown = nil
	s.backupDir = ""
	s.mu.Unlock()

	if err := s.rollback(ctx, current, previous); err != nil {
		s.mu.Lock()
		s.phase = PhaseIdle
		s.progress = 0
		s.shutdown = nil
		s.lastErr = err.Error()
		s.mu.Unlock()
		s.log.Error("rollback failed", "from", current, "to", previous, "err", err)
		return err
	}
	return nil
}

func (s *Service) rollback(ctx context.Context, current, previous string) error {
	s.log.Info("rollback starting", "from", current, "to", previous)

	// Only going down costs anything: the older build does not know this one's
	// fields and drops them the first time it saves. Going back up — undoing a
	// rollback — adds fields rather than losing them, so there is nothing to
	// protect and no directory worth leaving behind.
	if CompareVersions(previous, current) < 0 && s.hooks.BackupState != nil {
		dir, err := s.hooks.BackupState(current, previous)
		if err != nil {
			// Refuse rather than continue: the backup is the entire reason a
			// downgrade is safe to offer.
			return fmt.Errorf("back up the panel state: %w", err)
		}
		s.mu.Lock()
		s.backupDir = dir
		s.mu.Unlock()
		s.log.Info("panel state backed up before the downgrade", "dir", dir)
	}

	if err := s.stopServers(ctx); err != nil {
		s.resumeServers()
		return fmt.Errorf("stop the servers: %w", err)
	}

	s.mu.Lock()
	s.phase = PhaseInstalling
	s.mu.Unlock()

	exe, err := s.up.SwapToOld()
	if err != nil {
		s.resumeServers()
		return err
	}
	s.log.Info("previous binary installed", "version", previous)

	// What sat at <exe>.old is now the running binary's neighbour, so the build
	// just left is where a second rollback would look for it.
	if s.hooks.RecordPrevious != nil {
		s.hooks.RecordPrevious(current)
	}
	s.SetPreviousVersion(current)

	s.mu.Lock()
	s.phase = PhaseRestarting
	s.mu.Unlock()

	if s.hooks.TriggerRestart != nil {
		s.hooks.TriggerRestart(exe)
	}
	return nil
}
