package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/lanscarlos/hypercraft/internal/plugin"
)

func TestOverviewNamesWhichInstancesAreBehind(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	// Two servers, one plugin, two versions — the case the whole page exists
	// for. The older release is installed on one and the newer on the other.
	item := env.addTestPlugin("o/foo")
	env.downloadTestPlugin(item.ID, "v1.0.0")
	env.downloadTestPlugin(item.ID, "v2.0.0")

	behind := env.newTestInstance("behind")
	current := env.newTestInstance("current")
	env.installPlugin(behind.ID, item.ID, "v1.0.0")
	env.installPlugin(current.ID, item.ID, "v2.0.0")

	var overview overviewResponse
	decodeBody(t, env.do(http.MethodGet, "/api/plugins/overview", nil), &overview)

	row := findRow(t, overview, item.ID)
	if row.Status != overviewMixed {
		t.Errorf("status = %q, want %q", row.Status, overviewMixed)
	}
	if len(row.Used) != 2 {
		t.Fatalf("used = %+v", row.Used)
	}
	for _, use := range row.Used {
		wantOutdated := use.Name == "behind"
		if use.Outdated != wantOutdated {
			t.Errorf("%s: outdated = %v, want %v", use.Name, use.Outdated, wantOutdated)
		}
		if use.InstanceID == "" {
			t.Errorf("%s: the row has to be able to link to the instance", use.Name)
		}
	}
}

func TestOverviewSurfacesCacheNobodyIsUsing(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	// A jar downloaded and then uninstalled everywhere. Nothing else in the
	// panel would ever mention it again, and it keeps its disk.
	item := env.addTestPlugin("o/foo")
	env.downloadTestPlugin(item.ID, "v2.0.0")

	var overview overviewResponse
	decodeBody(t, env.do(http.MethodGet, "/api/plugins/overview", nil), &overview)

	row := findRow(t, overview, item.ID)
	if row.Status != overviewUnused {
		t.Errorf("status = %q, want %q", row.Status, overviewUnused)
	}
	if overview.Unused != 1 || overview.UnusedSize != row.Size || row.Size == 0 {
		t.Errorf("unused summary = %d/%d, row size %d", overview.Unused, overview.UnusedSize, row.Size)
	}
}

func TestBulkUpgradePreviewNamesEveryAffectedInstance(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	item := env.addTestPlugin("o/foo")
	env.downloadTestPlugin(item.ID, "v1.0.0")
	env.downloadTestPlugin(item.ID, "v2.0.0")

	first := env.newTestInstance("alpha")
	second := env.newTestInstance("beta")
	env.installPlugin(first.ID, item.ID, "v1.0.0")
	env.installPlugin(second.ID, item.ID, "v1.0.0")

	var impact bulkImpact
	decodeBody(t, env.do(http.MethodPost, "/api/plugins/bulk/preview",
		bulkUpgradeRequest{PluginIDs: []string{item.ID}}), &impact)

	// The confirmation cannot say "两台实例均需重启" without this list.
	if len(impact.Instances) != 2 {
		t.Fatalf("instances = %+v", impact.Instances)
	}
	if impact.Instances[0].Name != "alpha" || impact.Instances[1].Name != "beta" {
		t.Errorf("instances should be named and ordered: %+v", impact.Instances)
	}
	// Neither is running, so nothing needs restarting — and saying "两台需重启"
	// about two stopped servers would be a warning nobody should act on.
	if impact.Restarts != 0 {
		t.Errorf("restarts = %d, want 0 for stopped servers", impact.Restarts)
	}
	if len(impact.Plugins) != 1 || impact.Plugins[0].To != "2.0.0" {
		t.Fatalf("plugins = %+v", impact.Plugins)
	}
}

func TestBulkUpgradeBringsEveryInstanceUpAndReportsWhatItDid(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	item := env.addTestPlugin("o/foo")
	env.downloadTestPlugin(item.ID, "v1.0.0")
	env.downloadTestPlugin(item.ID, "v2.0.0")

	first := env.newTestInstance("alpha")
	second := env.newTestInstance("beta")
	env.installPlugin(first.ID, item.ID, "v1.0.0")
	env.installPlugin(second.ID, item.ID, "v1.0.0")

	var result bulkUpgradeResult
	decodeBody(t, env.do(http.MethodPost, "/api/plugins/bulk/upgrade",
		bulkUpgradeRequest{PluginIDs: []string{item.ID}}), &result)

	if result.Applied != 2 || len(result.Failures) != 0 {
		t.Fatalf("result = %+v", result)
	}

	var overview overviewResponse
	decodeBody(t, env.do(http.MethodGet, "/api/plugins/overview", nil), &overview)
	if row := findRow(t, overview, item.ID); row.Status != overviewAllCurrent {
		t.Errorf("after the upgrade the row should be all-current, got %q", row.Status)
	}
}

func TestInstanceListingReportsTheServersDetectedVersion(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	created := env.newTestInstance("survival")
	history := `{"currentVersion":"git-Paper-496 (MC: 1.20.4)"}`
	path := filepath.Join(created.Directory, "version_history.json")
	if err := os.WriteFile(path, []byte(history), 0o644); err != nil {
		t.Fatal(err)
	}

	var listing instancePluginsResponse
	decodeBody(t, env.do(http.MethodGet, "/api/instances/"+created.ID+"/plugins", nil), &listing)

	if listing.Target.MCVersion != "1.20.4" || listing.Target.Loader != "paper" {
		t.Errorf("target = %+v", listing.Target)
	}
}

func TestInstalledPluginsCarryACompatibilityVerdict(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	item := env.addTestPlugin("o/foo")
	env.downloadTestPlugin(item.ID, "v2.0.0")
	created := env.newTestInstance("survival")
	env.installPlugin(created.ID, item.ID, "v2.0.0")

	var listing instancePluginsResponse
	decodeBody(t, env.do(http.MethodGet, "/api/instances/"+created.ID+"/plugins", nil), &listing)

	if len(listing.Entries) == 0 {
		t.Fatal("no entries")
	}
	// A GitHub release says nothing about game versions, and neither does a
	// server directory with no version_history.json. Unknown is the only
	// honest verdict, and it must not be green.
	for _, entry := range listing.Entries {
		if entry.Compat == nil {
			t.Fatalf("%s has no verdict", entry.Name)
		}
		if entry.Compat.State != plugin.CompatUnknown {
			t.Errorf("%s: state = %q, want unknown", entry.Name, entry.Compat.State)
		}
	}
}

func TestEveryRowOffersItsConfigDirectory(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	item := env.addTestPlugin("o/foo")
	env.downloadTestPlugin(item.ID, "v2.0.0")
	created := env.newTestInstance("survival")
	env.installPlugin(created.ID, item.ID, "v2.0.0")

	var listing instancePluginsResponse
	decodeBody(t, env.do(http.MethodGet, "/api/instances/"+created.ID+"/plugins", nil), &listing)

	for _, entry := range listing.Entries {
		if entry.ConfigDir == "" {
			t.Errorf("%s has no config directory to link to", entry.Name)
		}
	}
}

// A stopped server has nothing pending: every change will be picked up by its
// next start, so a banner counting them would be telling the operator to
// restart something that is not running.
func TestAStoppedServerHasNothingPending(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	item := env.addTestPlugin("o/foo")
	env.downloadTestPlugin(item.ID, "v2.0.0")
	created := env.newTestInstance("survival")
	env.installPlugin(created.ID, item.ID, "v2.0.0")

	var listing instancePluginsResponse
	decodeBody(t, env.do(http.MethodGet, "/api/instances/"+created.ID+"/plugins", nil), &listing)

	if listing.Live {
		t.Fatal("the instance should not be running")
	}
	if len(listing.Pending) != 0 {
		t.Errorf("pending = %+v", listing.Pending)
	}
}

func TestBrowseOffersEveryInstanceAsATarget(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	env.newTestInstance("survival")
	env.newTestInstance("creative")

	// Reaching the real registries is not something a test should do, so the
	// sources are all deselected. What is being checked is the part the page
	// cannot work without: the 安装到 list, and the filter vocabulary.
	var resp browseResponse
	decodeBody(t, env.do(http.MethodGet, "/api/plugins/browse?sources=none", nil), &resp)

	if len(resp.Targets) != 2 {
		t.Fatalf("targets = %+v", resp.Targets)
	}
	for _, target := range resp.Targets {
		if target.PluginDir != "plugins" {
			t.Errorf("%s: plugin dir = %q", target.Name, target.PluginDir)
		}
	}
	if len(resp.Sources) != 3 {
		t.Errorf("sources = %+v", resp.Sources)
	}
	if len(resp.Categories) == 0 {
		t.Error("the rail needs categories")
	}
}

// ----------------------------------------------------------------- helpers

func (e *testEnv) installPlugin(instanceID, pluginID, tag string) {
	e.t.Helper()

	resp := e.do(http.MethodPost, "/api/instances/"+instanceID+"/plugins",
		installPluginRequest{PluginID: pluginID, Tag: tag})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("install %s into %s: %d", pluginID, instanceID, resp.StatusCode)
	}
}

func findRow(t *testing.T, overview overviewResponse, id string) overviewRow {
	t.Helper()
	for _, row := range overview.Rows {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("no row for %s in %+v", id, overview.Rows)
	return overviewRow{}
}
