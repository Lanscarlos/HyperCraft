package plugin

// An upgrade is a transaction, not a file copy.
//
// The common way to break a server is to put the new jar in and stop there.
// LuckPerms-5.5.70.jar and LuckPerms-5.5.71.jar are two different file names,
// so "copy the new one in" leaves both, the server loads both, and the plugin
// registers its commands and listeners twice. What that looks like from the
// console is a spray of unrelated errors that name neither version — which is
// why it is the upgrade accident that costs the most time to diagnose.
//
// The order is fixed:
//
//	1. read what is there, by descriptor rather than by file name
//	2. back up every jar that has to go, and the plugin's config directory
//	3. delete every jar declaring the same plugin name   ← the step that matters
//	4. put the new jar in
//	5. write the snapshot
//	6. reconcile, so the ledger describes the directory that now exists
//
// Step 3 is why step 2 of the identity work exists at all. "Same plugin" is
// what the jar's own descriptor says its name is — a Bukkit server keys its
// duplicate check on exactly that field — so a panel that cannot read the
// descriptor cannot do this step cleanly, and a panel that cannot do this step
// cleanly should not be offering an upgrade button.
//
// Any step failing restores from the backup taken in step 2 and leaves the
// server as it was. The snapshot is what a rollback reads afterwards: it names
// the jar's digest and where its bytes were kept, so rolling back works even
// though the library may since have pruned that version.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lanscarlos/hypercraft/internal/serverfiles"
)

// keepSnapshots bounds the undo history per instance. This is a rollback path,
// not an archive: what anybody actually reaches for is the version they were on
// an hour ago, and the disk cost is a full copy of a jar per entry.
const keepSnapshots = 5

// maxConfigBackup caps what one snapshot copies out of a plugin's config
// directory. Most are a few kilobytes of YAML; a few carry a player database
// that runs to gigabytes, and silently copying that on every upgrade is how a
// panel fills the disk it is running on. Past the cap the jar is still backed
// up and the snapshot says the config was not — see Snapshot.Note.
const maxConfigBackup = 256 << 20

// Snapshot is what an upgrade left behind, and everything a rollback needs.
//
// It records the bytes rather than pointing at the library, deliberately. The
// library prunes old versions, an operator can delete one by hand, and a
// rollback that depended on the old jar still being in the library would work
// right up until the day somebody tidied up.
type Snapshot struct {
	ID       string `json:"id"`
	PluginID string `json:"pluginId"`
	// PluginName is the descriptor name the sweep matched on, kept so a
	// rollback can repeat the same sweep.
	PluginName string    `json:"pluginName,omitempty"`
	Dir        string    `json:"dir"`
	At         time.Time `json:"at"`
	// By is who asked for it. A fleet with several operators needs the log to
	// say which of them moved production onto a new build.
	By     string `json:"by,omitempty"`
	Action string `json:"action"`

	// From is the state being left. Nil for a first install, which is what
	// makes "there is nothing to roll back to" a fact rather than a guess.
	From *SnapshotSide `json:"from,omitempty"`
	To   SnapshotSide  `json:"to"`

	// BackupDir holds the jars listed in Removed, and the config copy when
	// there is one. An absolute path outside every instance directory.
	BackupDir string   `json:"backupDir,omitempty"`
	Removed   []string `json:"removed,omitempty"`

	ConfigDir   string `json:"configDir,omitempty"`
	ConfigSaved bool   `json:"configSaved"`
	// Note explains anything the snapshot could not do, in the operator's own
	// terms. Shown on the rollback button, because it changes what that button
	// will actually restore.
	Note string `json:"note,omitempty"`
}

// SnapshotSide is one end of an upgrade: which release, which jar, which bytes.
type SnapshotSide struct {
	Tag      string `json:"tag,omitempty"`
	Version  string `json:"version,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	FileName string `json:"fileName,omitempty"`
}

// Snapshots returns an instance's undo history, newest first.
func (m *Instances) Snapshots(instanceID string) []Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	book := m.load()[instanceID]
	if book == nil {
		return nil
	}
	out := make([]Snapshot, 0, len(book.Snapshots))
	for i := len(book.Snapshots) - 1; i >= 0; i-- {
		out = append(out, book.Snapshots[i])
	}
	return out
}

// Install puts a library version onto an instance, as a transaction.
//
// Both "add" and "change version": a plugin the instance already has is
// upgraded or rolled back in place, keeping whether it was switched off, so
// changing the version of a disabled plugin does not quietly turn it back on.
func (m *Instances) Install(instanceID, directory, pluginID, tag string) (Entry, error) {
	entry, _, err := m.InstallArtifact(instanceID, directory, pluginID, tag, "", "")
	return entry, err
}

// InstallArtifact is Install with a choice of jar and a name for the log.
//
// The digest picks which jar of the release goes on: a plugin that ships one
// build per platform has a different right answer for a Velocity proxy than for
// a Paper server, and that is a property of the artifact, not of the version.
// Empty means the release's primary jar.
func (m *Instances) InstallArtifact(instanceID, directory, pluginID, tag, sha, actor string) (Entry, Snapshot, error) {
	source, item, version, artifact, err := m.library.OpenArtifact(pluginID, tag, sha)
	if err != nil {
		return Entry{}, Snapshot{}, err
	}
	defer source.Close()

	// The instance directory may have been removed since the instance was
	// made, and os.OpenRoot needs it to exist before anything below it does.
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return Entry{}, Snapshot{}, err
	}
	browser := serverfiles.New(directory)
	if err := browser.Mkdir(item.TargetDir); err != nil {
		return Entry{}, Snapshot{}, err
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

	// --- 1. what has to go -------------------------------------------------
	//
	// By descriptor name, not by file name. Two releases of the same plugin
	// almost never share a file name, and a jar somebody renamed shares it
	// with nothing at all.
	sweepName := strings.TrimSpace(artifact.PluginName)
	if sweepName == "" && previous != nil {
		sweepName = previous.PluginName
	}
	doomed := m.sameName(browser, item, sweepName, previous)

	stamp := time.Now()
	snapshot := Snapshot{
		ID:         fmt.Sprintf("%d", stamp.UnixNano()),
		PluginID:   pluginID,
		PluginName: sweepName,
		Dir:        item.TargetDir,
		At:         stamp,
		By:         actor,
		Action:     ActionInstall,
		To: SnapshotSide{
			Tag: version.Tag, Version: version.Version,
			SHA256: artifact.SHA256, FileName: artifact.FileName,
		},
		ConfigDir: item.TargetDir + "/" + configName(artifact, item),
	}
	if previous != nil {
		snapshot.Action = ActionUpgrade
		snapshot.From = &SnapshotSide{
			Tag: previous.Tag, Version: previous.Version,
			SHA256: previous.SHA256, FileName: previous.FileName,
		}
	}

	// --- 2. back it up -----------------------------------------------------
	backupDir := filepath.Join(m.backups, safeName(instanceID), snapshot.ID)
	saved, note, err := m.backup(browser, backupDir, item.TargetDir, doomed, snapshot.ConfigDir)
	if err != nil {
		_ = os.RemoveAll(backupDir)
		return Entry{}, Snapshot{}, fmt.Errorf("升级前备份失败，没有动服务器上的任何文件：%w", err)
	}
	snapshot.Removed = doomed
	snapshot.ConfigSaved = saved
	snapshot.Note = note
	// A first install has nothing to keep. Recording a backup directory that
	// holds nothing would offer a rollback that restores nothing.
	if len(doomed) == 0 && !saved {
		_ = os.RemoveAll(backupDir)
	} else {
		snapshot.BackupDir = backupDir
	}

	// --- 3. delete every jar declaring the same name -----------------------
	//
	// Explicitly, rather than trusting the copy below to overwrite. Overwriting
	// only works when the two releases happen to share a file name, and when
	// they do not, what is left is two jars of the same plugin and a server
	// that loads both.
	for _, name := range doomed {
		_ = browser.Remove(item.TargetDir + "/" + name)
		_ = browser.Remove(item.TargetDir + "/" + name + disabledSuffix)
	}

	// --- 4. put the new jar in ---------------------------------------------
	landing := item.TargetDir + "/" + artifact.FileName
	if !enabled {
		landing += disabledSuffix
	}
	if err := copyInto(browser, source, landing); err != nil {
		// Nothing half-done survives: the directory goes back to exactly the
		// jars it had, and the failure is about a server that is still running
		// what it was running.
		restoreErr := m.restore(browser, backupDir, item.TargetDir, doomed)
		_ = browser.Remove(landing)
		return Entry{}, Snapshot{}, errors.Join(
			fmt.Errorf("复制新版本失败，已还原到升级前：%w", err), restoreErr)
	}

	// --- 5. write it down --------------------------------------------------
	record := Installed{
		PluginID:     pluginID,
		Tag:          version.Tag,
		Version:      version.Version,
		FileName:     artifact.FileName,
		Dir:          item.TargetDir,
		InstalledAt:  stamp,
		SHA256:       artifact.SHA256,
		PluginName:   artifact.PluginName,
		ObservedSHA:  artifact.SHA256,
		Recon:        ReconOK,
		CheckedAt:    stamp,
		GameVersions: artifactVersions(artifact, version),
		Loaders:      artifactLoaders(artifact, version),
	}
	if err := m.put(instanceID, record); err != nil {
		return Entry{}, Snapshot{}, err
	}
	if err := m.pushSnapshot(instanceID, snapshot); err != nil {
		return Entry{}, Snapshot{}, err
	}

	return Entry{
		Key:           keyPluginPrefix + pluginID,
		PluginID:      pluginID,
		Name:          item.Name,
		FileName:      artifact.FileName,
		Dir:           item.TargetDir,
		Enabled:       enabled,
		Managed:       true,
		Size:          artifact.Size,
		Tag:           version.Tag,
		Version:       version.Version,
		InstalledAt:   stamp,
		SHA256:        artifact.SHA256,
		RecordSHA:     artifact.SHA256,
		Recon:         ReconOK,
		CheckedAt:     stamp,
		SelfUpdate:    item.Policy.AllowSelfUpdate,
		GameVersions:  record.GameVersions,
		Loaders:       record.Loaders,
		PendingAction: snapshot.Action,
		ConfigDir:     snapshot.ConfigDir,
	}, snapshot, nil
}

// Rollback undoes the last upgrade of one plugin on one instance.
//
// It reads the snapshot rather than the library: the point of a rollback is
// that the version you want is the one that was working an hour ago, and
// whether the library still holds it is a separate question with a separate
// answer. Restoring the config directory is a choice, not part of the jar
// swap — a plugin that has been running since the upgrade has written data
// nobody asked to throw away.
func (m *Instances) Rollback(instanceID, directory string, pluginID string, withConfig bool) (Entry, error) {
	snapshot, ok := m.lastSnapshot(instanceID, pluginID)
	if !ok || snapshot.From == nil {
		return Entry{}, fmt.Errorf("%w: 这个插件在这台实例上没有可回滚的上一版本", ErrNotFound)
	}
	if snapshot.BackupDir == "" || len(snapshot.Removed) == 0 {
		return Entry{}, fmt.Errorf("%w: 上一版本的备份已经不在了，没法回滚", ErrNotFound)
	}
	if _, err := os.Stat(filepath.Join(snapshot.BackupDir, snapshot.From.FileName)); err != nil {
		return Entry{}, fmt.Errorf("%w: 备份文件 %s 已经不在了，没法回滚", ErrNotFound, snapshot.From.FileName)
	}

	browser := serverfiles.New(directory)
	item, libErr := m.library.Get(pluginID)

	// The same sweep the upgrade did, in reverse: everything declaring this
	// plugin's name goes, then the backed-up jars come back. Without the sweep
	// a rollback leaves the newer jar beside the restored older one, which is
	// the exact accident the upgrade path exists to avoid.
	current := m.record(instanceID, pluginID)
	enabled := true
	if current != nil {
		if _, on, ok := m.locate(browser, current.Dir, current.FileName); ok {
			enabled = on
		}
	}
	for _, name := range m.sameName(browser, item, snapshot.PluginName, current) {
		_ = browser.Remove(snapshot.Dir + "/" + name)
		_ = browser.Remove(snapshot.Dir + "/" + name + disabledSuffix)
	}

	if err := m.restore(browser, snapshot.BackupDir, snapshot.Dir, snapshot.Removed); err != nil {
		return Entry{}, err
	}
	if withConfig && snapshot.ConfigSaved {
		if err := restoreTree(browser, filepath.Join(snapshot.BackupDir, configBackupName), snapshot.ConfigDir); err != nil {
			return Entry{}, fmt.Errorf("jar 已回滚，但恢复配置目录失败：%w", err)
		}
	}
	if !enabled {
		_ = browser.Rename(snapshot.Dir+"/"+snapshot.From.FileName,
			snapshot.Dir+"/"+snapshot.From.FileName+disabledSuffix)
	}

	now := time.Now()
	record := Installed{
		PluginID:    pluginID,
		Tag:         snapshot.From.Tag,
		Version:     snapshot.From.Version,
		FileName:    snapshot.From.FileName,
		Dir:         snapshot.Dir,
		InstalledAt: now,
		SHA256:      snapshot.From.SHA256,
		PluginName:  snapshot.PluginName,
		ObservedSHA: snapshot.From.SHA256,
		Recon:       ReconOK,
		CheckedAt:   now,
	}
	if libErr == nil {
		if version := item.Version(snapshot.From.Tag); version != nil {
			record.GameVersions = version.GameVersions
			record.Loaders = version.Loaders
		}
	}
	if err := m.put(instanceID, record); err != nil {
		return Entry{}, err
	}
	// The snapshot is spent. Leaving it would offer a second rollback that
	// restores the same bytes over themselves and reports success.
	if err := m.dropSnapshot(instanceID, snapshot.ID); err != nil {
		return Entry{}, err
	}

	name := pluginID
	if libErr == nil {
		name = item.Name
	}
	return Entry{
		Key:           keyPluginPrefix + pluginID,
		PluginID:      pluginID,
		Name:          name,
		FileName:      snapshot.From.FileName,
		Dir:           snapshot.Dir,
		Enabled:       enabled,
		Managed:       true,
		Tag:           snapshot.From.Tag,
		Version:       snapshot.From.Version,
		InstalledAt:   now,
		SHA256:        snapshot.From.SHA256,
		RecordSHA:     snapshot.From.SHA256,
		Recon:         ReconOK,
		CheckedAt:     now,
		PendingAction: ActionUpgrade,
		ConfigDir:     snapshot.ConfigDir,
	}, nil
}

// sameName lists the jars in a plugin's directory that declare the same plugin
// name as the one going in — the set step 3 has to delete.
//
// Three sources, in order of how much they can be trusted. The descriptor name
// read out of each jar on the spot, which is what the server itself keys on.
// The names this plugin's library downloads declare, which catches a jar whose
// descriptor will not parse today but did when it was downloaded. And the file
// the panel's own record points at, which catches the jar that has none of the
// above because it was installed before any of this existed.
func (m *Instances) sameName(browser *serverfiles.Browser, item Plugin, declared string, record *Installed) []string {
	wanted := map[string]bool{}
	if name := strings.ToLower(strings.TrimSpace(declared)); name != "" {
		wanted[name] = true
	}
	for _, name := range item.DeclaredNames() {
		wanted[name] = true
	}

	dir := item.TargetDir
	if dir == "" && record != nil {
		dir = record.Dir
	}
	if dir == "" {
		dir = DefaultTargetDir
	}

	out := make([]string, 0, 2)
	seen := map[string]bool{}
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}

	entries, err := browser.List(dir)
	if err == nil && len(wanted) > 0 {
		for _, file := range entries {
			if file.IsDir {
				continue
			}
			name, _, ok := jarName(file.Name)
			if !ok {
				continue
			}
			handle, info, closer, err := browser.Open(dir + "/" + file.Name)
			if err != nil {
				continue
			}
			jar, read := ReadJarInfo(handle, info.Size())
			closer()
			if read && wanted[strings.ToLower(strings.TrimSpace(jar.Name))] {
				add(name)
			}
		}
	}
	if record != nil && record.Dir == dir {
		add(record.FileName)
	}
	return out
}

// configName is what the plugin calls its own directory inside the instance.
//
// Bukkit names it after the descriptor's name, not after the jar —
// EssentialsX-2.20.1.jar writes to plugins/Essentials/ — so the declared name
// is used wherever there is one.
func configName(artifact Artifact, item Plugin) string {
	if name := strings.TrimSpace(artifact.PluginName); name != "" {
		return name
	}
	return item.Name
}

func artifactVersions(artifact Artifact, version Version) []string {
	if len(artifact.GameVersions) > 0 {
		return artifact.GameVersions
	}
	return version.GameVersions
}

func artifactLoaders(artifact Artifact, version Version) []string {
	if len(artifact.Loaders) > 0 {
		return artifact.Loaders
	}
	return version.Loaders
}

// configBackupName is the subdirectory a snapshot keeps the config copy under,
// so it cannot collide with a jar of the same name.
const configBackupName = "__config"

// backup copies the jars about to be deleted, and the plugin's config
// directory, out of the instance.
//
// The second return says whether the config made it, and the third explains in
// the operator's own words when it did not. Both surface on the rollback
// button: "roll back the jar" and "roll back the jar and its config" are
// different offers and the panel has to know which one it can make.
func (m *Instances) backup(browser *serverfiles.Browser, dst, dir string, jars []string, configDir string) (bool, string, error) {
	if len(jars) == 0 && configDir == "" {
		return false, "", nil
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return false, "", err
	}

	for _, name := range jars {
		from, _, ok := m.locate(browser, dir, name)
		if !ok {
			continue
		}
		if err := backupFile(browser, from, filepath.Join(dst, name)); err != nil {
			return false, "", err
		}
	}

	size, err := treeSize(browser, configDir)
	switch {
	case err != nil:
		// No config directory yet is the normal case for a first install.
		return false, "", nil
	case size > maxConfigBackup:
		return false, fmt.Sprintf("配置目录有 %d MB，超过了快照 %d MB 的上限，这次只备份了 jar；回滚只会换回旧版本的 jar，配置保持现状。",
			size>>20, maxConfigBackup>>20), nil
	}
	if err := backupTree(browser, configDir, filepath.Join(dst, configBackupName)); err != nil {
		return false, "备份配置目录失败，这次只备份了 jar。", nil
	}
	return true, "", nil
}

// restore puts backed-up jars back where they came from.
func (m *Instances) restore(browser *serverfiles.Browser, backupDir, dir string, jars []string) error {
	var failures []error
	for _, name := range jars {
		src := filepath.Join(backupDir, name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := restoreFile(browser, src, dir+"/"+name); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (m *Instances) pushSnapshot(instanceID string, snapshot Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	records := m.load()
	book := m.ledgerFor(records, instanceID)
	book.Snapshots = append(book.Snapshots, snapshot)

	// Oldest first out, and their bytes with them — a bounded history that
	// left its backups on disk would be an unbounded directory.
	for len(book.Snapshots) > keepSnapshots {
		if dir := book.Snapshots[0].BackupDir; dir != "" {
			_ = os.RemoveAll(dir)
		}
		book.Snapshots = book.Snapshots[1:]
	}
	return m.save(records)
}

func (m *Instances) lastSnapshot(instanceID, pluginID string) (Snapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	book := m.load()[instanceID]
	if book == nil {
		return Snapshot{}, false
	}
	for i := len(book.Snapshots) - 1; i >= 0; i-- {
		if book.Snapshots[i].PluginID == pluginID {
			return book.Snapshots[i], true
		}
	}
	return Snapshot{}, false
}

func (m *Instances) dropSnapshot(instanceID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	records := m.load()
	book := records[instanceID]
	if book == nil {
		return nil
	}
	kept := make([]Snapshot, 0, len(book.Snapshots))
	for _, snapshot := range book.Snapshots {
		if snapshot.ID == id {
			if snapshot.BackupDir != "" {
				_ = os.RemoveAll(snapshot.BackupDir)
			}
			continue
		}
		kept = append(kept, snapshot)
	}
	book.Snapshots = kept
	return m.save(records)
}

// ForgetBackups deletes an instance's snapshot directory, for when the instance
// itself goes.
func (m *Instances) ForgetBackups(instanceID string) {
	_ = os.RemoveAll(filepath.Join(m.backups, safeName(instanceID)))
}

// --------------------------------------------------------------- file moving

func backupFile(browser *serverfiles.Browser, rel, dst string) error {
	source, _, closer, err := browser.Open(rel)
	if err != nil {
		return err
	}
	defer closer()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	file, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, source)
	return errors.Join(copyErr, file.Close())
}

func restoreFile(browser *serverfiles.Browser, src, rel string) error {
	file, err := os.Open(src)
	if err != nil {
		return err
	}
	defer file.Close()
	return copyInto(browser, file, rel)
}

// treeSize adds up a directory inside the instance. Errors mean "no directory",
// which for a plugin that has never run is the ordinary case.
func treeSize(browser *serverfiles.Browser, rel string) (int64, error) {
	entries, err := browser.List(rel)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, entry := range entries {
		if entry.IsDir {
			nested, err := treeSize(browser, rel+"/"+entry.Name)
			if err != nil {
				continue
			}
			total += nested
			continue
		}
		total += entry.Size
	}
	return total, nil
}

func backupTree(browser *serverfiles.Browser, rel, dst string) error {
	entries, err := browser.List(rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir {
			if err := backupTree(browser, rel+"/"+entry.Name, filepath.Join(dst, entry.Name)); err != nil {
				return err
			}
			continue
		}
		if err := backupFile(browser, rel+"/"+entry.Name, filepath.Join(dst, entry.Name)); err != nil {
			return err
		}
	}
	return nil
}

func restoreTree(browser *serverfiles.Browser, src, rel string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := browser.Mkdir(rel); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			if err := restoreTree(browser, filepath.Join(src, entry.Name()), rel+"/"+entry.Name()); err != nil {
				return err
			}
			continue
		}
		if err := restoreFile(browser, filepath.Join(src, entry.Name()), rel+"/"+entry.Name()); err != nil {
			return err
		}
	}
	return nil
}

// safeName reduces an instance id to something that cannot escape the backup
// directory. Instance ids are already constrained, and this is the one place
// where being wrong about that writes outside the panel's own data.
func safeName(id string) string {
	out := slug(id)
	if out == "" {
		return "instance"
	}
	return out
}
