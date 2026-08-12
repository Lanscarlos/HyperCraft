package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/mcyaml"
)

func (e *testEnv) getServerConfigs(id string) serverConfigResponse {
	e.t.Helper()
	resp := e.do(http.MethodGet, "/api/instances/"+id+"/configs", nil)
	var out serverConfigResponse
	decodeBody(e.t, resp, &out)
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("GET configs: %d", resp.StatusCode)
	}
	return out
}

func fileWithID(t *testing.T, out serverConfigResponse, id string) serverConfigFileResponse {
	t.Helper()
	for _, file := range out.Files {
		if file.ID == id {
			return file
		}
	}
	t.Fatalf("no %s among %d files", id, len(out.Files))
	return serverConfigFileResponse{}
}

func seed(t *testing.T, inst instance.Status, rel, content string) {
	t.Helper()
	full := filepath.Join(inst.Directory, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("seed %s: %v", rel, err)
	}
}

func TestServerConfigsListsTheFilesAndTheirValues(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	server := env.createInstance("生存服")

	seed(t, server, "spigot.yml", "settings:\n  bungeecord: true\n  timeout-time: 90\n")

	out := env.getServerConfigs(server.ID)
	spigot := fileWithID(t, out, fileSpigot)
	if !spigot.Exists {
		t.Error("spigot.yml exists but was reported missing")
	}
	if got := entryValue(spigot.Entries, "settings.bungeecord"); got != "true" {
		t.Errorf("bungeecord = %q", got)
	}
	if got := entryValue(spigot.Entries, "settings.timeout-time"); got != "90" {
		t.Errorf("timeout-time = %q", got)
	}
	// A key the file does not carry is absent from entries, not defaulted into
	// it: the page has to be able to say "still the server's own default".
	if entryValue(spigot.Entries, "settings.netty-threads") != "" {
		t.Error("an absent key came back as an entry")
	}
	if len(spigot.Known) == 0 || len(spigot.Groups) == 0 {
		t.Error("the file was described without settings or groups")
	}

	bukkit := fileWithID(t, out, fileBukkit)
	if bukkit.Exists {
		t.Error("bukkit.yml does not exist yet")
	}
	if !contains(out.Missing, "bukkit.yml") {
		t.Errorf("missing = %#v", out.Missing)
	}
}

// Which Paper layout is offered follows the one on disk. Showing both would put
// two 反矿透 switches on one page, only one of which does anything.
func TestServerConfigsFollowThePaperLayoutOnDisk(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	modern := env.createInstance("新版")
	seed(t, modern, pathPaperGlobal, "proxies:\n  velocity:\n    enabled: false\n")
	ids := fileIDs(env.getServerConfigs(modern.ID))
	if !contains(ids, filePaperGlobal) || contains(ids, filePaperLegacy) {
		t.Errorf("modern layout offered %#v", ids)
	}

	legacy := env.createInstance("老版")
	seed(t, legacy, pathPaperLegacy, "settings:\n  velocity-support:\n    enabled: false\n")
	ids = fileIDs(env.getServerConfigs(legacy.ID))
	if !contains(ids, filePaperLegacy) || contains(ids, filePaperGlobal) {
		t.Errorf("legacy layout offered %#v", ids)
	}

	// A server that has never booted has neither, and gets the layout anything
	// installed today would write.
	fresh := env.createInstance("全新")
	ids = fileIDs(env.getServerConfigs(fresh.ID))
	if !contains(ids, filePaperGlobal) {
		t.Errorf("fresh instance offered %#v", ids)
	}
}

func TestServerConfigSaveKeepsCommentsAndCreatesParents(t *testing.T) {
	env := newTestEnv(t, withConfigHistory)
	env.login()
	server := env.createInstance("生存服")

	seed(t, server, "spigot.yml", "# Spigot 配置\nsettings:\n  bungeecord: false # 走代理时打开\n")

	resp := env.do(http.MethodPut, "/api/instances/"+server.ID+"/configs/"+fileSpigot,
		putServerConfigRequest{Entries: []mcyaml.Entry{
			{Key: "settings.bungeecord", Value: "true"},
			{Key: "messages.whitelist", Value: "本服需要白名单"},
			{Key: "world-settings.default.mob-spawn-range", Value: "4"},
		}})
	var saved serverConfigFileResponse
	decodeBody(t, resp, &saved)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT config: %d", resp.StatusCode)
	}
	if got := entryValue(saved.Entries, "messages.whitelist"); got != "本服需要白名单" {
		t.Errorf("whitelist = %q", got)
	}

	raw, err := os.ReadFile(filepath.Join(server.Directory, "spigot.yml"))
	if err != nil {
		t.Fatalf("read spigot.yml: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		"# Spigot 配置",
		"  bungeecord: true # 走代理时打开",
		"messages:",
		"  whitelist: 本服需要白名单",
		"world-settings:",
		"    mob-spawn-range: 4",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("spigot.yml is missing %q:\n%s", want, text)
		}
	}

	// A change through the panel is a change the timeline has to carry.
	waitFor(t, func() bool { return len(env.configHistory(server.ID).Timeline) > 0 })
	if got := env.configHistory(server.ID).Timeline[0].Message; got != "编辑 spigot.yml" {
		t.Errorf("snapshot message = %q", got)
	}
}

// Paper's config/ does not exist until the server has booted once, and setting
// a server up before its first boot is the normal case rather than the odd one.
func TestServerConfigSaveCreatesPaperConfigDirectory(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	server := env.createInstance("生存服")

	resp := env.do(http.MethodPut, "/api/instances/"+server.ID+"/configs/"+filePaperGlobal,
		putServerConfigRequest{Entries: []mcyaml.Entry{
			{Key: "proxies.velocity.enabled", Value: "true"},
			{Key: "proxies.velocity.secret", Value: "s3cret"},
		}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT config: %d", resp.StatusCode)
	}

	raw, err := os.ReadFile(filepath.Join(server.Directory, "config", "paper-global.yml"))
	if err != nil {
		t.Fatalf("read paper-global.yml: %v", err)
	}
	want := "proxies:\n  velocity:\n    enabled: true\n    secret: s3cret\n"
	if string(raw) != want {
		t.Errorf("paper-global.yml =\n%s\nwant\n%s", raw, want)
	}
}

func TestServerConfigRejectsBadInput(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	server := env.createInstance("生存服")

	cases := map[string]putServerConfigRequest{
		"a number that is not a number": {Entries: []mcyaml.Entry{
			{Key: "settings.timeout-time", Value: "一分钟"},
		}},
		"a boolean that is not one": {Entries: []mcyaml.Entry{
			{Key: "settings.bungeecord", Value: "开"},
		}},
		"a key the panel does not offer": {Entries: []mcyaml.Entry{
			{Key: "settings.definitely-not-spigot", Value: "1"},
		}},
	}
	for name, req := range cases {
		resp := env.do(http.MethodPut, "/api/instances/"+server.ID+"/configs/"+fileSpigot, req)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", name, resp.StatusCode)
		}
	}
	if _, err := os.Stat(filepath.Join(server.Directory, "spigot.yml")); !os.IsNotExist(err) {
		t.Error("a refused save still wrote the file")
	}

	resp := env.do(http.MethodPut, "/api/instances/"+server.ID+"/configs/not-a-file",
		putServerConfigRequest{})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown file: expected 404, got %d", resp.StatusCode)
	}
}

// A proxy has none of these files. Offering them would be an invitation to
// configure something nothing reads.
func TestServerConfigsRefuseAProxy(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	proxy := env.createProxy("velocity")

	resp := env.do(http.MethodGet, "/api/instances/"+proxy.ID+"/configs", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("GET configs on a proxy: expected 400, got %d", resp.StatusCode)
	}
}

func entryValue(entries []mcyaml.Entry, key string) string {
	for _, entry := range entries {
		if entry.Key == key {
			return entry.Value
		}
	}
	return ""
}

func fileIDs(out serverConfigResponse) []string {
	ids := make([]string, 0, len(out.Files))
	for _, file := range out.Files {
		ids = append(ids, file.ID)
	}
	return ids
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
