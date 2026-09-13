package javaruntime

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMigrateRegistersUnknownPaths(t *testing.T) {
	reg := NewRegistry(filepath.Join(t.TempDir(), "java-registry.json"), testLogger())
	store := NewStore(t.TempDir())

	added := MigrateInstanceJava(context.Background(), store, reg,
		[]string{"/opt/jdk21/bin/java", "/opt/jdk17/bin/java"})
	if added != 2 {
		t.Fatalf("want 2 registered, got %d", added)
	}
	if got := reg.List(); len(got) != 2 {
		t.Fatalf("registry = %+v", got)
	}
	for _, entry := range reg.List() {
		if entry.AddedBy != AddedMigrated {
			t.Fatalf("entry %+v should be marked migrated", entry)
		}
	}
}

// A path the panel cannot probe is still recorded. It is what an instance is
// actually launching with, which is more certain than any detection.
func TestMigrateKeepsUnprobeablePaths(t *testing.T) {
	reg := NewRegistry(filepath.Join(t.TempDir(), "java-registry.json"), testLogger())

	MigrateInstanceJava(context.Background(), NewStore(t.TempDir()), reg,
		[]string{"/nowhere/jdk8/bin/java"})

	list := reg.List()
	if len(list) != 1 {
		t.Fatalf("want the unprobeable path kept, got %+v", list)
	}
	if list[0].JavaPath != "/nowhere/jdk8/bin/java" {
		t.Fatalf("the original path must survive verbatim: %+v", list[0])
	}
	if list[0].Version != "" {
		t.Fatalf("a version the panel could not read must stay blank, got %q", list[0].Version)
	}
}

// "java" means "follow PATH", and an instance that says so said it on
// purpose. Pinning it to whatever absolute path PATH resolves to today would
// be the panel making a decision nobody asked it to make.
func TestMigrateKeepsBareJavaVerbatim(t *testing.T) {
	reg := NewRegistry(filepath.Join(t.TempDir(), "java-registry.json"), testLogger())

	MigrateInstanceJava(context.Background(), NewStore(t.TempDir()), reg, []string{"java"})

	list := reg.List()
	if len(list) != 1 || list[0].JavaPath != "java" {
		t.Fatalf(`want a single entry whose path is still "java", got %+v`, list)
	}
}

func TestMigrateDeduplicatesAndSkipsKnown(t *testing.T) {
	reg := NewRegistry(filepath.Join(t.TempDir(), "java-registry.json"), testLogger())
	if _, err := reg.Add(Entry{JavaPath: "/opt/jdk21/bin/java", AddedBy: AddedManual}); err != nil {
		t.Fatal(err)
	}

	added := MigrateInstanceJava(context.Background(), NewStore(t.TempDir()), reg,
		[]string{"/opt/jdk21/bin/java", "/opt/jdk17/bin/java", "/opt/jdk17/bin/java"})
	if added != 1 {
		t.Fatalf("only the one new path should be registered, got %d", added)
	}
	if got := reg.List(); len(got) != 2 {
		t.Fatalf("registry = %+v", got)
	}
	// The pre-existing entry keeps the label it was added with.
	if got, _ := reg.Get(EntryID("/opt/jdk21/bin/java")); got.AddedBy != AddedManual {
		t.Fatalf("migration must not relabel an existing entry: %+v", got)
	}
}

// A runtime under the runtimes root is already available; registering it again
// would put the same Java in the list twice.
func TestMigrateSkipsManagedRuntimes(t *testing.T) {
	root := t.TempDir()
	launcher := fakeRuntimeDir(t, root, "temurin-21", "21.0.12")
	reg := NewRegistry(filepath.Join(t.TempDir(), "java-registry.json"), testLogger())

	added := MigrateInstanceJava(context.Background(), NewStore(root), reg, []string{launcher})
	if added != 0 {
		t.Fatalf("a managed runtime needs no registry entry, got %d", added)
	}
}

func TestMigrateIgnoresBlankPaths(t *testing.T) {
	reg := NewRegistry(filepath.Join(t.TempDir(), "java-registry.json"), testLogger())
	if added := MigrateInstanceJava(context.Background(), NewStore(t.TempDir()), reg, []string{"", "   "}); added != 0 {
		t.Fatalf("blank paths are not a java, got %d", added)
	}
}
