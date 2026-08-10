package serverjar

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLibraryListsJarsAndSkipsEverythingElse(t *testing.T) {
	library := NewLibrary(t.TempDir())
	write(t, library, "paper-1.21.11-132.jar", "core")
	write(t, library, "notes.txt", "not a core")
	write(t, library, "half-done.jar"+partSuffix, "in flight")
	if err := os.Mkdir(filepath.Join(library.Root(), "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cores, err := library.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(cores) != 1 || cores[0].ID != "paper-1.21.11-132.jar" {
		t.Fatalf("unexpected listing: %+v", cores)
	}
	// A jar the panel did not download is still usable; it just has nothing to
	// say about which build it is.
	if !cores[0].Imported || cores[0].Build != 0 {
		t.Errorf("a hand-placed jar should be marked imported: %+v", cores[0])
	}
	if cores[0].Size != int64(len("core")) {
		t.Errorf("size is %d", cores[0].Size)
	}
}

func TestLibraryListsNewestFirst(t *testing.T) {
	library := NewLibrary(t.TempDir())
	for _, name := range []string{"old.jar", "new.jar"} {
		write(t, library, name, name)
	}
	older := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(library.Root(), "old.jar"), older, older); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	cores, err := library.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(cores) != 2 || cores[0].ID != "new.jar" {
		t.Fatalf("expected the newest core first, got %+v", cores)
	}
}

func TestLibraryRemembersDownloadedMetadata(t *testing.T) {
	library := NewLibrary(t.TempDir())
	write(t, library, "paper-1.21.11-132.jar", "core")

	added := time.Now().Add(-time.Minute).Truncate(time.Second)
	err := library.record(Core{
		ID: "paper-1.21.11-132.jar", FileName: "paper-1.21.11-132.jar",
		Project: "paper", ProjectName: "Paper", Kind: "server",
		Version: "1.21.11", Build: 132, Channel: "STABLE", AddedAt: added,
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	core, err := library.Get("paper-1.21.11-132.jar")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if core.Version != "1.21.11" || core.Build != 132 || core.Imported {
		t.Fatalf("metadata was not joined onto the file: %+v", core)
	}
	if !core.AddedAt.Equal(added) {
		t.Errorf("addedAt is %v, want %v", core.AddedAt, added)
	}
	// Size always comes from disk, never from the index: the file is the truth.
	if core.Size != int64(len("core")) {
		t.Errorf("size is %d", core.Size)
	}
}

func TestLibraryRemoveDropsFileAndMetadata(t *testing.T) {
	library := NewLibrary(t.TempDir())
	write(t, library, "paper-1.21.11-132.jar", "core")
	if err := library.record(Core{ID: "paper-1.21.11-132.jar", Version: "1.21.11"}); err != nil {
		t.Fatalf("record: %v", err)
	}

	if err := library.Remove("paper-1.21.11-132.jar"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := library.Get("paper-1.21.11-132.jar"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	// Re-adding the same name by hand must not resurrect the old build number.
	write(t, library, "paper-1.21.11-132.jar", "different core")
	core, err := library.Get("paper-1.21.11-132.jar")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !core.Imported || core.Version != "" {
		t.Errorf("stale metadata survived deletion: %+v", core)
	}
}

func TestLibraryRejectsPathsInIDs(t *testing.T) {
	library := NewLibrary(t.TempDir())

	for _, id := range []string{"", ".", "..", "../escape.jar", `sub\escape.jar`, "sub/escape.jar"} {
		if _, err := library.Get(id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Get(%q) = %v, want ErrInvalidID", id, err)
		}
		if err := library.Remove(id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Remove(%q) = %v, want ErrInvalidID", id, err)
		}
	}
}

func TestLibraryOnMissingDirectoryIsEmpty(t *testing.T) {
	library := NewLibrary(filepath.Join(t.TempDir(), "not-created-yet"))

	cores, err := library.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(cores) != 0 {
		t.Errorf("expected an empty library, got %+v", cores)
	}
}
