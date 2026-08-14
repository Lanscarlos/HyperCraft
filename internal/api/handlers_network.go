package api

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/confighist"
	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/mcprops"
	"github.com/lanscarlos/hypercraft/internal/mcyaml"
	"github.com/lanscarlos/hypercraft/internal/velocitycfg"
)

// Putting a server behind a proxy is one decision and six edits, spread over
// four files in two directories, and getting any one of them wrong fails in a
// way that reads like something else: forget online-mode and everybody is
// kicked as an impostor, forget the secret and the proxy says the backend
// refused it, forget the [servers] entry and /server says the server does not
// exist. This is that decision as one act — draw a line, and the panel makes
// the six edits.
//
// The other half is reading the same thing back. An operator who wired this up
// by hand months ago must not be told they have no network: the links here are
// derived from the files themselves — the proxy's [servers] table matched
// against what each server's own server.properties says it listens on — so a
// hand-edited setup shows up as exactly the same picture, and the parts of it
// that are half-done show up as the problems they are.

// linkStatus grades one link. The wiring is either complete, complete but with
// something worth knowing, or broken in a way that will greet a player as an
// error message.
const (
	linkOK     = "ok"
	linkWarn   = "warn"
	linkBroken = "broken"
)

// The forwarding modes velocity.toml accepts, lower-cased the way the panel
// spells them.
const (
	forwardNone        = "none"
	forwardLegacy      = "legacy"
	forwardBungeeGuard = "bungeeguard"
	forwardModern      = "modern"
)

type networkProxyEntry struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	// InstanceID is the server this entry points at, empty when it points
	// somewhere this panel does not manage. Those are shown too: a proxy with
	// three sub-servers, one of them on another machine, is a real setup and
	// hiding the third one would misrepresent it.
	InstanceID string `json:"instanceId,omitempty"`
	Try        bool   `json:"try"`
}

type networkProxy struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	State instance.State `json:"state"`
	// ConfigExists is false before the proxy's first boot, when what is shown
	// is Velocity's own defaults.
	ConfigExists bool                `json:"configExists"`
	Bind         string              `json:"bind"`
	Forwarding   string              `json:"forwarding"`
	HasSecret    bool                `json:"hasSecret"`
	OnlineMode   bool                `json:"onlineMode"`
	Entries      []networkProxyEntry `json:"entries"`
}

type networkServer struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	State instance.State `json:"state"`
	// Address is what the proxy would have to be told: host:port, read from the
	// server's own server.properties.
	Address    string `json:"address"`
	OnlineMode bool   `json:"onlineMode"`
	// Paper is true when this server can do Velocity's modern forwarding at
	// all. Spigot cannot — it only speaks the BungeeCord protocol — and telling
	// somebody to use modern forwarding on Spigot is telling them to make their
	// server unjoinable.
	Paper bool `json:"paper"`
	// PaperLayout is which of Paper's two config layouts this server reads:
	// "modern" for config/paper-global.yml, "legacy" for paper.yml.
	PaperLayout        string `json:"paperLayout,omitempty"`
	VelocityForwarding bool   `json:"velocityForwarding"`
	BungeeForwarding   bool   `json:"bungeeForwarding"`
}

type networkLink struct {
	ProxyID  string   `json:"proxyId"`
	ServerID string   `json:"serverId"`
	Name     string   `json:"name"`
	Address  string   `json:"address"`
	Try      bool     `json:"try"`
	Status   string   `json:"status"`
	Issues   []string `json:"issues"`
}

type networkResponse struct {
	Proxies []networkProxy  `json:"proxies"`
	Servers []networkServer `json:"servers"`
	Links   []networkLink   `json:"links"`
}

// backendState is everything the linking code needs to know about one server,
// read once from its files rather than four times from four call sites.
type backendState struct {
	address    string
	onlineMode bool
	paper      bool
	// legacyPaper is true for the pre-1.19 layout, where the same three
	// settings live in paper.yml instead of config/paper-global.yml.
	legacyPaper        bool
	velocityForwarding bool
	velocitySecret     string
	// velocityOnline is what the backend believes about the proxy in front of
	// it. Paper mints the player's UUID from it, so a backend that disagrees
	// with the proxy hands everyone a different UUID than the one they own —
	// which reads in game as every player's inventory and home being gone.
	velocityOnline   bool
	bungeeForwarding bool
}

func (s *Server) readBackend(inst *instance.Instance) backendState {
	cfg := inst.Config()
	state := backendState{
		address:    backendAddress(cfg.Directory),
		onlineMode: true,
	}

	if file, err := mcprops.Load(filepath.Join(cfg.Directory, "server.properties")); err == nil {
		if value, ok := file.Get("online-mode"); ok {
			state.onlineMode = !strings.EqualFold(strings.TrimSpace(value), "false")
		}
	}

	browser := s.browserFor(inst)
	if spigot, _, err := s.loadServerConfig(inst, pathSpigot); err == nil {
		state.bungeeForwarding = spigot.Bool("settings.bungeecord", false)
	}

	switch {
	case !fileMissing(browser, pathPaperGlobal):
		state.paper = true
	case !fileMissing(browser, pathPaperLegacy):
		state.paper, state.legacyPaper = true, true
	default:
		// A server that has never booted has neither file. What it will write
		// on its first boot is decided by the jar, which is the only evidence
		// there is at this point.
		state.paper = looksLikePaper(cfg.Jar)
	}

	// false is Paper's own default for both online-mode keys, so an absent key
	// reads as the behaviour the server actually has.
	if state.legacyPaper {
		if paper, _, err := s.loadServerConfig(inst, pathPaperLegacy); err == nil {
			state.velocityForwarding = paper.Bool("settings.velocity-support.enabled", false)
			state.velocitySecret, _ = paper.Get("settings.velocity-support.secret")
			state.velocityOnline = paper.Bool("settings.velocity-support.online-mode", false)
		}
		return state
	}
	if paper, _, err := s.loadServerConfig(inst, pathPaperGlobal); err == nil {
		state.velocityForwarding = paper.Bool("proxies.velocity.enabled", false)
		state.velocitySecret, _ = paper.Get("proxies.velocity.secret")
		state.velocityOnline = paper.Bool("proxies.velocity.online-mode", false)
	}
	return state
}

// looksLikePaper reads the jar's name. It is a guess, and it is only ever used
// to pick a default for a server with no config files yet — everything after
// the first boot reads the files instead.
func looksLikePaper(jar string) bool {
	name := strings.ToLower(filepath.Base(jar))
	for _, fork := range []string{"paper", "purpur", "folia", "pufferfish", "airplane", "leaf", "leaves"} {
		if strings.Contains(name, fork) {
			return true
		}
	}
	return false
}

func (state backendState) paperLayout() string {
	switch {
	case !state.paper:
		return ""
	case state.legacyPaper:
		return "legacy"
	default:
		return "modern"
	}
}

// ------------------------------------------------------------------- reading

func (s *Server) handleNetwork(w http.ResponseWriter, r *http.Request) {
	out, err := s.networkResponse()
	if err != nil {
		s.writeFileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) networkResponse() (networkResponse, error) {
	out := networkResponse{
		Proxies: []networkProxy{},
		Servers: []networkServer{},
		Links:   []networkLink{},
	}

	var proxies, servers []*instance.Instance
	for _, inst := range s.mgr.List() {
		if inst.Config().IsProxy() {
			proxies = append(proxies, inst)
			continue
		}
		servers = append(servers, inst)
	}

	// Which instances claim each address. Two servers cannot both listen on one
	// port, so more than one name here is a misconfiguration — and one the
	// panel has to say out loud, because the [servers] table can only ever name
	// the address, which means a link to either of them looks like a link to
	// whichever this loop happened to see first.
	claiming := make(map[string][]string, len(servers))
	backends := make(map[string]backendState, len(servers))
	for _, inst := range servers {
		cfg := inst.Config()
		state := s.readBackend(inst)
		backends[cfg.ID] = state
		claiming[state.address] = append(claiming[state.address], cfg.Name)
		out.Servers = append(out.Servers, networkServer{
			ID:                 cfg.ID,
			Name:               cfg.Name,
			State:              inst.State(),
			Address:            state.address,
			OnlineMode:         state.onlineMode,
			Paper:              state.paper,
			PaperLayout:        state.paperLayout(),
			VelocityForwarding: state.velocityForwarding,
			BungeeForwarding:   state.bungeeForwarding,
		})
	}

	for _, inst := range proxies {
		cfg := inst.Config()
		file, exists, err := s.loadVelocity(inst)
		if err != nil {
			return networkResponse{}, err
		}

		mode := forwardingMode(file)
		secret := strings.TrimSpace(s.readForwardingSecret(inst, file).Value)
		bind, _ := file.Value("", "bind")
		online := true
		if value, ok := file.Value("", "online-mode"); ok {
			online = !strings.EqualFold(strings.TrimSpace(value), "false")
		}

		try, _ := file.List(velocityServersTable, velocityTryKey)
		side := proxyState{
			mode:       mode,
			secret:     secret,
			onlineMode: online,
			// An arriving player is routed by the try list, or by a forced host
			// when they came in on one of its names. Neither means the proxy
			// kicks everybody the moment it has somewhere to send them.
			landing: len(try) > 0 || len(file.ListEntries(velocityForcedHostsTable)) > 0,
		}
		proxy := networkProxy{
			ID:           cfg.ID,
			Name:         cfg.Name,
			State:        inst.State(),
			ConfigExists: exists,
			Bind:         bind,
			Forwarding:   mode,
			HasSecret:    secret != "",
			OnlineMode:   online,
			Entries:      []networkProxyEntry{},
		}

		for _, entry := range file.Entries(velocityServersTable, velocityTryKey) {
			row := networkProxyEntry{
				Name:    entry.Key,
				Address: entry.Value,
				Try:     containsFold(try, entry.Key),
			}
			// Which instance this points at, if any. This is the whole trick
			// that makes a hand-wired setup readable: nothing is recorded
			// anywhere about links, they are recognised from the addresses.
			for _, backend := range servers {
				if sameBackend(entry.Value, backends[backend.Config().ID].address) {
					row.InstanceID = backend.Config().ID
					break
				}
			}
			proxy.Entries = append(proxy.Entries, row)

			if row.InstanceID == "" {
				continue
			}
			state := backends[row.InstanceID]
			issues := linkIssues(side, state, claiming[state.address])
			out.Links = append(out.Links, networkLink{
				ProxyID:  cfg.ID,
				ServerID: row.InstanceID,
				Name:     row.Name,
				Address:  row.Address,
				Try:      row.Try,
				Status:   statusOf(issues),
				Issues:   issueTexts(issues),
			})
		}
		out.Proxies = append(out.Proxies, proxy)
	}
	return out, nil
}

// proxyState is the proxy half of a link, read once per proxy rather than once
// per sub-server.
type proxyState struct {
	mode       string
	secret     string
	onlineMode bool
	// landing is false when nothing routes an arriving player anywhere: an
	// empty try list and no forced hosts is a proxy that kicks everybody the
	// moment they connect, no matter how well the links behind it are wired.
	landing bool
}

// linkIssue is one thing wrong with a link, and how wrong. fatal is the
// difference between a player being kicked and an operator being told
// something they may already know — grading by the text, which is what this
// used to do, breaks the moment a sentence is reworded.
type linkIssue struct {
	text  string
	fatal bool
}

func fatal(text string) linkIssue { return linkIssue{text: text, fatal: true} }
func warn(text string) linkIssue  { return linkIssue{text: text} }

// linkIssues is the difference between "these two files mention each other" and
// "a player can walk from one to the other". Every string here is something
// that will otherwise be discovered by a player being kicked.
//
// claiming is every instance sitting on this backend's address, which is
// normally just the one.
func linkIssues(proxy proxyState, state backendState, claiming []string) []linkIssue {
	issues := []linkIssue{}

	// Two instances on one address is not a link problem, it is the reason
	// this link cannot be trusted to mean what it says: the [servers] table
	// names an address, so the panel — and Velocity — can only reach whichever
	// of them managed to bind the port.
	if len(claiming) > 1 {
		issues = append(issues, fatal("有 "+strconv.Itoa(len(claiming))+" 个实例都在 "+state.address+
			"（"+strings.Join(claiming, "、")+"）。两台服务器不可能同时监听一个端口，"+
			"代理端也分不清这条线连的是哪一台 —— 先去「服务器配置」给其中一个改端口"))
	}

	if !proxy.landing {
		issues = append(issues, fatal("代理端的登录顺序（try）是空的，也没有域名映射，玩家一连上来就会被踢"))
	}

	if state.onlineMode {
		issues = append(issues, fatal("子服自己还开着正版验证。验证要交给代理端做，否则玩家会被子服判定为盗版号踢出"))
	}

	secret := strings.TrimSpace(proxy.secret)
	switch proxy.mode {
	case forwardModern:
		if !state.paper {
			issues = append(issues, fatal("代理端用的是 modern 转发，但这个子服不像 Paper 系核心 —— Spigot 只支持 legacy 转发"))
			break
		}
		if !state.velocityForwarding {
			issues = append(issues, fatal("子服没有打开 Velocity 转发"))
		}
		// The proxy's own secret first: an empty one is not the sub-server's
		// fault, and Velocity refuses to start at all without it — so blaming
		// the sub-server sends the operator to edit the wrong file.
		if secret == "" {
			issues = append(issues, fatal("代理端还没有转发密钥。modern 转发少了密钥文件，Velocity 会直接拒绝启动"))
		} else if strings.TrimSpace(state.velocitySecret) == "" {
			issues = append(issues, fatal("子服没有填转发密钥"))
		} else if strings.TrimSpace(state.velocitySecret) != secret {
			issues = append(issues, fatal("子服的转发密钥和代理端的对不上"))
		}
		if state.velocityOnline != proxy.onlineMode {
			// Both sides "work" — nobody is kicked — and every player gets a
			// UUID minted the wrong way, which in game looks like the world
			// eating everyone's inventory and home.
			issues = append(issues, fatal(velocityOnlineIssue(proxy.onlineMode, state.legacyPaper)))
		}
		if state.bungeeForwarding {
			issues = append(issues, fatal("子服的 spigot.yml 还开着 bungeecord，和 modern 转发冲突"))
		}
	case forwardLegacy, forwardBungeeGuard:
		if !state.bungeeForwarding {
			issues = append(issues, fatal("子服的 spigot.yml 没有打开 bungeecord"))
		}
		if state.velocityForwarding {
			issues = append(issues, fatal("子服还开着 Velocity 转发，和传统转发冲突"))
		}
		if proxy.mode == forwardBungeeGuard {
			// BungeeGuard's token lives in a plugin's own config, which the
			// panel does not read — so this is the one part of the wiring it
			// can only point at, not check.
			issues = append(issues, warn("bungeeguard 转发还要把代理端的转发密钥填进子服 BungeeGuard 插件的配置里，这一步面板代劳不了"))
		}
	default:
		issues = append(issues, fatal("代理端没有开玩家信息转发，子服看到的会是代理端的 IP 和离线 UUID"))
	}

	if !proxy.onlineMode && !state.onlineMode {
		// Not a fault, but the thing people forget they chose.
		issues = append(issues, warn("整条链路都是离线模式，任何人都能用任意 ID 进服"))
	}
	return issues
}

func velocityOnlineIssue(proxyOnline, legacyPaper bool) string {
	key := "paper-global.yml 的 proxies.velocity.online-mode"
	if legacyPaper {
		key = "paper.yml 的 settings.velocity-support.online-mode"
	}
	if proxyOnline {
		return "代理端开着正版验证，但 " + key + " 是关的 —— 玩家拿到的会是离线 UUID，进服后存档对不上"
	}
	return "代理端没开正版验证，但 " + key + " 是开的 —— 子服会按正版 UUID 认人，和代理端发过来的对不上"
}

// statusOf grades the issues. Anything that stops a player joining, or hands
// them the wrong identity, is broken; the rest is merely worth saying.
func statusOf(issues []linkIssue) string {
	switch {
	case len(issues) == 0:
		return linkOK
	case slices.ContainsFunc(issues, func(issue linkIssue) bool { return issue.fatal }):
		return linkBroken
	default:
		return linkWarn
	}
}

func issueTexts(issues []linkIssue) []string {
	out := make([]string, 0, len(issues))
	for _, issue := range issues {
		out = append(out, issue.text)
	}
	return out
}

func forwardingMode(file *velocitycfg.File) string {
	value, ok := file.Value("", "player-info-forwarding-mode")
	if !ok {
		return forwardNone
	}
	return strings.ToLower(strings.TrimSpace(value))
}

// sameBackend decides whether a [servers] address points at a given instance.
//
// The port has to match exactly; the host is compared with every spelling of
// "this machine" treated as one, because that is what a panel-managed backend
// is: server.properties normally leaves server-ip blank, which means every
// address, and velocity.toml can just as reasonably say localhost as 127.0.0.1.
func sameBackend(address, backend string) bool {
	addressHost, addressPort, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return false
	}
	backendHost, backendPort, err := net.SplitHostPort(strings.TrimSpace(backend))
	if err != nil {
		return false
	}
	if addressPort != backendPort {
		return false
	}
	if localHost(addressHost) && localHost(backendHost) {
		return true
	}
	return strings.EqualFold(addressHost, backendHost)
}

func localHost(host string) bool {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]")) {
	case "", "0.0.0.0", "127.0.0.1", "localhost", "::", "::1":
		return true
	default:
		return false
	}
}

func containsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(value, want) {
			return true
		}
	}
	return false
}

// ------------------------------------------------------------------- writing

type networkLinkRequest struct {
	ProxyID  string `json:"proxyId"`
	ServerID string `json:"serverId"`
	// Name is what players type after /server. Left empty, one is derived from
	// the instance's name.
	Name string `json:"name"`
}

// networkLinkResponse carries the new picture plus a plain account of what was
// changed. The whole point of the page is that one gesture edits four files, so
// it has to say which four.
type networkLinkResponse struct {
	networkResponse
	Notes []string `json:"notes"`
}

func (s *Server) networkPair(w http.ResponseWriter, r *http.Request) (*instance.Instance, *instance.Instance, networkLinkRequest, bool) {
	var req networkLinkRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return nil, nil, req, false
	}

	proxy, err := s.mgr.Get(strings.TrimSpace(req.ProxyID))
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到这个代理端")
		return nil, nil, req, false
	}
	if !proxy.Config().IsProxy() {
		writeError(w, http.StatusBadRequest, "连线的左边必须是代理端")
		return nil, nil, req, false
	}

	server, err := s.mgr.Get(strings.TrimSpace(req.ServerID))
	if err != nil {
		writeError(w, http.StatusNotFound, "找不到这个服务端")
		return nil, nil, req, false
	}
	if server.Config().IsProxy() {
		writeError(w, http.StatusBadRequest, "连线的右边必须是服务端")
		return nil, nil, req, false
	}
	return proxy, server, req, true
}

func (s *Server) handleNetworkLink(w http.ResponseWriter, r *http.Request) {
	proxy, server, req, ok := s.networkPair(w, r)
	if !ok {
		return
	}

	notes, err := s.wireLink(proxy, server, req.Name, actorOf(r))
	if err != nil {
		s.writeLinkError(w, err)
		return
	}
	s.log.Info("proxy link created", "proxy", proxy.Config().Name, "server", server.Config().Name)
	s.writeNetwork(w, notes)
}

// handleNetworkRepair re-applies the wiring for a link that already exists. It
// is the same act as drawing the line — which is why it is the same code — and
// it is what the 修复 button on a broken link does.
func (s *Server) handleNetworkRepair(w http.ResponseWriter, r *http.Request) {
	proxy, server, _, ok := s.networkPair(w, r)
	if !ok {
		return
	}

	notes, err := s.wireLink(proxy, server, "", actorOf(r))
	if err != nil {
		s.writeLinkError(w, err)
		return
	}
	s.log.Info("proxy link repaired", "proxy", proxy.Config().Name, "server", server.Config().Name)
	s.writeNetwork(w, notes)
}

func (s *Server) handleNetworkUnlink(w http.ResponseWriter, r *http.Request) {
	proxy, server, _, ok := s.networkPair(w, r)
	if !ok {
		return
	}

	notes, err := s.unwireLink(proxy, server, actorOf(r))
	if err != nil {
		s.writeLinkError(w, err)
		return
	}
	s.log.Info("proxy link removed", "proxy", proxy.Config().Name, "server", server.Config().Name)
	s.writeNetwork(w, notes)
}

func (s *Server) writeNetwork(w http.ResponseWriter, notes []string) {
	out, err := s.networkResponse()
	if err != nil {
		s.writeFileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, networkLinkResponse{networkResponse: out, Notes: notes})
}

// errLink marks the failures that are the operator's to fix — a name that
// clashes, a backend that cannot do what the proxy asks — as opposed to a disk
// that will not write.
var errLink = errors.New("link")

func (s *Server) writeLinkError(w http.ResponseWriter, err error) {
	if errors.Is(err, errLink) {
		writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), "link: "))
		return
	}
	s.writeFileError(w, err)
}

// wireLink is the whole act: the proxy learns where the server is, and the
// server learns to trust the proxy.
//
// It is deliberately idempotent. Running it on a link that already exists fixes
// whatever half of it drifted — which is what 修复 needs — and running it twice
// leaves one entry, not two.
func (s *Server) wireLink(proxy, server *instance.Instance, name, actor string) ([]string, error) {
	notes := []string{}

	file, _, err := s.loadVelocity(proxy)
	if err != nil {
		return nil, err
	}
	state := s.readBackend(server)

	// A [servers] entry names an address, nothing else. If a second instance is
	// sitting on the same one, writing this link would either silently adopt
	// the other one's entry or hand the proxy an address that reaches whichever
	// server won the port — so it is refused here rather than half-done and
	// drawn as a line that points at the wrong card. Two servers on one port do
	// not both start anyway; this is the moment that gets said out loud.
	if other, ok := s.addressClash(server, state.address); ok {
		return nil, errLinkf("%s 和 %s 都在 %s 上。两台服务器不可能同时监听一个端口，代理端也没法区分它们 —— 先去「服务器配置」给其中一个改端口再连线",
			server.Config().Name, other, state.address)
	}

	// Which forwarding this network runs on. A proxy that already made the
	// choice keeps it — changing a working network's forwarding mode because
	// somebody added a sixth server would take the other five offline.
	mode := forwardingMode(file)
	if mode == forwardNone || !slices.Contains([]string{forwardLegacy, forwardBungeeGuard, forwardModern}, mode) {
		if state.paper {
			mode = forwardModern
		} else {
			mode = forwardLegacy
		}
		file.SetString("", "player-info-forwarding-mode", strings.ToUpper(mode))
		notes = append(notes, "代理端的转发模式设成了 "+mode)
	}
	if mode == forwardModern && !state.paper {
		return nil, errLinkf("这条链路用的是 modern 转发，只有 Paper 系核心支持。%s 看起来不是 —— 先给它换个 Paper 核心，或者把代理端的转发模式改成 legacy", server.Config().Name)
	}

	secret := strings.TrimSpace(s.readForwardingSecret(proxy, file).Value)
	if mode == forwardModern || mode == forwardBungeeGuard {
		if secret == "" {
			secret = randomSecret()
			notes = append(notes, "代理端还没有转发密钥，已经生成了一个")
		}
	}

	// The [servers] entry. An address already in the table is the same link
	// being re-applied, so its name is kept: renaming it would break every
	// /server, warp sign and portal plugin that names it.
	entries := file.Entries(velocityServersTable, velocityTryKey)
	linkName := ""
	for _, entry := range entries {
		if sameBackend(entry.Value, state.address) {
			linkName = entry.Key
			break
		}
	}
	if linkName == "" {
		used := make(map[string]bool, len(entries))
		for _, entry := range entries {
			used[strings.ToLower(entry.Key)] = true
		}
		linkName = strings.TrimSpace(name)
		if linkName == "" {
			linkName = uniqueServerName(serverNameFrom(server.Config().Name), used)
		}
		if !serverNamePattern.MatchString(linkName) {
			return nil, errLinkf("子服名称 %s 不可用：只能用字母、数字、- . _，且以字母或数字开头", linkName)
		}
		if used[strings.ToLower(linkName)] {
			return nil, errLinkf("代理端已经有一个叫 %s 的子服了", linkName)
		}
		entries = append(entries, velocitycfg.Entry{Key: linkName, Value: state.address})
		file.SetEntries(velocityServersTable, entries, velocityTryKey)
		notes = append(notes, "代理端的子服列表里加了 "+linkName+" → "+state.address)
	}

	// A proxy whose try list is empty kicks everyone who connects, so the first
	// server linked becomes the lobby. Later ones do not: which server new
	// players land on is a decision, not a side effect. This sits outside the
	// branch above because an empty try list is exactly the kind of half-wired
	// state 修复 exists for — and a link that was made before this check
	// existed is the common way to arrive at it.
	if try, _ := file.List(velocityServersTable, velocityTryKey); len(try) == 0 {
		file.SetList(velocityServersTable, velocityTryKey, []string{linkName})
		notes = append(notes, linkName+" 是唯一/第一个子服，已设为玩家登录时的落点")
	}

	if err := s.saveVelocityFile(proxy, file, secret); err != nil {
		return nil, err
	}
	s.snapshotAfter(proxy, confighist.TriggerUser, actor, "连接子服 "+linkName)

	backendNotes, err := s.applyBackendForwarding(server, state, mode, secret, proxyOnlineMode(file))
	if err != nil {
		return nil, err
	}
	notes = append(notes, backendNotes...)
	s.snapshotAfter(server, confighist.TriggerUser, actor, "接入代理端 "+proxy.Config().Name)

	notes = append(notes, "两端都要重启才会生效")
	return notes, nil
}

// addressClash names another server instance listening where this one says it
// does, if there is one.
func (s *Server) addressClash(server *instance.Instance, address string) (string, bool) {
	for _, other := range s.mgr.List() {
		cfg := other.Config()
		if cfg.ID == server.Config().ID || cfg.IsProxy() {
			continue
		}
		if sameBackend(backendAddress(cfg.Directory), address) {
			return cfg.Name, true
		}
	}
	return "", false
}

// applyBackendForwarding teaches one server to accept players from a proxy.
func (s *Server) applyBackendForwarding(server *instance.Instance, state backendState, mode, secret string, proxyOnline bool) ([]string, error) {
	notes := []string{}

	if state.onlineMode {
		if err := s.setProperty(server, "online-mode", "false"); err != nil {
			return nil, err
		}
		notes = append(notes, "子服的正版验证关掉了 —— 验证在代理端做，子服再验一次会把所有人踢出去")
	}

	switch mode {
	case forwardModern:
		paperFile := pathPaperGlobal
		keys := map[string]string{
			"enabled": "proxies.velocity.enabled",
			"secret":  "proxies.velocity.secret",
			"online":  "proxies.velocity.online-mode",
		}
		if state.legacyPaper {
			paperFile = pathPaperLegacy
			keys = map[string]string{
				"enabled": "settings.velocity-support.enabled",
				"secret":  "settings.velocity-support.secret",
				"online":  "settings.velocity-support.online-mode",
			}
		}
		if err := s.editYAML(server, paperFile, func(file *mcyaml.File) error {
			if err := file.SetBool(keys["enabled"], true); err != nil {
				return err
			}
			if err := file.SetString(keys["secret"], secret); err != nil {
				return err
			}
			return file.SetBool(keys["online"], proxyOnline)
		}); err != nil {
			return nil, err
		}
		notes = append(notes, filepath.Base(paperFile)+" 里打开了 Velocity 转发并写入了密钥")

		// The two are mutually exclusive: Paper reads the BungeeCord handshake
		// and the Velocity one from the same packet.
		if state.bungeeForwarding {
			if err := s.editYAML(server, pathSpigot, func(file *mcyaml.File) error {
				return file.SetBool("settings.bungeecord", false)
			}); err != nil {
				return nil, err
			}
			notes = append(notes, "spigot.yml 里关掉了和它冲突的 bungeecord")
		}

	case forwardLegacy, forwardBungeeGuard:
		if !state.bungeeForwarding {
			if err := s.editYAML(server, pathSpigot, func(file *mcyaml.File) error {
				return file.SetBool("settings.bungeecord", true)
			}); err != nil {
				return nil, err
			}
			notes = append(notes, "spigot.yml 里打开了 bungeecord")
		}
		if state.velocityForwarding {
			path, key := pathPaperGlobal, "proxies.velocity.enabled"
			if state.legacyPaper {
				path, key = pathPaperLegacy, "settings.velocity-support.enabled"
			}
			if err := s.editYAML(server, path, func(file *mcyaml.File) error {
				return file.SetBool(key, false)
			}); err != nil {
				return nil, err
			}
			notes = append(notes, filepath.Base(path)+" 里关掉了和它冲突的 Velocity 转发")
		}
	}
	return notes, nil
}

// unwireLink takes the link apart from both ends.
//
// The backend is put back to standing on its own, which means online-mode back
// on. Leaving it off would leave a server that anybody can join as anybody —
// the proxy was the thing checking, and it is no longer in front.
func (s *Server) unwireLink(proxy, server *instance.Instance, actor string) ([]string, error) {
	notes := []string{}

	file, _, err := s.loadVelocity(proxy)
	if err != nil {
		return nil, err
	}
	state := s.readBackend(server)

	kept := make([]velocitycfg.Entry, 0)
	removed := []string{}
	for _, entry := range file.Entries(velocityServersTable, velocityTryKey) {
		if sameBackend(entry.Value, state.address) {
			removed = append(removed, entry.Key)
			continue
		}
		kept = append(kept, entry)
	}
	if len(removed) == 0 {
		return nil, errLinkf("%s 并没有连在 %s 上", server.Config().Name, proxy.Config().Name)
	}

	file.SetEntries(velocityServersTable, kept, velocityTryKey)
	promoted := ""
	if try, ok := file.List(velocityServersTable, velocityTryKey); ok {
		left := make([]string, 0, len(try))
		for _, name := range try {
			if !containsFold(removed, name) {
				left = append(left, name)
			}
		}
		// Taking the lobby out of a proxy that still has servers behind it
		// would leave every one of them unreachable: an empty try list kicks
		// each player as they connect. Disconnecting one server must not take
		// the rest of the network down with it, so whatever is left becomes
		// the landing spot — a decision worth saying out loud, which is why it
		// is a note rather than a quiet edit.
		// Compared before the promotion below: a one-name try list swapped for
		// another one is still a change, and length alone cannot see it.
		changed := len(left) != len(try)
		if len(left) == 0 && len(kept) > 0 {
			promoted = kept[0].Key
			left = append(left, promoted)
		}
		if changed {
			file.SetList(velocityServersTable, velocityTryKey, left)
		}
	}
	// Forced hosts pointing at a server that no longer exists stop Velocity
	// from starting at all, which is a worse outcome than the unlink itself.
	forced := file.ListEntries(velocityForcedHostsTable)
	trimmed := make([]velocitycfg.ListEntry, 0, len(forced))
	for _, entry := range forced {
		values := make([]string, 0, len(entry.Values))
		for _, value := range entry.Values {
			if !containsFold(removed, value) {
				values = append(values, value)
			}
		}
		if len(values) > 0 {
			trimmed = append(trimmed, velocitycfg.ListEntry{Key: entry.Key, Values: values})
		}
	}
	if len(trimmed) != len(forced) || !sameForced(forced, trimmed) {
		file.SetListEntries(velocityForcedHostsTable, trimmed)
		notes = append(notes, "顺手清掉了指向它的域名映射")
	}

	if err := s.saveVelocityFile(proxy, file, ""); err != nil {
		return nil, err
	}
	notes = append(notes, "代理端的子服列表里去掉了 "+strings.Join(removed, "、"))
	if promoted != "" {
		notes = append(notes, "它原本是玩家登录时的落点，落点改成了 "+promoted+" —— 落点空着的话代理端会把所有人踢出去")
	}
	s.snapshotAfter(proxy, confighist.TriggerUser, actor, "断开子服 "+strings.Join(removed, "、"))

	if state.velocityForwarding {
		path, key := pathPaperGlobal, "proxies.velocity.enabled"
		if state.legacyPaper {
			path, key = pathPaperLegacy, "settings.velocity-support.enabled"
		}
		if err := s.editYAML(server, path, func(file *mcyaml.File) error {
			return file.SetBool(key, false)
		}); err != nil {
			return nil, err
		}
		notes = append(notes, filepath.Base(path)+" 里关掉了 Velocity 转发")
	}
	if state.bungeeForwarding {
		if err := s.editYAML(server, pathSpigot, func(file *mcyaml.File) error {
			return file.SetBool("settings.bungeecord", false)
		}); err != nil {
			return nil, err
		}
		notes = append(notes, "spigot.yml 里关掉了 bungeecord")
	}
	if !state.onlineMode {
		if err := s.setProperty(server, "online-mode", "true"); err != nil {
			return nil, err
		}
		notes = append(notes, "子服的正版验证开回来了 —— 前面没有代理端挡着，关着的话谁都能顶号进来")
	}
	s.snapshotAfter(server, confighist.TriggerUser, actor, "脱离代理端 "+proxy.Config().Name)

	notes = append(notes, "两端都要重启才会生效")
	return notes, nil
}

func sameForced(a, b []velocitycfg.ListEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index].Key != b[index].Key || len(a[index].Values) != len(b[index].Values) {
			return false
		}
		for value := range a[index].Values {
			if a[index].Values[value] != b[index].Values[value] {
				return false
			}
		}
	}
	return true
}

func proxyOnlineMode(file *velocitycfg.File) bool {
	value, ok := file.Value("", "online-mode")
	if !ok {
		return true
	}
	return !strings.EqualFold(strings.TrimSpace(value), "false")
}

// saveVelocityFile writes velocity.toml, and the secret file with it when there
// is a secret to write. An empty secret means "leave whatever is there alone",
// the same way the config page's form does.
func (s *Server) saveVelocityFile(proxy *instance.Instance, file *velocitycfg.File, secret string) error {
	if err := s.writeInstanceFile(proxy, velocitycfg.FileName, file.Render()); err != nil {
		return err
	}
	if strings.TrimSpace(secret) == "" {
		return nil
	}
	return s.writeInstanceFile(proxy, secretFileName(file), secret)
}

// setProperty edits one key of a server.properties, leaving everything else —
// including keys this build has never heard of — exactly as it was.
func (s *Server) setProperty(server *instance.Instance, key, value string) error {
	path := s.propertiesPath(server.Config().Directory)
	file, err := mcprops.Load(path)
	if err != nil {
		return err
	}
	file.Set(key, value)
	return s.writeInstanceFile(server, "server.properties", string(file.Bytes()))
}

// editYAML reads a config file, applies one change and writes it back. The
// read-modify-write is per call rather than per link so a failure part way
// through leaves the files it already wrote correct, rather than half of one.
func (s *Server) editYAML(server *instance.Instance, rel string, edit func(*mcyaml.File) error) error {
	file, _, err := s.loadServerConfig(server, rel)
	if err != nil {
		return err
	}
	if err := edit(file); err != nil {
		if errors.Is(err, mcyaml.ErrNotScalar) {
			return errLinkf("%s 里的这一项不是普通的值，面板不敢改，请手动编辑", rel)
		}
		return err
	}
	return s.writeInstanceFile(server, rel, file.Render())
}

func errLinkf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errLink, fmt.Sprintf(format, args...))
}

// randomSecret is a forwarding secret nobody has to remember: 24 characters out
// of crypto/rand, with no lookalikes to mistype into a sub-server.
func randomSecret() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		// crypto/rand does not fail on any platform this runs on; if it ever
		// did, refusing to invent a weak secret is the right answer.
		return ""
	}
	out := make([]byte, len(bytes))
	for index, value := range bytes {
		out[index] = alphabet[int(value)%len(alphabet)]
	}
	return string(out)
}
