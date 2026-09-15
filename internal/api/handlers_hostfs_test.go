package api

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/lanscarlos/hypercraft/internal/hostfs"
)

func (e *testEnv) browse(dir string) hostDirResponse {
	e.t.Helper()

	resp := e.do(http.MethodGet, "/api/fs?path="+url.QueryEscape(dir), nil)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		e.t.Fatalf("browsing %q: expected 200, got %d", dir, resp.StatusCode)
	}
	var listing hostDirResponse
	decodeBody(e.t, resp, &listing)
	return listing
}

func TestBrowseListsDirectoriesAndJars(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "worlds"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{"server.jar", "eula.txt", ".hidden"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	listing := env.browse(root)
	if !listing.Exists {
		t.Fatalf("expected the directory to exist: %+v", listing)
	}
	// Directories first, then files, and dotfiles are noise in a path picker.
	names := make([]string, 0, len(listing.Entries))
	for _, entry := range listing.Entries {
		names = append(names, entry.Name)
	}
	if len(names) != 3 || names[0] != "worlds" {
		t.Fatalf("unexpected entries %v", names)
	}
	for _, name := range names {
		if name == ".hidden" {
			t.Errorf("hidden entries should not be listed: %v", names)
		}
	}
	if len(listing.Jars) != 1 || listing.Jars[0].Name != "server.jar" {
		t.Errorf("jar listing = %+v", listing.Jars)
	}
	if listing.Parent != filepath.Dir(root) {
		t.Errorf("parent = %q, want %q", listing.Parent, filepath.Dir(root))
	}
}

// Pointing an instance at a directory that does not exist yet is normal, so
// the picker reports it rather than failing.
func TestBrowseMissingDirectoryIsNotAnError(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	missing := filepath.Join(t.TempDir(), "not-created-yet")
	listing := env.browse(missing)
	if listing.Exists {
		t.Errorf("expected exists=false for %q", missing)
	}
	if listing.Path != missing || listing.Parent == "" {
		t.Errorf("a missing directory still needs a path and a parent: %+v", listing)
	}
}

// An empty path is the panel's own servers root, which is where most people
// keep their servers and the sensible place for the picker to open.
func TestBrowseDefaultsToTheServersRoot(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	listing := env.browse("")
	if listing.Path != env.paths.ServersRoot() {
		t.Errorf("default path = %q, want %q", listing.Path, env.paths.ServersRoot())
	}
	if len(listing.Shortcuts) == 0 || listing.Shortcuts[0].Path != env.paths.ServersRoot() {
		t.Errorf("expected the servers root offered first: %+v", listing.Shortcuts)
	}
}

func TestBrowseRejectsRelativePaths(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	for _, path := range []string{"relative/path", "../escape", "."} {
		resp := env.do(http.MethodGet, "/api/fs?path="+url.QueryEscape(path), nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("path %q: expected 400, got %d", path, resp.StatusCode)
		}
	}
}

func TestBrowseRequiresASession(t *testing.T) {
	env := newTestEnv(t)

	resp := env.do(http.MethodGet, "/api/fs?path=/", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without a session, got %d", resp.StatusCode)
	}
}

func (e *testEnv) shortcuts() []hostfs.Shortcut {
	e.t.Helper()
	return e.browse(e.t.TempDir()).Shortcuts
}

func shortcutFor(list []hostfs.Shortcut, path string) (hostfs.Shortcut, bool) {
	for _, entry := range list {
		if entry.Path == path {
			return entry, true
		}
	}
	return hostfs.Shortcut{}, false
}

func TestHostShortcutAddRenameRemove(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	dir := t.TempDir()

	resp := env.do(http.MethodPost, "/api/fs/shortcuts",
		map[string]string{"label": "我的服务端", "path": dir})
	if resp.StatusCode != http.StatusCreated {
		resp.Body.Close()
		t.Fatalf("adding a shortcut: expected 201, got %d", resp.StatusCode)
	}
	var added hostShortcutsResponse
	decodeBody(t, resp, &added)
	saved, ok := shortcutFor(added.Shortcuts, dir)
	if !ok || saved.Label != "我的服务端" || !saved.Custom || saved.ID == "" {
		t.Fatalf("unexpected shortcut list %+v", added.Shortcuts)
	}

	// It has to show up in a listing too — that is where the picker reads it.
	if _, ok := shortcutFor(env.shortcuts(), dir); !ok {
		t.Fatal("the saved shortcut is missing from a listing")
	}

	// Renaming keeps the id, which is what stops a rename detaching the chip.
	resp = env.do(http.MethodPut, "/api/fs/shortcuts/"+saved.ID,
		map[string]string{"label": "生存服的家"})
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("renaming: expected 200, got %d", resp.StatusCode)
	}
	var renamed hostShortcutsResponse
	decodeBody(t, resp, &renamed)
	after, ok := shortcutFor(renamed.Shortcuts, dir)
	if !ok || after.Label != "生存服的家" || after.ID != saved.ID {
		t.Fatalf("rename changed more than the label: %+v", after)
	}

	resp = env.do(http.MethodDelete, "/api/fs/shortcuts/"+saved.ID, nil)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("removing: expected 200, got %d", resp.StatusCode)
	}
	var removed hostShortcutsResponse
	decodeBody(t, resp, &removed)
	if _, ok := shortcutFor(removed.Shortcuts, dir); ok {
		t.Fatalf("the shortcut survived its own deletion: %+v", removed.Shortcuts)
	}
}

func TestHostShortcutRefusesDuplicateAndRelative(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	dir := t.TempDir()
	resp := env.do(http.MethodPost, "/api/fs/shortcuts", map[string]string{"path": dir})
	if resp.StatusCode != http.StatusCreated {
		resp.Body.Close()
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// The same directory again would be dropped by the merge's dedup and the
	// operator would see their click do nothing, so it is refused out loud.
	resp = env.do(http.MethodPost, "/api/fs/shortcuts", map[string]string{"path": dir})
	if resp.StatusCode != http.StatusConflict {
		resp.Body.Close()
		t.Fatalf("a duplicate path: expected 409, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.do(http.MethodPost, "/api/fs/shortcuts", map[string]string{"path": "servers"})
	if resp.StatusCode != http.StatusBadRequest {
		resp.Body.Close()
		t.Fatalf("a relative path: expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// An empty label is the common case — the operator clicks 收藏 and does not
// type anything — so it falls back to the directory's own name rather than
// leaving a nameless chip.
func TestHostShortcutLabelDefaultsToDirectoryName(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	dir := filepath.Join(t.TempDir(), "survival")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	resp := env.do(http.MethodPost, "/api/fs/shortcuts", map[string]string{"path": dir})
	if resp.StatusCode != http.StatusCreated {
		resp.Body.Close()
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var added hostShortcutsResponse
	decodeBody(t, resp, &added)
	saved, ok := shortcutFor(added.Shortcuts, dir)
	if !ok || saved.Label != "survival" {
		t.Fatalf("expected the directory name as the label, got %+v", added.Shortcuts)
	}
}
