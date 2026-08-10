package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/plugin"
)

// fakeGitHub serves the releases endpoint and the asset itself, so the whole
// add → download → install path is exercised without the network.
type fakeGitHub struct {
	server *httptest.Server
	body   []byte
}

// fakeGitHubToken is what the stub accepts as a valid access token. A
// repository called "private" behaves the way GitHub does about one: invisible
// — a plain 404, not a 403 — until a request proves who is asking.
const fakeGitHubToken = "ghp_stubtoken0000"

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	gh := &fakeGitHub{body: []byte(strings.Repeat("plugin", 512))}

	authenticated := func(r *http.Request) bool {
		return r.Header.Get("Authorization") == "Bearer "+fakeGitHubToken
	}

	mux := http.NewServeMux()
	// A private release publishes its assets through the API only, so this is
	// the shape the panel has to cope with: no browser_download_url at all.
	mux.HandleFunc("GET /repos/{owner}/{name}/releases/assets/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !authenticated(r) {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Accept") != "application/octet-stream" {
			// What GitHub does here is answer with the asset's JSON, which the
			// panel would happily write to disk and call a plugin.
			w.WriteHeader(http.StatusNotAcceptable)
			return
		}
		w.Write(gh.body)
	})
	mux.HandleFunc("GET /repos/{owner}/{name}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("name") != "private" {
			fmt.Fprint(w, `{"private":false}`)
			return
		}
		if !authenticated(r) {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"private":true}`)
	})
	mux.HandleFunc("GET /repos/{owner}/{name}/releases", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("name") == "missing" {
			http.NotFound(w, r)
			return
		}
		if r.PathValue("name") == "private" {
			if !authenticated(r) {
				http.NotFound(w, r)
				return
			}
			fmt.Fprintf(w, `[
				{"tag_name":"v1.0.0","name":"One","draft":false,"prerelease":false,
				 "published_at":"2026-01-01T00:00:00Z",
				 "assets":[{"name":"Mine-1.0.0.jar","size":%d,
				            "url":"%s/repos/%s/%s/releases/assets/1"}]}
			]`, len(gh.body), gh.URL(), r.PathValue("owner"), r.PathValue("name"))
			return
		}
		fmt.Fprintf(w, `[
			{"tag_name":"v2.0.0","name":"Two","draft":false,"prerelease":false,
			 "published_at":"2026-02-01T00:00:00Z",
			 "assets":[{"name":"Foo-2.0.0.jar","size":%d,"browser_download_url":"%s/asset"},
			           {"name":"Foo-2.0.0-sources.jar","size":9999,"browser_download_url":"%s/asset"}]},
			{"tag_name":"v1.0.0","name":"One","draft":false,"prerelease":false,
			 "published_at":"2026-01-01T00:00:00Z",
			 "assets":[{"name":"Foo-1.0.0.jar","size":%d,"browser_download_url":"%s/asset"}]}
		]`, len(gh.body), gh.URL(), gh.URL(), len(gh.body), gh.URL())
	})
	mux.HandleFunc("GET /asset", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(gh.body)
	})

	gh.server = httptest.NewServer(mux)
	t.Cleanup(gh.server.Close)
	return gh
}

func (g *fakeGitHub) URL() string { return g.server.URL }

// addTestPlugin tracks a plugin and returns it.
func (e *testEnv) addTestPlugin(repo string) plugin.Plugin {
	e.t.Helper()

	resp := e.do(http.MethodPost, "/api/plugins", pluginRequest{Repo: repo})
	if resp.StatusCode != http.StatusCreated {
		defer resp.Body.Close()
		e.t.Fatalf("add plugin: %d", resp.StatusCode)
	}
	var item plugin.Plugin
	decodeBody(e.t, resp, &item)
	return item
}

// awaitPluginDownload polls the library endpoint the way the UI does.
func (e *testEnv) awaitPluginDownload() plugin.Job {
	e.t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var library pluginLibraryResponse
		decodeBody(e.t, e.do(http.MethodGet, "/api/plugins", nil), &library)
		if library.Job != nil && library.Job.State != plugin.JobDownloading {
			return *library.Job
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatal("plugin download did not finish in time")
	return plugin.Job{}
}

// downloadTestPlugin fetches one release into the library.
func (e *testEnv) downloadTestPlugin(id, tag string) plugin.Job {
	e.t.Helper()

	resp := e.do(http.MethodPost, "/api/plugins/"+id+"/download", pluginDownloadRequest{Tag: tag})
	if resp.StatusCode != http.StatusAccepted {
		defer resp.Body.Close()
		e.t.Fatalf("start download: %d", resp.StatusCode)
	}
	resp.Body.Close()

	job := e.awaitPluginDownload()
	if job.State != plugin.JobDone {
		e.t.Fatalf("download did not succeed: %+v", job)
	}
	return job
}

// newTestInstance creates an instance with a real directory on disk.
func (e *testEnv) newTestInstance(name string) instance.Status {
	e.t.Helper()

	dir := filepath.Join(e.paths.ServersRoot(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatalf("mkdir: %v", err)
	}
	resp := e.do(http.MethodPost, "/api/instances", instanceRequest{Name: name, Directory: dir})
	if resp.StatusCode != http.StatusCreated {
		defer resp.Body.Close()
		e.t.Fatalf("create instance: %d", resp.StatusCode)
	}
	var created instance.Status
	decodeBody(e.t, resp, &created)
	return created
}

func TestAddPluginNormalisesTheRepoAndChecksItOnce(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	item := env.addTestPlugin("https://github.com/o/foo/releases")
	if item.Source.Repo != "o/foo" {
		t.Errorf("repo is %q", item.Source.Repo)
	}
	// Adding a plugin runs one check, so the card can offer a version straight
	// away rather than saying nothing until the operator clicks again.
	if item.Latest == nil || item.Latest.Tag != "v2.0.0" {
		t.Fatalf("expected the newest release to be cached: %+v", item.Latest)
	}
	// The sources jar is bigger; picking it would install something that does
	// nothing at all.
	if item.Latest.Asset.Name != "Foo-2.0.0.jar" {
		t.Errorf("picked asset %q", item.Latest.Asset.Name)
	}
}

func TestAddPluginRejectsANonsenseRepo(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/plugins", pluginRequest{Repo: "not-a-repo"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAddPluginRefusesADuplicate(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	env.addTestPlugin("o/foo")

	resp := env.do(http.MethodPost, "/api/plugins", pluginRequest{Repo: "o/foo"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", resp.StatusCode)
	}
}

func TestPluginDownloadLandsInTheLibrary(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	item := env.addTestPlugin("o/foo")

	job := env.downloadTestPlugin(item.ID, "v1.0.0")
	if job.FileName != "Foo-1.0.0.jar" {
		t.Errorf("downloaded %q", job.FileName)
	}

	var library pluginLibraryResponse
	decodeBody(t, env.do(http.MethodGet, "/api/plugins", nil), &library)
	if len(library.Plugins) != 1 || len(library.Plugins[0].Versions) != 1 {
		t.Fatalf("unexpected library: %+v", library.Plugins)
	}
	if library.Plugins[0].Versions[0].SHA256 == "" {
		t.Error("the download should have been digested")
	}
}

func TestInstallPluginIntoAnInstanceAndSwitchVersions(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	item := env.addTestPlugin("o/foo")
	env.downloadTestPlugin(item.ID, "v1.0.0")
	env.downloadTestPlugin(item.ID, "v2.0.0")
	created := env.newTestInstance("srv")

	install := func(tag string) {
		t.Helper()
		resp := env.do(http.MethodPost, "/api/instances/"+created.ID+"/plugins",
			installPluginRequest{PluginID: item.ID, Tag: tag})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("install %s: %d", tag, resp.StatusCode)
		}
	}

	install("v1.0.0")
	if _, err := os.Stat(filepath.Join(created.Directory, "plugins", "Foo-1.0.0.jar")); err != nil {
		t.Fatalf("the jar is not in plugins/: %v", err)
	}

	install("v2.0.0")
	// Two versions of one plugin in the same directory is how a server ends up
	// loading both and refusing to start.
	if _, err := os.Stat(filepath.Join(created.Directory, "plugins", "Foo-1.0.0.jar")); !os.IsNotExist(err) {
		t.Error("the old jar was left behind")
	}

	var listing instancePluginsResponse
	decodeBody(t, env.do(http.MethodGet, "/api/instances/"+created.ID+"/plugins", nil), &listing)
	if len(listing.Entries) != 1 || listing.Entries[0].Tag != "v2.0.0" || !listing.Entries[0].Enabled {
		t.Fatalf("unexpected listing: %+v", listing.Entries)
	}
}

func TestToggleAndUninstallAnInstancePlugin(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	item := env.addTestPlugin("o/foo")
	env.downloadTestPlugin(item.ID, "v1.0.0")
	created := env.newTestInstance("srv")

	resp := env.do(http.MethodPost, "/api/instances/"+created.ID+"/plugins",
		installPluginRequest{PluginID: item.ID, Tag: "v1.0.0"})
	resp.Body.Close()

	key := "plugin:" + item.ID
	resp = env.do(http.MethodPut, "/api/instances/"+created.ID+"/plugins",
		togglePluginRequest{Key: key, Enabled: false})
	var entries []plugin.Entry
	decodeBody(t, resp, &entries)
	if len(entries) != 1 || entries[0].Enabled {
		t.Fatalf("expected the plugin to read as disabled: %+v", entries)
	}
	if _, err := os.Stat(filepath.Join(created.Directory, "plugins", "Foo-1.0.0.jar.disabled")); err != nil {
		t.Fatalf("disabling should rename the jar: %v", err)
	}

	resp = env.do(http.MethodDelete, "/api/instances/"+created.ID+"/plugins?key="+key, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("uninstall: %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(created.Directory, "plugins", "Foo-1.0.0.jar.disabled")); !os.IsNotExist(err) {
		t.Error("the jar survived the uninstall")
	}
}

func TestLibraryReportsWhichInstancesUseAPlugin(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	item := env.addTestPlugin("o/foo")
	env.downloadTestPlugin(item.ID, "v1.0.0")
	created := env.newTestInstance("srv")

	resp := env.do(http.MethodPost, "/api/instances/"+created.ID+"/plugins",
		installPluginRequest{PluginID: item.ID, Tag: "v1.0.0"})
	resp.Body.Close()

	var library pluginLibraryResponse
	decodeBody(t, env.do(http.MethodGet, "/api/plugins", nil), &library)
	if len(library.Plugins) != 1 || len(library.Plugins[0].UsedBy) != 1 ||
		library.Plugins[0].UsedBy[0] != "srv" {
		t.Fatalf("unexpected usedBy: %+v", library.Plugins)
	}
}

func TestDeletingAnInstanceDropsItsPluginRecords(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	item := env.addTestPlugin("o/foo")
	env.downloadTestPlugin(item.ID, "v1.0.0")
	created := env.newTestInstance("srv")

	resp := env.do(http.MethodPost, "/api/instances/"+created.ID+"/plugins",
		installPluginRequest{PluginID: item.ID, Tag: "v1.0.0"})
	resp.Body.Close()

	resp = env.do(http.MethodDelete, "/api/instances/"+created.ID+"?deleteFiles=false", nil)
	resp.Body.Close()

	var library pluginLibraryResponse
	decodeBody(t, env.do(http.MethodGet, "/api/plugins", nil), &library)
	if len(library.Plugins[0].UsedBy) != 0 {
		t.Fatalf("the deleted instance is still listed: %+v", library.Plugins[0].UsedBy)
	}
}

func TestDeletingAPluginLeavesInstalledCopiesAlone(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	item := env.addTestPlugin("o/foo")
	env.downloadTestPlugin(item.ID, "v1.0.0")
	created := env.newTestInstance("srv")

	resp := env.do(http.MethodPost, "/api/instances/"+created.ID+"/plugins",
		installPluginRequest{PluginID: item.ID, Tag: "v1.0.0"})
	resp.Body.Close()

	resp = env.do(http.MethodDelete, "/api/plugins/"+item.ID, nil)
	resp.Body.Close()

	// The instance owns its copy, so deleting the library entry cannot change
	// what a running server has loaded.
	if _, err := os.Stat(filepath.Join(created.Directory, "plugins", "Foo-1.0.0.jar")); err != nil {
		t.Fatalf("the installed copy was taken away: %v", err)
	}
}

func TestPluginEndpointsReportUpstreamFailures(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/plugins", pluginRequest{Repo: "o/missing"})
	defer resp.Body.Close()
	// The plugin is still tracked — the operator may have added it before the
	// first release exists — but the check that ran with it failed, and that
	// is what the card shows.
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var item plugin.Plugin
	decodeBody(t, resp, &item)
	if item.CheckError == "" {
		t.Error("the failed check should have been recorded")
	}

	releases := env.do(http.MethodGet, "/api/plugins/"+item.ID+"/releases", nil)
	defer releases.Body.Close()
	// A repository nobody can see and one that does not exist are the same 404
	// from GitHub, so the panel reports the one an operator can act on: the
	// missing credential.
	if releases.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", releases.StatusCode)
	}
}

func TestPrivatePluginIsUnreachableUntilATokenIsConfigured(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	// Adding it is fine — the source is only a record, and nothing is fetched
	// until a version is picked — but the check that runs with it cannot see a
	// repository the panel has no credential for.
	resp := env.do(http.MethodPost, "/api/plugins", pluginRequest{Repo: "me/private", Private: true})
	if resp.StatusCode != http.StatusCreated {
		defer resp.Body.Close()
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var item plugin.Plugin
	decodeBody(t, resp, &item)
	if item.CheckError == "" {
		t.Fatal("the failed check should have been recorded")
	}

	blind := env.do(http.MethodGet, "/api/plugins/"+item.ID+"/releases", nil)
	blind.Body.Close()
	if blind.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 before a token is configured, got %d", blind.StatusCode)
	}

	var library pluginLibraryResponse
	decodeBody(t, env.do(http.MethodPut, "/api/plugins/config/token",
		pluginTokenRequest{Token: fakeGitHubToken}), &library)
	if !library.TokenConfigured || library.TokenHint != fakeGitHubToken[len(fakeGitHubToken)-4:] {
		t.Fatalf("the stored token should be reported, and only by its tail: %+v", library)
	}

	var releases []plugin.Release
	decodeBody(t, env.do(http.MethodGet, "/api/plugins/"+item.ID+"/releases", nil), &releases)
	if len(releases) != 1 || releases[0].Tag != "v1.0.0" {
		t.Fatalf("unexpected releases: %+v", releases)
	}

	started := env.do(http.MethodPost, "/api/plugins/"+item.ID+"/download",
		pluginDownloadRequest{Tag: "v1.0.0"})
	started.Body.Close()
	if started.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", started.StatusCode)
	}
	job := env.awaitPluginDownload()
	if job.State != plugin.JobDone {
		t.Fatalf("private download did not finish: %+v", job)
	}

	// The token has to survive a restart, or every update would silently stop
	// seeing the operator's own repositories.
	panel, err := env.store.LoadPanel()
	if err != nil {
		t.Fatalf("LoadPanel: %v", err)
	}
	if panel.GitHubToken != fakeGitHubToken {
		t.Errorf("the token was not persisted: %q", panel.GitHubToken)
	}
}

func TestPrivateRepositoryIsRecognisedWithoutBeingDeclared(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	stored := env.do(http.MethodPut, "/api/plugins/config/token", pluginTokenRequest{Token: fakeGitHubToken})
	stored.Body.Close()

	// No Private flag: the operator pasted a repository URL and pressed add,
	// which is the whole of what they should have to know. Reading the releases
	// works either way, so nothing would go wrong until the download asked the
	// public host for a jar it will never serve.
	var item plugin.Plugin
	decodeBody(t, env.do(http.MethodPost, "/api/plugins", pluginRequest{Repo: "me/private"}), &item)
	if !item.Source.Private {
		t.Fatalf("the repository's visibility should have been detected: %+v", item.Source)
	}

	started := env.do(http.MethodPost, "/api/plugins/"+item.ID+"/download",
		pluginDownloadRequest{Tag: "v1.0.0"})
	started.Body.Close()
	if started.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", started.StatusCode)
	}
	if job := env.awaitPluginDownload(); job.State != plugin.JobDone {
		t.Fatalf("download did not finish: %+v", job)
	}
}

func TestPublicRepositoryIsNotMarkedPrivateByAConfiguredToken(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	stored := env.do(http.MethodPut, "/api/plugins/config/token", pluginTokenRequest{Token: fakeGitHubToken})
	stored.Body.Close()

	// Ticked by hand, and wrong: GitHub says this one is public, so it goes back
	// to the download host — and to the mirror, which is the point of the flag.
	var item plugin.Plugin
	decodeBody(t, env.do(http.MethodPost, "/api/plugins",
		pluginRequest{Repo: "o/public", Private: true}), &item)
	if item.Source.Private {
		t.Fatalf("a public repository should not stay marked private: %+v", item.Source)
	}
}

func TestPluginMirrorIsChosenAndPersisted(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	var library pluginLibraryResponse
	decodeBody(t, env.do(http.MethodGet, "/api/plugins", nil), &library)
	if library.Mirror != plugin.MirrorAuto || len(library.Mirrors) < 2 {
		t.Fatalf("a fresh panel should offer mirrors and default to automatic: %+v", library)
	}

	decodeBody(t, env.do(http.MethodPut, "/api/plugins/config/mirror",
		pluginMirrorRequest{Mirror: plugin.MirrorDirect}), &library)
	if library.Mirror != plugin.MirrorDirect {
		t.Fatalf("mirror is %q", library.Mirror)
	}

	panel, err := env.store.LoadPanel()
	if err != nil {
		t.Fatalf("LoadPanel: %v", err)
	}
	if panel.PluginMirror != plugin.MirrorDirect {
		t.Errorf("the choice was not persisted: %q", panel.PluginMirror)
	}

	// An operator's own proxy is as valid as the ones this build ships with;
	// anything that is neither an id nor a prefix is refused rather than
	// quietly turned into the default.
	decodeBody(t, env.do(http.MethodPut, "/api/plugins/config/mirror",
		pluginMirrorRequest{Mirror: "https://my.proxy"}), &library)
	if library.Mirror != "https://my.proxy/" {
		t.Errorf("custom prefix is %q", library.Mirror)
	}

	rejected := env.do(http.MethodPut, "/api/plugins/config/mirror",
		pluginMirrorRequest{Mirror: "ghfast.top"})
	rejected.Body.Close()
	if rejected.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rejected.StatusCode)
	}
}

func TestPluginTokenIsNeverReadBackOut(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	stored := env.do(http.MethodPut, "/api/plugins/config/token", pluginTokenRequest{Token: fakeGitHubToken})
	stored.Body.Close()

	resp := env.do(http.MethodGet, "/api/plugins", nil)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(body), fakeGitHubToken) {
		t.Fatalf("the library listing handed the token back: %s", body)
	}

	// Clearing it is how a token that leaked or expired is taken away.
	var library pluginLibraryResponse
	decodeBody(t, env.do(http.MethodPut, "/api/plugins/config/token", pluginTokenRequest{Token: ""}), &library)
	if library.TokenConfigured {
		t.Error("the token should have been cleared")
	}

	rejected := env.do(http.MethodPut, "/api/plugins/config/token",
		pluginTokenRequest{Token: "ghp_with a space"})
	rejected.Body.Close()
	if rejected.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for a mangled token, got %d", rejected.StatusCode)
	}
}

func TestInstancePluginListsUnmanagedJars(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.newTestInstance("srv")

	dir := filepath.Join(created.Directory, "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Vault.jar"), []byte("v"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var listing instancePluginsResponse
	decodeBody(t, env.do(http.MethodGet, "/api/instances/"+created.ID+"/plugins", nil), &listing)
	if len(listing.Entries) != 1 || listing.Entries[0].Managed {
		t.Fatalf("expected one unmanaged row: %+v", listing.Entries)
	}
	if listing.Entries[0].Key != "file:plugins/Vault.jar" {
		t.Errorf("key is %q", listing.Entries[0].Key)
	}
}
