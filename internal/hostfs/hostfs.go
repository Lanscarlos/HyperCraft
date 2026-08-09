// Package hostfs lists directories on the machine the panel runs on, so the
// operator can point an instance at a path instead of typing it from memory.
//
// This is deliberately read-only and deliberately not jailed: an instance
// directory may legitimately be anywhere — a second disk, a NAS mount, a
// pre-existing server someone is adopting — and the panel already accepts any
// absolute path in that field. Nothing here opens, reads or writes a file; it
// reports names, sizes and which entries are directories, and callers are
// expected to keep it behind authentication.
package hostfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// ErrInvalidPath is returned for anything that is not an absolute path.
var ErrInvalidPath = errors.New("invalid path")

// maxEntries caps one listing. A directory with a hundred thousand files is
// not something anyone picks a path out of, and sending it all would cost more
// than it tells the operator.
const maxEntries = 2000

// Entry is one member of a directory. Only what a path picker needs: files are
// listed too, so a directory that already holds a server does not look empty.
type Entry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
}

// Jar is a launchable server jar found in the listed directory.
type Jar struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// Shortcut is a starting point offered beside the listing.
type Shortcut struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

// Listing is one directory as the path picker sees it.
type Listing struct {
	Path string `json:"path"`
	// Parent is empty at a filesystem root, which is where "go up" stops.
	Parent string `json:"parent"`
	// Exists is false for a path that has not been created yet. That is not an
	// error: creating an instance in a directory that does not exist is normal,
	// and the picker says so rather than refusing to show it.
	Exists    bool    `json:"exists"`
	Separator string  `json:"separator"`
	Entries   []Entry `json:"entries"`
	// Jars are the .jar files directly in this directory, which is what makes
	// the launch settings able to offer a dropdown for a hand-typed path.
	Jars []Jar `json:"jars"`
	// Truncated is set when the directory holds more than maxEntries members.
	Truncated bool `json:"truncated"`
	// Error explains a directory that exists but could not be read, usually
	// permissions. The listing is still returned, empty.
	Error string `json:"error,omitempty"`
}

// List describes one directory. The path must be absolute.
func List(dir string) (Listing, error) {
	if strings.TrimSpace(dir) == "" || !filepath.IsAbs(dir) {
		return Listing{}, fmt.Errorf("%w: %q is not an absolute path", ErrInvalidPath, dir)
	}
	if strings.ContainsRune(dir, 0) {
		return Listing{}, fmt.Errorf("%w: path contains a null byte", ErrInvalidPath)
	}

	dir = filepath.Clean(dir)
	listing := Listing{
		Path:      dir,
		Parent:    parentOf(dir),
		Separator: string(filepath.Separator),
		Entries:   []Entry{},
		Jars:      []Jar{},
	}

	info, err := os.Stat(dir)
	switch {
	case err != nil:
		return listing, nil // does not exist yet; Exists stays false
	case !info.IsDir():
		return Listing{}, fmt.Errorf("%w: %s is a file, not a directory", ErrInvalidPath, dir)
	}
	listing.Exists = true

	members, err := os.ReadDir(dir)
	if err != nil {
		listing.Error = err.Error()
		return listing, nil
	}
	if len(members) > maxEntries {
		members = members[:maxEntries]
		listing.Truncated = true
	}

	for _, member := range members {
		// Hidden entries are noise in a path picker and hide nothing useful:
		// an operator who wants .minecraft can still type it.
		if strings.HasPrefix(member.Name(), ".") {
			continue
		}
		info, err := member.Info()
		if err != nil {
			continue // vanished between listing and stat
		}
		entry := Entry{
			Name:  member.Name(),
			Path:  filepath.Join(dir, member.Name()),
			IsDir: member.IsDir(),
			Size:  info.Size(),
		}
		listing.Entries = append(listing.Entries, entry)
		if !entry.IsDir && strings.EqualFold(filepath.Ext(entry.Name), ".jar") {
			listing.Jars = append(listing.Jars, Jar{Name: entry.Name, Size: entry.Size})
		}
	}

	sort.Slice(listing.Entries, func(a, b int) bool {
		if listing.Entries[a].IsDir != listing.Entries[b].IsDir {
			return listing.Entries[a].IsDir
		}
		return strings.ToLower(listing.Entries[a].Name) < strings.ToLower(listing.Entries[b].Name)
	})
	sort.Slice(listing.Jars, func(a, b int) bool { return listing.Jars[a].Name < listing.Jars[b].Name })
	return listing, nil
}

// parentOf returns the directory above dir, or "" when dir is already a
// filesystem root (/ on Unix, C:\ on Windows).
func parentOf(dir string) string {
	parent := filepath.Dir(dir)
	if parent == dir {
		return ""
	}
	return parent
}

// Shortcuts are the starting points the picker offers: the panel's own
// directories first, since that is where most servers live, then the operator's
// home directory and the filesystem roots.
func Shortcuts(named []Shortcut) []Shortcut {
	out := make([]Shortcut, 0, len(named)+4)
	seen := make(map[string]bool)
	add := func(label, path string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		out = append(out, Shortcut{Label: label, Path: path})
	}

	for _, shortcut := range named {
		if abs, err := filepath.Abs(shortcut.Path); err == nil {
			add(shortcut.Label, abs)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		add("用户主目录", home)
	}
	for _, root := range roots() {
		add(root, root)
	}
	return out
}

// roots lists the tops of the filesystem: one on Unix, one per mounted drive
// on Windows.
func roots() []string {
	if runtime.GOOS != "windows" {
		return []string{string(filepath.Separator)}
	}
	var drives []string
	for letter := 'A'; letter <= 'Z'; letter++ {
		drive := string(letter) + `:\`
		if _, err := os.Stat(drive); err == nil {
			drives = append(drives, drive)
		}
	}
	return drives
}
