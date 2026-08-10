package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// serveJSON answers every request with one body, which is all these readers
// need: each test drives a single endpoint.
func serveJSON(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

// One Hangar version publishing a Paper build and a Velocity build is one
// release with two jars. It used to come through as two releases — the panel
// appended the platform to the tag — which is what made a plugin published once
// look like it had shipped twice and a fleet look inconsistent.
func TestHangarVersionsKeepPlatformBuildsTogether(t *testing.T) {
	server := serveJSON(t, `{"result":[{
		"name":"5.5.71",
		"createdAt":"2026-02-01T10:00:00Z",
		"channel":{"name":"Release"},
		"downloads":{
			"PAPER":{"fileInfo":{"name":"LuckPerms-Bukkit-5.5.71.jar","sizeBytes":100,"sha256Hash":"aa"},"downloadUrl":"https://example.invalid/bukkit.jar"},
			"VELOCITY":{"fileInfo":{"name":"LuckPerms-Velocity-5.5.71.jar","sizeBytes":90,"sha256Hash":"bb"},"downloadUrl":"https://example.invalid/velocity.jar"}
		},
		"platformDependencies":{"PAPER":["1.20.4"],"VELOCITY":["3.3.0"]},
		"stats":{"totalDownloads":7}
	}]}`)

	registry := NewRegistry("test")
	registry.hangarBase = server.URL

	releases, err := registry.hangarVersions(context.Background(), "LuckPerms")
	if err != nil {
		t.Fatalf("hangarVersions: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("read %d releases, want 1: %+v", len(releases), releases)
	}
	release := releases[0]
	if release.Tag != "5.5.71" {
		t.Errorf("tag is %q, want 5.5.71 with no platform on it", release.Tag)
	}
	if len(release.Assets) != 2 {
		t.Fatalf("release holds %d jars, want 2", len(release.Assets))
	}
	// Paper first: it is what a plain download takes, and almost every server
	// here is one.
	if release.Assets[0].Platform != "paper" || release.Asset.Platform != "paper" {
		t.Errorf("primary jar is %q, want paper", release.Asset.Platform)
	}
	if release.Assets[1].Platform != "velocity" {
		t.Errorf("second jar is %q, want velocity", release.Assets[1].Platform)
	}
	// Each jar carries what it in particular supports; the release carries the
	// union, which is what a search badge reads.
	if got := release.Assets[1].GameVersions; len(got) != 1 || got[0] != "3.3.0" {
		t.Errorf("velocity jar supports %v, want just its own line", got)
	}
	if len(release.Loaders) != 2 {
		t.Errorf("release loaders are %v, want both platforms", release.Loaders)
	}
}

// Modrinth files each loader's build as a version of its own, all carrying the
// release's version number. The number is the release.
func TestModrinthVersionsGroupByVersionNumber(t *testing.T) {
	server := serveJSON(t, `[
		{"id":"Ab12Cd34","name":"5.5.71","version_number":"5.5.71","version_type":"release",
		 "game_versions":["1.20.4"],"loaders":["paper"],"date_published":"2026-02-01T10:00:00Z","downloads":5,
		 "files":[{"url":"https://example.invalid/bukkit.jar","filename":"LuckPerms-Bukkit-5.5.71.jar","primary":true,"size":100}]},
		{"id":"Ef56Gh78","name":"5.5.71","version_number":"5.5.71","version_type":"release",
		 "game_versions":["1.20.4"],"loaders":["velocity"],"date_published":"2026-02-01T10:05:00Z","downloads":3,
		 "files":[{"url":"https://example.invalid/velocity.jar","filename":"LuckPerms-Velocity-5.5.71.jar","primary":true,"size":90}]},
		{"id":"Ij90Kl12","name":"5.5.70","version_number":"5.5.70","version_type":"release",
		 "game_versions":["1.20.4"],"loaders":["paper"],"date_published":"2026-01-20T10:00:00Z","downloads":9,
		 "files":[{"url":"https://example.invalid/old.jar","filename":"LuckPerms-Bukkit-5.5.70.jar","primary":true,"size":95}]}
	]`)

	registry := NewRegistry("test")
	registry.modrinthBase = server.URL

	releases, err := registry.modrinthVersions(context.Background(), "luckperms")
	if err != nil {
		t.Fatalf("modrinthVersions: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("read %d releases, want 2: %+v", len(releases), releases)
	}
	newest := releases[0]
	if newest.Version != "5.5.71" || newest.Tag != "5.5.71" {
		t.Fatalf("newest is %q tagged %q, want 5.5.71", newest.Version, newest.Tag)
	}
	if len(newest.Assets) != 2 {
		t.Fatalf("newest holds %d jars, want 2", len(newest.Assets))
	}
	if newest.Assets[0].Platform != "paper" || newest.Assets[1].Platform != "velocity" {
		t.Errorf("jars are %q and %q, want paper then velocity",
			newest.Assets[0].Platform, newest.Assets[1].Platform)
	}
	if len(newest.Loaders) != 2 {
		t.Errorf("release loaders are %v, want both", newest.Loaders)
	}
	if newest.Downloads != 8 {
		t.Errorf("downloads are %d, want the two builds added up", newest.Downloads)
	}
}

// LuckPerms, as Modrinth actually publishes it: a version per platform, with
// the platform written into the version number. Grouping on the number alone
// groups nothing — which is how a Paper server came to be offered the Velocity
// build as its next version.
func TestModrinthGroupsPlatformSuffixedNumbers(t *testing.T) {
	server := serveJSON(t, `[
		{"id":"aa","name":"v5.5.71 (Velocity)","version_number":"v5.5.71-velocity","version_type":"release",
		 "game_versions":["1.20.4"],"loaders":["velocity"],"date_published":"2026-02-01T10:05:00Z","downloads":3,
		 "files":[{"url":"https://example.invalid/v.jar","filename":"LuckPerms-Velocity-5.5.71.jar","primary":true,"size":90}]},
		{"id":"bb","name":"v5.5.71 (BungeeCord)","version_number":"v5.5.71-bungee","version_type":"release",
		 "game_versions":["1.20.4"],"loaders":["bungeecord","waterfall"],"date_published":"2026-02-01T10:03:00Z","downloads":2,
		 "files":[{"url":"https://example.invalid/b.jar","filename":"LuckPerms-Bungee-5.5.71.jar","primary":true,"size":91}]},
		{"id":"cc","name":"v5.5.71 (Bukkit)","version_number":"v5.5.71-bukkit","version_type":"release",
		 "game_versions":["1.20.4"],"loaders":["bukkit","folia","paper","spigot"],"date_published":"2026-02-01T10:00:00Z","downloads":5,
		 "files":[{"url":"https://example.invalid/p.jar","filename":"LuckPerms-Bukkit-5.5.71.jar","primary":true,"size":100}]},
		{"id":"dd","name":"v5.5.57 (Fabric)","version_number":"v5.5.57-fabric","version_type":"release",
		 "game_versions":["1.20.4"],"loaders":["fabric"],"date_published":"2026-01-20T10:00:00Z","downloads":9,
		 "files":[{"url":"https://example.invalid/f.jar","filename":"LuckPerms-Fabric-5.5.57.jar","primary":true,"size":95}]}
	]`)

	registry := NewRegistry("test")
	registry.modrinthBase = server.URL

	releases, err := registry.modrinthVersions(context.Background(), "luckperms")
	if err != nil {
		t.Fatalf("modrinthVersions: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("read %d releases, want 2: %+v", len(releases), releases)
	}
	newest := releases[0]
	if newest.Tag != "v5.5.71" || newest.Version != "v5.5.71" {
		t.Fatalf("newest is %q tagged %q, want v5.5.71 — the platform is not part of the version",
			newest.Version, newest.Tag)
	}
	if len(newest.Assets) != 3 {
		t.Fatalf("newest holds %d jars, want 3", len(newest.Assets))
	}
	// The jar a plain download takes has to be the one a Minecraft server
	// loads, not whichever build the registry happened to list first.
	if newest.Asset.Platform != "bukkit" {
		t.Errorf("primary jar is %q, want the bukkit build", newest.Asset.Platform)
	}
	for _, asset := range newest.Assets {
		if asset.Platform == "" {
			t.Errorf("%s has no platform, though its version number named one", asset.Name)
		}
	}
}

// A suffix that is not a platform is part of the version.
func TestReleaseNumberKeepsOrdinarySuffixes(t *testing.T) {
	for _, probe := range []struct{ in, release, platform string }{
		{"v5.5.71-bukkit", "v5.5.71", "bukkit"},
		{"v5.5.71-bungee", "v5.5.71", "bungeecord"},
		{"1.2.3-beta", "1.2.3-beta", ""},
		{"2.0-rc1", "2.0-rc1", ""},
		{"5.5.71", "5.5.71", ""},
		{"-velocity", "-velocity", ""},
	} {
		release, platform := releaseNumber(probe.in)
		if release != probe.release || platform != probe.platform {
			t.Errorf("releaseNumber(%q) = %q, %q; want %q, %q",
				probe.in, release, platform, probe.release, probe.platform)
		}
	}
}

// Two builds under one file name have to stay two files.
func TestHangarRenamesCollidingBuilds(t *testing.T) {
	server := serveJSON(t, `{"result":[{
		"name":"1.0",
		"createdAt":"2026-02-01T10:00:00Z",
		"channel":{"name":"Release"},
		"downloads":{
			"PAPER":{"fileInfo":{"name":"Thing.jar","sizeBytes":10},"downloadUrl":"https://example.invalid/p.jar"},
			"VELOCITY":{"fileInfo":{"name":"Thing.jar","sizeBytes":11},"downloadUrl":"https://example.invalid/v.jar"}
		},
		"platformDependencies":{"PAPER":["1.20.4"],"VELOCITY":["3.3.0"]},
		"stats":{"totalDownloads":1}
	}]}`)

	registry := NewRegistry("test")
	registry.hangarBase = server.URL

	releases, err := registry.hangarVersions(context.Background(), "Thing")
	if err != nil {
		t.Fatalf("hangarVersions: %v", err)
	}
	if len(releases) != 1 || len(releases[0].Assets) != 2 {
		t.Fatalf("want one release holding two jars, got %+v", releases)
	}
	if releases[0].Assets[0].Name == releases[0].Assets[1].Name {
		t.Fatalf("both jars are called %s", releases[0].Assets[0].Name)
	}
}
