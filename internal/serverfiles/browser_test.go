package serverfiles

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func newBrowser(t *testing.T) (*Browser, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "server.properties"), []byte("motd=hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugins", "config.yml"), []byte("a: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return New(dir), dir
}

func TestListSortsDirectoriesFirst(t *testing.T) {
	browser, _ := newBrowser(t)

	entries, err := browser.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if !entries[0].IsDir || entries[0].Name != "plugins" {
		t.Errorf("directories should sort first, got %+v", entries[0])
	}
	if entries[1].Name != "server.properties" || !entries[1].Editable {
		t.Errorf("expected an editable server.properties, got %+v", entries[1])
	}
	if entries[1].Path != "server.properties" {
		t.Errorf("path should be relative to the root, got %q", entries[1].Path)
	}
}

func TestListNestedPathBuildsRelativePaths(t *testing.T) {
	browser, _ := newBrowser(t)

	entries, err := browser.List("plugins")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Path != "plugins/config.yml" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}

// The jail is the whole point of this package: no request may name a location
// outside the instance directory, however it is spelled.
func TestPathTraversalIsRefused(t *testing.T) {
	browser, _ := newBrowser(t)

	escapes := []string{
		"..",
		"../",
		"../../etc/passwd",
		"plugins/../../etc/passwd",
		"/etc/passwd",
		"./../..",
		`..\..\windows\system32`,
		"plugins/../..",
	}

	for _, attempt := range escapes {
		t.Run(attempt, func(t *testing.T) {
			if _, err := browser.List(attempt); err == nil {
				t.Errorf("List(%q) should have been refused", attempt)
			}
			if _, err := browser.ReadText(attempt); err == nil {
				t.Errorf("ReadText(%q) should have been refused", attempt)
			}
			if err := browser.Remove(attempt); err == nil {
				t.Errorf("Remove(%q) should have been refused", attempt)
			}
			if err := browser.WriteText(attempt, "pwned"); err == nil {
				t.Errorf("WriteText(%q) should have been refused", attempt)
			}
		})
	}
}

// A symlink planted in the server directory — by a mod, a plugin, or an
// unpacked archive — must not become a way out of the jail.
func TestSymlinkOutOfTheRootIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on Windows")
	}
	browser, dir := newBrowser(t)

	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("top secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(dir, "escapedir")); err != nil {
		t.Fatal(err)
	}

	if content, err := browser.ReadText("escape.txt"); err == nil {
		t.Errorf("read through an escaping symlink returned %q", content)
	}
	if _, err := browser.List("escapedir"); err == nil {
		t.Error("listing through an escaping symlink should be refused")
	}

	// The link is still visible in the listing, flagged, so the operator can
	// see and delete it.
	entries, err := browser.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var found bool
	for _, entry := range entries {
		if entry.Name == "escape.txt" {
			found = true
			if !entry.Symlink {
				t.Error("symlink was not flagged in the listing")
			}
		}
	}
	if !found {
		t.Error("symlink missing from the listing")
	}
}

func TestReadWriteTextRoundTrip(t *testing.T) {
	browser, dir := newBrowser(t)

	if err := browser.WriteText("plugins/config.yml", "语言: 中文\n"); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	got, err := browser.ReadText("plugins/config.yml")
	if err != nil {
		t.Fatalf("ReadText: %v", err)
	}
	if got != "语言: 中文\n" {
		t.Errorf("content did not round trip: %q", got)
	}

	// The atomic-write temp file must not survive.
	entries, err := os.ReadDir(filepath.Join(dir, "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "hypercraft-tmp") {
			t.Errorf("temp file left behind: %s", entry.Name())
		}
	}
}

func TestReadTextRejectsOversizedFiles(t *testing.T) {
	browser, dir := newBrowser(t)

	big := make([]byte, MaxEditableBytes()+1)
	if err := os.WriteFile(filepath.Join(dir, "latest.log"), big, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := browser.ReadText("latest.log"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("expected ErrTooLarge, got %v", err)
	}

	entries, err := browser.List("")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name == "latest.log" && entry.Editable {
			t.Error("an oversized file should not be marked editable")
		}
	}
}

func TestCreateRefusesToOverwrite(t *testing.T) {
	browser, _ := newBrowser(t)

	_, _, err := browser.Create("server.properties", false)
	if !errors.Is(err, ErrExists) {
		t.Errorf("expected ErrExists, got %v", err)
	}

	// With overwrite the same call succeeds and truncates.
	file, closer, err := browser.Create("server.properties", true)
	if err != nil {
		t.Fatalf("Create with overwrite: %v", err)
	}
	if _, err := file.WriteString("motd=replaced\n"); err != nil {
		t.Fatal(err)
	}
	closer()

	got, err := browser.ReadText("server.properties")
	if err != nil || got != "motd=replaced\n" {
		t.Errorf("overwrite did not replace the file: %q (%v)", got, err)
	}
}

func TestRemoveDeletesDirectoriesRecursively(t *testing.T) {
	browser, dir := newBrowser(t)

	if err := os.MkdirAll(filepath.Join(dir, "world", "region"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "world", "region", "r.0.0.mca"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := browser.Remove("world"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "world")); !os.IsNotExist(err) {
		t.Error("directory was not removed")
	}
}

// Deleting the instance root belongs to instance deletion, which has its own
// confirmation flow; the file manager must not be a shortcut around it.
func TestRemoveRefusesTheRoot(t *testing.T) {
	browser, dir := newBrowser(t)

	for _, spelling := range []string{"", ".", "/", "./"} {
		if err := browser.Remove(spelling); err == nil {
			t.Errorf("Remove(%q) should have been refused", spelling)
		}
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("instance directory was damaged: %v", err)
	}
}

func TestRename(t *testing.T) {
	browser, dir := newBrowser(t)

	if err := browser.Rename("server.properties", "plugins/moved.properties"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugins", "moved.properties")); err != nil {
		t.Errorf("file was not moved: %v", err)
	}

	if err := browser.Rename("plugins/moved.properties", "plugins/config.yml"); !errors.Is(err, ErrExists) {
		t.Errorf("renaming onto an existing file should fail with ErrExists, got %v", err)
	}
	if err := browser.Rename("plugins/moved.properties", "../escaped"); err == nil {
		t.Error("renaming out of the root should be refused")
	}
}

func TestMkdir(t *testing.T) {
	browser, dir := newBrowser(t)

	if err := browser.Mkdir("plugins/EssentialsX/userdata"); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "plugins", "EssentialsX", "userdata"))
	if err != nil || !info.IsDir() {
		t.Errorf("nested directory was not created: %v", err)
	}
}

func TestCopyLimitedRejectsOversizedUploads(t *testing.T) {
	var sink strings.Builder

	if _, err := CopyLimited(&sink, strings.NewReader("12345"), 5); err != nil {
		t.Errorf("a file exactly at the limit should be accepted, got %v", err)
	}
	if _, err := CopyLimited(&sink, strings.NewReader("123456"), 5); !errors.Is(err, ErrTooLarge) {
		t.Errorf("expected ErrTooLarge, got %v", err)
	}
}

func TestMissingInstanceDirectoryIsNotFound(t *testing.T) {
	browser := New(filepath.Join(t.TempDir(), "gone"))

	if _, err := browser.List(""); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
