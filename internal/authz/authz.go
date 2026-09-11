// Package authz is the panel's permission vocabulary: the closed set of
// capabilities an API route can require, plus the metadata a role editor needs
// to present them.
//
// It is a package of its own rather than a handful of constants in
// internal/api because the dependency has to run this way. Roles are stored
// alongside the rest of the panel's settings, so internal/config will have to
// validate the capability names a role carries — and internal/config cannot
// import internal/api, which imports it.
//
// The vocabulary is closed on purpose. Operators compose roles out of these
// capabilities; they do not invent capabilities of their own. A capability is
// only meaningful if some route enforces it, and routes are code.
//
// See docs/proposal-multi-user.md for the design this implements.
package authz

import "slices"

// Cap is one capability. The string form is what a stored role carries, so
// these values are part of the panel's on-disk format: rename one and every
// role naming it silently loses that permission.
type Cap string

// Capabilities that apply to a single server, and are further narrowed by which
// instances a user is granted.
const (
	CapInstanceView       Cap = "instance:view"
	CapInstancePower      Cap = "instance:power"
	CapInstanceConsole    Cap = "instance:console"
	CapInstanceConfig     Cap = "instance:config"
	CapInstanceHistory    Cap = "instance:confighist"
	CapInstanceFilesRead  Cap = "instance:files:read"
	CapInstanceFilesWrite Cap = "instance:files:write"
	CapInstancePlugins    Cap = "instance:plugins"
	CapInstanceSchematics Cap = "instance:schematics"
	CapInstanceSettings   Cap = "instance:settings"
	CapInstanceLaunch     Cap = "instance:launch"
	CapInstanceDelete     Cap = "instance:delete"
)

// Capabilities that apply to the panel as a whole.
const (
	CapPanelSystem    Cap = "panel:system"
	CapPanelSecurity  Cap = "panel:security"
	CapPanelUsers     Cap = "panel:users"
	CapPanelCreate    Cap = "panel:instances:create"
	CapPanelNetwork   Cap = "panel:network"
	CapPanelJava      Cap = "panel:java"
	CapPanelDatabases Cap = "panel:databases"
	CapPanelTerminal  Cap = "panel:terminal"
	CapPanelHostFS    Cap = "panel:hostfs"
	CapPanelUpdate    Cap = "panel:update"
	CapPanelSettings  Cap = "panel:settings"
	CapLibraryCores   Cap = "library:cores"
	CapLibraryPlugins Cap = "library:plugins"
	CapLibrarySchems  Cap = "library:schematics"
)

// Scope says what a capability is granted over.
type Scope string

const (
	// ScopeInstance capabilities are also narrowed by the user's instance
	// grant: holding one says what may be done, not to which servers.
	ScopeInstance Scope = "instance"
	// ScopePanel capabilities are not narrowed by anything.
	ScopePanel Scope = "panel"
)

// Info describes one capability for the role editor.
type Info struct {
	Cap   Cap
	Scope Scope
	// Title is what the role editor calls this capability. User-facing, so
	// Chinese like the rest of the panel's copy.
	Title string
	// Note is the one line of explanation shown under the title, used where the
	// title alone would mislead. Empty for the ones that speak for themselves.
	Note string
	// Dangerous marks a capability that is, in practice, equivalent to handing
	// over the system account the panel runs as.
	//
	// Server processes are children of the panel and share its uid — see
	// internal/instance/process_unix.go, and note that internal/dbruntime is
	// the only place that ever drops privileges. So anything that decides what
	// code a server runs (a jar, a launch command, a directory) reaches
	// panel.json, databases.json and every other instance on the machine.
	//
	// These are not weaker forms of administrator. The flag exists so the role
	// editor can say so out loud rather than letting an operator discover it.
	Dangerous bool
}

// catalogue is the vocabulary, in the order a role editor should show it.
var catalogue = []Info{
	{CapInstanceView, ScopeInstance, "查看服务器", "状态、日志、指标、配置和控制台输出", false},
	{CapInstancePower, ScopeInstance, "开关机", "启动、停止、重启、强制结束", false},
	{CapInstanceConsole, ScopeInstance, "发送控制台命令", "等于游戏内最高权限：可以 /op 任何人", false},
	{CapInstanceConfig, ScopeInstance, "编辑服务器配置", "server.properties、bukkit.yml、velocity.toml 等", false},
	{CapInstanceHistory, ScopeInstance, "配置历史", "查看、比对、还原配置文件；能看到历史里的凭据", false},
	{CapInstanceFilesRead, ScopeInstance, "浏览与下载文件", "", false},
	{CapInstanceFilesWrite, ScopeInstance, "编辑、上传与删除文件", "传一个 jar 就能在服务端里执行代码", true},
	{CapInstancePlugins, ScopeInstance, "管理这个服的插件", "安装、启停、回滚，等于决定服务端加载什么代码", true},
	{CapInstanceSchematics, ScopeInstance, "导入建筑到这个服", "", false},
	{CapInstanceSettings, ScopeInstance, "实例基本设置", "名称、编码、自动启动 / 重启、停服命令", false},
	{CapInstanceLaunch, ScopeInstance, "启动设置与核心", "Java、启动参数、实例目录、更换核心，都是任意命令执行", true},
	{CapInstanceDelete, ScopeInstance, "删除实例", "", false},

	{CapPanelSystem, ScopePanel, "查看主机状态", "CPU、内存、磁盘", false},
	{CapPanelSecurity, ScopePanel, "登录记录", "", false},
	{CapLibraryCores, ScopePanel, "核心库", "下载和删除服务端核心", false},
	{CapLibraryPlugins, ScopePanel, "插件库", "面板级共享库，改动对所有实例可见", false},
	{CapLibrarySchems, ScopePanel, "建筑库", "", false},
	{CapPanelJava, ScopePanel, "Java 环境", "安装和删除 JDK", false},
	{CapPanelNetwork, ScopePanel, "代理连线", "会改动链路两端实例的配置文件，且不受实例授权限制", false},
	{CapPanelCreate, ScopePanel, "新建实例", "实例目录可以指向宿主机任意路径", true},
	{CapPanelDatabases, ScopePanel, "数据库", "面板保存的是明文密码", true},
	{CapPanelTerminal, ScopePanel, "本机终端", "宿主机 shell", true},
	{CapPanelHostFS, ScopePanel, "浏览主机目录", "不限于实例目录", true},
	{CapPanelUpdate, ScopePanel, "面板更新", "更换面板二进制", true},
	{CapPanelSettings, ScopePanel, "面板凭据与镜像", "GitHub 令牌等", true},
	{CapPanelUsers, ScopePanel, "用户与角色", "能给自己加权限，等于能成为管理员", true},
}

// byCap indexes the catalogue. Built once: Valid is called per request once
// enforcement lands, and per stored capability on every config load.
var byCap = func() map[Cap]Info {
	m := make(map[Cap]Info, len(catalogue))
	for _, info := range catalogue {
		m[info.Cap] = info
	}
	return m
}()

// All returns the vocabulary in display order.
func All() []Info { return slices.Clone(catalogue) }

// Lookup returns the metadata for a capability.
func Lookup(c Cap) (Info, bool) {
	info, ok := byCap[c]
	return info, ok
}

// Valid reports whether a capability is one this build knows. A stored role may
// name one this build does not — a downgrade, or a hand-edited file — and the
// only safe reading of an unknown capability is that it grants nothing.
func Valid(c Cap) bool {
	_, ok := byCap[c]
	return ok
}

// CapSignedIn is not something an operator can grant. It marks the handful of
// routes every signed-in user reaches whatever their role: reading their own
// account, signing out, changing their own password, managing their own paired
// devices.
//
// It is deliberately outside the catalogue, so it can never be typed into a
// role and Valid never accepts it. A route declaring it is making a claim —
// "this one is everybody's" — which is exactly the kind of thing that should
// have to be written down rather than left as an empty field.
const CapSignedIn Cap = "signed-in"
