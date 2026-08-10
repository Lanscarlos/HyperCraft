package plugin

import (
	"bytes"
	"strings"
	"testing"
)

// jarBytes, from jarinfo_test.go, is a jar carrying one descriptor — which is
// all ReadJarInfo looks at and all an import needs.

func TestImportReadsTheJarRatherThanAskingTheOperator(t *testing.T) {
	lib := NewLibrary(t.TempDir())

	jar := jarBytes(t, "plugin.yml", "name: CoolPlugin\nversion: 2.1.0\napi-version: 1.20\n")
	got, err := lib.ImportJar("", "cool-2.1.0.jar", bytes.NewReader(jar), 1<<20)
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if got.Plugin.Name != "CoolPlugin" {
		t.Errorf("name should come from the descriptor, got %q", got.Plugin.Name)
	}
	if got.Plugin.Source.Kind != SourceLocal {
		t.Errorf("source kind = %q", got.Plugin.Source.Kind)
	}
	if got.Version.Version != "2.1.0" {
		t.Errorf("version = %q", got.Version.Version)
	}
	// The two fields Judge reads. Without them an imported jar would be
	// unjudgeable everywhere a downloaded one is judged.
	if len(got.Version.Loaders) != 1 || got.Version.Loaders[0] != "bukkit" {
		t.Errorf("loaders = %v", got.Version.Loaders)
	}
	if len(got.Version.GameVersions) != 1 || got.Version.GameVersions[0] != "1.20" {
		t.Errorf("game versions = %v", got.Version.GameVersions)
	}
	if got.Replaced {
		t.Error("a first import has replaced nothing")
	}
}

func TestASecondJarJoinsThePluginItAlreadyMade(t *testing.T) {
	lib := NewLibrary(t.TempDir())

	first := jarBytes(t, "plugin.yml", "name: CoolPlugin\nversion: 2.1.0\n")
	if _, err := lib.ImportJar("", "cool-2.1.0.jar", bytes.NewReader(first), 1<<20); err != nil {
		t.Fatal(err)
	}
	second := jarBytes(t, "plugin.yml", "name: coolplugin\nversion: 2.2.0\n")
	got, err := lib.ImportJar("", "cool-2.2.0.jar", bytes.NewReader(second), 1<<20)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}

	// Matched on the slug, so a change of capitalisation is the same plugin —
	// which is what it is.
	if items := lib.List(); len(items) != 1 {
		t.Fatalf("expected one plugin, got %d", len(items))
	}
	if len(got.Plugin.Versions) != 2 {
		t.Errorf("expected two versions, got %d", len(got.Plugin.Versions))
	}
}

func TestReuploadingTheSameJarRepairsItRatherThanDuplicatingIt(t *testing.T) {
	lib := NewLibrary(t.TempDir())
	jar := jarBytes(t, "plugin.yml", "name: CoolPlugin\nversion: 2.1.0\n")

	first, err := lib.ImportJar("", "cool.jar", bytes.NewReader(jar), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	again, err := lib.ImportJar("", "cool.jar", bytes.NewReader(jar), 1<<20)
	if err != nil {
		t.Fatal(err)
	}

	if again.Version.Tag != first.Version.Tag {
		t.Errorf("the same bytes should be the same version: %q vs %q", first.Version.Tag, again.Version.Tag)
	}
	if !again.Replaced {
		t.Error("re-uploading an identical jar replaces it, and should say so")
	}
	if len(again.Plugin.Versions) != 1 {
		t.Errorf("expected one version, got %d", len(again.Plugin.Versions))
	}
}

func TestImportRefusesWhatIsNotAJar(t *testing.T) {
	lib := NewLibrary(t.TempDir())

	// Right extension, not an archive — a truncated download, which is worth
	// catching here rather than on a server that will not boot.
	_, err := lib.ImportJar("", "broken.jar", strings.NewReader("not a zip"), 1<<20)
	if err == nil {
		t.Fatal("expected a rejection")
	}

	if _, err := lib.ImportJar("", "notes.txt", strings.NewReader("hello"), 1<<20); err == nil {
		t.Error("expected a non-jar name to be refused")
	}
}

func TestImportStopsAtTheUploadLimit(t *testing.T) {
	lib := NewLibrary(t.TempDir())

	_, err := lib.ImportJar("", "big.jar", bytes.NewReader(make([]byte, 4096)), 1024)
	if err == nil {
		t.Fatal("expected the limit to be enforced")
	}
	// And nothing was left behind: the staging file is removed on every path
	// out, or a rejected upload would cost disk until the panel restarts.
	if items := lib.List(); len(items) != 0 {
		t.Errorf("a refused import should track nothing, got %d", len(items))
	}
}

func TestAnUploadedJarIsNeverCheckedForUpdates(t *testing.T) {
	// There is nowhere to check. The guard lives in Downloader.Check; this
	// pins the property the guard is written against.
	src, err := Source{Kind: SourceLocal, Repo: "coolplugin"}.Normalise()
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	if src.Private || src.AssetPattern != "" || src.Prerelease {
		t.Errorf("a local source carries no GitHub settings: %+v", src)
	}
	if (Plugin{Source: src}).UpdateAvailable() {
		t.Error("a plugin with no upstream can never have an update")
	}
}
