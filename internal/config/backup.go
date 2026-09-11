package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// backupPrefix marks the directories BackupState creates, and is what tells
// them apart from an instance directory when the old ones are pruned.
const backupPrefix = "rollback-"

// StateFiles are the files the panel writes for itself, as opposed to the
// server directories it manages. These are what a downgrade puts at risk: an
// older build does not know this build's fields, and Go writes back only the
// fields it declares, so whatever it cannot see is gone from the file the
// moment it saves.
//
// Server directories and worlds are deliberately not here. They are tens of
// gigabytes, and an older panel does not rewrite them — it only rewrites its
// own JSON.
func (p Paths) StateFiles() []string {
	return []string{
		p.PanelFile(),
		p.InstancesFile(),
		p.UsersFile(),
		p.DatabasesFile(),
		p.InstancePluginsFile(),
		p.PendingPluginsFile(),
		p.ConfigHistoryFile(),
		p.ResumeFile(),
	}
}

// BackupState copies StateFiles into a dated directory under the data root and
// returns it, keeping the newest keep backups and deleting the rest.
//
// It is taken before a downgrade, and it is there for the way back up rather
// than for the downgrade itself: rolling back does not touch these files, the
// older panel does, when it starts and saves. Without a copy taken while the
// newer fields are still in the file, they are unrecoverable.
//
// A file that does not exist is skipped rather than failing the backup: a panel
// that has never had an instance has no instances.json, and that is no reason
// to refuse the rollback it is about to protect.
func BackupState(p Paths, from, to string, keep int) (string, error) {
	dir := filepath.Join(p.Root, backupPrefix+time.Now().Format("20060102-150405")+
		"-"+sanitizeVersion(from)+"-to-"+sanitizeVersion(to))
	// Two rollbacks within the same second would otherwise land in the same
	// directory and the second would overwrite the first. The suffix keeps the
	// names sorting in creation order for anyone reading the directory listing;
	// the pruning below goes by modification time and does not depend on it.
	for n := 2; ; n++ {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			break
		}
		dir = filepath.Join(p.Root, backupPrefix+time.Now().Format("20060102-150405")+
			fmt.Sprintf("-%d-", n)+sanitizeVersion(from)+"-to-"+sanitizeVersion(to))
	}

	// 0700: users.json holds password hashes and is written 0600, so the
	// directory holding a copy of it must not be readable by anyone else.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create the backup directory: %w", err)
	}
	for _, src := range p.StateFiles() {
		if err := copyFile(src, filepath.Join(dir, filepath.Base(src))); err != nil {
			return "", err
		}
	}

	// A failed prune is not a failed backup: the copy is already on disk, which
	// is the part that cannot be redone later.
	pruneBackups(p.Root, keep)
	return dir, nil
}

// copyFile copies src to dst keeping its mode, and does nothing at all when src
// does not exist.
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(src), err)
	}

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(src), err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(dst), err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copy %s: %w", filepath.Base(src), err)
	}
	return out.Close()
}

// pruneBackups keeps the newest keep backup directories. Ordering is by
// modification time rather than by name: several rollbacks can share a second,
// and the timestamp in the name only has second resolution.
func pruneBackups(root string, keep int) {
	if keep < 1 {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}

	type backup struct {
		path string
		at   time.Time
	}
	var found []backup
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), backupPrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		found = append(found, backup{filepath.Join(root, e.Name()), info.ModTime()})
	}
	if len(found) <= keep {
		return
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].at.Equal(found[j].at) {
			return found[i].path < found[j].path
		}
		return found[i].at.After(found[j].at)
	})
	for _, b := range found[keep:] {
		_ = os.RemoveAll(b.path)
	}
}

// sanitizeVersion keeps a version string usable as one path segment. Versions
// come from release tags, which this panel does not author, so nothing about
// their contents is guaranteed.
func sanitizeVersion(v string) string {
	if v == "" {
		return "unknown"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return r
		}
		return '_'
	}, v)
}
