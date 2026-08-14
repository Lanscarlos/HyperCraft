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

func (e *testEnv) network() networkResponse {
	e.t.Helper()
	resp := e.do(http.MethodGet, "/api/network", nil)
	var out networkResponse
	decodeBody(e.t, resp, &out)
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("GET network: %d", resp.StatusCode)
	}
	return out
}

func (e *testEnv) link(path string, proxy, server instance.Status) networkLinkResponse {
	e.t.Helper()
	resp := e.do(http.MethodPost, "/api/network/"+path,
		networkLinkRequest{ProxyID: proxy.ID, ServerID: server.ID})
	var out networkLinkResponse
	decodeBody(e.t, resp, &out)
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("POST network/%s: %d", path, resp.StatusCode)
	}
	return out
}

// backend seeds a server instance that says where it listens and what it runs,
// which is all the linking code reads about it.
func (e *testEnv) backend(name, port string, paper bool) instance.Status {
	e.t.Helper()
	created := e.createInstance(name)
	seed(e.t, created, "server.properties", "server-ip=\nserver-port="+port+"\n")
	if paper {
		seed(e.t, created, pathPaperGlobal, "proxies:\n  velocity:\n    enabled: false\n")
	}
	return created
}

func readFile(t *testing.T, inst instance.Status, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(inst.Directory, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(raw)
}

// The whole gesture: one call, and both ends are configured.
func TestNetworkLinkWiresBothEnds(t *testing.T) {
	env := newTestEnv(t, withConfigHistory)
	env.login()
	proxy := env.createProxy("大厅代理")
	server := env.backend("survival", "25566", true)

	out := env.link("link", proxy, server)
	if len(out.Links) != 1 {
		t.Fatalf("links = %#v", out.Links)
	}
	link := out.Links[0]
	if link.Status != linkOK {
		t.Fatalf("status = %q, issues = %#v", link.Status, link.Issues)
	}
	if link.ServerID != server.ID || link.ProxyID != proxy.ID {
		t.Errorf("link points somewhere else: %#v", link)
	}
	if len(out.Notes) == 0 {
		t.Error("the link was made without saying what it changed")
	}

	toml := readFile(t, proxy, velocitycfg.FileName)
	if !strings.Contains(toml, `= "127.0.0.1:25566"`) {
		t.Errorf("velocity.toml has no sub-server:\n%s", toml)
	}
	// Nothing to fall back to means everyone is kicked, so the first server
	// linked becomes the landing spot.
	if !strings.Contains(toml, "try = [") {
		t.Errorf("try list was not set:\n%s", toml)
	}
	if !strings.Contains(toml, `player-info-forwarding-mode = "MODERN"`) {
		t.Errorf("forwarding mode was not set:\n%s", toml)
	}

	secret := strings.TrimSpace(readFile(t, proxy, velocitycfg.DefaultSecretFile))
	if len(secret) < 16 {
		t.Fatalf("forwarding secret = %q", secret)
	}

	props := readFile(t, server, "server.properties")
	if !strings.Contains(props, "online-mode=false") {
		t.Errorf("the backend still authenticates:\n%s", props)
	}
	paper := readFile(t, server, pathPaperGlobal)
	if !strings.Contains(paper, "enabled: true") || !strings.Contains(paper, secret) {
		t.Errorf("paper-global.yml was not wired:\n%s", paper)
	}

	// Both sides are configuration changes, so both sides get a snapshot.
	waitFor(t, func() bool {
		return len(env.configHistory(proxy.ID).Timeline) > 0 &&
			len(env.configHistory(server.ID).Timeline) > 0
	})
}

// A Spigot backend cannot speak modern forwarding, so the network is built on
// the one it can speak instead of on one that silently rejects every player.
func TestNetworkLinkFallsBackToLegacyForSpigot(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	proxy := env.createProxy("代理")
	server := env.backend("spigot", "25566", false)
	seed(t, server, "spigot.yml", "settings:\n  bungeecord: false\n")

	env.link("link", proxy, server)

	if got := readFile(t, proxy, velocitycfg.FileName); !strings.Contains(got, `player-info-forwarding-mode = "LEGACY"`) {
		t.Errorf("forwarding mode:\n%s", got)
	}
	if got := readFile(t, server, "spigot.yml"); !strings.Contains(got, "bungeecord: true") {
		t.Errorf("spigot.yml was not wired:\n%s", got)
	}
	if out := env.network(); out.Links[0].Status != linkOK {
		t.Errorf("link is not healthy: %#v", out.Links[0])
	}
}

// A proxy that already chose modern keeps it — and refuses a backend that
// cannot do it, rather than wiring up something that fails at login time.
func TestNetworkLinkRefusesModernOnNonPaper(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	proxy := env.createProxy("代理")
	seed(t, proxy, velocitycfg.FileName, "player-info-forwarding-mode = \"modern\"\n")
	server := env.backend("spigot", "25566", false)

	resp := env.do(http.MethodPost, "/api/network/link",
		networkLinkRequest{ProxyID: proxy.ID, ServerID: server.ID})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(server.Directory, "spigot.yml")); !os.IsNotExist(err) {
		t.Error("a refused link still touched the backend")
	}
}

// The point of reading links out of the files: somebody who wired this up by
// hand months ago has a network, and the panel has to show it — including the
// half of it they forgot.
func TestNetworkRecognisesHandWiredLinks(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	proxy := env.createProxy("代理")
	server := env.backend("survival", "25566", true)

	// Written by hand, with a different spelling of the same address and
	// nothing done on the backend at all.
	seed(t, proxy, velocitycfg.FileName, strings.Join([]string{
		`player-info-forwarding-mode = "modern"`,
		"[servers]",
		`lobby = "localhost:25566"`,
		`try = ["lobby"]`,
	}, "\n")+"\n")

	out := env.network()
	if len(out.Links) != 1 {
		t.Fatalf("links = %#v", out.Links)
	}
	link := out.Links[0]
	if link.ServerID != server.ID || link.Name != "lobby" || !link.Try {
		t.Fatalf("link = %#v", link)
	}
	if link.Status != linkBroken {
		t.Fatalf("a half-wired link should be broken, got %q: %#v", link.Status, link.Issues)
	}
	joined := strings.Join(link.Issues, " / ")
	for _, want := range []string{"正版验证", "转发密钥"} {
		if !strings.Contains(joined, want) {
			t.Errorf("issues did not mention %s: %s", want, joined)
		}
	}

	// And 修复 finishes the job the operator started.
	fixed := env.link("repair", proxy, server)
	if fixed.Links[0].Status != linkOK {
		t.Fatalf("repair left it %q: %#v", fixed.Links[0].Status, fixed.Links[0].Issues)
	}
	// Repairing does not rename the sub-server: /server lobby is in every warp
	// sign and portal plugin on the network.
	if fixed.Links[0].Name != "lobby" {
		t.Errorf("name = %q, want the one already in the file", fixed.Links[0].Name)
	}
	if got := strings.Count(readFile(t, proxy, velocitycfg.FileName), "25566"); got != 1 {
		t.Errorf("the sub-server was added a second time (%d)", got)
	}
}

// A sub-server pointing somewhere this panel does not manage is a real setup —
// another machine — and it stays visible without pretending to be a link.
func TestNetworkKeepsForeignSubServersVisible(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	proxy := env.createProxy("代理")
	seed(t, proxy, velocitycfg.FileName, "[servers]\nfar = \"10.0.0.9:25566\"\n")

	out := env.network()
	if len(out.Proxies) != 1 || len(out.Proxies[0].Entries) != 1 {
		t.Fatalf("proxies = %#v", out.Proxies)
	}
	if entry := out.Proxies[0].Entries[0]; entry.InstanceID != "" {
		t.Errorf("a foreign address was matched to %q", entry.InstanceID)
	}
	if len(out.Links) != 0 {
		t.Errorf("links = %#v", out.Links)
	}
}

func TestNetworkUnlinkPutsTheServerBack(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	proxy := env.createProxy("代理")
	server := env.backend("survival", "25566", true)

	env.link("link", proxy, server)
	out := env.link("unlink", proxy, server)
	if len(out.Links) != 0 {
		t.Fatalf("links = %#v", out.Links)
	}

	toml := readFile(t, proxy, velocitycfg.FileName)
	if strings.Contains(toml, "25566") {
		t.Errorf("the sub-server is still listed:\n%s", toml)
	}
	// Standing on its own again, so it has to check who is joining it.
	if got := readFile(t, server, "server.properties"); !strings.Contains(got, "online-mode=true") {
		t.Errorf("online-mode was not restored:\n%s", got)
	}
	if got := readFile(t, server, pathPaperGlobal); !strings.Contains(got, "enabled: false") {
		t.Errorf("velocity forwarding was left on:\n%s", got)
	}

	resp := env.do(http.MethodPost, "/api/network/unlink",
		networkLinkRequest{ProxyID: proxy.ID, ServerID: server.ID})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unlinking twice: expected 400, got %d", resp.StatusCode)
	}
}

// Two servers, one proxy: the second one does not steal the landing spot, and
// both names stay distinct.
func TestNetworkLinkKeepsTheFirstServerAsTheLanding(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	proxy := env.createProxy("代理")
	first := env.backend("lobby", "25566", true)
	second := env.backend("lobby", "25567", true)

	env.link("link", proxy, first)
	out := env.link("link", proxy, second)

	if len(out.Links) != 2 {
		t.Fatalf("links = %#v", out.Links)
	}
	tries := 0
	names := map[string]bool{}
	for _, link := range out.Links {
		if link.Try {
			tries++
		}
		if names[link.Name] {
			t.Fatalf("two sub-servers share the name %q", link.Name)
		}
		names[link.Name] = true
	}
	if tries != 1 {
		t.Errorf("%d servers are in the try list, want 1", tries)
	}
}

func TestNetworkLinkRefusesTheWrongKinds(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	proxy := env.createProxy("代理")
	server := env.backend("survival", "25566", true)

	cases := map[string]networkLinkRequest{
		"a server on the left": {ProxyID: server.ID, ServerID: server.ID},
		"a proxy on the right": {ProxyID: proxy.ID, ServerID: proxy.ID},
	}
	for name, req := range cases {
		resp := env.do(http.MethodPost, "/api/network/link", req)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", name, resp.StatusCode)
		}
	}

	resp := env.do(http.MethodPost, "/api/network/link",
		networkLinkRequest{ProxyID: "nope", ServerID: server.ID})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown proxy: expected 404, got %d", resp.StatusCode)
	}
}

func TestSameBackendMatchesEverySpellingOfThisMachine(t *testing.T) {
	same := []struct{ address, backend string }{
		{"127.0.0.1:25566", "127.0.0.1:25566"},
		{"localhost:25566", "127.0.0.1:25566"},
		{"0.0.0.0:25566", "127.0.0.1:25566"},
		{"[::1]:25566", "127.0.0.1:25566"},
		{"mc.example.com:25566", "MC.EXAMPLE.COM:25566"},
	}
	for _, test := range same {
		if !sameBackend(test.address, test.backend) {
			t.Errorf("sameBackend(%q, %q) = false", test.address, test.backend)
		}
	}

	different := []struct{ address, backend string }{
		{"127.0.0.1:25567", "127.0.0.1:25566"},
		{"10.0.0.9:25566", "127.0.0.1:25566"},
		{"127.0.0.1", "127.0.0.1:25566"},
	}
	for _, test := range different {
		if sameBackend(test.address, test.backend) {
			t.Errorf("sameBackend(%q, %q) = true", test.address, test.backend)
		}
	}
}

// Two instances on one address is the case the [servers] table cannot express:
// it names an address, so a link to either of them reads as a link to
// whichever one the panel saw first. Refusing is the only honest answer, and
// it has to happen before either backend is touched.
func TestNetworkLinkRefusesAnAmbiguousAddress(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	proxy := env.createProxy("代理")
	first := env.backend("生存", "25566", true)
	second := env.backend("创造", "25566", true)

	for _, server := range []instance.Status{first, second} {
		resp := env.do(http.MethodPost, "/api/network/link",
			networkLinkRequest{ProxyID: proxy.ID, ServerID: server.ID})
		var body struct {
			Error string `json:"error"`
		}
		decodeBody(t, resp, &body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", server.Name, resp.StatusCode)
		}
		if !strings.Contains(body.Error, "25566") {
			t.Errorf("%s: the error does not say what clashes: %s", server.Name, body.Error)
		}
	}

	// A refused link leaves both backends exactly as they were — the failure
	// mode this replaces silently disabled the second server's online-mode and
	// then never listed it.
	for _, server := range []instance.Status{first, second} {
		if _, err := os.Stat(filepath.Join(server.Directory, pathSpigot)); !os.IsNotExist(err) {
			t.Errorf("%s was edited by a refused link", server.Name)
		}
	}
}

// A hand-wired network where the two ends disagree about whether the proxy
// authenticates. Nobody is kicked — every player just gets a UUID minted the
// wrong way, which in game is indistinguishable from the world losing their
// inventory — so it has to be said here rather than found there.
func TestNetworkFlagsVelocityOnlineModeMismatch(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	proxy := env.createProxy("代理")
	server := env.backend("survival", "25566", true)

	seed(t, proxy, velocitycfg.FileName, strings.Join([]string{
		`online-mode = true`,
		`player-info-forwarding-mode = "modern"`,
		"[servers]",
		`lobby = "127.0.0.1:25566"`,
		`try = ["lobby"]`,
	}, "\n")+"\n")
	seed(t, proxy, velocitycfg.DefaultSecretFile, "sekrit")
	seed(t, server, "server.properties", "server-port=25566\nonline-mode=false\n")
	seed(t, server, pathPaperGlobal,
		"proxies:\n  velocity:\n    enabled: true\n    online-mode: false\n    secret: sekrit\n")

	link := env.network().Links[0]
	if link.Status != linkBroken {
		t.Fatalf("status = %q, issues = %#v", link.Status, link.Issues)
	}
	if !strings.Contains(strings.Join(link.Issues, " / "), "online-mode") {
		t.Errorf("issues did not name the key: %#v", link.Issues)
	}

	if fixed := env.link("repair", proxy, server); fixed.Links[0].Status != linkOK {
		t.Fatalf("repair left it %q: %#v", fixed.Links[0].Status, fixed.Links[0].Issues)
	}
}

// modern forwarding without a secret file is not the sub-server's fault and is
// not survivable: Velocity refuses to start at all.
func TestNetworkFlagsAMissingProxySecret(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	proxy := env.createProxy("代理")
	server := env.backend("survival", "25566", true)

	seed(t, proxy, velocitycfg.FileName, strings.Join([]string{
		`player-info-forwarding-mode = "modern"`,
		"[servers]",
		`lobby = "127.0.0.1:25566"`,
		`try = ["lobby"]`,
	}, "\n")+"\n")
	seed(t, server, "server.properties", "server-port=25566\nonline-mode=false\n")
	seed(t, server, pathPaperGlobal,
		"proxies:\n  velocity:\n    enabled: true\n    online-mode: true\n    secret: sekrit\n")

	link := env.network().Links[0]
	if link.Status != linkBroken {
		t.Fatalf("status = %q, issues = %#v", link.Status, link.Issues)
	}
	if !strings.Contains(strings.Join(link.Issues, " / "), "代理端还没有转发密钥") {
		t.Errorf("the missing secret was blamed on the wrong end: %#v", link.Issues)
	}
}

// Disconnecting the lobby must not take the rest of the network with it: a
// proxy whose try list is empty kicks every player who connects, including the
// ones on the servers that are still linked.
func TestNetworkUnlinkKeepsALandingSpot(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	proxy := env.createProxy("代理")
	first := env.backend("lobby", "25566", true)
	second := env.backend("survival", "25567", true)

	env.link("link", proxy, first)
	env.link("link", proxy, second)
	out := env.link("unlink", proxy, first)

	if len(out.Links) != 1 {
		t.Fatalf("links = %#v", out.Links)
	}
	if !out.Links[0].Try {
		t.Errorf("nothing is left to land on: %#v", out.Links[0])
	}
	if out.Links[0].Status != linkOK {
		t.Errorf("the surviving link is %q: %#v", out.Links[0].Status, out.Links[0].Issues)
	}
	// A changed landing spot is a decision, so it is reported rather than made
	// quietly.
	if !strings.Contains(strings.Join(out.Notes, " / "), "落点") {
		t.Errorf("the new landing spot was not mentioned: %#v", out.Notes)
	}
}

// The last server leaving takes the try list with it — there is nothing to
// promote, and inventing an entry would name a server that no longer exists.
func TestNetworkUnlinkOfTheLastServerEmptiesTry(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	proxy := env.createProxy("代理")
	server := env.backend("lobby", "25566", true)

	env.link("link", proxy, server)
	env.link("unlink", proxy, server)

	if got := readFile(t, proxy, velocitycfg.FileName); !strings.Contains(got, "try = []") {
		t.Errorf("try list:\n%s", got)
	}
}

// An empty landing spot is exactly the half-wired state 修复 exists for, so it
// is reported against the links behind it and repairing one restores it.
func TestNetworkRepairRestoresTheLandingSpot(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	proxy := env.createProxy("代理")
	server := env.backend("survival", "25566", true)

	env.link("link", proxy, server)
	// Emptied by hand — the try list is an ordinary field of the 代理配置 page.
	toml := readFile(t, proxy, velocitycfg.FileName)
	seed(t, proxy, velocitycfg.FileName,
		strings.ReplaceAll(toml, `try = ["survival"]`, "try = []"))

	link := env.network().Links[0]
	if link.Status != linkBroken {
		t.Fatalf("status = %q, issues = %#v", link.Status, link.Issues)
	}
	if !strings.Contains(strings.Join(link.Issues, " / "), "登录顺序") {
		t.Errorf("issues did not mention the try list: %#v", link.Issues)
	}

	fixed := env.link("repair", proxy, server)
	if fixed.Links[0].Status != linkOK || !fixed.Links[0].Try {
		t.Fatalf("repair left it %q: %#v", fixed.Links[0].Status, fixed.Links[0].Issues)
	}
}
