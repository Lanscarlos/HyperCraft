package serverjar

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	// ErrNotFound is returned for a core that is not in the library.
	ErrNotFound = errors.New("core not found")
	// ErrInvalidID is returned for an ID that does not name a file in the
	// library directory.
	ErrInvalidID = errors.New("invalid core id")
)

// indexName holds what the panel knows about the jars it downloaded — project,
// version, build, checksum. The jars themselves are the source of truth for
// what exists; this only annotates them, so losing it costs metadata and never
// a file.
const indexName = "index.json"

// partSuffix marks a download that is still in flight. Nothing with this
// suffix is ever listed or launched.
const partSuffix = ".hypercraft-part"

// Core is one server jar kept in the panel-wide library.
//
// The library exists so a downloaded core is a panel-level asset rather than a
// file that happens to sit in one instance's directory: fetch Paper 1.21.11
// once, then stamp out as many servers from it as you like, offline.
type Core struct {
	// ID is the file name, which is unique within one directory and reads
	// better in a URL or a log line than an opaque hash would.
	ID          string    `json:"id"`
	FileName    string    `json:"fileName"`
	Project     string    `json:"project"`
	ProjectName string    `json:"projectName"`
	Kind        string    `json:"kind"`
	Version     string    `json:"version"`
	Build       int       `json:"build"`
	Channel     string    `json:"channel"`
	SHA256      string    `json:"sha256"`
	Size        int64     `json:"size"`
	AddedAt     time.Time `json:"addedAt"`
	// Imported marks a jar the panel found in the library directory but did
	// not download, so the UI can say "自行放入" rather than show blank fields.
	// Dropping a Forge or Fabric jar in there is a supported way to use the
	// library with a core the catalogue does not offer.
	Imported bool `json:"imported"`
	// JavaMinimum is the lowest Java major this jar runs on, 0 when unknown.
	//
	// Recorded here rather than looked up per render because the catalogue it
	// came from is a network call and this list is not: a jar downloaded a
	// month ago has to be able to say what it needs while the machine is
	// offline. 0 is a real answer — a hand-dropped jar nobody filled in — and
	// the UI shows it as 未知 rather than filling in a guess.
	JavaMinimum int `json:"javaMinimum"`
	// Minecraft is which game versions this jar serves, as a label. See
	// MinecraftOf: the version id for a world server, a range for a proxy,
	// empty when nothing knows.
	Minecraft string `json:"minecraft"`
}

// IsProxy reports whether this core is a proxy rather than a world server.
func (c Core) IsProxy() bool { return c.Kind == "proxy" }

// Library owns the cores directory, normally <data>/cores.
type Library struct {
	root string

	// mu serialises index reads and writes. The jar files themselves are only
	// ever created by the downloader, which runs one job at a time.
	mu sync.Mutex
}

func NewLibrary(root string) *Library { return &Library{root: root} }

// Root is the directory cores are stored in.
func (l *Library) Root() string { return l.root }

// List returns every core, newest first.
func (l *Library) List() ([]Core, error) {
	entries, err := os.ReadDir(l.root)
	if err != nil {
		if os.IsNotExist(err) {
			return []Core{}, nil
		}
		return nil, err
	}

	l.mu.Lock()
	index := l.readIndex()
	l.mu.Unlock()

	cores := make([]Core, 0, len(entries))
	for _, entry := range entries {
		if !isJar(entry) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		cores = append(cores, describe(entry.Name(), info, index))
	}

	sort.Slice(cores, func(a, b int) bool {
		if !cores[a].AddedAt.Equal(cores[b].AddedAt) {
			return cores[a].AddedAt.After(cores[b].AddedAt)
		}
		return cores[a].ID < cores[b].ID
	})
	return cores, nil
}

// Get returns one core by ID.
func (l *Library) Get(id string) (Core, error) {
	if err := validCoreID(id); err != nil {
		return Core{}, err
	}
	info, err := os.Stat(filepath.Join(l.root, id))
	if err != nil || info.IsDir() {
		return Core{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	l.mu.Lock()
	index := l.readIndex()
	l.mu.Unlock()
	return describe(id, info, index), nil
}

// Open returns a core's contents for copying into an instance directory.
func (l *Library) Open(id string) (*os.File, Core, error) {
	core, err := l.Get(id)
	if err != nil {
		return nil, Core{}, err
	}
	file, err := os.Open(filepath.Join(l.root, core.ID))
	if err != nil {
		return nil, Core{}, err
	}
	return file, core, nil
}

// Remove deletes a core from the library. The copies already handed out to
// instances are untouched — they are ordinary files in their own directories.
func (l *Library) Remove(id string) error {
	if err := validCoreID(id); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(l.root, id)); err != nil {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err := os.Remove(filepath.Join(l.root, id)); err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	index := l.readIndex()
	if _, ok := index[id]; ok {
		delete(index, id)
		return l.writeIndex(index)
	}
	return nil
}

// Has reports whether a jar of that name is already in the library.
func (l *Library) Has(fileName string) bool {
	if validCoreID(fileName) != nil {
		return false
	}
	info, err := os.Stat(filepath.Join(l.root, fileName))
	return err == nil && !info.IsDir()
}

// Import writes a jar the operator uploaded into the library.
//
// The metadata cannot come from anywhere but them: a jar the catalogue does
// not offer has no upstream to ask, which is exactly why the 添加核心 dialog
// makes them fill it in. Whatever they leave blank stays blank and the row
// says 信息不全 — the library never invents a version or a Java requirement.
func (l *Library) Import(name string, src io.Reader, meta Core) (Core, error) {
	if err := validCoreID(name); err != nil {
		return Core{}, err
	}
	if !strings.EqualFold(filepath.Ext(name), ".jar") {
		return Core{}, fmt.Errorf("%w: %q is not a .jar", ErrInvalidID, name)
	}
	if l.Has(name) {
		return Core{}, fmt.Errorf("%w: %s", ErrExists, name)
	}
	if err := os.MkdirAll(l.root, 0o755); err != nil {
		return Core{}, err
	}

	// Written under the part suffix and renamed, for the same reason a download
	// is: nothing with that suffix is ever listed or launched, so an upload cut
	// off halfway cannot leave a truncated jar looking like a core.
	temp := filepath.Join(l.root, name+partSuffix)
	file, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return Core{}, err
	}
	digest := sha256.New()
	size, err := io.Copy(file, io.TeeReader(src, digest))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(temp)
		return Core{}, err
	}
	if err := os.Rename(temp, filepath.Join(l.root, name)); err != nil {
		_ = os.Remove(temp)
		return Core{}, err
	}

	core := meta
	core.ID = name
	core.FileName = name
	core.Size = size
	core.SHA256 = hex.EncodeToString(digest.Sum(nil))
	core.AddedAt = time.Now()
	// Imported is what the row's 手动放入 chip reads, and an uploaded jar is
	// one: the panel holds the bytes but did not choose them.
	core.Imported = true
	if err := l.record(core); err != nil {
		return core, fmt.Errorf("文件已存入核心库，但记录核心信息失败: %w", err)
	}
	return core, nil
}

// record stores what a finished download was, so the listing can show a
// version and a build number rather than just a file name.
func (l *Library) record(core Core) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	index := l.readIndex()
	index[core.ID] = core
	return l.writeIndex(index)
}

// readIndex loads the metadata sidecar. A missing or corrupt file is not an
// error: every jar in the directory is still listed, just without its build
// details, which is a better outcome than hiding cores that are on disk.
func (l *Library) readIndex() map[string]Core {
	data, err := os.ReadFile(filepath.Join(l.root, indexName))
	if err != nil {
		return map[string]Core{}
	}
	var index map[string]Core
	if err := json.Unmarshal(data, &index); err != nil || index == nil {
		return map[string]Core{}
	}
	return index
}

func (l *Library) writeIndex(index map[string]Core) error {
	if err := os.MkdirAll(l.root, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}

	// Written beside the file and renamed over it, so an interrupted save
	// cannot leave a half-written index behind.
	temp := filepath.Join(l.root, indexName+".tmp")
	if err := os.WriteFile(temp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(temp, filepath.Join(l.root, indexName)); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

// describe joins what is on disk with what the index remembers about it.
func describe(name string, info os.FileInfo, index map[string]Core) Core {
	core, known := index[name]
	core.ID = name
	core.FileName = name
	core.Size = info.Size()
	if !known {
		core.Imported = true
		core.AddedAt = info.ModTime()
	}
	if core.AddedAt.IsZero() {
		core.AddedAt = info.ModTime()
	}
	return core
}

func isJar(entry os.DirEntry) bool {
	if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
		return false
	}
	return strings.EqualFold(filepath.Ext(entry.Name()), ".jar")
}

// validCoreID rejects anything that names something other than a plain file
// directly inside the library directory.
func validCoreID(id string) error {
	if id == "" || id == "." || id == ".." ||
		strings.ContainsAny(id, `/\`+"\x00") || strings.HasSuffix(id, partSuffix) {
		return fmt.Errorf("%w: %q", ErrInvalidID, id)
	}
	return nil
}
