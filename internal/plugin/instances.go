package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lanscarlos/hypercraft/internal/serverfiles"
)

// ErrNotInstalled is returned for an instance plugin that is not there.
var ErrNotInstalled = errors.New("plugin is not installed on this instance")

// disabledSuffix is how a plugin is switched off.
//
// Bukkit, Spigot, Paper, Velocity and BungeeCord all load exactly the *.jar
// files in their plugin directory and ignore everything else, so renaming the
// jar is the switch every server admin already uses. It beats moving the file
// somewhere else in every way that matters: the plugin's own config and data
// directory stay where they are, and turning it back on is the same rename in
// reverse.
const disabledSuffix = ".disabled"

// keyPluginPrefix and keyFilePrefix distinguish the two kinds of row an
// instance's plugin list holds: one the panel installed and one it merely
// found. Both can be switched on and off, and one namespaced key means the API
// does not need a separate route per kind.
const (
	keyPluginPrefix = "plugin:"
	keyFilePrefix   = "file:"
)

// Installed is the panel's record of a plugin it put into an instance.
//
// The record exists so the panel can answer "which version is this, and where
// did it come from" for a file whose name may say neither. The jar in the
// instance directory is still the thing that runs — if the record and the disk
// disagree, the listing reports what is actually there.
type Installed struct {
	PluginID    string    `json:"pluginId"`
	Tag         string    `json:"tag"`
	Version     string    `json:"version"`
	FileName    string    `json:"fileName"`
	Dir         string    `json:"dir"`
	InstalledAt time.Time `json:"installedAt"`
}

// Entry is one row of an instance's plugin list: the record joined with what
// is on disk right now.
type Entry struct {
	// Key addresses this row in the enable/remove calls. See keyPluginPrefix.
	Key      string `json:"key"`
	PluginID string `json:"pluginId,omitempty"`
	Name     string `json:"name"`
	FileName string `json:"fileName"`
	Dir      string `json:"dir"`
	// Enabled is false for a jar renamed to *.jar.disabled.
	Enabled bool `json:"enabled"`
	// Managed marks a plugin installed from the library, as opposed to a jar
	// the panel found in the directory.
	Managed bool `json:"managed"`
	// Missing marks a plugin the panel installed whose file is no longer
	// there — deleted through the file manager, or by hand over SSH.
	Missing     bool      `json:"missing"`
	Size        int64     `json:"size"`
	Modified    time.Time `json:"modified,omitempty"`
	Tag         string    `json:"tag,omitempty"`
	Version     string    `json:"version,omitempty"`
	InstalledAt time.Time `json:"installedAt,omitempty"`
}

// Instances tracks which library plugins each server has, and applies changes
// to the server's directory.
//
// The records live with the panel rather than in the instance directory: they
// describe a relationship between two panel-managed things, and a server
// directory should stay something you can hand to a plain Minecraft launcher.
type Instances struct {
	library *Library
	path    string

	mu      sync.Mutex
	records map[string][]Installed
	loaded  bool
}

func NewInstances(library *Library, path string) *Instances {
	return &Instances{library: library, path: path}
}

// List returns everything in an instance's plugin directories: what the panel
// installed, and any jar it did not.
//
// Unmanaged jars are listed rather than hidden. Someone who uploaded a plugin
// through the file manager, or restored a server directory from a backup,
// should see it here instead of wondering why the page claims the server has
// no plugins.
func (m *Instances) List(instanceID, directory string) ([]Entry, error) {
	records := m.recordsFor(instanceID)
	browser := serverfiles.New(directory)

	// Every directory a record points at, plus the default one, so an instance
	// with nothing installed still shows the jars sitting in plugins/.
	dirs := map[string]bool{DefaultTargetDir: true}
	for _, record := range records {
		dirs[record.Dir] = true
	}
	for _, item := range m.library.List() {
		dirs[item.TargetDir] = true
	}

	// found maps dir/name (with any .disabled stripped) to the entry built
	// from disk, so a record can claim its file in one lookup.
	found := map[string]*Entry{}
	order := make([]string, 0, 16)
	for dir := range dirs {
		entries, err := browser.List(dir)
		if err != nil {
			// A server that has never started has no plugins directory, which
			// is not an error worth failing the page over.
			if errors.Is(err, serverfiles.ErrNotFound) {
				continue
			}
			return nil, err
		}
		for _, file := range entries {
			if file.IsDir {
				continue
			}
			name, enabled, ok := jarName(file.Name)
			if !ok {
				continue
			}
			key := dir + "/" + name
			found[key] = &Entry{
				Key:      keyFilePrefix + key,
				Name:     strings.TrimSuffix(name, path.Ext(name)),
				FileName: name,
				Dir:      dir,
				Enabled:  enabled,
				Size:     file.Size,
				Modified: file.Modified,
			}
			order = append(order, key)
		}
	}

	out := make([]Entry, 0, len(order)+len(records))
	claimed := map[string]bool{}
	for _, record := range records {
		key := record.Dir + "/" + record.FileName
		entry := Entry{
			Key:         keyPluginPrefix + record.PluginID,
			PluginID:    record.PluginID,
			Name:        record.PluginID,
			FileName:    record.FileName,
			Dir:         record.Dir,
			Managed:     true,
			Tag:         record.Tag,
			Version:     record.Version,
			InstalledAt: record.InstalledAt,
		}
		if item, err := m.library.Get(record.PluginID); err == nil {
			entry.Name = item.Name
		}
		if disk, ok := found[key]; ok {
			claimed[key] = true
			entry.Enabled = disk.Enabled
			entry.Size = disk.Size
			entry.Modified = disk.Modified
		} else {
			entry.Missing = true
		}
		out = append(out, entry)
	}
	for _, key := range order {
		if claimed[key] {
			continue
		}
		out = append(out, *found[key])
	}

	sort.Slice(out, func(a, b int) bool {
		// Managed plugins first: they are the ones this page can act on, and
		// the unmanaged jars are context rather than the subject.
		if out[a].Managed != out[b].Managed {
			return out[a].Managed
		}
		return strings.ToLower(out[a].Name) < strings.ToLower(out[b].Name)
	})
	return out, nil
}

// Install copies a library version into an instance, replacing whatever that
// plugin was on before.
//
// This is both "add" and "switch version": a plugin the instance already has
// is upgraded or rolled back in place, keeping whether it was switched off, so
// changing the version of a disabled plugin does not quietly turn it back on.
func (m *Instances) Install(instanceID, directory, pluginID, tag string) (Entry, error) {
	source, item, version, err := m.library.Open(pluginID, tag)
	if err != nil {
		return Entry{}, err
	}
	defer source.Close()

	// The instance directory may have been removed since the instance was
	// made, and os.OpenRoot needs it to exist before anything below it does.
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return Entry{}, err
	}
	browser := serverfiles.New(directory)
	if err := browser.Mkdir(item.TargetDir); err != nil {
		return Entry{}, err
	}

	previous := m.record(instanceID, pluginID)
	enabled := true
	if previous != nil {
		// Whether the old file was enabled is on disk, not in the record: the
		// operator may have renamed it by hand.
		if _, on, ok := m.locate(browser, previous.Dir, previous.FileName); ok {
			enabled = on
		}
	}

	name := item.TargetDir + "/" + version.FileName
	if !enabled {
		name += disabledSuffix
	}
	if err := copyInto(browser, source, name); err != nil {
		return Entry{}, err
	}

	// The old jar goes only after the new one is safely in place, and only if
	// it is a different file — a plugin that publishes the same name every
	// release has already been overwritten above.
	if previous != nil {
		old := previous.Dir + "/" + previous.FileName
		if old != item.TargetDir+"/"+version.FileName {
			_ = browser.Remove(old)
			_ = browser.Remove(old + disabledSuffix)
		}
	}

	if err := m.put(instanceID, Installed{
		PluginID:    pluginID,
		Tag:         version.Tag,
		Version:     version.Version,
		FileName:    version.FileName,
		Dir:         item.TargetDir,
		InstalledAt: time.Now(),
	}); err != nil {
		return Entry{}, err
	}

	return Entry{
		Key:         keyPluginPrefix + pluginID,
		PluginID:    pluginID,
		Name:        item.Name,
		FileName:    version.FileName,
		Dir:         item.TargetDir,
		Enabled:     enabled,
		Managed:     true,
		Size:        version.Size,
		Tag:         version.Tag,
		Version:     version.Version,
		InstalledAt: time.Now(),
	}, nil
}

// SetEnabled switches a plugin on or off by renaming its jar.
func (m *Instances) SetEnabled(instanceID, directory, key string, enabled bool) error {
	dir, name, err := m.resolveKey(instanceID, key)
	if err != nil {
		return err
	}
	browser := serverfiles.New(directory)

	current, currentlyEnabled, ok := m.locate(browser, dir, name)
	if !ok {
		return fmt.Errorf("%w: %s/%s is not there", ErrNotInstalled, dir, name)
	}
	if currentlyEnabled == enabled {
		return nil
	}

	target := dir + "/" + name
	if !enabled {
		target += disabledSuffix
	}
	// Rename refuses to clobber, so a leftover file at the target name — a
	// disabled copy from an earlier round, say — goes first.
	_ = browser.Remove(target)
	return browser.Rename(current, target)
}

// Uninstall removes a plugin from an instance.
//
// The plugin's config directory is deliberately left behind: it is the
// operator's data, an uninstall is often a version troubleshooting step, and
// deleting a world-adjacent directory is not something a jar removal should
// decide on its own.
func (m *Instances) Uninstall(instanceID, directory, key string) error {
	dir, name, err := m.resolveKey(instanceID, key)
	if err != nil {
		return err
	}
	browser := serverfiles.New(directory)

	current, _, ok := m.locate(browser, dir, name)
	if ok {
		if err := browser.Remove(current); err != nil {
			return err
		}
	}
	if pluginID, found := strings.CutPrefix(key, keyPluginPrefix); found {
		return m.forgetPlugin(instanceID, pluginID)
	}
	if !ok {
		return fmt.Errorf("%w: %s/%s is not there", ErrNotInstalled, dir, name)
	}
	return nil
}

// Forget drops every record for an instance, for when the instance itself is
// deleted. The files go with the instance directory, or stay with it if the
// operator kept the directory.
func (m *Instances) Forget(instanceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	records := m.load()
	if _, ok := records[instanceID]; !ok {
		return nil
	}
	delete(records, instanceID)
	return m.save(records)
}

// UsedBy maps a plugin id to the instance ids that have it installed, so the
// library page can say what a version is actually being used for.
func (m *Instances) UsedBy() map[string][]string {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := map[string][]string{}
	for instanceID, records := range m.load() {
		for _, record := range records {
			out[record.PluginID] = append(out[record.PluginID], instanceID)
		}
	}
	return out
}

// Records returns an instance's install records, for callers that need the
// pinned versions without touching the disk.
func (m *Instances) Records(instanceID string) []Installed {
	return m.recordsFor(instanceID)
}

// locate finds a plugin's jar under either name, reporting whether it is
// currently enabled.
func (m *Instances) locate(browser *serverfiles.Browser, dir, name string) (string, bool, bool) {
	enabledPath := dir + "/" + name
	if _, err := browser.Stat(enabledPath); err == nil {
		return enabledPath, true, true
	}
	disabledPath := enabledPath + disabledSuffix
	if _, err := browser.Stat(disabledPath); err == nil {
		return disabledPath, false, true
	}
	return "", false, false
}

// resolveKey turns a list key back into the directory and file it names.
func (m *Instances) resolveKey(instanceID, key string) (string, string, error) {
	if pluginID, ok := strings.CutPrefix(key, keyPluginPrefix); ok {
		record := m.record(instanceID, pluginID)
		if record == nil {
			return "", "", fmt.Errorf("%w: %s", ErrNotInstalled, pluginID)
		}
		return record.Dir, record.FileName, nil
	}

	rel, ok := strings.CutPrefix(key, keyFilePrefix)
	if !ok {
		return "", "", fmt.Errorf("%w: %q", ErrInvalidID, key)
	}
	dir, name := path.Split(strings.Trim(rel, "/"))
	dir = strings.Trim(dir, "/")
	if dir == "" || name == "" {
		return "", "", fmt.Errorf("%w: %q", ErrInvalidID, key)
	}
	// serverfiles confines everything to the instance directory, but the file
	// half of a key must still name a jar rather than, say, server.properties.
	if trimmed, _, jar := jarName(name); !jar || trimmed != name {
		return "", "", fmt.Errorf("%w: %q is not a plugin jar", ErrInvalidID, key)
	}
	return dir, name, nil
}

func (m *Instances) record(instanceID, pluginID string) *Installed {
	for _, record := range m.recordsFor(instanceID) {
		if record.PluginID == pluginID {
			found := record
			return &found
		}
	}
	return nil
}

func (m *Instances) recordsFor(instanceID string) []Installed {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Installed(nil), m.load()[instanceID]...)
}

func (m *Instances) put(instanceID string, record Installed) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	records := m.load()
	list := records[instanceID]
	for i := range list {
		if list[i].PluginID == record.PluginID {
			list[i] = record
			records[instanceID] = list
			return m.save(records)
		}
	}
	records[instanceID] = append(list, record)
	return m.save(records)
}

func (m *Instances) forgetPlugin(instanceID, pluginID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	records := m.load()
	list := records[instanceID]
	kept := make([]Installed, 0, len(list))
	for _, record := range list {
		if record.PluginID == pluginID {
			continue
		}
		kept = append(kept, record)
	}
	if len(kept) == len(list) {
		return nil
	}
	if len(kept) == 0 {
		delete(records, instanceID)
	} else {
		records[instanceID] = kept
	}
	return m.save(records)
}

func (m *Instances) load() map[string][]Installed {
	if m.loaded {
		return m.records
	}
	m.loaded = true
	m.records = map[string][]Installed{}

	data, err := os.ReadFile(m.path)
	if err != nil {
		return m.records
	}
	var stored map[string][]Installed
	if err := json.Unmarshal(data, &stored); err != nil || stored == nil {
		return m.records
	}
	m.records = stored
	return m.records
}

func (m *Instances) save(records map[string][]Installed) error {
	m.records = records
	if err := os.MkdirAll(path.Dir(m.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}

	temp := m.path + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(temp, m.path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

// jarName strips a .disabled suffix and reports whether what is left is a jar.
func jarName(name string) (string, bool, bool) {
	if trimmed, disabled := strings.CutSuffix(name, disabledSuffix); disabled {
		return trimmed, false, strings.EqualFold(path.Ext(trimmed), ".jar")
	}
	return name, true, strings.EqualFold(path.Ext(name), ".jar")
}

// copyInto writes a jar to a temporary name inside the instance directory and
// renames it into place, so an interrupted copy cannot leave a truncated jar
// that a server would try to load.
func copyInto(browser *serverfiles.Browser, source io.Reader, name string) error {
	temp := name + partSuffix
	_ = browser.Remove(temp)

	file, closer, err := browser.Create(temp, true)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, source)
	closeErr := file.Close()
	closer()
	if copyErr != nil || closeErr != nil {
		_ = browser.Remove(temp)
		return errors.Join(copyErr, closeErr)
	}

	// Rename refuses to clobber, so anything already at the target name goes
	// first — only now, with the replacement complete on disk.
	if _, err := browser.Stat(name); err == nil {
		if err := browser.Remove(name); err != nil {
			_ = browser.Remove(temp)
			return err
		}
	}
	if err := browser.Rename(temp, name); err != nil {
		_ = browser.Remove(temp)
		return err
	}
	return nil
}
