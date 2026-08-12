package api

import (
	"errors"
	"net/http"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/confighist"
	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/mcyaml"
	"github.com/lanscarlos/hypercraft/internal/serverfiles"
)

// The settings a server keeps outside server.properties.
//
// bukkit.yml, spigot.yml and Paper's config are where the answers to most of
// what people actually ask a panel for live — why are there no mobs, why is the
// server lagging, how do I put it behind a proxy — and until now the only way
// to reach them was the file manager, which means reading nine hundred lines of
// YAML to change one boolean. This is the same treatment server.properties and
// velocity.toml already get: the settings worth a form control get one, and
// everything else stays in the file, untouched, for the editor to handle.
//
// It is deliberately a whitelist. These files are long, most of their keys are
// tuning nobody should touch on a guess, and a page that showed all of them
// would be the file manager with worse formatting.

type serverConfigSettingUI struct {
	// Key is the dotted path inside the file — "world-settings.default.view-distance".
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Type    string   `json:"type"` // "text" | "number" | "boolean" | "select"
	Options []string `json:"options,omitempty"`
	Hint    string   `json:"hint,omitempty"`
	// Default is what the server itself uses, shown for a key the file does not
	// carry yet so an absent setting reads as its real behaviour.
	Default string `json:"default"`
	Group   string `json:"group"`
}

type serverConfigGroup struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Hint  string `json:"hint,omitempty"`
}

// serverConfigFile is one editable file: what it is called, where it lives, and
// which of its keys the panel offers.
type serverConfigFile struct {
	ID     string
	Label  string
	Lead   string
	Path   string
	Groups []serverConfigGroup
	Known  []serverConfigSettingUI
}

type serverConfigFileResponse struct {
	ID     string                  `json:"id"`
	Label  string                  `json:"label"`
	Lead   string                  `json:"lead"`
	Path   string                  `json:"path"`
	Exists bool                    `json:"exists"`
	Groups []serverConfigGroup     `json:"groups"`
	Known  []serverConfigSettingUI `json:"known"`
	// Entries are the keys the file actually carries, so the UI can tell a value
	// that was chosen from one that is merely the default.
	Entries []mcyaml.Entry `json:"entries"`
}

type serverConfigResponse struct {
	Files []serverConfigFileResponse `json:"files"`
	// Missing names the files that would have been offered but do not exist
	// yet, so the page can say why rather than silently showing less.
	Missing []string `json:"missing"`
}

const (
	fileBukkit      = "bukkit"
	fileSpigot      = "spigot"
	filePaperGlobal = "paper-global"
	filePaperWorld  = "paper-world"
	filePaperLegacy = "paper-legacy"
)

const (
	pathBukkit      = "bukkit.yml"
	pathSpigot      = "spigot.yml"
	pathPaperGlobal = "config/paper-global.yml"
	pathPaperWorld  = "config/paper-world-defaults.yml"
	pathPaperLegacy = "paper.yml"
)

var bukkitConfig = serverConfigFile{
	ID:    fileBukkit,
	Label: "bukkit.yml",
	Lead:  "Bukkit 的老配置，所有插件服都有。生物生成上限和自动保存间隔在这里。",
	Path:  pathBukkit,
	Groups: []serverConfigGroup{
		{ID: "basic", Label: "基本"},
		{ID: "spawn", Label: "生物生成上限", Hint: "每个玩家周围能同时存在的数量。调低能省性能，调太低会让人觉得「怎么没有怪」。"},
		{ID: "ticks", Label: "刷新间隔", Hint: "单位是游戏刻，20 刻 = 1 秒。"},
	},
	Known: []serverConfigSettingUI{
		{Key: "settings.allow-end", Label: "允许末地", Type: "boolean", Default: "true", Group: "basic"},
		{Key: "settings.warn-on-overload", Label: "卡顿时打印警告", Type: "boolean", Default: "true", Group: "basic", Hint: "就是控制台里那句 Can't keep up"},
		{Key: "settings.connection-throttle", Label: "连接节流 (毫秒)", Type: "number", Default: "4000", Group: "basic", Hint: "同一个 IP 两次连接的最小间隔，-1 关闭。挂在代理端后面建议设 -1，否则同一台机器过来的玩家会互相挡"},
		{Key: "settings.shutdown-message", Label: "关服提示", Type: "text", Default: "Server closed", Group: "basic"},
		{Key: "settings.query-plugins", Label: "Query 显示插件列表", Type: "boolean", Default: "true", Group: "basic"},

		{Key: "spawn-limits.monsters", Label: "怪物", Type: "number", Default: "70", Group: "spawn"},
		{Key: "spawn-limits.animals", Label: "动物", Type: "number", Default: "10", Group: "spawn"},
		{Key: "spawn-limits.water-animals", Label: "水生动物", Type: "number", Default: "5", Group: "spawn"},
		{Key: "spawn-limits.water-ambient", Label: "水生环境生物", Type: "number", Default: "20", Group: "spawn"},
		{Key: "spawn-limits.ambient", Label: "环境生物 (蝙蝠)", Type: "number", Default: "15", Group: "spawn"},

		{Key: "ticks-per.animal-spawns", Label: "动物生成间隔", Type: "number", Default: "400", Group: "ticks"},
		{Key: "ticks-per.monster-spawns", Label: "怪物生成间隔", Type: "number", Default: "1", Group: "ticks"},
		{Key: "ticks-per.water-spawns", Label: "水生生物生成间隔", Type: "number", Default: "1", Group: "ticks"},
		{Key: "ticks-per.ambient-spawns", Label: "环境生物生成间隔", Type: "number", Default: "1", Group: "ticks"},
		{Key: "ticks-per.autosave", Label: "自动保存间隔", Type: "number", Default: "6000", Group: "ticks", Hint: "6000 刻 = 5 分钟。Paper 有自己的保存策略，一般不用动"},
		{Key: "chunk-gc.period-in-ticks", Label: "区块回收间隔", Type: "number", Default: "600", Group: "ticks"},
	},
}

var spigotConfig = serverConfigFile{
	ID:    fileSpigot,
	Label: "spigot.yml",
	Lead:  "Spigot 及其衍生核心的配置。挂代理端要开的 bungeecord 就在这里。",
	Path:  pathSpigot,
	Groups: []serverConfigGroup{
		{ID: "basic", Label: "基本"},
		{ID: "messages", Label: "提示消息", Hint: "玩家被拦下来时看到的那几句话。"},
		{ID: "world", Label: "世界优化", Hint: "写在 world-settings.default 下，对所有世界生效。"},
	},
	Known: []serverConfigSettingUI{
		{Key: "settings.bungeecord", Label: "BungeeCord / 传统转发", Type: "boolean", Default: "false", Group: "basic", Hint: "代理端用 legacy 或 bungeeguard 转发时打开；用 modern（Velocity）转发的话保持关闭。「代理连线」页会自动配它"},
		{Key: "settings.restart-on-crash", Label: "崩溃后自动重启", Type: "boolean", Default: "false", Group: "basic", Hint: "面板自己有守护，这里一般保持关闭，两边都开会打架"},
		{Key: "settings.restart-script", Label: "重启脚本", Type: "text", Default: "./start.sh", Group: "basic"},
		{Key: "settings.timeout-time", Label: "看门狗超时 (秒)", Type: "number", Default: "60", Group: "basic", Hint: "主线程卡这么久就判定服务器死了"},
		{Key: "settings.netty-threads", Label: "网络线程数", Type: "number", Default: "4", Group: "basic"},
		{Key: "settings.save-user-cache-on-stop-only", Label: "只在关服时写玩家缓存", Type: "boolean", Default: "false", Group: "basic"},

		{Key: "messages.whitelist", Label: "不在白名单", Type: "text", Default: "You are not whitelisted on this server!", Group: "messages"},
		{Key: "messages.unknown-command", Label: "未知命令", Type: "text", Default: "Unknown command. Type \"/help\" for help.", Group: "messages"},
		{Key: "messages.server-full", Label: "服务器已满", Type: "text", Default: "The server is full!", Group: "messages"},
		{Key: "messages.outdated-client", Label: "客户端版本过低", Type: "text", Default: "Outdated client! Please use {0}", Group: "messages"},
		{Key: "messages.outdated-server", Label: "服务端版本过低", Type: "text", Default: "Outdated server! I'm still on {0}", Group: "messages"},
		{Key: "messages.restart", Label: "重启中", Type: "text", Default: "Server is restarting", Group: "messages"},

		{Key: "world-settings.default.view-distance", Label: "视距覆盖", Type: "text", Default: "default", Group: "world", Hint: "default 表示跟随 server.properties"},
		{Key: "world-settings.default.mob-spawn-range", Label: "怪物生成范围 (区块)", Type: "number", Default: "6", Group: "world"},
		{Key: "world-settings.default.entity-activation-range.animals", Label: "动物活动范围", Type: "number", Default: "32", Group: "world", Hint: "超出这个距离的实体不再计算 AI，调小最省 CPU"},
		{Key: "world-settings.default.entity-activation-range.monsters", Label: "怪物活动范围", Type: "number", Default: "32", Group: "world"},
		{Key: "world-settings.default.entity-activation-range.misc", Label: "杂项实体活动范围", Type: "number", Default: "16", Group: "world"},
		{Key: "world-settings.default.item-despawn-rate", Label: "掉落物消失时间", Type: "number", Default: "6000", Group: "world", Hint: "6000 刻 = 5 分钟"},
		{Key: "world-settings.default.arrow-despawn-rate", Label: "箭消失时间", Type: "number", Default: "1200", Group: "world"},
		{Key: "world-settings.default.merge-radius.item", Label: "掉落物合并半径", Type: "number", Default: "2.5", Group: "world"},
		{Key: "world-settings.default.merge-radius.exp", Label: "经验球合并半径", Type: "number", Default: "3.0", Group: "world"},
		{Key: "world-settings.default.nerf-spawner-mobs", Label: "刷怪笼生物无 AI", Type: "boolean", Default: "false", Group: "world", Hint: "开了之后刷怪塔还能用，但怪不会自己走动"},
		{Key: "world-settings.default.max-tnt-per-tick", Label: "每刻最多 TNT", Type: "number", Default: "100", Group: "world"},
		{Key: "world-settings.default.hopper-transfer", Label: "漏斗传输间隔", Type: "number", Default: "8", Group: "world"},
		{Key: "world-settings.default.hopper-check", Label: "漏斗吸取检查间隔", Type: "number", Default: "1", Group: "world"},
	},
}

var paperGlobalConfig = serverConfigFile{
	ID:    filePaperGlobal,
	Label: "paper-global.yml",
	Lead:  "Paper 1.19 之后的全局配置。代理端转发（Velocity modern）在这里配。",
	Path:  pathPaperGlobal,
	Groups: []serverConfigGroup{
		{ID: "proxy", Label: "代理转发", Hint: "挂在代理端后面时，子服从哪里拿玩家的真实 IP 和 UUID。「代理连线」页会自动配这几项。"},
		{ID: "messages", Label: "踢出提示"},
		{ID: "misc", Label: "杂项"},
	},
	Known: []serverConfigSettingUI{
		{Key: "proxies.velocity.enabled", Label: "启用 Velocity 转发", Type: "boolean", Default: "false", Group: "proxy"},
		{Key: "proxies.velocity.secret", Label: "Velocity 转发密钥", Type: "text", Default: "", Group: "proxy", Hint: "必须和代理端 forwarding.secret 里的一模一样"},
		{Key: "proxies.velocity.online-mode", Label: "Velocity 侧正版验证", Type: "boolean", Default: "false", Group: "proxy", Hint: "代理端开了正版验证，这里也要开"},
		{Key: "proxies.bungee-cord.online-mode", Label: "BungeeCord 侧正版验证", Type: "boolean", Default: "true", Group: "proxy", Hint: "用传统转发时才有意义"},
		{Key: "proxies.proxy-protocol", Label: "HAProxy 协议", Type: "boolean", Default: "false", Group: "proxy", Hint: "不知道是什么就别开"},

		{Key: "messages.no-permission", Label: "没有权限", Type: "text", Default: "<red>I'm sorry, but you do not have permission to perform this command.", Group: "messages"},
		{Key: "messages.kick.authentication-servers-down", Label: "验证服务器挂了", Type: "text", Default: "<lang:multiplayer.disconnect.authservers_down>", Group: "messages"},
		{Key: "messages.kick.connection-throttle", Label: "连接太频繁", Type: "text", Default: "Connection throttled! Please wait before reconnecting.", Group: "messages"},
		{Key: "messages.kick.flying-player", Label: "疑似飞行", Type: "text", Default: "<lang:multiplayer.disconnect.flying>", Group: "messages"},

		{Key: "misc.max-joins-per-tick", Label: "每刻最多进服人数", Type: "number", Default: "5", Group: "misc", Hint: "开服瞬间一堆人挤进来时，这个值决定卡多久"},
		{Key: "misc.region-file-cache-size", Label: "区域文件缓存", Type: "number", Default: "256", Group: "misc"},
		{Key: "player-auto-save.rate", Label: "玩家数据保存间隔", Type: "number", Default: "-1", Group: "misc", Hint: "-1 表示跟随 bukkit.yml 的 autosave"},
		{Key: "player-auto-save.max-per-tick", Label: "每刻最多保存玩家数", Type: "number", Default: "-1", Group: "misc"},
		{Key: "watchdog.early-warning-delay", Label: "看门狗预警延迟 (毫秒)", Type: "number", Default: "10000", Group: "misc"},
		{Key: "watchdog.early-warning-every", Label: "看门狗预警间隔 (毫秒)", Type: "number", Default: "5000", Group: "misc"},
		{Key: "spam-limiter.tab-spam-limit", Label: "Tab 补全刷屏上限", Type: "number", Default: "500", Group: "misc"},
		{Key: "console.enable-brigadier-highlighting", Label: "控制台命令高亮", Type: "boolean", Default: "true", Group: "misc"},
		{Key: "console.enable-brigadier-completions", Label: "控制台命令补全", Type: "boolean", Default: "true", Group: "misc"},
	},
}

var paperWorldConfig = serverConfigFile{
	ID:    filePaperWorld,
	Label: "paper-world-defaults.yml",
	Lead:  "Paper 对所有世界生效的默认值。大部分「优化服务端」的教程改的是这里。",
	Path:  pathPaperWorld,
	Groups: []serverConfigGroup{
		{ID: "spawning", Label: "生物生成与清理"},
		{ID: "optimize", Label: "性能"},
		{ID: "anticheat", Label: "反矿透"},
	},
	Known: []serverConfigSettingUI{
		{Key: "entities.spawning.per-player-mob-spawns", Label: "按玩家计算生成上限", Type: "boolean", Default: "true", Group: "spawning", Hint: "关掉的话人多时后进来的玩家附近就不刷怪了"},
		{Key: "entities.spawning.despawn-ranges.monster.soft", Label: "怪物软消失距离", Type: "number", Default: "32", Group: "spawning"},
		{Key: "entities.spawning.despawn-ranges.monster.hard", Label: "怪物硬消失距离", Type: "number", Default: "128", Group: "spawning"},
		{Key: "entities.spawning.alt-item-despawn-rate.enabled", Label: "按物品分别设置消失时间", Type: "boolean", Default: "false", Group: "spawning"},
		{Key: "entities.armor-stands.tick", Label: "盔甲架参与计算", Type: "boolean", Default: "true", Group: "spawning", Hint: "很多插件用盔甲架做全息字，关掉能省不少"},
		{Key: "tick-rates.mob-spawner", Label: "刷怪笼检查间隔", Type: "number", Default: "1", Group: "spawning"},

		{Key: "chunks.prevent-moving-into-unloaded-chunks", Label: "阻止走进未加载区块", Type: "boolean", Default: "false", Group: "optimize"},
		{Key: "chunks.max-auto-save-chunks-per-tick", Label: "每刻最多保存区块", Type: "number", Default: "24", Group: "optimize"},
		{Key: "collisions.max-entity-collisions", Label: "实体碰撞上限", Type: "number", Default: "8", Group: "optimize", Hint: "挤在一起的生物最多互相推几次，刷怪塔卡就调小"},
		{Key: "environment.optimize-explosions", Label: "优化爆炸计算", Type: "boolean", Default: "false", Group: "optimize"},
		{Key: "hopper.disable-move-event", Label: "关闭漏斗移动事件", Type: "boolean", Default: "false", Group: "optimize", Hint: "省性能，但依赖该事件的反作弊/箱子插件会失效"},
		{Key: "hopper.ignore-occluding-blocks", Label: "漏斗忽略遮挡方块", Type: "boolean", Default: "false", Group: "optimize"},
		{Key: "misc.disable-end-credits", Label: "跳过末地结束诗", Type: "boolean", Default: "false", Group: "optimize"},

		{Key: "anticheat.anti-xray.enabled", Label: "启用反矿透", Type: "boolean", Default: "false", Group: "anticheat"},
		{Key: "anticheat.anti-xray.engine-mode", Label: "反矿透模式", Type: "select", Options: []string{"1", "2", "3"}, Default: "1", Group: "anticheat", Hint: "1 = 把矿藏换成石头，2 = 到处塞假矿，3 = 只藏矿。2 更狠也更吃 CPU"},
	},
}

var paperLegacyConfig = serverConfigFile{
	ID:    filePaperLegacy,
	Label: "paper.yml",
	Lead:  "Paper 1.19 之前的配置。新版本已经拆成 config/ 下的几个文件了。",
	Path:  pathPaperLegacy,
	Groups: []serverConfigGroup{
		{ID: "proxy", Label: "代理转发"},
		{ID: "world", Label: "世界优化"},
	},
	Known: []serverConfigSettingUI{
		{Key: "settings.velocity-support.enabled", Label: "启用 Velocity 转发", Type: "boolean", Default: "false", Group: "proxy"},
		{Key: "settings.velocity-support.secret", Label: "Velocity 转发密钥", Type: "text", Default: "", Group: "proxy", Hint: "必须和代理端 forwarding.secret 里的一模一样"},
		{Key: "settings.velocity-support.online-mode", Label: "Velocity 侧正版验证", Type: "boolean", Default: "false", Group: "proxy"},
		{Key: "settings.bungee-online-mode", Label: "BungeeCord 侧正版验证", Type: "boolean", Default: "true", Group: "proxy"},

		{Key: "world-settings.default.per-player-mob-spawns", Label: "按玩家计算生成上限", Type: "boolean", Default: "false", Group: "world"},
		{Key: "world-settings.default.despawn-ranges.soft", Label: "生物软消失距离", Type: "number", Default: "32", Group: "world"},
		{Key: "world-settings.default.despawn-ranges.hard", Label: "生物硬消失距离", Type: "number", Default: "128", Group: "world"},
		{Key: "world-settings.default.optimize-explosions", Label: "优化爆炸计算", Type: "boolean", Default: "false", Group: "world"},
		{Key: "world-settings.default.hopper.disable-move-event", Label: "关闭漏斗移动事件", Type: "boolean", Default: "false", Group: "world"},
		{Key: "world-settings.default.max-auto-save-chunks-per-tick", Label: "每刻最多保存区块", Type: "number", Default: "24", Group: "world"},
		{Key: "world-settings.default.anti-xray.enabled", Label: "启用反矿透", Type: "boolean", Default: "false", Group: "world"},
		{Key: "world-settings.default.anti-xray.engine-mode", Label: "反矿透模式", Type: "select", Options: []string{"1", "2", "3"}, Default: "1", Group: "world"},
	},
}

// serverConfigFiles is the set offered for one instance, in the order the page
// shows them.
//
// Paper's config exists in two layouts — one file until 1.18, a config/
// directory from 1.19 — and which one a server reads is decided by the jar, not
// by us. Offering both would put two 反矿透 switches on one page, only one of
// which does anything.
func serverConfigFiles(browser *serverfiles.Browser) []serverConfigFile {
	files := []serverConfigFile{bukkitConfig, spigotConfig}
	if fileMissing(browser, pathPaperGlobal) && !fileMissing(browser, pathPaperLegacy) {
		return append(files, paperLegacyConfig)
	}
	return append(files, paperGlobalConfig, paperWorldConfig)
}

func serverConfigFor(browser *serverfiles.Browser, id string) (serverConfigFile, bool) {
	for _, file := range serverConfigFiles(browser) {
		if file.ID == id {
			return file, true
		}
	}
	return serverConfigFile{}, false
}

func fileMissing(browser *serverfiles.Browser, rel string) bool {
	_, err := browser.Stat(rel)
	return err != nil
}

// loadServerConfig reads one file. A missing file parses as empty rather than
// failing: what the page then shows is the server's own defaults, and saving
// writes a file holding exactly the keys that were changed — which is a file
// Paper and Spigot both merge their defaults into on the next boot.
func (s *Server) loadServerConfig(inst *instance.Instance, rel string) (*mcyaml.File, bool, error) {
	text, err := s.browserFor(inst).ReadText(rel)
	switch {
	case errors.Is(err, serverfiles.ErrNotFound):
		return &mcyaml.File{}, false, nil
	case err != nil:
		return nil, false, err
	}
	file, err := mcyaml.Parse(strings.NewReader(text))
	if err != nil {
		return nil, false, err
	}
	return file, true, nil
}

func (s *Server) serverConfigFileResponse(inst *instance.Instance, spec serverConfigFile) (serverConfigFileResponse, error) {
	file, exists, err := s.loadServerConfig(inst, spec.Path)
	if err != nil {
		return serverConfigFileResponse{}, err
	}

	entries := make([]mcyaml.Entry, 0, len(spec.Known))
	for _, setting := range spec.Known {
		if value, ok := file.Get(setting.Key); ok {
			entries = append(entries, mcyaml.Entry{Key: setting.Key, Value: value})
		}
	}
	return serverConfigFileResponse{
		ID:      spec.ID,
		Label:   spec.Label,
		Lead:    spec.Lead,
		Path:    spec.Path,
		Exists:  exists,
		Groups:  spec.Groups,
		Known:   spec.Known,
		Entries: entries,
	}, nil
}

func (s *Server) handleGetServerConfigs(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.serverFromPath(w, r)
	if !ok {
		return
	}

	browser := s.browserFor(inst)
	out := serverConfigResponse{Files: []serverConfigFileResponse{}, Missing: []string{}}
	for _, spec := range serverConfigFiles(browser) {
		response, err := s.serverConfigFileResponse(inst, spec)
		if err != nil {
			s.writeFileError(w, err)
			return
		}
		out.Files = append(out.Files, response)
		if !response.Exists {
			out.Missing = append(out.Missing, spec.Path)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type putServerConfigRequest struct {
	Entries []mcyaml.Entry `json:"entries"`
}

func (s *Server) handlePutServerConfig(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.serverFromPath(w, r)
	if !ok {
		return
	}

	spec, known := serverConfigFor(s.browserFor(inst), r.PathValue("file"))
	if !known {
		writeError(w, http.StatusNotFound, "不认识的配置文件")
		return
	}

	var req putServerConfigRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	file, _, err := s.loadServerConfig(inst, spec.Path)
	if err != nil {
		s.writeFileError(w, err)
		return
	}

	for _, entry := range req.Entries {
		setting, ok := settingIn(spec, strings.TrimSpace(entry.Key))
		if !ok {
			writeError(w, http.StatusBadRequest, "不认识的设置项 "+entry.Key)
			return
		}
		if err := applyServerConfigSetting(file, setting, entry.Value); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	if err := s.writeInstanceFile(inst, spec.Path, file.Render()); err != nil {
		s.writeFileError(w, err)
		return
	}

	s.snapshotAfter(inst, confighist.TriggerUser, actorOf(r), "编辑 "+path.Base(spec.Path))
	s.log.Info("server config saved", "instance", inst.Config().Name, "file", spec.Path, "keys", len(req.Entries))

	response, err := s.serverConfigFileResponse(inst, spec)
	if err != nil {
		s.writeFileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// writeInstanceFile writes inside the instance, creating the directory it lives
// in. Paper's config/ does not exist until the server has booted once, and a
// proxy set up before the first boot is the normal case rather than the odd one.
func (s *Server) writeInstanceFile(inst *instance.Instance, rel, content string) error {
	dir := inst.Config().Directory
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	browser := s.browserFor(inst)
	if parent := path.Dir(rel); parent != "." && parent != "/" {
		if err := browser.Mkdir(parent); err != nil {
			return err
		}
	}
	return browser.WriteText(rel, content)
}

func settingIn(spec serverConfigFile, key string) (serverConfigSettingUI, bool) {
	for _, setting := range spec.Known {
		if setting.Key == key {
			return setting, true
		}
	}
	return serverConfigSettingUI{}, false
}

// applyServerConfigSetting writes one value in the type its key is declared
// with. A number that is not a number would be written verbatim and stop the
// server from loading the file at all — which it reports as a stack trace on
// boot, several minutes after the save that caused it.
func applyServerConfigSetting(file *mcyaml.File, setting serverConfigSettingUI, value string) error {
	value = strings.TrimSpace(value)

	var err error
	switch setting.Type {
	case "number":
		if _, parseErr := strconv.ParseFloat(value, 64); parseErr != nil {
			return errors.New(setting.Label + " 需要填数字")
		}
		err = file.SetRaw(setting.Key, value)
	case "boolean":
		if value != "true" && value != "false" {
			return errors.New(setting.Label + " 只能是 true 或 false")
		}
		err = file.SetRaw(setting.Key, value)
	case "select":
		if !slices.Contains(setting.Options, value) {
			return errors.New(setting.Label + " 不接受 " + value)
		}
		err = file.SetRaw(setting.Key, value)
	default:
		err = file.SetString(setting.Key, value)
	}

	if errors.Is(err, mcyaml.ErrNotScalar) {
		return errors.New(setting.Label + " 在这个文件里不是一个普通的值，请到文件管理里改")
	}
	return err
}
