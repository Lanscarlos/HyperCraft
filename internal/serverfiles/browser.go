// Package serverfiles gives the panel access to one instance's directory.
//
// Every operation goes through os.Root, which confines it to the instance
// directory: ".." components, absolute paths, and symlinks pointing outside
// the tree are all rejected by the kernel-level open, not by string matching.
// A file manager's worst failure is letting a request reach /etc/shadow, and
// hand-rolled path cleaning is exactly how that happens.
package serverfiles

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

var (
	// ErrInvalidPath covers anything that does not name a location inside the
	// instance directory.
	ErrInvalidPath = errors.New("invalid path")
	// ErrNotFound is returned for a missing file or directory.
	ErrNotFound = errors.New("file not found")
	// ErrTooLarge is returned when a file exceeds the caller's byte budget.
	ErrTooLarge = errors.New("file is too large")
	// ErrIsDirectory is returned when a file operation targets a directory.
	ErrIsDirectory = errors.New("path is a directory")
	// ErrExists is returned when a create would clobber something.
	ErrExists = errors.New("path already exists")
)

// maxEditableBytes is the largest file the built-in text editor will open.
// Anything bigger is a log or a world file, not something to edit in a
// browser textarea.
const maxEditableBytes = 1 << 20 // 1 MiB

// Browser serves one instance directory.
type Browser struct {
	dir string
}

func New(dir string) *Browser { return &Browser{dir: dir} }

// Entry describes one directory member.
type Entry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"` // slash-separated, relative to the instance root
	IsDir    bool      `json:"isDir"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	// Editable marks text files small enough for the in-browser editor.
	Editable bool `json:"editable"`
	// Symlink marks entries the panel will not follow. os.Root refuses links
	// that escape the tree, so these are shown but not opened.
	Symlink bool `json:"symlink"`
}

// open returns a root handle for the instance directory. A fresh handle per
// operation costs one openat and avoids pinning a directory that may be
// replaced while the panel runs.
func (b *Browser) open() (*os.Root, error) {
	root, err := os.OpenRoot(b.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: instance directory %s does not exist", ErrNotFound, b.dir)
		}
		return nil, err
	}
	return root, nil
}

// clean normalises a client-supplied path to a form os.Root accepts.
//
// Every path is relative to the instance root, so a leading slash is an alias
// for it rather than for the filesystem root — "/plugins" and "plugins" both
// mean the instance's plugins directory. The checks here exist for clear error
// messages; os.Root is the actual guard.
func clean(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	rel = strings.ReplaceAll(rel, "\\", "/")
	rel = strings.Trim(rel, "/")
	if rel == "" || rel == "." {
		return ".", nil
	}
	if strings.Contains(rel, "\x00") {
		return "", fmt.Errorf("%w: path contains a null byte", ErrInvalidPath)
	}

	cleaned := path.Clean(rel)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || path.IsAbs(cleaned) {
		return "", fmt.Errorf("%w: %q escapes the instance directory", ErrInvalidPath, rel)
	}
	return cleaned, nil
}

// List returns the contents of a directory, folders first then files, each
// group sorted by name.
func (b *Browser) List(rel string) ([]Entry, error) {
	dir, err := clean(rel)
	if err != nil {
		return nil, err
	}
	root, err := b.open()
	if err != nil {
		return nil, err
	}
	defer root.Close()

	members, err := fs.ReadDir(root.FS(), dir)
	if err != nil {
		return nil, translate(err)
	}

	entries := make([]Entry, 0, len(members))
	for _, member := range members {
		info, err := member.Info()
		if err != nil {
			// Vanished between listing and stat; skip rather than fail the page.
			continue
		}
		entryPath := member.Name()
		if dir != "." {
			entryPath = path.Join(dir, member.Name())
		}
		entries = append(entries, Entry{
			Name:     member.Name(),
			Path:     entryPath,
			IsDir:    member.IsDir(),
			Size:     info.Size(),
			Modified: info.ModTime(),
			Editable: !member.IsDir() && isEditable(member.Name(), info.Size()),
			Symlink:  info.Mode()&fs.ModeSymlink != 0,
		})
	}

	sort.Slice(entries, func(a, c int) bool {
		if entries[a].IsDir != entries[c].IsDir {
			return entries[a].IsDir
		}
		return strings.ToLower(entries[a].Name) < strings.ToLower(entries[c].Name)
	})
	return entries, nil
}

// Stat describes a single path.
func (b *Browser) Stat(rel string) (Entry, error) {
	name, err := clean(rel)
	if err != nil {
		return Entry{}, err
	}
	root, err := b.open()
	if err != nil {
		return Entry{}, err
	}
	defer root.Close()

	info, err := root.Stat(name)
	if err != nil {
		return Entry{}, translate(err)
	}
	return Entry{
		Name:     path.Base(name),
		Path:     name,
		IsDir:    info.IsDir(),
		Size:     info.Size(),
		Modified: info.ModTime(),
		Editable: !info.IsDir() && isEditable(name, info.Size()),
	}, nil
}

// ReadText returns a text file's contents for the in-browser editor.
func (b *Browser) ReadText(rel string) (string, error) {
	name, err := clean(rel)
	if err != nil {
		return "", err
	}
	root, err := b.open()
	if err != nil {
		return "", err
	}
	defer root.Close()

	info, err := root.Stat(name)
	if err != nil {
		return "", translate(err)
	}
	if info.IsDir() {
		return "", ErrIsDirectory
	}
	if info.Size() > maxEditableBytes {
		return "", fmt.Errorf("%w: %d bytes, the editor caps at %d", ErrTooLarge, info.Size(), maxEditableBytes)
	}

	data, err := root.ReadFile(name)
	if err != nil {
		return "", translate(err)
	}
	return string(data), nil
}

// WriteText replaces a text file's contents.
//
// The write goes to a sibling temp file and is renamed into place, so a
// failure part way through cannot leave a half-written config behind — the
// same guarantee the properties editor gives.
func (b *Browser) WriteText(rel, content string) error {
	name, err := clean(rel)
	if err != nil {
		return err
	}
	if name == "." {
		return ErrIsDirectory
	}
	root, err := b.open()
	if err != nil {
		return err
	}
	defer root.Close()

	tmp := name + ".hypercraft-tmp"
	if err := root.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return translate(err)
	}
	if err := root.Rename(tmp, name); err != nil {
		_ = root.Remove(tmp)
		return translate(err)
	}
	return nil
}

// Create opens a file for writing an upload into. It refuses to clobber an
// existing file unless overwrite is set, so replacing a server jar is a
// deliberate confirmation rather than something a mistyped name can do to a
// world file.
func (b *Browser) Create(rel string, overwrite bool) (*os.File, func(), error) {
	name, err := clean(rel)
	if err != nil {
		return nil, nil, err
	}
	if name == "." {
		return nil, nil, ErrIsDirectory
	}
	root, err := b.open()
	if err != nil {
		return nil, nil, err
	}

	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if overwrite {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}

	file, err := root.OpenFile(name, flags, 0o644)
	if err != nil {
		root.Close()
		if errors.Is(err, fs.ErrExist) {
			return nil, nil, fmt.Errorf("%w: %s", ErrExists, name)
		}
		return nil, nil, translate(err)
	}
	// The root must outlive the file handle, so hand back a combined closer.
	return file, func() { file.Close(); root.Close() }, nil
}

// Open returns a file for download along with its metadata.
func (b *Browser) Open(rel string) (*os.File, os.FileInfo, func(), error) {
	name, err := clean(rel)
	if err != nil {
		return nil, nil, nil, err
	}
	root, err := b.open()
	if err != nil {
		return nil, nil, nil, err
	}

	file, err := root.Open(name)
	if err != nil {
		root.Close()
		return nil, nil, nil, translate(err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		root.Close()
		return nil, nil, nil, err
	}
	if info.IsDir() {
		file.Close()
		root.Close()
		return nil, nil, nil, ErrIsDirectory
	}
	return file, info, func() { file.Close(); root.Close() }, nil
}

// Mkdir creates a directory, including any missing parents.
func (b *Browser) Mkdir(rel string) error {
	name, err := clean(rel)
	if err != nil {
		return err
	}
	if name == "." {
		return fmt.Errorf("%w: refusing to recreate the instance root", ErrInvalidPath)
	}
	root, err := b.open()
	if err != nil {
		return err
	}
	defer root.Close()

	if err := root.MkdirAll(name, 0o755); err != nil {
		return translate(err)
	}
	return nil
}

// Remove deletes a file, or a directory and everything under it.
func (b *Browser) Remove(rel string) error {
	name, err := clean(rel)
	if err != nil {
		return err
	}
	if name == "." {
		// Deleting the instance root belongs to instance deletion, which has
		// its own confirmation; the file manager must not offer a shortcut.
		return fmt.Errorf("%w: refusing to delete the instance root", ErrInvalidPath)
	}
	root, err := b.open()
	if err != nil {
		return err
	}
	defer root.Close()

	// RemoveAll treats a missing path as success. Reporting that back as
	// "deleted" would tell the operator a file is gone when it was never
	// there — usually a sign they are looking at the wrong directory.
	if _, err := root.Lstat(name); err != nil {
		return translate(err)
	}
	if err := root.RemoveAll(name); err != nil {
		return translate(err)
	}
	return nil
}

// Rename moves a file or directory within the instance.
func (b *Browser) Rename(from, to string) error {
	src, err := clean(from)
	if err != nil {
		return err
	}
	dst, err := clean(to)
	if err != nil {
		return err
	}
	if src == "." || dst == "." {
		return fmt.Errorf("%w: cannot rename the instance root", ErrInvalidPath)
	}
	root, err := b.open()
	if err != nil {
		return err
	}
	defer root.Close()

	if _, err := root.Stat(dst); err == nil {
		return fmt.Errorf("%w: %s", ErrExists, dst)
	}
	if err := root.Rename(src, dst); err != nil {
		return translate(err)
	}
	return nil
}

// editableExtensions are the file types the in-browser editor opens. Server
// configs are the point: ops.json, whitelist.json, bukkit.yml, spigot.yml,
// plugin configs, start scripts.
var editableExtensions = map[string]bool{
	".txt": true, ".json": true, ".yml": true, ".yaml": true, ".properties": true,
	".cfg": true, ".conf": true, ".toml": true, ".ini": true, ".log": true,
	".md": true, ".sh": true, ".bat": true, ".xml": true, ".csv": true,
	".mcmeta": true, ".snbt": true, ".lang": true, ".env": true,
}

func isEditable(name string, size int64) bool {
	if size > maxEditableBytes {
		return false
	}
	ext := strings.ToLower(path.Ext(name))
	if editableExtensions[ext] {
		return true
	}
	// Dotfiles and extension-less files like "eula.txt.bak" or "Dockerfile"
	// are usually text too, but only guess when they are small.
	return ext == "" && size <= 64*1024
}

// MaxEditableBytes exposes the editor's size cap to the API layer.
func MaxEditableBytes() int64 { return maxEditableBytes }

// translate maps filesystem errors onto this package's sentinels so the API
// can return a sensible status code.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	case errors.Is(err, fs.ErrExist):
		return fmt.Errorf("%w: %v", ErrExists, err)
	case errors.Is(err, fs.ErrInvalid), errors.Is(err, os.ErrInvalid):
		return fmt.Errorf("%w: %v", ErrInvalidPath, err)
	default:
		return err
	}
}

// CopyLimited streams an upload to dst, failing if it exceeds limit bytes.
func CopyLimited(dst io.Writer, src io.Reader, limit int64) (int64, error) {
	// Read one byte past the limit so an exactly-at-limit file still succeeds
	// while an oversized one is detected instead of silently truncated.
	written, err := io.Copy(dst, io.LimitReader(src, limit+1))
	if err != nil {
		return written, err
	}
	if written > limit {
		return written, fmt.Errorf("%w: upload exceeds %d bytes", ErrTooLarge, limit)
	}
	return written, nil
}
