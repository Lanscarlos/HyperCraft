package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
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
