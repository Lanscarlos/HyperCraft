package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/velocitycfg"
)

func (e *testEnv) createProxy(name string) instance.Status {
	e.t.Helper()
	resp := e.do(http.MethodPost, "/api/instances", instanceRequest{Name: name, Kind: instance.KindProxy})
	var created instance.Status
	decodeBody(e.t, resp, &created)
	if resp.StatusCode != http.StatusCreated {
		e.t.Fatalf("create proxy: %d", resp.StatusCode)
	}
	return created
}

func (e *testEnv) getVelocity(id string) velocityResponse {
	e.t.Helper()
	resp := e.do(http.MethodGet, "/api/instances/"+id+"/velocity", nil)
	var out velocityResponse
	decodeBody(e.t, resp, &out)
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("GET velocity: %d", resp.StatusCode)
	}
	return out
}

func valueOf(entries []velocitycfg.Entry, key string) string {
	for _, entry := range entries {
		if entry.Key == key {
			return entry.Value
		}
	}
	return ""
}

// A proxy that has never been started has no velocity.toml — Velocity writes it
// on its first boot. The page still has to be fillable then, which is the one
// moment there is most to fill in.
func TestVelocityConfigFallsBackToTheStockFile(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	proxy := env.createProxy("velocity")

	out := env.getVelocity(proxy.ID)
	if out.Exists {
		t.Error("exists is true before the proxy has ever run")
	}
	if got := valueOf(out.Entries, "bind"); got != "0.0.0.0:25577" {
		t.Errorf("bind = %q", got)
	}
	if got := valueOf(out.Entries, "player-info-forwarding-mode"); got != "none" {
		t.Errorf("forwarding mode = %q, want the lower-case spelling", got)
	}
	if len(out.Servers) != 0 {
		t.Errorf("a fresh proxy came with servers: %#v", out.Servers)
	}
	if len(out.Known) == 0 {
		t.Error("no settings were described")
	}
}

func TestVelocityConfigSaveWritesTheFile(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	proxy := env.createProxy("velocity")

	servers := []velocityServer{
		{Name: "lobby", Address: "127.0.0.1:25566"},
		{Name: "survival", Address: "127.0.0.1:25567"},
	}
	try := []string{"lobby"}
	resp := env.do(http.MethodPut, "/api/instances/"+proxy.ID+"/velocity", putVelocityRequest{
		Entries: []velocitycfg.Entry{
			{Key: "bind", Value: "0.0.0.0:25565"},
			{Key: "motd", Value: "跨服大厅"},
			{Key: "show-max-players", Value: "120"},
			{Key: "online-mode", Value: "false"},
			{Key: "player-info-forwarding-mode", Value: "modern"},
		},
		Servers:          &servers,
		Try:              &try,
		ForwardingSecret: "s3cret-token",
	})
	var saved velocityResponse
	decodeBody(t, resp, &saved)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT velocity: %d", resp.StatusCode)
	}

	if got := valueOf(saved.Entries, "motd"); got != "跨服大厅" {
		t.Errorf("motd = %q", got)
	}
	if len(saved.Servers) != 2 || saved.Servers[0].Name != "lobby" {
		t.Errorf("servers = %#v", saved.Servers)
	}
	if len(saved.Try) != 1 || saved.Try[0] != "lobby" {
		t.Errorf("try = %#v", saved.Try)
	}

	// What Velocity itself will read, on disk.
	raw, err := os.ReadFile(filepath.Join(proxy.Directory, velocitycfg.FileName))
	if err != nil {
		t.Fatalf("read velocity.toml: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		`bind = "0.0.0.0:25565"`,
		`motd = "跨服大厅"`,
		"show-max-players = 120",
		"online-mode = false",
		// The enum goes back in the capitals Velocity writes, not the
		// lower-case spelling the UI shows.
		`player-info-forwarding-mode = "MODERN"`,
		`lobby = "127.0.0.1:25566"`,
		`try = ["lobby"]`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("velocity.toml is missing %s:\n%s", want, text)
		}
	}
	// Velocity's own comments came along with its defaults, and stay.
	if !strings.Contains(text, "# Config version. Do not change this") {
		t.Error("the stock comments were dropped")
	}

	secret, err := os.ReadFile(filepath.Join(proxy.Directory, velocitycfg.DefaultSecretFile))
	if err != nil {
		t.Fatalf("read forwarding.secret: %v", err)
	}
	if strings.TrimSpace(string(secret)) != "s3cret-token" {
		t.Errorf("forwarding secret = %q", secret)
	}
	if got := env.getVelocity(proxy.ID); got.Secret.Value != "s3cret-token" || !got.Secret.Exists {
		t.Errorf("secret came back as %#v", got.Secret)
	}
}

// Saving twice must not stack up copies of the same key, and must not lose the
// sub-server added by the first save.
func TestVelocityConfigSavesAreIdempotent(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	proxy := env.createProxy("velocity")

	servers := []velocityServer{{Name: "lobby", Address: "127.0.0.1:25566"}}
	for range 2 {
		resp := env.do(http.MethodPut, "/api/instances/"+proxy.ID+"/velocity", putVelocityRequest{
			Entries: []velocitycfg.Entry{{Key: "bind", Value: "0.0.0.0:25565"}},
			Servers: &servers,
		})
		resp.Body.Close()
	}

	raw, err := os.ReadFile(filepath.Join(proxy.Directory, velocitycfg.FileName))
	if err != nil {
		t.Fatalf("read velocity.toml: %v", err)
	}
	if got := strings.Count(string(raw), "bind = "); got != 1 {
		t.Errorf("bind appears %d times", got)
	}
	if got := strings.Count(string(raw), "lobby = "); got != 1 {
		t.Errorf("lobby appears %d times", got)
	}
}

// An address without a port is the mistake this page exists to prevent:
// Velocity does not assume 25565, it refuses to load the config at all.
func TestVelocityConfigRejectsBadInput(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	proxy := env.createProxy("velocity")

	cases := map[string]putVelocityRequest{
		"an address with no port": {Servers: &[]velocityServer{{Name: "lobby", Address: "127.0.0.1"}}},
		"a name with a space":     {Servers: &[]velocityServer{{Name: "my lobby", Address: "127.0.0.1:1"}}},
		"a duplicate name": {Servers: &[]velocityServer{
			{Name: "lobby", Address: "127.0.0.1:1"},
			{Name: "lobby", Address: "127.0.0.1:2"},
		}},
		"a try entry that names nothing": {Try: &[]string{"nowhere"}},
		"a number that is not a number":  {Entries: []velocitycfg.Entry{{Key: "show-max-players", Value: "十"}}},
		"a setting the panel does not know": {
			Entries: []velocitycfg.Entry{{Key: "definitely-not-velocity", Value: "1"}},
		},
	}
	for name, req := range cases {
		resp := env.do(http.MethodPut, "/api/instances/"+proxy.ID+"/velocity", req)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", name, resp.StatusCode)
		}
	}

	if _, err := os.Stat(filepath.Join(proxy.Directory, velocitycfg.FileName)); !os.IsNotExist(err) {
		t.Error("a refused save still wrote the file")
	}
}

// The two config pages are exclusive. A server has no velocity.toml and a proxy
// has no server.properties; answering either request with an empty form would
// be an invitation to configure a file nothing reads.
func TestConfigEndpointsRefuseTheWrongKind(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	proxy := env.createProxy("velocity")
	server := env.createInstance("paper")

	for _, path := range []string{"/properties", "/eula"} {
		resp := env.do(http.MethodGet, "/api/instances/"+proxy.ID+path, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET %s on a proxy: expected 400, got %d", path, resp.StatusCode)
		}
	}
	resp := env.do(http.MethodPut, "/api/instances/"+proxy.ID+"/properties",
		putPropertiesRequest{Entries: nil})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("PUT properties on a proxy: expected 400, got %d", resp.StatusCode)
	}

	resp = env.do(http.MethodGet, "/api/instances/"+server.ID+"/velocity", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("GET velocity on a server: expected 400, got %d", resp.StatusCode)
	}
}

// The launch settings form does not know about kinds and does not send one.
// Saving it must not quietly turn a proxy back into a server — which would put
// --nogui back on the command line and stop it booting.
func TestUpdateKeepsTheKindWhenItIsNotSent(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	proxy := env.createProxy("velocity")

	resp := env.do(http.MethodPut, "/api/instances/"+proxy.ID, instanceRequest{
		Name:      proxy.Name,
		Directory: proxy.Directory,
		Jar:       "velocity.jar",
	})
	var updated instance.Status
	decodeBody(t, resp, &updated)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT instance: %d", resp.StatusCode)
	}
	if updated.Kind != instance.KindProxy {
		t.Errorf("kind = %q, want proxy", updated.Kind)
	}
}

// The address of a sub-server is written down in that server's own
// server.properties. Making the operator go and read it is the sort of errand
// a panel exists to save.
func TestVelocitySuggestsTheOtherInstances(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	proxy := env.createProxy("velocity")
	backend := env.createInstance("生存服")

	seed := "server-port=25570\nserver-ip=\n"
	if err := os.WriteFile(filepath.Join(backend.Directory, "server.properties"), []byte(seed), 0o644); err != nil {
		t.Fatalf("seed properties: %v", err)
	}

	out := env.getVelocity(proxy.ID)
	if len(out.Suggests) != 1 {
		t.Fatalf("suggests = %#v, want the one backend", out.Suggests)
	}
	got := out.Suggests[0]
	if got.Address != "127.0.0.1:25570" {
		t.Errorf("address = %q", got.Address)
	}
	if got.Instance != "生存服" {
		t.Errorf("instance = %q", got.Instance)
	}
	// A Chinese instance name has nothing to slugify, and "" is not a name a
	// player can type after /server.
	if got.Name == "" || strings.ContainsAny(got.Name, " 生存服") {
		t.Errorf("suggested name = %q, want something typeable", got.Name)
	}
	if got.Added {
		t.Error("nothing has been added yet")
	}
}

func TestServerNameFromInstanceName(t *testing.T) {
	cases := map[string]string{
		"Lobby":         "lobby",
		"My Survival":   "my-survival",
		"生存服":           "server",
		"  spaced  ":    "spaced",
		"a__b":          "a-b",
		"paper-1.21.11": "paper-1-21-11",
	}
	for in, want := range cases {
		if got := serverNameFrom(in); got != want {
			t.Errorf("serverNameFrom(%q) = %q, want %q", in, got, want)
		}
	}
}
