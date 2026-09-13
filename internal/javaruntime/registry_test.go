package javaruntime

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRegistryAddListRemove(t *testing.T) {
	file := filepath.Join(t.TempDir(), "java-registry.json")
	reg := NewRegistry(file, testLogger())

	if got := reg.List(); len(got) != 0 {
		t.Fatalf("a fresh registry should be empty, got %d", len(got))
	}

	added, err := reg.Add(Entry{JavaPath: "/opt/jdk21/bin/java", Major: 21, Version: "21.0.12", AddedBy: AddedManual})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if added.ID == "" {
		t.Fatal("Add should assign an ID")
	}
	if added.AddedAt.IsZero() {
		t.Fatal("Add should stamp AddedAt")
	}

	if got := reg.List(); len(got) != 1 || got[0].JavaPath != "/opt/jdk21/bin/java" {
		t.Fatalf("List after Add = %+v", got)
	}

	got, err := reg.Get(added.ID)
	if err != nil || got.Major != 21 {
		t.Fatalf("Get = %+v, %v", got, err)
	}

	if err := reg.Remove(added.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := reg.List(); len(got) != 0 {
		t.Fatalf("List after Remove = %+v", got)
	}
}

func TestRegistryPersistsAcrossReload(t *testing.T) {
	file := filepath.Join(t.TempDir(), "java-registry.json")
	reg := NewRegistry(file, testLogger())
	if _, err := reg.Add(Entry{JavaPath: "/opt/jdk17/bin/java", Major: 17, AddedBy: AddedDetected}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	reloaded := NewRegistry(file, testLogger())
	if got := reloaded.List(); len(got) != 1 || got[0].Major != 17 {
		t.Fatalf("reload lost the entry: %+v", got)
	}
}

func TestRegistryRejectsDuplicatePath(t *testing.T) {
	file := filepath.Join(t.TempDir(), "java-registry.json")
	reg := NewRegistry(file, testLogger())
	if _, err := reg.Add(Entry{JavaPath: "/opt/jdk21/bin/java", AddedBy: AddedManual}); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	if _, err := reg.Add(Entry{JavaPath: "/opt/jdk21/bin/java", AddedBy: AddedManual}); err == nil {
		t.Fatal("a second Add of the same path should fail")
	}
}

// A registry the panel cannot parse must not stop the panel from starting: it
// is a list of conveniences, not a source of truth about anything running.
func TestRegistryTreatsCorruptFileAsEmpty(t *testing.T) {
	file := filepath.Join(t.TempDir(), "java-registry.json")
	if err := os.WriteFile(file, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry(file, testLogger())
	if got := reg.List(); len(got) != 0 {
		t.Fatalf("corrupt registry should read as empty, got %+v", got)
	}
	if _, err := reg.Add(Entry{JavaPath: "/opt/jdk21/bin/java", AddedBy: AddedManual}); err != nil {
		t.Fatalf("a corrupt registry must still accept writes: %v", err)
	}
	if got := NewRegistry(file, testLogger()).List(); len(got) != 1 {
		t.Fatalf("the rewritten file should hold one entry, got %+v", got)
	}
}

func TestRegistryRemoveUnknownIsNotFound(t *testing.T) {
	reg := NewRegistry(filepath.Join(t.TempDir(), "java-registry.json"), testLogger())
	if err := reg.Remove("nope"); err == nil {
		t.Fatal("removing an unknown id should fail")
	}
}

func TestEntryIDIsStableForTheSamePath(t *testing.T) {
	if EntryID("/opt/jdk21/bin/java") != EntryID("/opt/jdk21/bin/java") {
		t.Fatal("EntryID should be deterministic")
	}
	if EntryID("/opt/jdk21/bin/java") == EntryID("/opt/jdk17/bin/java") {
		t.Fatal("different paths should get different ids")
	}
}
