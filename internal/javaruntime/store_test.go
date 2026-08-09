package javaruntime

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeFakeRuntime lays out a directory that looks like an unpacked JDK.
func writeFakeRuntime(t *testing.T, root, id, release string) string {
	t.Helper()

	dir := filepath.Join(root, id)
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", javaBinary()), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write java: %v", err)
	}
	if release != "" {
		if err := os.WriteFile(filepath.Join(dir, "release"), []byte(release), 0o644); err != nil {
			t.Fatalf("write release: %v", err)
		}
	}
	return dir
}

func TestListReadsReleaseFileAndSortsNewestFirst(t *testing.T) {
	root := t.TempDir()
	writeFakeRuntime(t, root, "temurin-17.0.9-7-jre",
		"JAVA_VERSION=\"17.0.9\"\nIMPLEMENTOR=\"Eclipse Adoptium\"\nIMAGE_TYPE=\"JRE\"\n")
	writeFakeRuntime(t, root, "temurin-21.0.1-12-jdk",
		"JAVA_VERSION=\"21.0.1\"\nIMPLEMENTOR=\"Eclipse Adoptium\"\nIMAGE_TYPE=\"JDK\"\n")
	// A directory with nothing runnable in it is not a runtime.
	if err := os.MkdirAll(filepath.Join(root, "leftovers"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	runtimes, err := NewStore(root).List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runtimes) != 2 {
		t.Fatalf("expected 2 runtimes, got %+v", runtimes)
	}
	if runtimes[0].Major != 21 || runtimes[0].ImageType != "jdk" {
		t.Errorf("newest should come first: %+v", runtimes[0])
	}
	if runtimes[1].Version != "17.0.9" || runtimes[1].Vendor != "Eclipse Adoptium" {
		t.Errorf("release file not parsed: %+v", runtimes[1])
	}
	if runtimes[0].JavaPath == "" {
		t.Errorf("javaPath should point at the launcher")
	}
}

// An operator can unpack their own JDK into the directory; every OpenJDK build
// ships a release file, so it shows up like any other.
func TestListPicksUpAHandPlacedRuntime(t *testing.T) {
	root := t.TempDir()
	writeFakeRuntime(t, root, "my-own-jdk", "JAVA_VERSION=\"1.8.0_502\"\nIMPLEMENTOR=\"Temurin\"\n")

	runtimes, err := NewStore(root).List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runtimes) != 1 || runtimes[0].Major != 8 {
		t.Fatalf("expected a Java 8 runtime, got %+v", runtimes)
	}
}

func TestListOfMissingDirectoryIsEmpty(t *testing.T) {
	runtimes, err := NewStore(filepath.Join(t.TempDir(), "nope")).List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runtimes) != 0 {
		t.Errorf("expected no runtimes, got %+v", runtimes)
	}
}

func TestGetAndRemove(t *testing.T) {
	root := t.TempDir()
	dir := writeFakeRuntime(t, root, "temurin-21.0.1-12-jre", "JAVA_VERSION=\"21.0.1\"\n")
	store := NewStore(root)

	if _, err := store.Get("temurin-21.0.1-12-jre"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := store.Remove("temurin-21.0.1-12-jre"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Errorf("directory survived Remove")
	}
	if err := store.Remove("temurin-21.0.1-12-jre"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Remove: got %v, want ErrNotFound", err)
	}
}

// The ID names a directory under the runtimes root and nothing else: it
// arrives in a URL path, and Remove deletes recursively.
func TestIDsThatEscapeAreRejected(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, bad := range []string{"", ".", "..", "../../etc", "a/b", `a\b`} {
		if _, err := store.Get(bad); !errors.Is(err, ErrInvalidID) && !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q): got %v", bad, err)
		}
		if err := store.Remove(bad); !errors.Is(err, ErrInvalidID) && !errors.Is(err, ErrNotFound) {
			t.Errorf("Remove(%q): got %v", bad, err)
		}
	}
}

func TestCurrentPlatformIsKnown(t *testing.T) {
	platform, err := CurrentPlatform()
	if err != nil {
		t.Skipf("no Adoptium builds for this platform: %v", err)
	}
	if platform.OS == "" || platform.Arch == "" {
		t.Errorf("platform not filled in: %+v", platform)
	}
}
