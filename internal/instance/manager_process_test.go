//go:build !windows

// Manager tests that need a running server. The fake server is a shell script,
// so they live apart from the rest of the manager tests: with them in the same
// file the whole package failed to build on Windows, taking the tests that have
// nothing to do with processes — slugify, directory naming, listing — down with
// them on the platform most operators run.

package instance

import (
	"testing"
	"time"
)

func TestDeleteRefusesWhileRunning(t *testing.T) {
	mgr, _ := newTestManager(t)

	created, err := mgr.Create(Config{Name: "busy"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	dir := created.Config().Directory
	updated := created.Config()
	updated.Command = fakeServer(t, dir, wellBehavedServer)
	if _, err := mgr.Update(created.Config().ID, updated); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if err := created.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForState(t, created, StateRunning)
	t.Cleanup(func() { _ = created.Kill() })

	if err := mgr.Delete(created.Config().ID, false); err == nil {
		t.Error("deleting a running instance should fail")
	}
	if _, err := mgr.Get(created.Config().ID); err != nil {
		t.Error("the instance should still be registered after a refused delete")
	}
}

// StopAll is the shutdown half of a panel update. It has to leave the
// instances usable — the update may yet fail, and a panel that came back on the
// old binary with dead instance objects would be worse than the update it was
// trying to perform.
func TestStopAllEmptiesTheMachineAndLeavesTheInstancesUsable(t *testing.T) {
	mgr, _ := newTestManager(t)

	var running []*Instance
	for _, name := range []string{"survival", "creative"} {
		created, err := mgr.Create(Config{Name: name})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		cfg := created.Config()
		cfg.Command = fakeServer(t, cfg.Directory, wellBehavedServer)
		if _, err := mgr.Update(cfg.ID, cfg); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if err := created.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		waitForState(t, created, StateRunning)
		t.Cleanup(func() { _ = created.Kill() })
		running = append(running, created)
	}

	var last Stopping
	stopped := mgr.StopAll(10*time.Second, func(progress Stopping) { last = progress })

	if len(stopped) != 2 {
		t.Errorf("StopAll reported %d stopped, want the two that were running", len(stopped))
	}
	if last.Total != 2 || last.Stopped != 2 || len(last.Pending) != 0 {
		t.Errorf("last report = %+v, want 2/2 with nothing pending", last)
	}
	for _, inst := range running {
		if inst.State().Running() {
			t.Errorf("%s is still running after StopAll", inst.Config().Name)
		}
	}

	// Usable afterwards: this is what the update falls back on when the
	// download it was racing turns out to be corrupt.
	mgr.StartEach(stopped)
	for _, inst := range running {
		waitForState(t, inst, StateRunning)
	}
}
