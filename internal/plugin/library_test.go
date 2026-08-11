package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newLibrary(t *testing.T) *Library {
	t.Helper()
	return NewLibrary(t.TempDir())
}

// addPlugin registers a plugin and fails the test if it cannot.
func addPlugin(t *testing.T, library *Library, name, repo string) Plugin {
	t.Helper()
	item, err := library.Add(name, Source{Repo: repo}, "", "")
	if err != nil {
		t.Fatalf("Add(%s): %v", repo, err)
	}
	return item
}

// storeVersion puts a jar in the library the way a finished download would.
func storeVersion(t *testing.T, library *Library, id, tag, fileName, body string) {
	t.Helper()
	slug, err := versionSlug(tag)
	if err != nil {
		t.Fatalf("versionSlug: %v", err)
	}
	dir := filepath.Join(library.Root(), id, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	err = library.record(id, Version{
		Tag:         tag,
		Version:     VersionOf(tag),
		FileName:    fileName,
		Size:        int64(len(body)),
		PublishedAt: time.Now(),
		AddedAt:     time.Now(),
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
}

func TestAddNormalisesTheSourceAndDefaultsTheName(t *testing.T) {
	library := newLibrary(t)

	item, err := library.Add("", Source{Repo: "https://github.com/EssentialsX/Essentials/releases"}, "", "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if item.Source.Repo != "EssentialsX/Essentials" {
		t.Errorf("repo is %q", item.Source.Repo)
	}
	// The repository name is what the plugin is called on its own release page.
	if item.Name != "Essentials" {
		t.Errorf("name is %q", item.Name)
	}
	if item.TargetDir != DefaultTargetDir {
		t.Errorf("target dir is %q", item.TargetDir)
	}
	if item.ID != "essentials" {
		t.Errorf("id is %q", item.ID)
	}
}

func TestAddRefusesTheSameRepositoryTwice(t *testing.T) {
	library := newLibrary(t)
	addPlugin(t, library, "Essentials", "EssentialsX/Essentials")

	// Case differs, and GitHub owners and repos are case-insensitive, so this
	// is the same plugin rather than a second one.
	if _, err := library.Add("Again", Source{Repo: "essentialsx/essentials"}, "", ""); !errors.Is(err, ErrExists) {
		t.Fatalf("expected ErrExists, got %v", err)
	}
}

func TestAddGivesCollidingNamesDistinctIDs(t *testing.T) {
	library := newLibrary(t)
	first := addPlugin(t, library, "Chat", "a/chat")
	second := addPlugin(t, library, "Chat", "b/chat")

	if first.ID == second.ID {
		t.Fatalf("both plugins got the id %q", first.ID)
	}
}

func TestListDropsVersionsWhoseJarHasGone(t *testing.T) {
	library := newLibrary(t)
	item := addPlugin(t, library, "Essentials", "EssentialsX/Essentials")
	storeVersion(t, library, item.ID, "v1.0.0", "Foo-1.0.0.jar", "one")
	storeVersion(t, library, item.ID, "v2.0.0", "Foo-2.0.0.jar", "two")

	slug, _ := versionSlug("v1.0.0")
	if err := os.RemoveAll(filepath.Join(library.Root(), item.ID, slug)); err != nil {
		t.Fatalf("remove: %v", err)
	}

	stored, err := library.Get(item.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// An entry that fails on click is worse than one that is honestly absent.
	if len(stored.Versions) != 1 || stored.Versions[0].Tag != "v2.0.0" {
		t.Fatalf("unexpected versions: %+v", stored.Versions)
	}
}

func TestRemoveVersionDeletesOnlyThatVersion(t *testing.T) {
	library := newLibrary(t)
	item := addPlugin(t, library, "Essentials", "EssentialsX/Essentials")
	storeVersion(t, library, item.ID, "v1.0.0", "Foo.jar", "one")
	storeVersion(t, library, item.ID, "v2.0.0", "Foo.jar", "two")

	if err := library.RemoveVersion(item.ID, "v1.0.0"); err != nil {
		t.Fatalf("RemoveVersion: %v", err)
	}
	stored, _ := library.Get(item.ID)
	if len(stored.Versions) != 1 || stored.Versions[0].Tag != "v2.0.0" {
		t.Fatalf("unexpected versions: %+v", stored.Versions)
	}
	if err := library.RemoveVersion(item.ID, "v9.9.9"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestVersionsWithTheSameFileNameDoNotOverwriteEachOther(t *testing.T) {
	library := newLibrary(t)
	item := addPlugin(t, library, "Foo", "o/foo")
	// A plugin that publishes "Foo.jar" every release is common, and is exactly
	// why a version gets a directory of its own.
	storeVersion(t, library, item.ID, "v1.0.0", "Foo.jar", "one")
	storeVersion(t, library, item.ID, "v2.0.0", "Foo.jar", "two")

	for tag, want := range map[string]string{"v1.0.0": "one", "v2.0.0": "two"} {
		file, _, _, err := library.Open(item.ID, tag)
		if err != nil {
			t.Fatalf("Open(%s): %v", tag, err)
		}
		body := make([]byte, 8)
		n, _ := file.Read(body)
		file.Close()
		if string(body[:n]) != want {
			t.Errorf("%s holds %q, want %q", tag, body[:n], want)
		}
	}
}

func TestRemoveDeletesTheDownloadsToo(t *testing.T) {
	library := newLibrary(t)
	item := addPlugin(t, library, "Foo", "o/foo")
	storeVersion(t, library, item.ID, "v1.0.0", "Foo.jar", "one")

	if err := library.Remove(item.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(library.Root(), item.ID)); !os.IsNotExist(err) {
		t.Errorf("the plugin directory survived: %v", err)
	}
	if _, err := library.Get(item.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestEditKeepsDownloadsAndClearsTheStaleCheck(t *testing.T) {
	library := newLibrary(t)
	item := addPlugin(t, library, "Foo", "o/foo")
	storeVersion(t, library, item.ID, "v1.0.0", "Foo.jar", "one")
	if err := library.RecordCheck(item.ID, &Release{Tag: "v9.0.0"}, nil); err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}

	edited, err := library.Edit(item.ID, "Foo", Source{Repo: "o/foo-renamed"}, "mods", "note")
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	// A corrected source should not throw away jars that are already on disk…
	if len(edited.Versions) != 1 {
		t.Errorf("versions were lost: %+v", edited.Versions)
	}
	// …but the cached "latest" described the old repository.
	if edited.Latest != nil {
		t.Errorf("the stale check survived: %+v", edited.Latest)
	}
	if edited.TargetDir != "mods" {
		t.Errorf("target dir is %q", edited.TargetDir)
	}
}

func TestSetPrivateCorrectsTheFlagAndLeavesEverythingElseAlone(t *testing.T) {
	library := newLibrary(t)
	item, err := library.Add("Mine", Source{Repo: "me/mine", AssetPattern: "Mine-*.jar"}, "mods", "note")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	changed, err := library.SetPrivate(item.ID, true)
	if err != nil || !changed {
		t.Fatalf("SetPrivate: changed=%v err=%v", changed, err)
	}
	// Nothing but the flag: this runs behind an operator's back, before checks
	// and downloads, and must not undo an edit they are in the middle of.
	stored, err := library.Get(item.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := Source{Kind: SourceGitHub, Repo: "me/mine", AssetPattern: "Mine-*.jar", Private: true}
	if stored.Source != want || stored.TargetDir != "mods" || stored.Note != "note" {
		t.Fatalf("unexpected plugin: %+v", stored)
	}

	// Saying the same thing twice is not a change, so nothing is rewritten.
	if changed, err := library.SetPrivate(item.ID, true); err != nil || changed {
		t.Fatalf("expected no change, got changed=%v err=%v", changed, err)
	}
	if changed, err := library.SetPrivate(item.ID, false); err != nil || !changed {
		t.Fatalf("a repository that went public should change back: changed=%v err=%v", changed, err)
	}
}

func TestRecordCheckKeepsTheLastKnownLatestOnFailure(t *testing.T) {
	library := newLibrary(t)
	item := addPlugin(t, library, "Foo", "o/foo")

	if err := library.RecordCheck(item.ID, &Release{Tag: "v2.0.0", Version: "2.0.0"}, nil); err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}
	if err := library.RecordCheck(item.ID, nil, errors.New("network is down")); err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}

	stored, _ := library.Get(item.ID)
	// A flaky network should not make a known update disappear from the page.
	if stored.Latest == nil || stored.Latest.Tag != "v2.0.0" {
		t.Fatalf("the known latest was lost: %+v", stored.Latest)
	}
	if stored.CheckError == "" {
		t.Error("the failure should still be reported")
	}
}

func TestUpdateAvailableTracksWhatIsDownloaded(t *testing.T) {
	item := Plugin{Versions: []Version{{Tag: "v1.0.0"}}}
	if item.UpdateAvailable() {
		t.Error("no check has run yet")
	}
	item.Latest = &Release{Tag: "v2.0.0"}
	if !item.UpdateAvailable() {
		t.Error("v2.0.0 is not downloaded")
	}
	item.Versions = append(item.Versions, Version{Tag: "v2.0.0"})
	if item.UpdateAvailable() {
		t.Error("v2.0.0 is downloaded now")
	}
}

func TestRegistrySurvivesAReopen(t *testing.T) {
	root := t.TempDir()
	library := NewLibrary(root)
	item := addPlugin(t, library, "Foo", "o/foo")
	storeVersion(t, library, item.ID, "v1.0.0", "Foo.jar", "one")

	reopened := NewLibrary(root)
	stored, err := reopened.Get(item.ID)
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if stored.Source.Repo != "o/foo" || len(stored.Versions) != 1 {
		t.Fatalf("unexpected plugin after reopen: %+v", stored)
	}
}

func TestCleanTargetDirRefusesToEscapeTheInstance(t *testing.T) {
	for _, bad := range []string{"..", "../evil", "plugins/../..", "../../etc"} {
		if _, err := CleanTargetDir(bad); !errors.Is(err, ErrInvalidTarget) {
			t.Errorf("CleanTargetDir(%q) should have been rejected, got %v", bad, err)
		}
	}
	for input, want := range map[string]string{
		"":               DefaultTargetDir,
		"  ":             DefaultTargetDir,
		"mods":           "mods",
		"/etc":           "etc", // a leading slash means the instance root, as it does in the file manager
		"/plugins/":      "plugins",
		"plugins\\extra": "plugins/extra",
	} {
		got, err := CleanTargetDir(input)
		if err != nil {
			t.Errorf("CleanTargetDir(%q): %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("CleanTargetDir(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestVersionSlugSurvivesSlashesInTags(t *testing.T) {
	slug, err := versionSlug("release/1.2.3")
	if err != nil {
		t.Fatalf("versionSlug: %v", err)
	}
	if slug != "release-1.2.3" {
		t.Fatalf("slug is %q", slug)
	}
	if _, err := versionSlug("///"); err == nil {
		t.Error("a tag with no usable characters should be refused")
	}
}

func TestDescriptorFoldsWhatTheJarsDeclare(t *testing.T) {
	library := newLibrary(t)
	// The name in the library is the repository's; the name in the jar is what
	// the server files the plugin under, and they are routinely different.
	item := addPlugin(t, library, "EssentialsX", "EssentialsX/Essentials")
	storeDeclaredJar(t, library, item.ID, "v1.0.0", "EssentialsX-1.0.0.jar", "Essentials", "1.0.0")

	stored, err := library.Get(item.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	facts := stored.Descriptor()
	if facts.Name != "Essentials" {
		t.Errorf("declared name = %q, want Essentials", facts.Name)
	}
	if !facts.Scanned {
		t.Error("a jar that was read should be marked as read")
	}
}

func TestDescriptorIsUnscannedUntilAJarHasBeenRead(t *testing.T) {
	library := newLibrary(t)
	item := addPlugin(t, library, "Vault", "MilkBowl/Vault")
	// storeVersion writes a file that is not a jar and records no descriptor —
	// the shape of every version downloaded before the panel read them.
	storeVersion(t, library, item.ID, "v1.0.0", "Vault-1.0.0.jar", "not a jar")

	stored, err := library.Get(item.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// "Nothing declared" and "never looked" have to be tellable apart, or the
	// page cannot choose between showing a blank and saying it does not know.
	if stored.Descriptor().Scanned {
		t.Error("a version nobody has opened must not claim to have been read")
	}
}

func TestRescanReadsJarsRecordedBeforeDescriptorsExisted(t *testing.T) {
	library := newLibrary(t)
	item := addPlugin(t, library, "CoolPlugin", "me/cool")

	// A real jar on disk, filed by a panel that did not read descriptors: the
	// bytes are right and the record says nothing about them. Nothing will ever
	// re-download this, so without a sweep the row stays blank forever.
	slug, err := versionSlug("v2.1.0")
	if err != nil {
		t.Fatalf("versionSlug: %v", err)
	}
	dir := filepath.Join(library.Root(), item.ID, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := jarBytes(t, "plugin.yml",
		"name: CoolPlugin\nversion: 2.1.0\napi-version: '1.20'\ndescription: Does cool things.\nauthors: [ada, grace]\ndepend: [Vault]\n")
	if err := os.WriteFile(filepath.Join(dir, "cool-2.1.0.jar"), body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	err = library.record(item.ID, Version{
		Tag: "v2.1.0", Version: "2.1.0",
		FileName: "cool-2.1.0.jar", Size: int64(len(body)),
		PublishedAt: time.Now(), AddedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	if read := library.Rescan(); read != 1 {
		t.Fatalf("Rescan read %d jars, want 1", read)
	}

	stored, err := library.Get(item.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	facts := stored.Descriptor()
	if facts.Name != "CoolPlugin" || facts.APIVersion != "1.20" {
		t.Errorf("descriptor = %+v", facts)
	}
	if facts.Description != "Does cool things." {
		t.Errorf("description = %q", facts.Description)
	}
	if len(facts.Authors) != 2 || facts.Authors[0] != "ada" {
		t.Errorf("authors = %v", facts.Authors)
	}
	if len(facts.Depend) != 1 || facts.Depend[0] != "Vault" {
		t.Errorf("depend = %v", facts.Depend)
	}

	// And it is recorded, not recomputed: a second sweep has nothing to do.
	if read := library.Rescan(); read != 0 {
		t.Errorf("a second Rescan re-read %d jars", read)
	}
}

func TestRescanMarksAJarItCannotParseAsRead(t *testing.T) {
	library := newLibrary(t)
	item := addPlugin(t, library, "SomeMod", "me/mod")
	// A Forge mod's descriptor is TOML, which jarinfo deliberately does not
	// parse. Re-opening it on every start to learn that again is the cost this
	// avoids.
	storeVersion(t, library, item.ID, "v1.0.0", "mod-1.0.0.jar", "not a zip at all")

	library.Rescan()
	if read := library.Rescan(); read != 0 {
		t.Errorf("an unreadable jar was opened again by the second sweep")
	}
}
