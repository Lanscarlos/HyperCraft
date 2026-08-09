package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	// ErrNotFound is returned for a plugin or version that is not in the library.
	ErrNotFound = errors.New("plugin not found")
	// ErrInvalidID rejects an id that does not name something inside the
	// library directory.
	ErrInvalidID = errors.New("invalid plugin id")
	// ErrExists is returned when adding a plugin that is already tracked.
	ErrExists = errors.New("this plugin is already in the library")
	// ErrInvalidTarget rejects a target directory that is not a plain relative
	// directory inside the instance.
	ErrInvalidTarget = errors.New("invalid target directory")
)

// registryName is the library's index. Unlike the core library, where the jars
// on disk are the source of truth, a plugin is only meaningful together with
// its source and version history — a bare jar in a directory says nothing
// about where updates come from. So the registry owns the list, and the files
// beside it are what the entries point at.
const registryName = "registry.json"

// partSuffix marks a download still in flight. Nothing with this suffix is
// ever listed or installed.
const partSuffix = ".hypercraft-part"

// DefaultTargetDir is where a plugin lands inside an instance. Bukkit, Spigot,
// Paper, Velocity and BungeeCord all read "plugins"; Fabric and Forge read
// "mods", which is why this is a per-plugin field rather than a constant the
// installer hard-codes.
const DefaultTargetDir = "plugins"

// Version is one downloaded release of a plugin.
type Version struct {
	// Tag is the GitHub tag, and the id of this version everywhere else. It is
	// what the source names, so it is what a re-download can be matched against.
	Tag     string `json:"tag"`
	Version string `json:"version"`
	// FileName is the jar's name, both in the library and in the instance
	// directory it is copied to. Keeping the published name means a plugin's
	// own "check my version" log line matches what is on disk.
	FileName    string    `json:"fileName"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	Prerelease  bool      `json:"prerelease"`
	Notes       string    `json:"notes,omitempty"`
	PublishedAt time.Time `json:"publishedAt"`
	AddedAt     time.Time `json:"addedAt"`
}

// Plugin is one tracked plugin: where it comes from, and which of its versions
// the panel has downloaded.
type Plugin struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Note is the operator's own description. Nothing reads it.
	Note      string    `json:"note,omitempty"`
	Source    Source    `json:"source"`
	TargetDir string    `json:"targetDir"`
	AddedAt   time.Time `json:"addedAt"`

	// Versions are the downloads held in the library, newest release first.
	Versions []Version `json:"versions"`

	// Latest is what the last update check found upstream, and CheckedAt is
	// when it looked. Both are cached rather than fetched per page load: the
	// anonymous GitHub API allows 60 calls an hour, which a panel with twenty
	// plugins would burn through in three refreshes.
	Latest     *Release  `json:"latest,omitempty"`
	CheckedAt  time.Time `json:"checkedAt,omitempty"`
	CheckError string    `json:"checkError,omitempty"`
}

// HasVersion reports whether a tag is among the downloaded versions.
func (p Plugin) HasVersion(tag string) bool {
	return p.Version(tag) != nil
}

// Version returns one downloaded version by tag, or nil.
func (p Plugin) Version(tag string) *Version {
	for i := range p.Versions {
		if p.Versions[i].Tag == tag {
			return &p.Versions[i]
		}
	}
	return nil
}

// UpdateAvailable reports whether the last check found a release newer than
// everything downloaded.
//
// "Newer" is by tag identity, not by version arithmetic: plugin authors number
// releases in every scheme there is, and the only claim the panel can honestly
// make is "upstream's newest is not one you have".
func (p Plugin) UpdateAvailable() bool {
	return p.Latest != nil && !p.HasVersion(p.Latest.Tag)
}

// Library owns the plugin directory, normally <data>/plugins.
type Library struct {
	root string

	// mu serialises registry reads and writes. Downloads run one at a time and
	// each writes its own version directory, so the files need no lock of
	// their own.
	mu       sync.Mutex
	registry map[string]Plugin
	loaded   bool
}

func NewLibrary(root string) *Library { return &Library{root: root} }

// Root is the directory plugins are stored in.
func (l *Library) Root() string { return l.root }

// List returns every tracked plugin, newest addition first.
func (l *Library) List() []Plugin {
	l.mu.Lock()
	defer l.mu.Unlock()

	registry := l.load()
	out := make([]Plugin, 0, len(registry))
	for _, item := range registry {
		out = append(out, l.describe(item))
	}
	sort.Slice(out, func(a, b int) bool {
		if !out[a].AddedAt.Equal(out[b].AddedAt) {
			return out[a].AddedAt.After(out[b].AddedAt)
		}
		return out[a].ID < out[b].ID
	})
	return out
}

// Get returns one plugin by id.
func (l *Library) Get(id string) (Plugin, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.get(id)
}

func (l *Library) get(id string) (Plugin, error) {
	if err := validID(id); err != nil {
		return Plugin{}, err
	}
	registry := l.load()
	item, ok := registry[id]
	if !ok {
		return Plugin{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return l.describe(item), nil
}

// describe drops versions whose jar has gone missing. The registry is the list
// of what the panel tracks, but it cannot install a file that is not there, and
// an entry that fails on click is worse than one that is honestly absent.
func (l *Library) describe(item Plugin) Plugin {
	kept := make([]Version, 0, len(item.Versions))
	for _, version := range item.Versions {
		if info, err := os.Stat(l.versionFile(item.ID, version.Tag, version.FileName)); err == nil && !info.IsDir() {
			version.Size = info.Size()
			kept = append(kept, version)
		}
	}
	sort.SliceStable(kept, func(a, b int) bool {
		if !kept[a].PublishedAt.Equal(kept[b].PublishedAt) {
			return kept[a].PublishedAt.After(kept[b].PublishedAt)
		}
		return kept[a].AddedAt.After(kept[b].AddedAt)
	})
	item.Versions = kept
	if item.TargetDir == "" {
		item.TargetDir = DefaultTargetDir
	}
	return item
}

// Add starts tracking a plugin. The source is normalised first, so a pasted
// browser URL becomes an owner/name pair before anything is stored.
func (l *Library) Add(name string, src Source, targetDir, note string) (Plugin, error) {
	src, err := src.Normalise()
	if err != nil {
		return Plugin{}, err
	}
	targetDir, err = CleanTargetDir(targetDir)
	if err != nil {
		return Plugin{}, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	registry := l.load()

	for _, existing := range registry {
		if existing.Source.Kind == src.Kind && strings.EqualFold(existing.Source.Repo, src.Repo) {
			return Plugin{}, fmt.Errorf("%w: %s is already tracked as %q", ErrExists, src.Repo, existing.Name)
		}
	}

	if name = strings.TrimSpace(name); name == "" {
		// The repository name is what the plugin is called on its own release
		// page, so it is a better default than anything the panel could invent.
		name = repoName(src.Repo)
	}
	item := Plugin{
		ID:        l.freeID(registry, name, src.Repo),
		Name:      name,
		Note:      strings.TrimSpace(note),
		Source:    src,
		TargetDir: targetDir,
		AddedAt:   time.Now(),
		Versions:  []Version{},
	}
	registry[item.ID] = item
	if err := l.save(registry); err != nil {
		return Plugin{}, err
	}
	return item, nil
}

// Edit changes what a tracked plugin is called and where it comes from. The
// downloaded versions are untouched: a corrected asset pattern or a repository
// that moved should not throw away jars that are already on disk.
func (l *Library) Edit(id, name string, src Source, targetDir, note string) (Plugin, error) {
	src, err := src.Normalise()
	if err != nil {
		return Plugin{}, err
	}
	targetDir, err = CleanTargetDir(targetDir)
	if err != nil {
		return Plugin{}, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	registry := l.load()

	item, ok := registry[id]
	if !ok {
		return Plugin{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	for otherID, other := range registry {
		if otherID != id && other.Source.Kind == src.Kind && strings.EqualFold(other.Source.Repo, src.Repo) {
			return Plugin{}, fmt.Errorf("%w: %s is already tracked as %q", ErrExists, src.Repo, other.Name)
		}
	}

	if name = strings.TrimSpace(name); name != "" {
		item.Name = name
	}
	if item.Source != src {
		// The cached check describes the old source; keeping it would show a
		// "latest" the new source may not even publish.
		item.Latest = nil
		item.CheckedAt = time.Time{}
		item.CheckError = ""
	}
	item.Source = src
	item.TargetDir = targetDir
	item.Note = strings.TrimSpace(note)

	registry[id] = item
	if err := l.save(registry); err != nil {
		return Plugin{}, err
	}
	return l.describe(item), nil
}

// Remove stops tracking a plugin and deletes its downloads. Copies already
// handed to instances are untouched — they are ordinary files in their own
// directories, exactly like a core.
func (l *Library) Remove(id string) error {
	if err := validID(id); err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	registry := l.load()

	if _, ok := registry[id]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	delete(registry, id)
	if err := l.save(registry); err != nil {
		return err
	}
	// The registry goes first: an entry pointing at files that are gone is a
	// listing bug, while files with no entry are dead weight nobody can see.
	return os.RemoveAll(filepath.Join(l.root, id))
}

// RecordCheck stores the result of an update check, successful or not. A
// failed check keeps the previous "latest" — a flaky network should not make a
// known update disappear from the page.
func (l *Library) RecordCheck(id string, latest *Release, checkErr error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	registry := l.load()

	item, ok := registry[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	item.CheckedAt = time.Now()
	if checkErr != nil {
		item.CheckError = checkErr.Error()
	} else {
		item.CheckError = ""
		item.Latest = latest
	}
	registry[id] = item
	return l.save(registry)
}

// record adds a finished download to a plugin's version list.
func (l *Library) record(id string, version Version) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	registry := l.load()

	item, ok := registry[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	replaced := false
	for i := range item.Versions {
		if item.Versions[i].Tag == version.Tag {
			item.Versions[i] = version
			replaced = true
			break
		}
	}
	if !replaced {
		item.Versions = append(item.Versions, version)
	}
	registry[id] = item
	return l.save(registry)
}

// RemoveVersion deletes one downloaded version.
func (l *Library) RemoveVersion(id, tag string) error {
	if err := validID(id); err != nil {
		return err
	}
	slug, err := versionSlug(tag)
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	registry := l.load()

	item, ok := registry[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	kept := make([]Version, 0, len(item.Versions))
	found := false
	for _, version := range item.Versions {
		if version.Tag == tag {
			found = true
			continue
		}
		kept = append(kept, version)
	}
	if !found {
		return fmt.Errorf("%w: %s has no version %s", ErrNotFound, id, tag)
	}
	item.Versions = kept
	registry[id] = item
	if err := l.save(registry); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(l.root, id, slug))
}

// Open returns a version's jar for copying into an instance directory.
func (l *Library) Open(id, tag string) (*os.File, Plugin, Version, error) {
	item, err := l.Get(id)
	if err != nil {
		return nil, Plugin{}, Version{}, err
	}
	version := item.Version(tag)
	if version == nil {
		return nil, Plugin{}, Version{}, fmt.Errorf("%w: %s has no downloaded version %s", ErrNotFound, item.Name, tag)
	}
	file, err := os.Open(l.versionFile(id, tag, version.FileName))
	if err != nil {
		return nil, Plugin{}, Version{}, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	return file, item, *version, nil
}

// versionFile is where one downloaded jar lives. Versions get a directory
// each because a plugin that always publishes "Foo.jar" would otherwise have
// every release overwrite the last one.
func (l *Library) versionFile(id, tag, fileName string) string {
	slug, err := versionSlug(tag)
	if err != nil {
		return filepath.Join(l.root, id, "invalid", fileName)
	}
	return filepath.Join(l.root, id, slug, fileName)
}

// load reads the registry once and keeps it in memory. The panel is the only
// writer, so re-reading per call would buy nothing.
func (l *Library) load() map[string]Plugin {
	if l.loaded {
		return l.registry
	}
	l.loaded = true
	l.registry = map[string]Plugin{}

	data, err := os.ReadFile(filepath.Join(l.root, registryName))
	if err != nil {
		return l.registry
	}
	var stored map[string]Plugin
	if err := json.Unmarshal(data, &stored); err != nil || stored == nil {
		// A corrupt registry is not worth refusing to boot over; the operator
		// can re-add their plugins, and the jars are still on disk.
		return l.registry
	}
	for id, item := range stored {
		item.ID = id
		if item.TargetDir == "" {
			item.TargetDir = DefaultTargetDir
		}
		if item.Versions == nil {
			item.Versions = []Version{}
		}
		l.registry[id] = item
	}
	return l.registry
}

func (l *Library) save(registry map[string]Plugin) error {
	l.registry = registry
	if err := os.MkdirAll(l.root, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}

	// Written beside the file and renamed over it, so an interrupted save
	// cannot leave a half-written registry behind.
	temp := filepath.Join(l.root, registryName+".tmp")
	if err := os.WriteFile(temp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(temp, filepath.Join(l.root, registryName)); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

// freeID picks a directory-safe id that no other plugin holds.
func (l *Library) freeID(registry map[string]Plugin, name, repo string) string {
	base := slug(name)
	if base == "" {
		base = slug(repoName(repo))
	}
	if base == "" {
		base = "plugin"
	}
	if _, taken := registry[base]; !taken {
		return base
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", base, n)
		if _, taken := registry[candidate]; !taken {
			return candidate
		}
	}
}

func repoName(repo string) string {
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		return repo[i+1:]
	}
	return repo
}

// slug reduces a name to something safe as a directory and readable in a URL.
func slug(in string) string {
	var b strings.Builder
	lastDash := true // leading dashes are dropped
	for _, r := range strings.ToLower(strings.TrimSpace(in)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '.' || r == '_':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-.")
	if len(out) > 48 {
		out = strings.Trim(out[:48], "-.")
	}
	return out
}

// versionSlug turns a tag into a directory name. Tags carry slashes often
// enough ("release/1.2.3") that this cannot be a straight pass-through.
func versionSlug(tag string) (string, error) {
	out := slug(tag)
	if out == "" {
		return "", fmt.Errorf("%w: version %q has no usable name", ErrInvalidID, tag)
	}
	return out, nil
}

// validID rejects anything that names something other than a directory
// directly inside the library.
func validID(id string) error {
	if id == "" || id == "." || id == ".." ||
		strings.ContainsAny(id, `/\`+"\x00") || strings.HasSuffix(id, partSuffix) {
		return fmt.Errorf("%w: %q", ErrInvalidID, id)
	}
	return nil
}

// CleanTargetDir validates the directory a plugin is installed into. It is a
// path inside the instance, so the same rules the file manager applies hold:
// relative, no escaping, no absolute paths.
func CleanTargetDir(dir string) (string, error) {
	dir = strings.TrimSpace(strings.ReplaceAll(dir, "\\", "/"))
	dir = strings.Trim(dir, "/")
	if dir == "" {
		return DefaultTargetDir, nil
	}
	if strings.Contains(dir, "\x00") {
		return "", fmt.Errorf("%w: %q", ErrInvalidTarget, dir)
	}
	cleaned := filepath.ToSlash(filepath.Clean(dir))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("%w: %q must stay inside the instance directory", ErrInvalidTarget, dir)
	}
	return cleaned, nil
}
