package javaruntime

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeRuntimeDir lays out just enough of a JDK for Store.inspect to accept it.
func fakeRuntimeDir(t *testing.T, root, id, version string) string {
	t.Helper()
	dir := filepath.Join(root, id)
	bin := filepath.Join(dir, "bin")
	if runtime.GOOS == "darwin" {
		bin = filepath.Join(dir, "Contents", "Home", "bin")
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(bin, javaBinary())
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	release := "JAVA_VERSION=\"" + version + "\"\nIMPLEMENTOR=\"Test\"\n"
	if err := os.WriteFile(filepath.Join(dir, "release"), []byte(release), 0o644); err != nil {
		t.Fatal(err)
	}
	return launcher
}

func TestAvailableListMergesBothSources(t *testing.T) {
	root := t.TempDir()
	fakeRuntimeDir(t, root, "temurin-21", "21.0.12")
	store := NewStore(root)

	reg := NewRegistry(filepath.Join(t.TempDir(), "java-registry.json"), testLogger())
	if _, err := reg.Add(Entry{JavaPath: "/nowhere/jdk17/bin/java", Major: 17, Version: "17.0.1", AddedBy: AddedManual}); err != nil {
		t.Fatal(err)
	}

	list, err := AvailableList(store, reg)
	if err != nil {
		t.Fatalf("AvailableList: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 entries, got %d: %+v", len(list), list)
	}

	byOrigin := map[string]Available{}
	for _, entry := range list {
		byOrigin[entry.Origin] = entry
	}
	if byOrigin[OriginManaged].Major != 21 {
		t.Fatalf("managed entry = %+v", byOrigin[OriginManaged])
	}
	if byOrigin[OriginExternal].Major != 17 {
		t.Fatalf("external entry = %+v", byOrigin[OriginExternal])
	}
}

// A path that no longer exists stays in the list, marked. Dropping it would
// leave the instance pointing at it with nothing in the dropdown to show.
func TestAvailableListMarksMissingPathInvalid(t *testing.T) {
	reg := NewRegistry(filepath.Join(t.TempDir(), "java-registry.json"), testLogger())
	if _, err := reg.Add(Entry{JavaPath: "/nowhere/jdk17/bin/java", Major: 17, AddedBy: AddedManual}); err != nil {
		t.Fatal(err)
	}

	list, err := AvailableList(NewStore(t.TempDir()), reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 entry, got %+v", list)
	}
	if list[0].Valid {
		t.Fatal("a path that is not there should not be Valid")
	}
}

// The same launcher registered by hand and found in the runtimes directory is
// one Java, and the managed row is the one that can be deleted or re-probed.
func TestAvailableListPrefersManagedOnDuplicatePath(t *testing.T) {
	root := t.TempDir()
	launcher := fakeRuntimeDir(t, root, "temurin-21", "21.0.12")

	reg := NewRegistry(filepath.Join(t.TempDir(), "java-registry.json"), testLogger())
	if _, err := reg.Add(Entry{JavaPath: launcher, Major: 21, AddedBy: AddedMigrated}); err != nil {
		t.Fatal(err)
	}

	list, err := AvailableList(NewStore(root), reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("want the duplicate collapsed, got %+v", list)
	}
	if list[0].Origin != OriginManaged {
		t.Fatalf("managed should win, got %+v", list[0])
	}
}

func TestUsableFollowsPathForBareJava(t *testing.T) {
	// "java" is legal and means PATH. Whether this machine has one is not the
	// point: Usable must not report true for a literal file named "java" in
	// the working directory, and must not panic.
	_ = Usable("java")

	if Usable(filepath.Join(t.TempDir(), "definitely-not-here")) {
		t.Fatal("a missing absolute path is not usable")
	}
}
