package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
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

	// SHA256 is the digest of the jar the panel put here, and PluginName is
	// what that jar declares itself as. Together they are the ledger entry the
	// reconciliation checks the directory against — the file name is not, and
	// cannot be, because operators rename jars and plugins self-update.
	SHA256     string `json:"sha256,omitempty"`
	PluginName string `json:"pluginName,omitempty"`

	// ObservedSHA and Recon are what the last reconciliation actually found.
	// Empty Recon means this record has never been checked, which is a
	// different statement from "checked and fine" and is shown as one.
	ObservedSHA string    `json:"observedSha,omitempty"`
	Recon       string    `json:"recon,omitempty"`
	CheckedAt   time.Time `json:"checkedAt,omitempty"`
	// GameVersions and Loaders are what the source said this exact jar
	// supports, copied out of the library version at install time.
	//
	// Copied rather than looked up: the compatibility badge on an instance's
	// plugin list has to keep working after the library entry is deleted or
	// the version pruned, and what is in this server's directory does not
	// change when the library does.
	GameVersions []string `json:"gameVersions,omitempty"`
	Loaders      []string `json:"loaders,omitempty"`
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
	// Jar is what the file says about itself: the name, version, description,
	// authors and dependencies the server will read at startup, rather than
	// whatever the file happens to be called.
	//
	// Read for every jar in the directory, managed ones included. The panel's
	// record says which release it installed; it does not say what the plugin
	// requires to load, and a missing 前置依赖 is the single most common reason
	// a plugin that is definitely in the directory is definitely not running.
	Jar *JarInfo `json:"jar,omitempty"`
	// Conflicts are the other jars in this instance that declare the same
	// plugin name. Bukkit loads exactly one of them and refuses the rest, and
	// which one it picks is directory order — so this is a real, silent
	// failure, and the file names are what tells the operator which copy to
	// delete. Empty for the normal case.
	Conflicts []string `json:"conflicts,omitempty"`
	// Adoptable names the library plugin an unmanaged jar turned out to be,
	// matched by content rather than by name, so the panel can offer to start
	// tracking a file somebody installed by hand. Nil when the jar is not
	// byte-for-byte one of the library's downloads.
	Adoptable *Adoptable `json:"adoptable,omitempty"`

	// Recon is how this row's file compares with the ledger: ReconForeign for
	// a jar nobody recorded, ReconDrift for one whose bytes have changed under
	// the record, ReconMissing for a record whose file is gone. Empty means
	// the two agree, or — for a managed row — that nothing has checked yet.
	//
	// This outranks every version badge on the row. "有更新" computed from a
	// ledger that does not match the disk is a sentence about a file that is
	// not there.
	Recon string `json:"recon,omitempty"`
	// SHA256 is what is on disk right now, RecordSHA what the ledger expected.
	// Both are shown for a drift, because the useful question is which of the
	// two the operator recognises.
	SHA256    string    `json:"sha256,omitempty"`
	RecordSHA string    `json:"recordSha,omitempty"`
	CheckedAt time.Time `json:"checkedAt,omitempty"`
	// SelfUpdate is this plugin's 允许自更新 setting, so the row can explain
	// why a drift is being reported quietly rather than as a problem.
	SelfUpdate bool `json:"selfUpdate,omitempty"`

	// The three fields below are filled in by the API layer, which is the only
	// place that knows what the server is running and what it printed while
	// starting. They are on Entry rather than in a parallel list because every
	// one of them is a property of this row, and a page that had to join three
	// lists by name would get the join wrong for exactly the plugins whose
	// names disagree with their file names — which is most of the broken ones.

	// Compat is whether this jar suits the server's version and loader.
	Compat *Compat `json:"compat,omitempty"`
	// Update is the newest version in the library this server could move to,
	// and which jar of it. Nil when it is already on the newest, when the
	// plugin is pinned, or when everything newer is built for another
	// platform — the last of which is why this is decided here rather than by
	// the page taking versions[0]. See UpdateFor.
	Update *Offer `json:"update,omitempty"`
	// Failure is what the server said when it could not load this plugin. The
	// whole reason this page is a table and not a list of names.
	Failure *Failure `json:"failure,omitempty"`
	// PendingAction is set when this row has a change the running server has
	// not seen: it was installed, upgraded or switched off since it started.
	PendingAction string `json:"pendingAction,omitempty"`
	// ConfigDir is the plugin's own directory inside the instance, whether or
	// not it exists yet — the file manager link, and the thing an operator is
	// actually looking for nine times out of ten.
	ConfigDir string `json:"configDir,omitempty"`
	// GameVersions and Loaders are what this jar claims to support, for the
	// compatibility check and for the row's tooltip.
	GameVersions []string `json:"gameVersions,omitempty"`
	Loaders      []string `json:"loaders,omitempty"`
}

// Adoptable is a library version an unmanaged jar was recognised as.
//
// The match is on the SHA-256 the library recorded when it downloaded the file,
// so this says "this is that download", not "this looks like it". Nothing
// weaker would do: adopting a jar makes the panel claim it knows the version,
// and a guess from a similar file name is exactly how a server ends up pinned
// to a version it is not running.
type Adoptable struct {
	PluginID string `json:"pluginId"`
	Name     string `json:"name"`
	Tag      string `json:"tag"`
	Version  string `json:"version"`
	// FileName is what the library calls this jar, which is often not what the
	// file on the server is called — that mismatch is exactly what a rename
	// looks like, and saying both names is how the operator recognises it.
	FileName string `json:"fileName,omitempty"`
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
	// backups is where an upgrade puts the jar and config it is about to
	// replace. Outside every instance directory, because a backup kept inside
	// the thing being changed is not a backup.
	backups string

	mu      sync.Mutex
	records map[string]*ledger
	loaded  bool
	// configHistory is the config-version side of an upgrade transaction, set
	// once at wiring time. Guarded by mu like the records it writes refs into.
	// See ConfigHistory.
	configHistory ConfigHistory

	// jarsMu guards the descriptor cache. Separate from mu: reading a jar has
	// nothing to do with the records, and one lock over both would make every
	// plugin page load wait behind whatever is writing the ledger.
	jarsMu sync.Mutex
	jars   map[string]*JarInfo
}

// jarCacheMax bounds the descriptor cache. Every entry is keyed by path, size
// and modification time, so a plugin directory that is edited all day grows a
// new key per edit and none of the old ones will ever be asked for again. Past
// the cap the whole map goes: it is a cache of things that cost one small read
// to recompute, and an eviction policy for that would be more machinery than
// the thing it protects.
const jarCacheMax = 2048

func NewInstances(library *Library, path string) *Instances {
	return &Instances{
		library: library,
		path:    path,
		backups: filepath.Join(filepath.Dir(path), "plugin-backups"),
		jars:    map[string]*JarInfo{},
	}
}

// ledger is everything the panel knows about one instance's plugin directory:
// what it put there, when it last checked, and what it would need to undo the
// last upgrade.
type ledger struct {
	Plugins      []Installed `json:"plugins"`
	ReconciledAt time.Time   `json:"reconciledAt,omitempty"`
	// Snapshots are the undo history, newest last. Bounded — see keepSnapshots
	// — because this is a rollback path, not an archive.
	Snapshots []Snapshot `json:"snapshots,omitempty"`
}

// UnmarshalJSON reads both shapes of the record file. Before the ledger it was
// a bare array of installs per instance, and an operator upgrading the panel
// should not have to re-record what every server has.
func (l *ledger) UnmarshalJSON(data []byte) error {
	var legacy []Installed
	if err := json.Unmarshal(data, &legacy); err == nil {
		l.Plugins = legacy
		return nil
	}
	type plain ledger
	var next plain
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	*l = ledger(next)
	return nil
}

func (l *ledger) find(pluginID string) *Installed {
	for i := range l.Plugins {
		if l.Plugins[i].PluginID == pluginID {
			return &l.Plugins[i]
		}
	}
	return nil
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
			Key:          keyPluginPrefix + record.PluginID,
			PluginID:     record.PluginID,
			Name:         record.PluginID,
			FileName:     record.FileName,
			Dir:          record.Dir,
			Managed:      true,
			Tag:          record.Tag,
			Version:      record.Version,
			InstalledAt:  record.InstalledAt,
			RecordSHA:    record.SHA256,
			SHA256:       record.ObservedSHA,
			Recon:        record.Recon,
			CheckedAt:    record.CheckedAt,
			GameVersions: record.GameVersions,
			Loaders:      record.Loaders,
		}
		if item, err := m.library.Get(record.PluginID); err == nil {
			entry.Name = item.Name
			entry.SelfUpdate = item.Policy.AllowSelfUpdate
			// A record written before the panel started copying compatibility
			// metadata has none. The library may still hold the version it was
			// installed from, and reading it back is what stops every plugin
			// installed by an older release reading 未知兼容性 forever.
			if len(entry.GameVersions) == 0 && len(entry.Loaders) == 0 {
				if version := item.Version(record.Tag); version != nil {
					entry.GameVersions = version.GameVersions
					entry.Loaders = version.Loaders
				}
			}
		}
		// Whether the file is there is cheap and is answered from this listing;
		// whether it is still the same file is not, and is answered from the
		// last reconciliation. The two are kept apart on purpose: a deletion
		// is visible immediately, drift is only as fresh as the last check,
		// and the page says which of those it is looking at.
		if disk, ok := found[key]; ok {
			claimed[key] = true
			entry.Enabled = disk.Enabled
			entry.Size = disk.Size
			entry.Modified = disk.Modified
			if entry.Recon == ReconMissing {
				entry.Recon = "" // the file came back since the last pass
			}
			// After the disk fields, because the descriptor is cached against
			// this file's size and modification time.
			m.describe(browser, directory, &entry)
		} else {
			entry.Missing = true
			entry.Recon = ReconMissing
		}
		out = append(out, entry)
	}
	for _, key := range order {
		if claimed[key] {
			continue
		}
		entry := *found[key]
		// A jar no record claims. That is the reconciliation's 库外来源 and it
		// is stated here rather than waiting for a scan, because unlike drift
		// it costs nothing to see: the file is either in the ledger or it is
		// not, and this listing has already read both sides.
		entry.Recon = ReconForeign
		m.identify(browser, directory, &entry)
		out = append(out, entry)
	}

	for i := range out {
		out[i].ConfigDir = out[i].Dir + "/" + configDirName(out[i])
	}
	markConflicts(out)

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

// configDirName is what a plugin calls its own directory.
//
// Bukkit gives a plugin a directory named after the name in its plugin.yml,
// not after the jar — EssentialsX-2.20.1.jar writes to plugins/Essentials/ —
// so the declared name is used wherever the panel has read one. This is a
// link target rather than a claim the directory exists: a plugin that has
// never started has no directory yet, and the file manager saying "this is
// empty" is a better answer than the panel refusing to offer the link.
func configDirName(entry Entry) string {
	if entry.Jar != nil && entry.Jar.Name != "" {
		return entry.Jar.Name
	}
	if entry.Name != "" {
		return entry.Name
	}
	return strings.TrimSuffix(entry.FileName, path.Ext(entry.FileName))
}

// identify says what an unmanaged jar actually is.
//
// Two questions, both answered from the file itself. What does it call itself —
// which is what the server will call it, and rarely what the file is named. And
// is it one of the library's own downloads that simply was not installed
// through the panel, which is the common case for a server restored from a
// backup or set up before the panel existed.
//
// Failure is silence: this is decoration on a listing that has to work for a
// directory full of arbitrary files, and a jar that cannot be read is still a
// jar the operator can see, switch off and delete.
func (m *Instances) identify(browser *serverfiles.Browser, root string, entry *Entry) {
	file, info, closer, err := browser.Open(jarPath(entry))
	if err != nil {
		return
	}
	defer closer()

	key := jarCacheKey(root, entry)
	jar, cached := m.lookupJar(key)
	if !cached {
		jar = m.rememberJar(key, file, info.Size())
	}
	if jar != nil {
		entry.Jar = cloneJarInfo(jar)
		if jar.Name != "" {
			entry.Name = jar.Name
		}
		if entry.Version == "" {
			entry.Version = jar.Version
		}
	}
	entry.Adoptable, entry.SHA256 = m.recognise(file, info.Size())
}

// describe reads what a jar declares about itself, without asking any of the
// questions identify asks about an unmanaged one.
//
// A managed row already knows its name and version from the record — those come
// from the source, which is the better answer. What the record does not hold is
// the description, the authors, and above all the dependency list, and that list
// is the same question whether the panel installed the jar or found it.
//
// Silent on failure, and no fallbacks: a managed row whose jar cannot be read is
// still a row with a name, a version and a working 移除 button.
func (m *Instances) describe(browser *serverfiles.Browser, root string, entry *Entry) {
	key := jarCacheKey(root, entry)
	if jar, cached := m.lookupJar(key); cached {
		entry.Jar = cloneJarInfo(jar)
		return
	}
	file, info, closer, err := browser.Open(jarPath(entry))
	if err != nil {
		return
	}
	defer closer()
	entry.Jar = cloneJarInfo(m.rememberJar(key, file, info.Size()))
}

// jarPath is the file as it sits on disk, which for a disabled plugin is the
// renamed one.
func jarPath(entry *Entry) string {
	name := entry.FileName
	if !entry.Enabled {
		name += disabledSuffix
	}
	return entry.Dir + "/" + name
}

// jarCacheKey identifies one exact file. Size and modification time are in it
// because the point of the cache is to survive polling, not to survive an edit:
// a jar swapped under the same name is a different jar and has to be re-read.
func jarCacheKey(root string, entry *Entry) string {
	return fmt.Sprintf("%s\x00%s\x00%d\x00%d", root, jarPath(entry), entry.Size, entry.Modified.UnixNano())
}

// lookupJar reports what was cached for a file, and whether anything was. The
// two are different: a jar that is not a plugin caches a nil, and re-opening it
// on every poll to learn that again is exactly what the cache is for.
func (m *Instances) lookupJar(key string) (*JarInfo, bool) {
	m.jarsMu.Lock()
	defer m.jarsMu.Unlock()
	jar, ok := m.jars[key]
	return jar, ok
}

// rememberJar reads a descriptor and stores it. The read happens outside the
// lock: two page loads racing on the same jar read it twice and store the same
// answer, which is cheaper than making every other jar in the directory wait.
func (m *Instances) rememberJar(key string, reader io.ReaderAt, size int64) *JarInfo {
	var jar *JarInfo
	if read, ok := ReadJarInfo(reader, size); ok {
		jar = &read
	}
	m.jarsMu.Lock()
	defer m.jarsMu.Unlock()
	if m.jars == nil || len(m.jars) >= jarCacheMax {
		m.jars = make(map[string]*JarInfo, jarCacheMax/4)
	}
	m.jars[key] = jar
	return jar
}

// cloneJarInfo hands each row its own copy, so nothing a caller does to an
// Entry can reach into the cache the next listing will read.
func cloneJarInfo(jar *JarInfo) *JarInfo {
	if jar == nil {
		return nil
	}
	copied := *jar
	return &copied
}

// markConflicts flags jars that declare the same plugin name.
//
// This is not "the same file twice". Two jars can be built from different
// releases, carry different file names and different sizes, and still declare
// `name: Atalanta` — which is what happens when somebody uploads a build by
// hand next to the one the panel installed. Bukkit loads whichever it reaches
// first and refuses the other with a line nobody reads, so the server runs a
// version the panel is not showing, and every version number on the page is
// then about the wrong file.
//
// Only enabled, present jars take part. A .disabled jar is not loaded, so it
// clashes with nothing, and telling someone their switched-off spare is a
// conflict would be crying wolf about the very thing that resolves one.
func markConflicts(entries []Entry) {
	byName := map[string][]int{}
	for i := range entries {
		if entries[i].Missing || !entries[i].Enabled {
			continue
		}
		// The declared name only. The panel's own name for a plugin comes from
		// the source's listing page and is regularly not what the jar declares
		// — matching on it would report clashes the server will never see.
		if entries[i].Jar == nil || entries[i].Jar.Name == "" {
			continue
		}
		name := strings.ToLower(entries[i].Jar.Name)
		byName[name] = append(byName[name], i)
	}

	for _, group := range byName {
		if len(group) < 2 {
			continue
		}
		for _, i := range group {
			for _, j := range group {
				if i != j {
					entries[i].Conflicts = append(entries[i].Conflicts, jarPath(&entries[j]))
				}
			}
		}
	}
}

// recognise matches a file against the library's downloads by content.
//
// Size first, digest second: the size is already known for every version, and
// hashing a few megabytes per jar on every page load to answer "no" for all of
// them would be a page that gets slower with every plugin the operator keeps.
// The digest is returned whether or not it matched anything: a foreign jar's
// checksum is the one thing that makes it identifiable at all, and the row
// shows it so an operator can compare it against whatever they downloaded.
func (m *Instances) recognise(file io.ReadSeeker, size int64) (*Adoptable, string) {
	sized := false
	for _, item := range m.library.List() {
		for _, version := range item.Versions {
			for _, artifact := range version.Artifacts {
				if artifact.Size == size && artifact.SHA256 != "" {
					sized = true
				}
			}
		}
	}
	if !sized {
		return nil, ""
	}

	digest, err := fileDigest(file)
	if err != nil {
		return nil, ""
	}
	return m.matchDigest(digest), digest
}

func fileDigest(file io.ReadSeeker) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// Adopt starts tracking a jar the panel found rather than installed.
//
// Only a jar that is byte-for-byte one of the library's downloads can be
// adopted — see Adoptable. Nothing on disk changes: the file is already in
// place and already loading, and all this adds is the panel's record of which
// plugin and version it is, which is what makes "update it" and "roll it back"
// available afterwards.
func (m *Instances) Adopt(instanceID, directory, key string) (Entry, error) {
	dir, name, err := m.resolveKey(instanceID, key)
	if err != nil {
		return Entry{}, err
	}
	browser := serverfiles.New(directory)

	path, enabled, ok := m.locate(browser, dir, name)
	if !ok {
		return Entry{}, fmt.Errorf("%w: %s/%s is not there", ErrNotInstalled, dir, name)
	}
	file, info, closer, err := browser.Open(path)
	if err != nil {
		return Entry{}, err
	}
	defer closer()

	match, digest := m.recognise(file, info.Size())
	if match == nil {
		return Entry{}, fmt.Errorf("%w: %s does not match any version in the plugin library", ErrNotFound, name)
	}
	declared := ""
	if jar, ok := ReadJarInfo(file, info.Size()); ok {
		declared = jar.Name
	}
	// One record per plugin per instance, so adopting a second copy of a plugin
	// the instance already tracks would silently drop the first.
	if existing := m.record(instanceID, match.PluginID); existing != nil &&
		(existing.Dir != dir || existing.FileName != name) {
		return Entry{}, fmt.Errorf("%w: %s is already tracked here as %s/%s",
			ErrExists, match.Name, existing.Dir, existing.FileName)
	}

	// Adopting is where a foreign jar becomes a ledger entry, so it is where
	// the ledger's two keys get written: the digest that just matched, and the
	// name the jar declares — which the upgrade sweep will need long before
	// anybody thinks to run a reconciliation here.
	now := time.Now()
	if err := m.put(instanceID, Installed{
		PluginID:    match.PluginID,
		Tag:         match.Tag,
		Version:     match.Version,
		FileName:    name,
		Dir:         dir,
		InstalledAt: now,
		SHA256:      digest,
		PluginName:  declared,
		ObservedSHA: digest,
		Recon:       ReconOK,
		CheckedAt:   now,
	}); err != nil {
		return Entry{}, err
	}

	return Entry{
		Key:         keyPluginPrefix + match.PluginID,
		PluginID:    match.PluginID,
		Name:        match.Name,
		FileName:    name,
		Dir:         dir,
		Enabled:     enabled,
		Managed:     true,
		Size:        info.Size(),
		Tag:         match.Tag,
		Version:     match.Version,
		InstalledAt: time.Now(),
	}, nil
}

// ImportToLibrary files a jar found on a server as a library plugin, and then
// records it as this server's copy of it.
//
// The other half of Adopt, and the half that was missing. Adopt only works on a
// file that is byte-for-byte one of the library's own downloads, because the
// version number it writes into the ledger has to come from somewhere and a
// guess off a file name is how a server ends up pinned to a version it is not
// running. For the jars that make up most of 库外来源 — a build from a fork, a
// marketplace plugin, a jar restored from a backup — the library has no such
// answer, and until now the panel's only reply was to name a place to import it
// that did not exist: 导入 jar uploads from the operator's machine, and the file
// is already here.
//
// So it is read where it lies. The library gets a copy, checksummed and filed
// as a version of its own exactly like an upload, and the instance gets the
// ledger entry that makes 换版本, 回滚 and the cross-instance view work
// afterwards. The one thing it does not get is update checking: an imported jar
// has no upstream to check, whichever door it came through.
//
// Nothing in the instance directory moves. The file the server is already
// loading stays where it is, under the name it already has.
func (m *Instances) ImportToLibrary(instanceID, directory, key string, limit int64) (Entry, error) {
	// A managed row is already a library version — importing it would file a
	// second copy of bytes the library holds, under a version number invented
	// beside the real one.
	if !strings.HasPrefix(key, keyFilePrefix) {
		return Entry{}, fmt.Errorf("%w: %q is already tracked", ErrExists, key)
	}
	dir, name, err := m.resolveKey(instanceID, key)
	if err != nil {
		return Entry{}, err
	}
	browser := serverfiles.New(directory)

	path, enabled, ok := m.locate(browser, dir, name)
	if !ok {
		return Entry{}, fmt.Errorf("%w: %s/%s is not there", ErrNotInstalled, dir, name)
	}
	file, info, closer, err := browser.Open(path)
	if err != nil {
		return Entry{}, err
	}
	defer closer()

	// The library may already hold these exact bytes — the operator clicked
	// import on a jar that a reconciliation would have called adoptable. That
	// is Adopt's case, and answering it here keeps one button working for both.
	if match, _ := m.recognise(file, info.Size()); match != nil {
		return m.Adopt(instanceID, directory, key)
	}
	// recognise read to the end to hash; the import starts from the top again.
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Entry{}, err
	}

	imported, err := m.library.ImportJar("", name, file, limit)
	if err != nil {
		return Entry{}, err
	}

	digest, gameVersions, loaders := "", []string(nil), []string(nil)
	if len(imported.Version.Artifacts) > 0 {
		artifact := imported.Version.Artifacts[0]
		digest, gameVersions, loaders = artifact.SHA256, artifact.GameVersions, artifact.Loaders
	}

	// One record per plugin per instance. Reachable when the same jar sits in
	// the directory twice under two names: the first import created the library
	// entry, the second one lands on it. The file is in the library either way,
	// which is what the message has to say — the operator asked for that part
	// and got it.
	if existing := m.record(instanceID, imported.Plugin.ID); existing != nil &&
		(existing.Dir != dir || existing.FileName != name) {
		return Entry{}, fmt.Errorf("%w: %s is in the plugin library now, but this server already tracks it as %s/%s",
			ErrExists, imported.Plugin.Name, existing.Dir, existing.FileName)
	}

	now := time.Now()
	if err := m.put(instanceID, Installed{
		PluginID:    imported.Plugin.ID,
		Tag:         imported.Version.Tag,
		Version:     imported.Version.Version,
		FileName:    name,
		Dir:         dir,
		InstalledAt: now,
		SHA256:      digest,
		PluginName:  imported.Info.Name,
		ObservedSHA: digest,
		Recon:       ReconOK,
		CheckedAt:   now,
		// Copied from the version that was just filed, on the same terms as an
		// install: what this jar declares does not change when the library does.
		GameVersions: gameVersions,
		Loaders:      loaders,
	}); err != nil {
		return Entry{}, err
	}

	return Entry{
		Key:         keyPluginPrefix + imported.Plugin.ID,
		PluginID:    imported.Plugin.ID,
		Name:        imported.Plugin.Name,
		FileName:    name,
		Dir:         dir,
		Enabled:     enabled,
		Managed:     true,
		Size:        info.Size(),
		Tag:         imported.Version.Tag,
		Version:     imported.Version.Version,
		InstalledAt: now,
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

// Uninstall removes a plugin from an instance, and optionally the config
// directory it wrote.
//
// The config directory stays unless it is asked for. It is the operator's data
// — economy balances, land claims, permission groups, all of which live in
// there beside the settings — an uninstall is often a version troubleshooting
// step, and deleting a world-adjacent directory is not something a jar removal
// should decide on its own. But "decide on its own" was doing two jobs: it was
// also the reason an operator who genuinely wanted the plugin gone had to go
// and find the directory in the file manager, guess which of them it was, and
// delete it by hand. Now the panel offers it, off by default, having worked out
// which directory it actually is.
//
// The jar goes first. If removing the config fails the plugin is still gone,
// which is the half the operator asked for either way, and the error says what
// was left behind.
func (m *Instances) Uninstall(instanceID, directory, key string, purgeConfig bool) error {
	dir, name, err := m.resolveKey(instanceID, key)
	if err != nil {
		return err
	}
	browser := serverfiles.New(directory)

	// Read before the jar goes: the config directory is named by what the jar
	// declares, and after the file is deleted there is nothing left to ask.
	config := ""
	if purgeConfig {
		config = m.configPath(instanceID, browser, directory, dir, key)
	}

	current, _, ok := m.locate(browser, dir, name)
	if ok {
		if err := browser.Remove(current); err != nil {
			return err
		}
	}

	var configErr error
	if config != "" {
		if err := browser.Remove(config); err != nil && !errors.Is(err, serverfiles.ErrNotFound) {
			// Not fatal: the jar is gone, which is most of what was asked for.
			configErr = fmt.Errorf("插件已移除，但配置目录 %s 没能删掉：%w", config, err)
		}
	}

	if pluginID, found := strings.CutPrefix(key, keyPluginPrefix); found {
		if err := m.forgetPlugin(instanceID, pluginID); err != nil {
			return err
		}
		return configErr
	}
	if !ok {
		return fmt.Errorf("%w: %s/%s is not there", ErrNotInstalled, dir, name)
	}
	return configErr
}

// configPath is the directory this plugin writes its settings into, as a path
// safe to hand to Remove — or empty when the panel cannot name one it is sure
// about.
//
// Empty rather than a guess, and this is the whole reason it is a function.
// Deleting the wrong directory here costs an operator their permission groups
// or their economy, so every case where the answer is less than certain
// declines instead: a jar whose descriptor will not read, a name that resolves
// to the plugin directory itself, and — the one that actually happens — a
// directory another jar in the same folder also answers to. Two plugins
// declaring one name is already flagged on the page as 重名; it is also exactly
// when "delete this plugin's config" means "delete the other plugin's config".
func (m *Instances) configPath(instanceID string, browser *serverfiles.Browser, directory, dir, key string) string {
	entries, err := m.List(instanceID, directory)
	if err != nil {
		return ""
	}
	var self *Entry
	for i := range entries {
		if entries[i].Key == key {
			self = &entries[i]
			break
		}
	}
	// A row with no descriptor read out of it has no name to go on but the file
	// name, and a file name is not what Bukkit calls the directory.
	if self == nil || self.Jar == nil || self.Jar.Name == "" {
		return ""
	}
	if len(self.Conflicts) > 0 {
		return ""
	}

	folder := strings.TrimSpace(self.Jar.Name)
	// A declared name is author-supplied text and it lands in a path. Anything
	// that is not one plain directory component is refused outright rather than
	// cleaned up into something that might still escape.
	if folder == "" || folder == "." || folder == ".." ||
		strings.ContainsAny(folder, "/\\") || strings.HasPrefix(folder, ".") {
		return ""
	}
	target := dir + "/" + folder
	// Never the plugin directory itself, whatever the jar declares.
	if strings.EqualFold(target, dir) {
		return ""
	}

	info, err := browser.Stat(target)
	if err != nil || !info.IsDir {
		// A plugin that has never started has no directory yet. Nothing to do
		// and nothing to report — the jar removal is the whole operation.
		return ""
	}
	return target
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
	for instanceID, book := range m.load() {
		for _, record := range book.Plugins {
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
	book := m.load()[instanceID]
	if book == nil {
		return nil
	}
	return append([]Installed(nil), book.Plugins...)
}

// ReconciledAt is when this instance's directory was last compared with the
// ledger. The zero time means never, which the page says out loud rather than
// leaving a blank where a date goes.
func (m *Instances) ReconciledAt(instanceID string) time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	if book := m.load()[instanceID]; book != nil {
		return book.ReconciledAt
	}
	return time.Time{}
}

// ledgerFor returns an instance's book, creating it on first write.
func (m *Instances) ledgerFor(records map[string]*ledger, instanceID string) *ledger {
	book := records[instanceID]
	if book == nil {
		book = &ledger{}
		records[instanceID] = book
	}
	return book
}

func (m *Instances) put(instanceID string, record Installed) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	records := m.load()
	book := m.ledgerFor(records, instanceID)
	for i := range book.Plugins {
		if book.Plugins[i].PluginID == record.PluginID {
			book.Plugins[i] = record
			return m.save(records)
		}
	}
	book.Plugins = append(book.Plugins, record)
	return m.save(records)
}

func (m *Instances) forgetPlugin(instanceID, pluginID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	records := m.load()
	book := records[instanceID]
	if book == nil {
		return nil
	}
	kept := make([]Installed, 0, len(book.Plugins))
	for _, record := range book.Plugins {
		if record.PluginID == pluginID {
			continue
		}
		kept = append(kept, record)
	}
	if len(kept) == len(book.Plugins) {
		return nil
	}
	book.Plugins = kept
	if len(kept) == 0 && len(book.Snapshots) == 0 {
		delete(records, instanceID)
	}
	return m.save(records)
}

func (m *Instances) load() map[string]*ledger {
	if m.loaded {
		return m.records
	}
	m.loaded = true
	m.records = map[string]*ledger{}

	data, err := os.ReadFile(m.path)
	if err != nil {
		return m.records
	}
	var stored map[string]*ledger
	if err := json.Unmarshal(data, &stored); err != nil || stored == nil {
		return m.records
	}
	for id, book := range stored {
		if book != nil {
			m.records[id] = book
		}
	}
	return m.records
}

func (m *Instances) save(records map[string]*ledger) error {
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
