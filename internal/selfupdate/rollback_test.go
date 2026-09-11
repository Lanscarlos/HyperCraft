package selfupdate

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

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
