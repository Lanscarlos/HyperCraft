package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// errWorldRefusedToSave stands in for a shutdown that will not finish.
var errWorldRefusedToSave = errors.New("a world refused to save")

// fakeBinary writes an executable that answers -version the way the real one
// does, so InspectRollback can be tested against something it actually runs
// rather than against a mock of the running.
func fakeBinary(t *testing.T, path, version string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	script := fmt.Sprintf("#!/bin/sh\necho 'hypercraft %s'\n", version)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// updaterAt returns an updater whose executable is a real file on disk.
func updaterAt(t *testing.T, current string) (*Updater, string) {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "hypercraft")
	fakeBinary(t, exe, NormalizeVersion(current))
	u := New("owner/repo", current)
	u.exePath = exe
	return u, exe
}

func TestInspectRollbackRejectsAnEmptyRecord(t *testing.T) {
	// A fresh install, or a binary put in place by hand: there is a file at
	// <exe>.old on plenty of machines, but nothing says what it is.
	u, exe := updaterAt(t, "v1.2.0")
	fakeBinary(t, exe+".old", "1.1.0")

	path, why := u.InspectRollback("")
	if why == "" {
		t.Fatalf("InspectRollback accepted an empty record, pointing at %q", path)
	}
}

func TestInspectRollbackRejectsAMissingOldBinary(t *testing.T) {
	// The record survives in panel.json; the file does not have to. Someone
	// cleaning up a data directory, or a fresh unpack over the top.
	u, _ := updaterAt(t, "v1.2.0")

	if _, why := u.InspectRollback("1.1.0"); why == "" {
		t.Fatal("InspectRollback accepted a record whose binary is gone")
	}
}

func TestInspectRollbackRejectsAVersionMismatch(t *testing.T) {
	// <exe>.old is a real, runnable binary — of the wrong version. Somebody
	// replaced it by hand, or an interrupted update left something else there.
	// Installing it would move the panel somewhere nobody asked for.
	u, exe := updaterAt(t, "v1.2.0")
	fakeBinary(t, exe+".old", "0.9.0")

	if _, why := u.InspectRollback("1.1.0"); why == "" {
		t.Fatal("InspectRollback accepted a binary that reports a different version")
	}
}

func TestInspectRollbackRejectsABinaryThatWillNotRun(t *testing.T) {
	// Truncated by a full disk, or never executable in the first place. Better
	// to refuse than to swap the panel for a file that cannot start.
	u, exe := updaterAt(t, "v1.2.0")
	if err := os.WriteFile(exe+".old", []byte("not a program"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, why := u.InspectRollback("1.1.0"); why == "" {
		t.Fatal("InspectRollback accepted a binary that cannot run")
	}
}

func TestInspectRollbackAcceptsAMatchingOldBinary(t *testing.T) {
	u, exe := updaterAt(t, "v1.2.0")
	fakeBinary(t, exe+".old", "1.1.0")

	path, why := u.InspectRollback("v1.1.0")
	if why != "" {
		t.Fatalf("InspectRollback refused a good binary: %s", why)
	}
	if path != exe+".old" {
		t.Errorf("InspectRollback returned %q, want %q", path, exe+".old")
	}
}

func TestStatusCarriesTheRollbackTarget(t *testing.T) {
	// The UI renders the button from the status alone, so everything it needs
	// to decide has to be in there — including why it cannot, when it cannot.
	svc := NewService("owner/repo", "v1.2.0", "", ChannelStable, Hooks{},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	exe := filepath.Join(t.TempDir(), "hypercraft")
	fakeBinary(t, exe, "1.2.0")
	svc.up.exePath = exe
	fakeBinary(t, exe+".old", "1.1.0")

	svc.SetPreviousVersion("1.1.0")
	st := svc.Status()
	if st.PreviousVersion != "1.1.0" {
		t.Errorf("PreviousVersion = %q, want 1.1.0", st.PreviousVersion)
	}
	if !st.RollbackAvailable {
		t.Errorf("RollbackAvailable = false, why = %q", st.RollbackWhy)
	}
	if st.RollbackWhy != "" {
		t.Errorf("RollbackWhy = %q, want empty while a rollback is possible", st.RollbackWhy)
	}

	// And the other way round: the file goes, the reason appears.
	if err := os.Remove(exe + ".old"); err != nil {
		t.Fatal(err)
	}
	svc.SetPreviousVersion("1.1.0")
	st = svc.Status()
	if st.RollbackAvailable || st.RollbackWhy == "" {
		t.Errorf("status still offers a rollback with no binary: %+v", st)
	}
}

// rollbackService wires a service whose executable and <exe>.old are real
// files, and reports what the hooks saw.
type rollbackProbe struct {
	svc            *Service
	exe            string
	stopped        bool
	resumed        bool
	restartedWith  string
	recorded       string
	backupFrom     string
	backupTo       string
	exeWhenBacked  []byte
	backupCalls    int
	backupDirGiven string
}

func newRollbackProbe(t *testing.T, current, previous string, stopErr error) *rollbackProbe {
	t.Helper()
	p := &rollbackProbe{exe: filepath.Join(t.TempDir(), "hypercraft")}
	p.backupDirGiven = filepath.Join(t.TempDir(), "rollback-backup")

	p.svc = NewService("owner/repo", current, "", ChannelStable, Hooks{
		StopServers: func(context.Context, func(Shutdown)) error {
			p.stopped = true
			return stopErr
		},
		ServersAborted: func() { p.resumed = true },
		TriggerRestart: func(binary string) { p.restartedWith = binary },
		RecordPrevious: func(version string) { p.recorded = version },
		BackupState: func(from, to string) (string, error) {
			p.backupCalls++
			p.backupFrom, p.backupTo = from, to
			p.exeWhenBacked, _ = os.ReadFile(p.exe)
			return p.backupDirGiven, nil
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	p.svc.up.exePath = p.exe
	fakeBinary(t, p.exe, NormalizeVersion(current))
	fakeBinary(t, p.exe+".old", NormalizeVersion(previous))
	p.svc.SetPreviousVersion(previous)
	return p
}

func TestRollbackSwapsTheTwoBinaries(t *testing.T) {
	p := newRollbackProbe(t, "v1.2.0", "1.1.0", nil)
	before, err := os.ReadFile(p.exe)
	if err != nil {
		t.Fatal(err)
	}

	if err := p.svc.Rollback(context.Background()); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	// The panel is now the old build, and the build it left is where a second
	// rollback would find it — the swap is symmetric on purpose, so changing
	// one's mind twice works.
	got, err := probeVersion(p.exe)
	if err != nil {
		t.Fatalf("the binary in place does not run: %v", err)
	}
	if got != "1.1.0" {
		t.Errorf("panel binary reports %s, want the rolled-back 1.1.0", got)
	}
	kept, err := os.ReadFile(p.exe + ".old")
	if err != nil {
		t.Fatalf("nothing was kept to go forward to: %v", err)
	}
	if string(kept) != string(before) {
		t.Error("<exe>.old does not hold the build the rollback left")
	}
	if !p.stopped {
		t.Error("servers were not stopped before the binary was swapped")
	}
	if p.restartedWith != p.exe {
		t.Errorf("restarted %q, want %q", p.restartedWith, p.exe)
	}
	if p.recorded != "v1.2.0" {
		t.Errorf("recorded %q as the way forward, want v1.2.0", p.recorded)
	}
}

func TestRollbackBacksUpBeforeAnythingMoves(t *testing.T) {
	// The copy has to be of the files as this build left them: it exists so the
	// fields the older build is about to drop can be recovered.
	p := newRollbackProbe(t, "v1.2.0", "1.1.0", nil)
	current, err := os.ReadFile(p.exe)
	if err != nil {
		t.Fatal(err)
	}

	if err := p.svc.Rollback(context.Background()); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	if p.backupCalls != 1 {
		t.Fatalf("BackupState ran %d times, want once", p.backupCalls)
	}
	if p.backupFrom != "v1.2.0" || p.backupTo != "1.1.0" {
		t.Errorf("backup labelled %s -> %s, want v1.2.0 -> 1.1.0", p.backupFrom, p.backupTo)
	}
	if string(p.exeWhenBacked) != string(current) {
		t.Error("the backup was taken after the swap, when the old build was already in place")
	}
	if dir := p.svc.Status().BackupDir; dir != p.backupDirGiven {
		t.Errorf("Status().BackupDir = %q, want %q — the UI has to be able to name it", dir, p.backupDirGiven)
	}
}

func TestRollbackRefusesWhenTheOldBinaryIsUnusable(t *testing.T) {
	// Nothing may move: not the binaries, not the servers. A refused rollback
	// has to leave a panel that is still running and still serving.
	p := newRollbackProbe(t, "v1.2.0", "1.1.0", nil)
	if err := os.WriteFile(p.exe+".old", []byte("not a program"), 0o755); err != nil {
		t.Fatal(err)
	}
	p.svc.SetPreviousVersion("1.1.0")
	before, err := os.ReadFile(p.exe)
	if err != nil {
		t.Fatal(err)
	}

	if err := p.svc.Rollback(context.Background()); err == nil {
		t.Fatal("Rollback went ahead with a binary that cannot run")
	}
	after, err := os.ReadFile(p.exe)
	if err != nil || string(after) != string(before) {
		t.Errorf("the running binary was touched: %v", err)
	}
	if p.stopped {
		t.Error("servers were stopped for a rollback that could not happen")
	}
	if p.backupCalls != 0 {
		t.Error("a backup was taken for a rollback that could not happen")
	}
	if phase := p.svc.Status().Phase; phase != PhaseIdle {
		t.Errorf("Phase = %q, want idle after a refusal", phase)
	}
}

func TestRollbackResumesTheServersWhenTheShutdownFails(t *testing.T) {
	// Same rule the update path follows: a panel on the old version with its
	// servers down is an outage, not a failed rollback.
	p := newRollbackProbe(t, "v1.2.0", "1.1.0", errWorldRefusedToSave)
	before, err := os.ReadFile(p.exe)
	if err != nil {
		t.Fatal(err)
	}

	if err := p.svc.Rollback(context.Background()); err == nil {
		t.Fatal("Rollback succeeded although the servers would not stop")
	}
	after, err := os.ReadFile(p.exe)
	if err != nil || string(after) != string(before) {
		t.Errorf("the binary was swapped after the shutdown failed: %v", err)
	}
	if !p.resumed {
		t.Error("servers stopped for the rollback were left down")
	}
}

func TestRollbackForwardTakesNoBackup(t *testing.T) {
	// Going back up to the build you just left adds fields rather than dropping
	// them, so there is nothing to protect and no directory to leave behind.
	p := newRollbackProbe(t, "v1.1.0", "1.2.0", nil)

	if err := p.svc.Rollback(context.Background()); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if p.backupCalls != 0 {
		t.Errorf("BackupState ran %d times going forward, want none", p.backupCalls)
	}
}
