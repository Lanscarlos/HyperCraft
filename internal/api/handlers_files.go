package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/confighist"
	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/mcprops"
)

type propertiesResponse struct {
	Exists  bool              `json:"exists"`
	Path    string            `json:"path"`
	Entries []mcprops.Entry   `json:"entries"`
	Known   []knownPropertyUI `json:"known"`
}

// knownPropertyUI annotates the settings worth surfacing as real form controls
// instead of a raw text box. Anything not listed still shows up as a plain
// key/value row, so unknown or modded keys are never hidden or dropped.
type knownPropertyUI struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Type    string   `json:"type"` // "text" | "number" | "boolean" | "select"
	Options []string `json:"options,omitempty"`
	Hint    string   `json:"hint,omitempty"`
	// Default is what Minecraft itself uses when the key is absent. The UI
	// shows it for keys the file does not contain yet, so an unwritten
	// online-mode reads as "on" rather than as a silently disabled setting.
	Default string `json:"default"`
	// Group is the section of the form this belongs to, and the rail down the
	// left is built from these in table order. Empty is not allowed — see
	// TestKnownPropertiesAreGrouped — because a key with no section would
	// either drop off the rail or invent one.
	Group string `json:"group,omitempty"`
	// Live is the in-game command that applies this setting without a restart,
	// for the few keys that have one.
	//
	// The obvious field here is the opposite one — a 需重启 flag per row — and
	// it would be on every row: the server reads server.properties once, at
	// startup, so every edit on this page waits for a restart. A badge that is
	// always on is read by nobody. What actually varies is which handful can
	// also be changed right now from the console, so that is what a row
	// carries, and the restart is said once in the save bar where it is true
	// of everything being saved.
	Live string `json:"live,omitempty"`
	// Risk spells out what goes wrong if this is set carelessly: a server
	// anyone can walk into under someone else's name, a world that cannot be
	// put back. Only the few keys that can do that carry one — a warning on
	// every row is a warning on none.
	Risk string `json:"risk,omitempty"`
}

var knownProperties = []knownPropertyUI{
	// 基础
	{Key: "motd", Label: "服务器标语 (MOTD)", Type: "text", Default: "A Minecraft Server", Hint: "中文会自动转成 \\uXXXX 转义，游戏内显示正常", Group: "基础"},
	{Key: "gamemode", Label: "默认游戏模式", Type: "select", Default: "survival", Options: []string{"survival", "creative", "adventure", "spectator"}, Group: "基础", Live: "/defaultgamemode <模式>"},
	{Key: "difficulty", Label: "难度", Type: "select", Default: "easy", Options: []string{"peaceful", "easy", "normal", "hard"}, Group: "基础", Live: "/difficulty <难度>"},
	{Key: "pvp", Label: "允许 PVP", Type: "boolean", Default: "true", Group: "基础"},
	{Key: "hardcore", Label: "极限模式", Type: "boolean", Default: "false", Group: "基础",
		Risk: "开启后玩家死亡即永久旁观，且这个改动对已有存档不可逆"},

	// 世界与生成
	{Key: "level-name", Label: "存档名称", Type: "text", Default: "world", Group: "世界与生成",
		Risk: "改成一个不存在的名字会直接生成新世界：原存档不会被删掉，但服务器从此不再加载它"},
	{Key: "level-seed", Label: "世界种子", Type: "text", Default: "", Hint: "留空为随机生成", Group: "世界与生成"},
	{Key: "allow-nether", Label: "允许下界", Type: "boolean", Default: "true", Group: "世界与生成"},

	// 玩家与权限
	{Key: "max-players", Label: "最大玩家数", Type: "number", Default: "20", Group: "玩家与权限"},
	{Key: "online-mode", Label: "正版验证", Type: "boolean", Default: "true", Group: "玩家与权限",
		Risk: "关闭后任何人都能冒用他人 ID 进入，必须配合前置验证插件，且不要直接暴露在公网"},
	{Key: "white-list", Label: "启用白名单", Type: "boolean", Default: "false", Group: "玩家与权限", Live: "/whitelist on"},
	{Key: "enable-command-block", Label: "启用命令方块", Type: "boolean", Default: "false", Group: "玩家与权限"},
	{Key: "spawn-protection", Label: "出生点保护半径", Type: "number", Default: "16", Group: "玩家与权限"},
	{Key: "allow-flight", Label: "允许飞行", Type: "boolean", Default: "false", Group: "玩家与权限",
		Hint: "关着的时候，飞行类插件和鞘翅加速容易被服务端判定为作弊踢出"},
	{Key: "enforce-secure-profile", Label: "强制安全档案", Type: "boolean", Default: "true", Group: "玩家与权限"},

	// 网络与端口
	{Key: "server-port", Label: "端口", Type: "number", Default: "25565", Group: "网络与端口"},

	// 性能
	{Key: "view-distance", Label: "视距 (区块)", Type: "number", Default: "10", Group: "性能",
		Hint: "对 TPS 影响最大的单项设置，8–12 通常是性价比区间"},
	{Key: "simulation-distance", Label: "模拟距离 (区块)", Type: "number", Default: "10", Group: "性能",
		Hint: "比视距更吃 CPU：只有这个半径内的实体和红石才真的在跑"},
}

func (s *Server) propertiesPath(dir string) string {
	return filepath.Join(dir, "server.properties")
}

// serverFromPath resolves the instance and refuses a proxy.
//
// A proxy has no server.properties and no EULA — it reads velocity.toml, which
// has its own routes. Writing either file into a Velocity directory produces
// something that looks like configuration, is read by nothing, and is then very
// hard to tell apart from a setting that simply did not take effect.
func (s *Server) serverFromPath(w http.ResponseWriter, r *http.Request) (*instance.Instance, bool) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return nil, false
	}
	if inst.Config().IsProxy() {
		writeError(w, http.StatusBadRequest, "代理端没有 server.properties 和 EULA，请在「代理配置」里设置")
		return nil, false
	}
	return inst, true
}

func (s *Server) handleGetProperties(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.serverFromPath(w, r)
	if !ok {
		return
	}

	path := s.propertiesPath(inst.Config().Directory)
	file, err := mcprops.Load(path)
	if err != nil {
		s.writeDomainError(w, err)
		return
	}

	exists := true
	if _, statErr := os.Stat(path); statErr != nil {
		exists = false
	}
	writeJSON(w, http.StatusOK, propertiesResponse{
		Exists:  exists,
		Path:    path,
		Entries: file.Entries(),
		Known:   knownProperties,
	})
}

type putPropertiesRequest struct {
	Entries []mcprops.Entry `json:"entries"`
}

// handlePutProperties applies the submitted keys onto the existing file.
//
// It updates and appends but never deletes: the panel only knows about the
// keys the UI rendered, and a future Minecraft version (or a plugin) may add
// keys this build has never heard of. Silently dropping them on save would be
// a nasty way to lose configuration.
func (s *Server) handlePutProperties(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.serverFromPath(w, r)
	if !ok {
		return
	}

	var req putPropertiesRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	dir := inst.Config().Directory
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.writeDomainError(w, err)
		return
	}

	path := s.propertiesPath(dir)
	file, err := mcprops.Load(path)
	if err != nil {
		s.writeDomainError(w, err)
		return
	}
	for _, entry := range req.Entries {
		key := strings.TrimSpace(entry.Key)
		if key == "" {
			continue
		}
		file.Set(key, entry.Value)
	}
	if err := file.Save(path); err != nil {
		s.writeDomainError(w, err)
		return
	}

	s.snapshotAfter(inst, confighist.TriggerUser, actorOf(r), "编辑 server.properties")
	s.log.Info("server.properties saved", "instance", inst.Config().Name, "keys", len(req.Entries))
	writeJSON(w, http.StatusOK, propertiesResponse{
		Exists:  true,
		Path:    path,
		Entries: file.Entries(),
		Known:   knownProperties,
	})
}

type eulaResponse struct {
	Exists   bool   `json:"exists"`
	Accepted bool   `json:"accepted"`
	Path     string `json:"path"`
}

func (s *Server) readEULA(dir string) eulaResponse {
	path := filepath.Join(dir, "eula.txt")
	resp := eulaResponse{Path: path}

	data, err := os.ReadFile(path)
	if err != nil {
		return resp
	}
	resp.Exists = true
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if found && strings.TrimSpace(key) == "eula" && strings.EqualFold(strings.TrimSpace(value), "true") {
			resp.Accepted = true
		}
	}
	return resp
}

func (s *Server) handleGetEULA(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.serverFromPath(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.readEULA(inst.Config().Directory))
}

// handleAcceptEULA writes eula=true. The operator clicks this in the UI next to
// a link to Mojang's terms; the panel is only recording their decision.
func (s *Server) handleAcceptEULA(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.serverFromPath(w, r)
	if !ok {
		return
	}

	dir := inst.Config().Directory
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.writeDomainError(w, err)
		return
	}

	path := filepath.Join(dir, "eula.txt")
	content := "# Accepted via HyperCraft panel by the server operator.\n" +
		"# https://aka.ms/MinecraftEULA\n" +
		"eula=true\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		s.writeDomainError(w, err)
		return
	}

	s.snapshotAfter(inst, confighist.TriggerUser, actorOf(r), "接受 EULA")
	s.log.Info("EULA accepted", "instance", inst.Config().Name)
	writeJSON(w, http.StatusOK, s.readEULA(dir))
}
