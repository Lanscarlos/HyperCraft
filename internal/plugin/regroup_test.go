package plugin

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// storeArtifact puts one jar of one release in the library, the way a finished
// download would, without going through the network.
func storeArtifact(t *testing.T, library *Library, id, tag, version, fileName, body string, published ...time.Time) {
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
	at := time.Now()
	if len(published) > 0 {
		at = published[0]
	}
	err = library.record(id, Version{
		Tag:         tag,
		Version:     version,
		PublishedAt: at,
		AddedAt:     time.Now(),
		Artifacts: []Artifact{{
			SHA256:   body, // stands in for a digest; only its identity matters
			FileName: fileName,
			Size:     int64(len(body)),
		}},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
}

// A Hangar plugin downloaded by an older build holds one entry per platform,
// tagged "1.2.3@PAPER" and "1.2.3@VELOCITY". That is one release.
func TestRegroupMergesHangarPlatformTags(t *testing.T) {
	library := newLibrary(t)
	item, err := library.Add("LuckPerms", Source{Kind: SourceHangar, Repo: "LuckPerms"}, "", "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	storeArtifact(t, library, item.ID, "5.5.71@PAPER", "5.5.71", "LuckPerms-Bukkit-5.5.71.jar", "paper-bytes")
	storeArtifact(t, library, item.ID, "5.5.71@VELOCITY", "5.5.71", "LuckPerms-Velocity-5.5.71.jar", "velocity-bytes")

	if fixed := library.Regroup(quiet()); fixed != 1 {
		t.Fatalf("Regroup rewrote %d plugins, want 1", fixed)
	}

	stored, err := library.Get(item.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(stored.Versions) != 1 {
		t.Fatalf("holds %d versions, want 1: %+v", len(stored.Versions), stored.Versions)
	}
	release := stored.Versions[0]
	if release.Tag != "5.5.71" {
		t.Errorf("tag is %q, want 5.5.71", release.Tag)
	}
	if len(release.Artifacts) != 2 {
		t.Fatalf("release holds %d jars, want 2", len(release.Artifacts))
	}
	// describe() drops artifacts whose file is missing, so two surviving jars
	// means both files were moved into the merged release's directory.
	for _, artifact := range release.Artifacts {
		if _, err := os.Stat(library.versionFile(item.ID, release.Tag, artifact.FileName)); err != nil {
			t.Errorf("%s did not move: %v", artifact.FileName, err)
		}
		if artifact.Platform == "" {
			t.Errorf("%s lost the platform its tag carried", artifact.FileName)
		}
	}
}

// Modrinth files each loader's build as a version of its own, all carrying the
// release's version number.
func TestRegroupMergesModrinthVersionsByNumber(t *testing.T) {
	library := newLibrary(t)
	item, err := library.Add("LuckPerms", Source{Kind: SourceModrinth, Repo: "luckperms"}, "", "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	day := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	storeArtifact(t, library, item.ID, "Ab12Cd34", "5.5.71", "LuckPerms-Bukkit-5.5.71.jar", "bukkit-bytes", day)
	storeArtifact(t, library, item.ID, "Ef56Gh78", "5.5.71", "LuckPerms-Velocity-5.5.71.jar", "velocity-bytes", day.Add(3*time.Minute))
	storeArtifact(t, library, item.ID, "Ij90Kl12", "5.5.70", "LuckPerms-Bukkit-5.5.70.jar", "older-bytes", day.AddDate(0, 0, -7))

	if fixed := library.Regroup(quiet()); fixed != 1 {
		t.Fatalf("Regroup rewrote %d plugins, want 1", fixed)
	}

	stored, err := library.Get(item.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(stored.Versions) != 2 {
		t.Fatalf("holds %d versions, want 2: %+v", len(stored.Versions), stored.Versions)
	}
	newest := stored.Versions[0]
	if newest.Tag != "5.5.71" || len(newest.Artifacts) != 2 {
		t.Fatalf("newest is %q with %d jars, want 5.5.71 with 2", newest.Tag, len(newest.Artifacts))
	}
	for _, artifact := range newest.Artifacts {
		if _, err := os.Stat(library.versionFile(item.ID, newest.Tag, artifact.FileName)); err != nil {
			t.Errorf("%s did not move: %v", artifact.FileName, err)
		}
	}
}

// A GitHub repository publishes one release per tag. Two releases sharing a
// version string are still two releases, and merging them would lose one.
func TestRegroupLeavesGitHubAlone(t *testing.T) {
	library := newLibrary(t)
	item := addPlugin(t, library, "Essentials", "EssentialsX/Essentials")
	storeArtifact(t, library, item.ID, "v2.20.1", "2.20.1", "EssentialsX-2.20.1.jar", "one")
	storeArtifact(t, library, item.ID, "2.20.1", "2.20.1", "EssentialsX-2.20.1-hotfix.jar", "two")

	if fixed := library.Regroup(quiet()); fixed != 0 {
		t.Fatalf("Regroup rewrote %d plugins, want 0", fixed)
	}
	stored, err := library.Get(item.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(stored.Versions) != 2 {
		t.Fatalf("holds %d versions, want both left alone", len(stored.Versions))
	}
}

// Two platform builds published under one file name would be one file on disk.
func TestRegroupRenamesCollidingJars(t *testing.T) {
	library := newLibrary(t)
	item, err := library.Add("Thing", Source{Kind: SourceHangar, Repo: "Thing"}, "", "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	storeArtifact(t, library, item.ID, "1.0@PAPER", "1.0", "Thing.jar", "paper-bytes")
	storeArtifact(t, library, item.ID, "1.0@VELOCITY", "1.0", "Thing.jar", "velocity-bytes")

	if fixed := library.Regroup(quiet()); fixed != 1 {
		t.Fatalf("Regroup rewrote %d plugins, want 1", fixed)
	}
	stored, err := library.Get(item.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(stored.Versions) != 1 || len(stored.Versions[0].Artifacts) != 2 {
		t.Fatalf("want one release holding two jars, got %+v", stored.Versions)
	}
	names := map[string]bool{}
	for _, artifact := range stored.Versions[0].Artifacts {
		if names[artifact.FileName] {
			t.Fatalf("both jars are still called %s", artifact.FileName)
		}
		names[artifact.FileName] = true
	}
	if !names["Thing-velocity.jar"] {
		t.Errorf("the velocity build was not renamed apart: %v", names)
	}
}

// Running twice must not undo the first run.
func TestRegroupIsIdempotent(t *testing.T) {
	library := newLibrary(t)
	item, err := library.Add("LuckPerms", Source{Kind: SourceHangar, Repo: "LuckPerms"}, "", "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	storeArtifact(t, library, item.ID, "5.5.71@PAPER", "5.5.71", "LP-Bukkit.jar", "paper-bytes")
	storeArtifact(t, library, item.ID, "5.5.71@VELOCITY", "5.5.71", "LP-Velocity.jar", "velocity-bytes")

	library.Regroup(quiet())
	if fixed := library.Regroup(quiet()); fixed != 0 {
		t.Fatalf("second Regroup rewrote %d plugins, want 0", fixed)
	}
	stored, err := library.Get(item.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(stored.Versions) != 1 || len(stored.Versions[0].Artifacts) != 2 {
		t.Fatalf("want one release holding two jars, got %+v", stored.Versions)
	}
}

// LuckPerms as Modrinth actually publishes it: the platform is written into
// the version number, so the library written by an older build holds
// "v5.5.71-bukkit" and "v5.5.71-velocity" as two releases.
func TestRegroupMergesPlatformSuffixedNumbers(t *testing.T) {
	library := newLibrary(t)
	item, err := library.Add("LuckPerms", Source{Kind: SourceModrinth, Repo: "luckperms"}, "", "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	day := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	storeArtifact(t, library, item.ID, "aa", "v5.5.71-bukkit", "LuckPerms-Bukkit-5.5.71.jar", "bukkit-bytes", day)
	storeArtifact(t, library, item.ID, "bb", "v5.5.71-velocity", "LuckPerms-Velocity-5.5.71.jar", "velocity-bytes", day.Add(5*time.Minute))

	if fixed := library.Regroup(quiet()); fixed != 1 {
		t.Fatalf("Regroup rewrote %d plugins, want 1", fixed)
	}
	stored, err := library.Get(item.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(stored.Versions) != 1 {
		t.Fatalf("holds %d versions, want 1: %+v", len(stored.Versions), stored.Versions)
	}
	release := stored.Versions[0]
	if release.Tag != "v5.5.71" || release.Version != "v5.5.71" {
		t.Errorf("release is %q tagged %q, want v5.5.71 with no platform on it",
			release.Version, release.Tag)
	}
	if len(release.Artifacts) != 2 {
		t.Fatalf("release holds %d jars, want 2", len(release.Artifacts))
	}
	for _, artifact := range release.Artifacts {
		if artifact.Platform == "" {
			t.Errorf("%s lost the platform its version number named", artifact.FileName)
		}
		if _, err := os.Stat(library.versionFile(item.ID, release.Tag, artifact.FileName)); err != nil {
			t.Errorf("%s did not move: %v", artifact.FileName, err)
		}
	}
}

// The servers' records name the tags the merge just rewrote. Re-pointed by
// digest, which is the one thing about a jar that did not change.
func TestRealignRepointsRecordsAtTheirRelease(t *testing.T) {
	library := newLibrary(t)
	item, err := library.Add("LuckPerms", Source{Kind: SourceModrinth, Repo: "luckperms"}, "", "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	day := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	storeArtifact(t, library, item.ID, "aa", "v5.5.71-bukkit", "LuckPerms-Bukkit-5.5.71.jar", "bukkit-bytes", day)
	storeArtifact(t, library, item.ID, "bb", "v5.5.71-velocity", "LuckPerms-Velocity-5.5.71.jar", "velocity-bytes", day.Add(time.Minute))

	instances := NewInstances(library, filepath.Join(t.TempDir(), "instance-plugins.json"))
	if err := instances.put("test", Installed{
		PluginID: item.ID,
		Tag:      "aa",
		Version:  "v5.5.71-bukkit",
		FileName: "LuckPerms-Bukkit-5.5.71.jar",
		Dir:      "plugins",
		SHA256:   "bukkit-bytes",
	}); err != nil {
		t.Fatalf("put: %v", err)
	}

	library.Regroup(quiet())
	if fixed := instances.Realign(library, quiet()); fixed != 1 {
		t.Fatalf("Realign fixed %d records, want 1", fixed)
	}

	records := instances.Records("test")
	if len(records) != 1 {
		t.Fatalf("holds %d records, want 1", len(records))
	}
	if records[0].Tag != "v5.5.71" || records[0].Version != "v5.5.71" {
		t.Errorf("record still points at %q / %q", records[0].Tag, records[0].Version)
	}
	// The jar's own claims travel with it: this server has the bukkit build,
	// not "everything the release supports".
	if len(records[0].Loaders) == 0 || records[0].Loaders[0] != "bukkit" {
		t.Errorf("record claims %v, want the bukkit jar's own loaders", records[0].Loaders)
	}
	if second := instances.Realign(library, quiet()); second != 0 {
		t.Errorf("Realign changed %d records the second time, want 0", second)
	}
}
