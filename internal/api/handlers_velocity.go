package api

import (
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/confighist"
	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/mcprops"
	"github.com/lanscarlos/hypercraft/internal/serverfiles"
	"github.com/lanscarlos/hypercraft/internal/velocitycfg"
)

// A proxy's configuration is not a subset of a server's — it is a different
// file with different keys — so it gets its own endpoints rather than a flag on
// the properties ones. Only Velocity is supported; BungeeCord's config.yml is a
// different shape again and pretending otherwise would be worse than saying no.

// velocitySettingUI is one setting the panel offers as a real form control,
// with everything the UI needs to render it and nothing about where it lives in
// the file — that half stays on the server.
type velocitySettingUI struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Type    string   `json:"type"` // "text" | "number" | "boolean" | "select"
	Options []string `json:"options,omitempty"`
	Hint    string   `json:"hint,omitempty"`
	// Default is what Velocity itself uses, shown for a key the file does not
	// contain yet so an absent online-mode reads as "on".
	Default string `json:"default"`
	// Group is the panel this setting is rendered in.
	Group string `json:"group"` // "basic" | "forwarding" | "advanced" | "query"
}

type velocitySetting struct {
	velocitySettingUI
	section string
	name    string
	// upper marks the enums Velocity writes in capitals — "NONE", "MODERN",
	// "DISABLED". The panel shows them the way its own documentation writes
	// them, in lower case, and puts them back the way Velocity expects.
	upper bool
}

const (
	velocityServersTable = "servers"
	velocityTryKey       = "try"
	forwardingSecretKey  = "forwarding-secret-file"
)

var velocitySettings = []velocitySetting{
	{velocitySettingUI{Key: "bind", Label: "监听地址", Type: "text", Default: "0.0.0.0:25577", Group: "basic", Hint: "玩家连的是这个地址；子服不要再占用它"}, "", "bind", false},
	{velocitySettingUI{Key: "motd", Label: "服务器标语 (MOTD)", Type: "text", Default: "<#09add3>A Velocity Server", Group: "basic", Hint: "MiniMessage 格式，<red>这类标签会生效"}, "", "motd", false},
	{velocitySettingUI{Key: "show-max-players", Label: "显示的最大人数", Type: "number", Default: "500", Group: "basic", Hint: "只是列表上显示的数字，Velocity 本身不限制在线人数"}, "", "show-max-players", false},
	{velocitySettingUI{Key: "online-mode", Label: "正版验证", Type: "boolean", Default: "true", Group: "basic", Hint: "验证在代理端做；子服要各自关掉自己的正版验证"}, "", "online-mode", false},
	{velocitySettingUI{Key: "force-key-authentication", Label: "强制聊天签名", Type: "boolean", Default: "true", Group: "basic"}, "", "force-key-authentication", false},
	{velocitySettingUI{Key: "kick-existing-players", Label: "顶号登录", Type: "boolean", Default: "false", Group: "basic", Hint: "同名玩家再次登录时踢掉旧连接"}, "", "kick-existing-players", false},
	{velocitySettingUI{Key: "ping-passthrough", Label: "列表信息透传", Type: "select", Options: []string{"disabled", "mods", "description", "all"}, Default: "disabled", Group: "basic", Hint: "让服务器列表显示子服的信息，而不是这里填的"}, "", "ping-passthrough", true},
	{velocitySettingUI{Key: "announce-forge", Label: "声明支持 Forge", Type: "boolean", Default: "false", Group: "basic", Hint: "子服是模组服时打开"}, "", "announce-forge", false},
	{velocitySettingUI{Key: "enable-player-address-logging", Label: "日志记录玩家 IP", Type: "boolean", Default: "true", Group: "basic"}, "", "enable-player-address-logging", false},

	{velocitySettingUI{Key: "player-info-forwarding-mode", Label: "玩家信息转发", Type: "select", Options: []string{"none", "legacy", "bungeeguard", "modern"}, Default: "none", Group: "forwarding", Hint: "1.13 以上的子服用 modern；不转发的话子服只能看到代理端的 IP 和离线 UUID"}, "", "player-info-forwarding-mode", true},
	{velocitySettingUI{Key: forwardingSecretKey, Label: "转发密钥文件", Type: "text", Default: velocitycfg.DefaultSecretFile, Group: "forwarding", Hint: "相对代理端目录的文件名"}, "", forwardingSecretKey, false},
	{velocitySettingUI{Key: "prevent-client-proxy-connections", Label: "拦截 VPN / 代理连接", Type: "boolean", Default: "false", Group: "forwarding", Hint: "会误伤一部分正常玩家，保护力度也有限"}, "", "prevent-client-proxy-connections", false},

	{velocitySettingUI{Key: "advanced.compression-threshold", Label: "压缩阈值 (字节)", Type: "number", Default: "256", Group: "advanced", Hint: "0 压缩所有包，-1 完全关闭"}, "advanced", "compression-threshold", false},
	{velocitySettingUI{Key: "advanced.compression-level", Label: "压缩级别", Type: "number", Default: "-1", Group: "advanced", Hint: "0-9，-1 表示用默认的 6"}, "advanced", "compression-level", false},
	{velocitySettingUI{Key: "advanced.login-ratelimit", Label: "登录限速 (毫秒)", Type: "number", Default: "3000", Group: "advanced", Hint: "同一个地址两次连接的最小间隔，0 为不限"}, "advanced", "login-ratelimit", false},
	{velocitySettingUI{Key: "advanced.connection-timeout", Label: "连接超时 (毫秒)", Type: "number", Default: "5000", Group: "advanced"}, "advanced", "connection-timeout", false},
	{velocitySettingUI{Key: "advanced.read-timeout", Label: "读取超时 (毫秒)", Type: "number", Default: "30000", Group: "advanced"}, "advanced", "read-timeout", false},
	{velocitySettingUI{Key: "advanced.haproxy-protocol", Label: "HAProxy 协议", Type: "boolean", Default: "false", Group: "advanced", Hint: "不知道是什么就别开"}, "advanced", "haproxy-protocol", false},
	{velocitySettingUI{Key: "advanced.tcp-fast-open", Label: "TCP Fast Open", Type: "boolean", Default: "false", Group: "advanced", Hint: "只有 Linux 支持"}, "advanced", "tcp-fast-open", false},
	{velocitySettingUI{Key: "advanced.bungee-plugin-message-channel", Label: "BungeeCord 插件通道", Type: "boolean", Default: "true", Group: "advanced", Hint: "关掉的话，用 BungeeCord 通道跨服通信的插件会失效"}, "advanced", "bungee-plugin-message-channel", false},
	{velocitySettingUI{Key: "advanced.failover-on-unexpected-server-disconnect", Label: "子服掉线时转移玩家", Type: "boolean", Default: "true", Group: "advanced"}, "advanced", "failover-on-unexpected-server-disconnect", false},
	{velocitySettingUI{Key: "advanced.announce-proxy-commands", Label: "向客户端声明代理命令", Type: "boolean", Default: "true", Group: "advanced"}, "advanced", "announce-proxy-commands", false},
	{velocitySettingUI{Key: "advanced.log-command-executions", Label: "记录命令执行", Type: "boolean", Default: "false", Group: "advanced"}, "advanced", "log-command-executions", false},
	{velocitySettingUI{Key: "advanced.log-player-connections", Label: "记录玩家连接", Type: "boolean", Default: "true", Group: "advanced"}, "advanced", "log-player-connections", false},
	{velocitySettingUI{Key: "advanced.accepts-transfers", Label: "接受转移过来的玩家", Type: "boolean", Default: "false", Group: "advanced", Hint: "1.20.5 的转移包，来自别的服务器"}, "advanced", "accepts-transfers", false},

	{velocitySettingUI{Key: "query.enabled", Label: "启用 Query", Type: "boolean", Default: "false", Group: "query", Hint: "GameSpy 4 查询协议，一些服务器统计站点会用它抓在线人数"}, "query", "enabled", false},
	{velocitySettingUI{Key: "query.port", Label: "Query 端口", Type: "number", Default: "25577", Group: "query"}, "query", "port", false},
	{velocitySettingUI{Key: "query.map", Label: "Query 地图名", Type: "text", Default: "Velocity", Group: "query"}, "query", "map", false},
	{velocitySettingUI{Key: "query.show-plugins", Label: "Query 显示插件列表", Type: "boolean", Default: "false", Group: "query"}, "query", "show-plugins", false},
}

func velocitySettingFor(key string) (velocitySetting, bool) {
	for _, setting := range velocitySettings {
		if setting.Key == key {
			return setting, true
		}
	}
	return velocitySetting{}, false
}

// velocityServer is one entry of the [servers] table: the name players type
// after /server, and where the proxy sends them.
type velocityServer struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// velocityCandidate is a sub-server the panel already knows about, offered as a
// one-click row rather than an address to look up and retype. Reading it from
// the other instances is the only part of this page that a text editor cannot
// do for you.
type velocityCandidate struct {
	InstanceID string `json:"instanceId"`
	Instance   string `json:"instance"`
	Name       string `json:"name"`
	Address    string `json:"address"`
	// Added is true when the [servers] table already points at this address, so
	// the UI can show it as taken rather than offer it twice.
	Added bool `json:"added"`
}

type velocitySecret struct {
	// File is the name from forwarding-secret-file, relative to the proxy's
	// directory.
	File   string `json:"file"`
	Exists bool   `json:"exists"`
	Value  string `json:"value"`
}

type velocityResponse struct {
	Exists   bool                `json:"exists"`
	Path     string              `json:"path"`
	Entries  []velocitycfg.Entry `json:"entries"`
	Known    []velocitySettingUI `json:"known"`
	Servers  []velocityServer    `json:"servers"`
	Try      []string            `json:"try"`
	Secret   velocitySecret      `json:"secret"`
	Suggests []velocityCandidate `json:"suggests"`
}

// proxyFromPath resolves the instance and refuses the ones this page is not
// about, so a server instance cannot be handed a velocity.toml by URL.
func (s *Server) proxyFromPath(w http.ResponseWriter, r *http.Request) (*instance.Instance, bool) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return nil, false
	}
	if !inst.Config().IsProxy() {
		writeError(w, http.StatusBadRequest, "这个实例不是代理端")
		return nil, false
	}
	return inst, true
}

// loadVelocity reads the proxy's config, falling back to Velocity's own
// defaults for a proxy that has never been started. Velocity writes the file on
// its first boot, and waiting for that would mean the config page is empty at
// exactly the moment there is most to fill in.
func (s *Server) loadVelocity(inst *instance.Instance) (*velocitycfg.File, bool, error) {
	browser := s.browserFor(inst)
	text, err := browser.ReadText(velocitycfg.FileName)
	switch {
	case errors.Is(err, serverfiles.ErrNotFound):
		return velocitycfg.Default(), false, nil
	case err != nil:
		return nil, false, err
	}

	file, err := velocitycfg.Parse(strings.NewReader(text))
	if err != nil {
		return nil, false, err
	}
	if file.Empty() {
		return velocitycfg.Default(), true, nil
	}
	return file, true, nil
}

func (s *Server) handleGetVelocity(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.proxyFromPath(w, r)
	if !ok {
		return
	}

	file, exists, err := s.loadVelocity(inst)
	if err != nil {
		s.writeFileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.velocityResponse(inst, file, exists))
}

func (s *Server) velocityResponse(inst *instance.Instance, file *velocitycfg.File, exists bool) velocityResponse {
	known := make([]velocitySettingUI, 0, len(velocitySettings))
	entries := make([]velocitycfg.Entry, 0, len(velocitySettings))
	for _, setting := range velocitySettings {
		known = append(known, setting.velocitySettingUI)
		value, ok := file.Value(setting.section, setting.name)
		if !ok {
			continue
		}
		if setting.upper {
			value = strings.ToLower(value)
		}
		entries = append(entries, velocitycfg.Entry{Key: setting.Key, Value: value})
	}

	servers := make([]velocityServer, 0)
	for _, entry := range file.Entries(velocityServersTable, velocityTryKey) {
		servers = append(servers, velocityServer{Name: entry.Key, Address: entry.Value})
	}
	try, _ := file.List(velocityServersTable, velocityTryKey)
	if try == nil {
		try = []string{}
	}

	return velocityResponse{
		Exists:   exists,
		Path:     filepath.Join(inst.Config().Directory, velocitycfg.FileName),
		Entries:  entries,
		Known:    known,
		Servers:  servers,
		Try:      try,
		Secret:   s.readForwardingSecret(inst, file),
		Suggests: s.subServerSuggestions(inst, servers),
	}
}

// secretFileName is the file Velocity reads the forwarding secret from. A path
// that leaves the instance directory is ignored rather than followed: the panel
// only ever offers to read and write inside the server it belongs to.
func secretFileName(file *velocitycfg.File) string {
	name, ok := file.Value("", forwardingSecretKey)
	if !ok || strings.TrimSpace(name) == "" {
		return velocitycfg.DefaultSecretFile
	}
	name = strings.TrimSpace(name)
	if filepath.IsAbs(name) || strings.Contains(filepath.ToSlash(name), "../") {
		return velocitycfg.DefaultSecretFile
	}
	return name
}

func (s *Server) readForwardingSecret(inst *instance.Instance, file *velocitycfg.File) velocitySecret {
	name := secretFileName(file)
	out := velocitySecret{File: name}

	text, err := s.browserFor(inst).ReadText(name)
	if err != nil {
		return out
	}
	out.Exists = true
	out.Value = strings.TrimSpace(text)
	return out
}

// subServerSuggestions are the other instances on this panel, with the address
// their own server.properties says they listen on.
func (s *Server) subServerSuggestions(proxy *instance.Instance, servers []velocityServer) []velocityCandidate {
	taken := make(map[string]bool, len(servers))
	for _, server := range servers {
		taken[strings.ToLower(server.Address)] = true
	}

	out := make([]velocityCandidate, 0)
	used := make(map[string]bool, len(servers))
	for _, server := range servers {
		used[strings.ToLower(server.Name)] = true
	}

	for _, other := range s.mgr.List() {
		cfg := other.Config()
		if cfg.ID == proxy.Config().ID || cfg.IsProxy() {
			continue
		}

		address := backendAddress(cfg.Directory)
		name := uniqueServerName(serverNameFrom(cfg.Name), used)
		used[strings.ToLower(name)] = true
		out = append(out, velocityCandidate{
			InstanceID: cfg.ID,
			Instance:   cfg.Name,
			Name:       name,
			Address:    address,
			Added:      taken[strings.ToLower(address)],
		})
	}
	return out
}

// backendAddress reads where a server instance listens. server-ip is normally
// blank, which means every address — from the proxy's side that is loopback,
// which is also the address it should be reached on: a backend server behind a
// proxy has no reason to be exposed.
func backendAddress(directory string) string {
	host, port := "127.0.0.1", "25565"
	if file, err := mcprops.Load(filepath.Join(directory, "server.properties")); err == nil {
		for _, entry := range file.Entries() {
			switch entry.Key {
			case "server-ip":
				if value := strings.TrimSpace(entry.Value); value != "" {
					host = value
				}
			case "server-port":
				if value := strings.TrimSpace(entry.Value); value != "" {
					port = value
				}
			}
		}
	}
	return net.JoinHostPort(host, port)
}

// serverNameFrom turns an instance name into something that can be typed after
// /server. Velocity's own names are lower-case words, and a Chinese instance
// name — which most of them are on this panel — has nothing to keep.
func serverNameFrom(name string) string {
	var out strings.Builder
	for _, char := range strings.ToLower(name) {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			out.WriteRune(char)
		case char == '-' || char == '_' || char == ' ' || char == '.':
			out.WriteByte('-')
		}
	}
	slug := strings.Trim(out.String(), "-")
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	if slug == "" {
		return "server"
	}
	if len(slug) > 24 {
		slug = strings.Trim(slug[:24], "-")
	}
	return slug
}

func uniqueServerName(name string, used map[string]bool) string {
	if !used[strings.ToLower(name)] {
		return name
	}
	for suffix := 2; suffix < 100; suffix++ {
		candidate := name + "-" + strconv.Itoa(suffix)
		if !used[strings.ToLower(candidate)] {
			return candidate
		}
	}
	return name
}

// ------------------------------------------------------------------- writing

type putVelocityRequest struct {
	// All three are optional: a nil field is one this request is not about, so
	// the sub-server list and the settings form can save on their own without
	// either of them having to send the other's state back.
	Entries []velocitycfg.Entry `json:"entries"`
	Servers *[]velocityServer   `json:"servers"`
	Try     *[]string           `json:"try"`
	// ForwardingSecret writes the secret file. Empty is not "clear it" — it is
	// "leave it alone", so a form that renders the secret masked cannot wipe it.
	ForwardingSecret string `json:"forwardingSecret"`
}

// serverNamePattern is what Velocity accepts as a server name in practice: it
// is typed after /server and appears in permission nodes, so anything with a
// space or a quote in it is a name that will not work where it matters.
var serverNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$`)

func (s *Server) handlePutVelocity(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.proxyFromPath(w, r)
	if !ok {
		return
	}

	var req putVelocityRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	file, _, err := s.loadVelocity(inst)
	if err != nil {
		s.writeFileError(w, err)
		return
	}

	for _, entry := range req.Entries {
		setting, known := velocitySettingFor(strings.TrimSpace(entry.Key))
		if !known {
			writeError(w, http.StatusBadRequest, "不认识的设置项 "+entry.Key)
			return
		}
		if err := applyVelocitySetting(file, setting, entry.Value); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	if req.Servers != nil {
		entries, err := velocityServerEntries(*req.Servers)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		file.SetEntries(velocityServersTable, entries, velocityTryKey)
	}

	if req.Try != nil {
		// The try list names servers, so it is checked against the table as it
		// stands after this request rather than as it was before it: adding a
		// server and putting it first has to be one save.
		defined := make(map[string]bool)
		for _, entry := range file.Entries(velocityServersTable, velocityTryKey) {
			defined[entry.Key] = true
		}
		try := make([]string, 0, len(*req.Try))
		for _, name := range *req.Try {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if !defined[name] {
				writeError(w, http.StatusBadRequest, "登录顺序里的 "+name+" 不在子服列表中")
				return
			}
			try = append(try, name)
		}
		file.SetList(velocityServersTable, velocityTryKey, try)
	}

	dir := inst.Config().Directory
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.writeDomainError(w, err)
		return
	}

	browser := s.browserFor(inst)
	if err := browser.WriteText(velocitycfg.FileName, file.Render()); err != nil {
		s.writeFileError(w, err)
		return
	}
	if secret := strings.TrimSpace(req.ForwardingSecret); secret != "" {
		if err := browser.WriteText(secretFileName(file), secret); err != nil {
			s.writeFileError(w, err)
			return
		}
	}

	s.snapshotAfter(inst, confighist.TriggerUser, actorOf(r), "编辑 velocity.toml")
	s.log.Info("velocity.toml saved", "instance", inst.Config().Name, "keys", len(req.Entries))
	writeJSON(w, http.StatusOK, s.velocityResponse(inst, file, true))
}

// applyVelocitySetting writes one value in the type its key is declared with.
// A number that is not a number would be written verbatim and stop the proxy
// from parsing its own config at all, which is a much worse failure than a 400.
func applyVelocitySetting(file *velocitycfg.File, setting velocitySetting, value string) error {
	value = strings.TrimSpace(value)
	switch setting.Type {
	case "number":
		if _, err := strconv.Atoi(value); err != nil {
			return errors.New(setting.Label + " 需要填整数")
		}
		file.SetRaw(setting.section, setting.name, value)
	case "boolean":
		if value != "true" && value != "false" {
			return errors.New(setting.Label + " 只能是 true 或 false")
		}
		file.SetRaw(setting.section, setting.name, value)
	case "select":
		lower := strings.ToLower(value)
		if !slices.Contains(setting.Options, lower) {
			return errors.New(setting.Label + " 不接受 " + value)
		}
		if setting.upper {
			lower = strings.ToUpper(lower)
		}
		file.SetString(setting.section, setting.name, lower)
	default:
		file.SetString(setting.section, setting.name, value)
	}
	return nil
}

func velocityServerEntries(servers []velocityServer) ([]velocitycfg.Entry, error) {
	entries := make([]velocitycfg.Entry, 0, len(servers))
	seen := make(map[string]bool, len(servers))

	for _, server := range servers {
		name := strings.TrimSpace(server.Name)
		address := strings.TrimSpace(server.Address)
		if name == "" && address == "" {
			continue
		}
		if !serverNamePattern.MatchString(name) {
			return nil, errors.New("子服名称 " + name + " 不可用：只能用字母、数字、- . _，且以字母或数字开头")
		}
		if seen[strings.ToLower(name)] {
			return nil, errors.New("子服名称 " + name + " 重复了")
		}
		if err := validBackendAddress(address); err != nil {
			return nil, err
		}
		seen[strings.ToLower(name)] = true
		entries = append(entries, velocitycfg.Entry{Key: name, Value: address})
	}
	return entries, nil
}

// validBackendAddress insists on host:port. Velocity needs the port — it does
// not assume 25565 — and a missing one is the mistake this page exists to stop
// somebody making at three in the morning.
func validBackendAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || strings.TrimSpace(host) == "" {
		return errors.New("子服地址 " + address + " 要写成 主机:端口，例如 127.0.0.1:25566")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return errors.New("子服地址 " + address + " 的端口不是 1-65535 之间的数字")
	}
	return nil
}
