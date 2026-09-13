package javaruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ErrDuplicate is returned when a path is already registered. The id is
// derived from the path, so a second Add would silently overwrite the first.
var ErrDuplicate = errors.New("java path already registered")

// How an entry got here, for the page to label it with.
const (
	AddedManual   = "manual"   // the operator typed the path
	AddedDetected = "detected" // probed off PATH or JAVA_HOME, then confirmed
	AddedMigrated = "migrated" // an instance was already launching with it
)

// Entry is a Java the panel did not install: a path the operator pointed at.
//
// The probed version is stored rather than re-read on every List. Probing
// forks a JVM, and this list is read on every instance save and every Java
// page poll.
type Entry struct {
	ID string `json:"id"`
	// JavaPath is the launcher. "java" is legal and means "follow PATH",
	// which is what an instance configured before this registry existed says.
	JavaPath string    `json:"javaPath"`
	Vendor   string    `json:"vendor"`
	Version  string    `json:"version"`
	Major    int       `json:"major"`
	AddedBy  string    `json:"addedBy"`
	AddedAt  time.Time `json:"addedAt"`
}

// EntryID derives a stable id from the path, so re-registering the same java
// is a duplicate rather than a second row that says the same thing.
func EntryID(javaPath string) string {
	sum := sha256.Sum256([]byte(javaPath))
	return "ext-" + hex.EncodeToString(sum[:6])
}

// Registry is the list of Java paths an instance is allowed to point at, on
// top of whatever Store finds in the runtimes directory.
//
// It is read into memory once and written through, rather than read per call.
// A whitelist that empties itself on a transient read error would start
// refusing saves that are perfectly valid, and the file is a few hundred bytes.
type Registry struct {
	path string
	log  *slog.Logger

	mu      sync.RWMutex
	entries []Entry
}

// NewRegistry loads the registry. A file that cannot be read or parsed is
// reported and treated as empty: this list is a set of conveniences, and none
// of it is needed to keep a running server running.
func NewRegistry(path string, log *slog.Logger) *Registry {
	r := &Registry{path: path, log: log, entries: []Entry{}}

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Error("could not read the java registry", "path", path, "err", err)
		}
		return r
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		log.Error("the java registry is not readable json, starting empty", "path", path, "err", err)
		return r
	}
	r.entries = entries
	return r
}

// List returns every registered entry, in the order they were added.
func (r *Registry) List() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Entry, len(r.entries))
	copy(out, r.entries)
	return out
}

// Get returns one entry by id.
func (r *Registry) Get(id string) (Entry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, entry := range r.entries {
		if entry.ID == id {
			return entry, nil
		}
	}
	return Entry{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// Add registers a path. The caller supplies the probed version fields; this
// type does not fork JVMs.
func (r *Registry) Add(entry Entry) (Entry, error) {
	if entry.JavaPath == "" {
		return Entry{}, fmt.Errorf("%w: java path is required", ErrInvalidID)
	}
	entry.ID = EntryID(entry.JavaPath)
	if entry.AddedAt.IsZero() {
		entry.AddedAt = time.Now()
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.entries {
		if existing.JavaPath == entry.JavaPath {
			return Entry{}, fmt.Errorf("%w: %s", ErrDuplicate, entry.JavaPath)
		}
	}
	r.entries = append(r.entries, entry)
	if err := r.save(); err != nil {
		r.entries = r.entries[:len(r.entries)-1]
		return Entry{}, err
	}
	return entry, nil
}

// Replace overwrites one entry in place, keeping its id and position. It is
// how a re-probe records a new version without the row jumping around.
func (r *Registry) Replace(id string, entry Entry) (Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, existing := range r.entries {
		if existing.ID != id {
			continue
		}
		entry.ID, entry.JavaPath, entry.AddedBy, entry.AddedAt =
			existing.ID, existing.JavaPath, existing.AddedBy, existing.AddedAt
		previous := r.entries[i]
		r.entries[i] = entry
		if err := r.save(); err != nil {
			r.entries[i] = previous
			return Entry{}, err
		}
		return entry, nil
	}
	return Entry{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// Remove drops one entry.
func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, entry := range r.entries {
		if entry.ID != id {
			continue
		}
		previous := r.entries
		r.entries = append(append([]Entry{}, r.entries[:i]...), r.entries[i+1:]...)
		if err := r.save(); err != nil {
			r.entries = previous
			return err
		}
		return nil
	}
	return fmt.Errorf("%w: %s", ErrNotFound, id)
}

// save writes the whole list. Callers hold the write lock.
func (r *Registry) save() error {
	data, err := json.MarshalIndent(r.entries, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	// Rename into place, so a crash mid-write leaves the old list rather than
	// half of the new one.
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}
