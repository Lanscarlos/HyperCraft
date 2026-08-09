package api

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
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
