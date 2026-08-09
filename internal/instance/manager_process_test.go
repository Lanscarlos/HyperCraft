//go:build !windows

// Manager tests that need a running server. The fake server is a shell script,
// so they live apart from the rest of the manager tests: with them in the same
// file the whole package failed to build on Windows, taking the tests that have
// nothing to do with processes — slugify, directory naming, listing — down with
// them on the platform most operators run.

package instance

import "testing"

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
