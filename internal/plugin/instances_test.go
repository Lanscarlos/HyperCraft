package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// instanceFixture builds a library with one downloaded plugin plus an empty
// instance directory to install it into.
func instanceFixture(t *testing.T) (*Instances, *Library, string, Plugin) {
	t.Helper()
	library := newLibrary(t)
	item := addPlugin(t, library, "Essentials", "EssentialsX/Essentials")
	storeVersion(t, library, item.ID, "v1.0.0", "EssentialsX-1.0.0.jar", "one")
	storeVersion(t, library, item.ID, "v2.0.0", "EssentialsX-2.0.0.jar", "two")

	dir := t.TempDir()
	instances := NewInstances(library, filepath.Join(t.TempDir(), "instance-plugins.json"))
	return instances, library, dir, item
}

func exists(t *testing.T, dir, rel string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
	return err == nil
}

func TestInstallPutsTheJarInThePluginsDirectory(t *testing.T) {
	instances, _, dir, item := instanceFixture(t)

	entry, err := instances.Install("inst", dir, item.ID, "v1.0.0")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if entry.Version != "1.0.0" || !entry.Enabled || !entry.Managed {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	if !exists(t, dir, "plugins/EssentialsX-1.0.0.jar") {
		t.Fatal("the jar is not in plugins/")
	}
}

func TestInstallSwapsTheVersionAndRemovesTheOldJar(t *testing.T) {
	instances, _, dir, item := instanceFixture(t)

	if _, err := instances.Install("inst", dir, item.ID, "v1.0.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := instances.Install("inst", dir, item.ID, "v2.0.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Two versions of one plugin in the same directory is how a server ends up
	// loading both and refusing to start.
	if exists(t, dir, "plugins/EssentialsX-1.0.0.jar") {
		t.Error("the old jar was left behind")
	}
	if !exists(t, dir, "plugins/EssentialsX-2.0.0.jar") {
		t.Error("the new jar is missing")
	}

	entries, err := instances.List("inst", dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Tag != "v2.0.0" {
		t.Fatalf("unexpected listing: %+v", entries)
	}
}

func TestSwitchingVersionKeepsAPluginDisabled(t *testing.T) {
	instances, _, dir, item := instanceFixture(t)

	if _, err := instances.Install("inst", dir, item.ID, "v1.0.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := instances.SetEnabled("inst", dir, keyPluginPrefix+item.ID, false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}

	entry, err := instances.Install("inst", dir, item.ID, "v2.0.0")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	// Changing the version of a plugin that was deliberately switched off must
	// not quietly turn it back on.
	if entry.Enabled {
		t.Error("the new version came back enabled")
	}
	if !exists(t, dir, "plugins/EssentialsX-2.0.0.jar"+disabledSuffix) {
		t.Error("the new jar should have landed disabled")
	}
	if exists(t, dir, "plugins/EssentialsX-1.0.0.jar"+disabledSuffix) {
		t.Error("the old disabled jar was left behind")
	}
}

func TestSetEnabledRenamesBothWays(t *testing.T) {
	instances, _, dir, item := instanceFixture(t)
	if _, err := instances.Install("inst", dir, item.ID, "v1.0.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	key := keyPluginPrefix + item.ID

	if err := instances.SetEnabled("inst", dir, key, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if exists(t, dir, "plugins/EssentialsX-1.0.0.jar") ||
		!exists(t, dir, "plugins/EssentialsX-1.0.0.jar"+disabledSuffix) {
		t.Fatal("disabling did not rename the jar")
	}

	if err := instances.SetEnabled("inst", dir, key, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !exists(t, dir, "plugins/EssentialsX-1.0.0.jar") ||
		exists(t, dir, "plugins/EssentialsX-1.0.0.jar"+disabledSuffix) {
		t.Fatal("enabling did not rename the jar back")
	}
}

func TestListReportsUnmanagedJarsAndTheirState(t *testing.T) {
	instances, _, dir, item := instanceFixture(t)
	if _, err := instances.Install("inst", dir, item.ID, "v1.0.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// A jar someone uploaded through the file manager, and one they switched
	// off by hand. Pretending they are not there is how a server ends up with
	// a plugin nobody can account for.
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, "plugins", name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("Vault.jar", "vault")
	write("Old.jar"+disabledSuffix, "old")
	write("readme.txt", "not a plugin")

	entries, err := instances.List("inst", dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected three rows, got %+v", entries)
	}
	// Managed first: they are what the page can act on.
	if !entries[0].Managed || entries[1].Managed || entries[2].Managed {
		t.Fatalf("unexpected ordering: %+v", entries)
	}

	byName := map[string]Entry{}
	for _, entry := range entries {
		byName[entry.FileName] = entry
	}
	if !byName["Vault.jar"].Enabled {
		t.Error("Vault.jar should be enabled")
	}
	if byName["Old.jar"].Enabled {
		t.Error("Old.jar is renamed .disabled and should read as off")
	}
	if _, ok := byName["readme.txt"]; ok {
		t.Error("a text file is not a plugin")
	}
}

func TestListMarksAnInstalledPluginWhoseFileHasGone(t *testing.T) {
	instances, _, dir, item := instanceFixture(t)
	if _, err := instances.Install("inst", dir, item.ID, "v1.0.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "plugins", "EssentialsX-1.0.0.jar")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	entries, err := instances.List("inst", dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || !entries[0].Missing {
		t.Fatalf("expected the row to be marked missing: %+v", entries)
	}
}

func TestListOnAServerWithNoPluginsDirectory(t *testing.T) {
	instances, _, dir, _ := instanceFixture(t)

	// A server that has never started has no plugins directory, which is not
	// an error worth failing the page over.
	entries, err := instances.List("inst", dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected an empty listing, got %+v", entries)
	}
}

func TestUninstallRemovesTheJarAndTheRecord(t *testing.T) {
	instances, _, dir, item := instanceFixture(t)
	if _, err := instances.Install("inst", dir, item.ID, "v1.0.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := instances.SetEnabled("inst", dir, keyPluginPrefix+item.ID, false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}

	// A disabled plugin is uninstalled by its disabled name; the caller should
	// not have to know which one it is on.
	if err := instances.Uninstall("inst", dir, keyPluginPrefix+item.ID); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if exists(t, dir, "plugins/EssentialsX-1.0.0.jar"+disabledSuffix) {
		t.Error("the jar survived")
	}
	if len(instances.Records("inst")) != 0 {
		t.Error("the record survived")
	}
}

func TestUninstallAnUnmanagedJar(t *testing.T) {
	instances, _, dir, _ := instanceFixture(t)
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugins", "Vault.jar"), []byte("v"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := instances.Uninstall("inst", dir, keyFilePrefix+"plugins/Vault.jar"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if exists(t, dir, "plugins/Vault.jar") {
		t.Error("the jar survived")
	}
}

func TestFileKeysMustNameAJar(t *testing.T) {
	instances, _, dir, _ := instanceFixture(t)

	for _, key := range []string{
		keyFilePrefix + "plugins/server.properties",
		keyFilePrefix + "server.properties",
		keyFilePrefix + "plugins/",
		"nonsense",
	} {
		if err := instances.Uninstall("inst", dir, key); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Uninstall(%q) should have been refused, got %v", key, err)
		}
	}
}

func TestInstallHonoursAPluginsTargetDirectory(t *testing.T) {
	library := newLibrary(t)
	item, err := library.Add("Sodium", Source{Repo: "o/sodium"}, "mods", "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	storeVersion(t, library, item.ID, "v1.0.0", "sodium-1.0.0.jar", "mod")

	dir := t.TempDir()
	instances := NewInstances(library, filepath.Join(t.TempDir(), "instance-plugins.json"))
	if _, err := instances.Install("inst", dir, item.ID, "v1.0.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	// Fabric and Forge read mods/, which is why the directory is a per-plugin
	// field rather than a constant.
	if !exists(t, dir, "mods/sodium-1.0.0.jar") {
		t.Fatal("the jar did not land in mods/")
	}
}

func TestUsedByAndForget(t *testing.T) {
	instances, _, dir, item := instanceFixture(t)
	if _, err := instances.Install("a", dir, item.ID, "v1.0.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := instances.Install("b", t.TempDir(), item.ID, "v2.0.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if used := instances.UsedBy()[item.ID]; len(used) != 2 {
		t.Fatalf("expected two users, got %v", used)
	}
	if err := instances.Forget("a"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if used := instances.UsedBy()[item.ID]; len(used) != 1 || used[0] != "b" {
		t.Fatalf("unexpected users after forget: %v", used)
	}
}

func TestRecordsSurviveAReopen(t *testing.T) {
	library := newLibrary(t)
	item := addPlugin(t, library, "Essentials", "EssentialsX/Essentials")
	storeVersion(t, library, item.ID, "v1.0.0", "EssentialsX-1.0.0.jar", "one")

	path := filepath.Join(t.TempDir(), "instance-plugins.json")
	dir := t.TempDir()
	if _, err := NewInstances(library, path).Install("inst", dir, item.ID, "v1.0.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	reopened := NewInstances(library, path)
	records := reopened.Records("inst")
	if len(records) != 1 || records[0].Tag != "v1.0.0" {
		t.Fatalf("unexpected records after reopen: %+v", records)
	}
}

func TestInstallRefusesAVersionTheLibraryDoesNotHold(t *testing.T) {
	instances, _, dir, item := instanceFixture(t)
	if _, err := instances.Install("inst", dir, item.ID, "v9.9.9"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// storeJar puts a real jar in the library the way a finished download would,
// digest included — which is what recognition matches against.
func storeJar(t *testing.T, library *Library, id, tag, fileName string, body []byte) {
	t.Helper()
	slug, err := versionSlug(tag)
	if err != nil {
		t.Fatalf("versionSlug: %v", err)
	}
	dir := filepath.Join(library.Root(), id, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	digest := sha256.Sum256(body)
	err = library.record(id, Version{
		Tag:         tag,
		Version:     VersionOf(tag),
		FileName:    fileName,
		Size:        int64(len(body)),
		SHA256:      hex.EncodeToString(digest[:]),
		PublishedAt: time.Now(),
		AddedAt:     time.Now(),
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
}

func findEntry(entries []Entry, fileName string) *Entry {
	for i := range entries {
		if entries[i].FileName == fileName {
			return &entries[i]
		}
	}
	return nil
}

func TestListSaysWhatAHandPlacedJarActuallyIs(t *testing.T) {
	_, _, dir, _ := instanceFixture(t)

	// The name a build produces and the name the server calls the plugin are
	// routinely different; the file name alone tells the operator nothing.
	body := jarBytes(t, "plugin.yml", "name: Vault\nversion: 1.7.3\nauthor: cereal\n")
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugins", "build-42-shaded.jar"), body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	instances, _, _, _ := instanceFixture(t)
	entries, err := instances.List("inst", dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	row := findEntry(entries, "build-42-shaded.jar")
	if row == nil {
		t.Fatal("the jar was not listed")
	}
	if row.Name != "Vault" || row.Version != "1.7.3" {
		t.Fatalf("the jar's own description was not read: %+v", row)
	}
	if row.Jar == nil || row.Jar.Platform != "bukkit" || len(row.Jar.Authors) != 1 {
		t.Errorf("unexpected jar info: %+v", row.Jar)
	}
	// Nothing in the library matches it, so there is nothing to adopt.
	if row.Adoptable != nil {
		t.Errorf("unexpected match: %+v", row.Adoptable)
	}
}

func TestAHandInstalledLibraryJarIsRecognisedAndAdoptable(t *testing.T) {
	library := newLibrary(t)
	item := addPlugin(t, library, "Vault", "MilkBowl/Vault")
	body := jarBytes(t, "plugin.yml", "name: Vault\nversion: 1.7.3\n")
	storeJar(t, library, item.ID, "v1.7.3", "Vault-1.7.3.jar", body)

	dir := t.TempDir()
	instances := NewInstances(library, filepath.Join(t.TempDir(), "instance-plugins.json"))
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Same bytes, different name: the operator copied it in over SSH.
	if err := os.WriteFile(filepath.Join(dir, "plugins", "vault.jar"), body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	entries, err := instances.List("inst", dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	row := findEntry(entries, "vault.jar")
	if row == nil || row.Managed {
		t.Fatalf("expected an unmanaged row: %+v", row)
	}
	if row.Adoptable == nil || row.Adoptable.Tag != "v1.7.3" || row.Adoptable.PluginID != item.ID {
		t.Fatalf("the library's own download was not recognised: %+v", row.Adoptable)
	}

	adopted, err := instances.Adopt("inst", dir, row.Key)
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if !adopted.Managed || adopted.Tag != "v1.7.3" || adopted.FileName != "vault.jar" {
		t.Fatalf("unexpected adopted entry: %+v", adopted)
	}

	// Adopting changes the record, not the disk: the file keeps its name, and
	// the row is now one managed plugin rather than two entries for one jar.
	after, err := instances.List("inst", dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(after) != 1 || !after[0].Managed || after[0].FileName != "vault.jar" {
		t.Fatalf("unexpected listing: %+v", after)
	}
	if !exists(t, dir, "plugins/vault.jar") {
		t.Error("the jar should have been left exactly where it was")
	}
}

func TestAdoptRefusesAJarTheLibraryDoesNotHave(t *testing.T) {
	instances, _, dir, _ := instanceFixture(t)
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugins", "stranger.jar"), []byte("who?"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Adopting is the panel claiming it knows which version this is. Without a
	// content match it does not, and saying so beats a plausible guess.
	if _, err := instances.Adopt("inst", dir, "file:plugins/stranger.jar"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// writeJar puts a real, readable jar into an instance's plugins directory.
func writeJar(t *testing.T, dir, name, descriptor string) {
	t.Helper()
	plugins := filepath.Join(dir, "plugins")
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(plugins, name), jarBytes(t, "plugin.yml", descriptor), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestListReadsTheDescriptorOfPluginsThePanelInstalledToo(t *testing.T) {
	library := newLibrary(t)
	item := addPlugin(t, library, "EssentialsX", "EssentialsX/Essentials")
	// The library's name for a plugin comes from its listing page; the jar
	// declares something else, which is what the server will call it.
	storeVersion(t, library, item.ID, "v1.0.0", "EssentialsX-1.0.0.jar", string(jarBytes(t, "plugin.yml",
		"name: Essentials\nversion: 1.0.0\ndescription: The essentials.\nauthors: [Ada]\ndepend: [Vault]\nsoftdepend: [WorldEdit]\n")))

	dir := t.TempDir()
	instances := NewInstances(library, filepath.Join(t.TempDir(), "instance-plugins.json"))
	if _, err := instances.Install("inst", dir, item.ID, "v1.0.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	entries, err := instances.List("inst", dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	row := findEntry(entries, "EssentialsX-1.0.0.jar")
	if row == nil || row.Jar == nil {
		t.Fatalf("a managed row should still say what its jar declares: %+v", row)
	}
	if row.Jar.Description != "The essentials." || len(row.Jar.Depend) != 1 || row.Jar.Depend[0] != "Vault" {
		t.Errorf("unexpected descriptor: %+v", row.Jar)
	}
	if len(row.Jar.SoftDepend) != 1 || row.Jar.SoftDepend[0] != "WorldEdit" {
		t.Errorf("soft dependencies: %v", row.Jar.SoftDepend)
	}
	// What the row is *called* still comes from the record. The descriptor adds
	// to a managed row; it does not take it over.
	if row.Name != "EssentialsX" || row.Version != "1.0.0" {
		t.Errorf("the descriptor overwrote the record: %+v", row)
	}
	// Bukkit names a plugin's directory after the declared name, so that is the
	// directory the 配置 link has to point at.
	if row.ConfigDir != "plugins/Essentials" {
		t.Errorf("config directory: %q", row.ConfigDir)
	}
}

func TestListFlagsTwoJarsDeclaringTheSamePlugin(t *testing.T) {
	instances, _, dir, _ := instanceFixture(t)

	// Different releases, different file names, same declared name — which is
	// what a hand-uploaded build next to an existing one looks like. The server
	// loads one and silently refuses the other.
	writeJar(t, dir, "Atalanta-1.0.0.jar", "name: Atalanta\nversion: 1.0.0\n")
	writeJar(t, dir, "Atalanta-1.0.0-snapshot.jar", "name: Atalanta\nversion: 1.0.0-snapshot\n")
	writeJar(t, dir, "Vault.jar", "name: Vault\nversion: 1.7.3\n")

	entries, err := instances.List("inst", dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	first, second := findEntry(entries, "Atalanta-1.0.0.jar"), findEntry(entries, "Atalanta-1.0.0-snapshot.jar")
	if first == nil || second == nil {
		t.Fatalf("both jars should be listed: %+v", entries)
	}
	if len(first.Conflicts) != 1 || first.Conflicts[0] != "plugins/Atalanta-1.0.0-snapshot.jar" {
		t.Errorf("first row's conflicts: %v", first.Conflicts)
	}
	if len(second.Conflicts) != 1 || second.Conflicts[0] != "plugins/Atalanta-1.0.0.jar" {
		t.Errorf("second row's conflicts: %v", second.Conflicts)
	}
	if row := findEntry(entries, "Vault.jar"); row == nil || len(row.Conflicts) != 0 {
		t.Errorf("a plugin nobody duplicated is not a conflict: %+v", row)
	}

	// Switching one off is how the clash is resolved, so the warning has to go
	// away when it is — a .disabled jar is not loaded and clashes with nothing.
	if err := instances.SetEnabled("inst", dir, "file:plugins/Atalanta-1.0.0.jar", false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	after, err := instances.List("inst", dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, row := range after {
		if len(row.Conflicts) != 0 {
			t.Errorf("%s still reports a conflict: %v", row.FileName, row.Conflicts)
		}
	}
}
