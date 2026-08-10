package plugin

import (
	"path/filepath"
	"testing"
	"time"
)

func newPending(t *testing.T) *Pending {
	t.Helper()
	return NewPending(filepath.Join(t.TempDir(), "pending.json"))
}

func TestOnlyChangesTheRunningProcessMissedArePending(t *testing.T) {
	pending := newPending(t)
	started := time.Now()

	if err := pending.Record("srv", Change{Key: "plugin:old", Name: "Old", Action: ActionDisable, At: started.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := pending.Record("srv", Change{Key: "plugin:new", Name: "New", Action: ActionInstall, At: started.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}

	changes := pending.Since("srv", started)
	if len(changes) != 1 || changes[0].Key != "plugin:new" {
		t.Fatalf("pending = %+v", changes)
	}

	// A restart is what clears this, and it clears it by being a later start
	// time — no cleanup call to forget on some path that might not run.
	if changes := pending.Since("srv", started.Add(time.Hour)); len(changes) != 0 {
		t.Errorf("a restart should clear everything, got %+v", changes)
	}
}

func TestAStoppedInstanceHasNothingPending(t *testing.T) {
	pending := newPending(t)
	if err := pending.Record("srv", Change{Key: "plugin:a", Name: "A", Action: ActionInstall}); err != nil {
		t.Fatal(err)
	}
	// Nothing is running that could have missed the change.
	if changes := pending.Since("srv", time.Time{}); len(changes) != 0 {
		t.Errorf("stopped instance = %+v", changes)
	}
}

func TestFlippingAPluginTwiceIsOnePendingChange(t *testing.T) {
	pending := newPending(t)
	started := time.Now()

	for _, action := range []string{ActionDisable, ActionEnable, ActionDisable} {
		if err := pending.Record("srv", Change{
			Key: "plugin:chatty", Name: "Chatty", Action: action, At: started.Add(time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}

	changes := pending.Since("srv", started)
	if len(changes) != 1 {
		t.Fatalf("expected one change, got %+v", changes)
	}
	if changes[0].Action != ActionDisable {
		t.Errorf("the newest action should win, got %q", changes[0].Action)
	}
}

func TestInstallingAndRemovingLeavesNothingPending(t *testing.T) {
	pending := newPending(t)
	started := time.Now()

	if err := pending.Record("srv", Change{Key: "plugin:oops", Name: "Oops", Action: ActionInstall, At: started.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := pending.Record("srv", Change{Key: "plugin:oops", Name: "Oops", Action: ActionRemove, At: started.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}

	// The running server never saw it arrive, so there is nothing for it to
	// see leave. A banner counting one pending change here would be counting
	// clicks rather than differences.
	if changes := pending.Since("srv", started); len(changes) != 0 {
		t.Errorf("expected nothing pending, got %+v", changes)
	}
}

func TestPendingSurvivesAReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	started := time.Now()

	first := NewPending(path)
	if err := first.Record("srv", Change{Key: "plugin:a", Name: "A", Action: ActionInstall, At: started.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}

	second := NewPending(path)
	if changes := second.Since("srv", started); len(changes) != 1 {
		t.Fatalf("after reload = %+v", changes)
	}
}
