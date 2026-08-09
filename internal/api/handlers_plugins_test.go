package api

import (
	"fmt"
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

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	gh := &fakeGitHub{body: []byte(strings.Repeat("plugin", 512))}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/{owner}/{name}/releases", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("name") == "missing" {
			http.NotFound(w, r)
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
	if releases.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", releases.StatusCode)
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
