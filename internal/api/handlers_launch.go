package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/mcprops"
	"github.com/lanscarlos/hypercraft/internal/plugin"
	"github.com/lanscarlos/hypercraft/internal/serverfiles"
)

// The launch check answers one question the console cannot: will this instance
// start, and is what it needs actually where the config says it is.
//
// It used to answer a second and more valuable one — whether the panel would
// still be holding the server afterwards. That question existed because the
// panel executed the operator's own start script, and a script that puts the
// JVM in the background (nohup, screen, tmux, a systemctl call) starts the
// server perfectly well while handing the panel a process that exits within
// the second. The panel would then report "已停止, exit 0" for a server
// everyone could see running, the console went nowhere, and AutoRestart
// cheerfully started a second copy on the same world.
//
// The panel no longer runs anybody's script: it reads one for the launch
// settings inside it and builds the command line itself. So that entire class
// of failure is gone rather than warned about, and what is left here is the
// mechanical half — a launch target that is not there, and a loader the panel
// could not identify.

const (
	launchLevelFatal = "fatal"
	launchLevelWarn  = "warn"
	// What the panel checked and found in order. Shown rather than dropped:
	// a check panel that lists only problems is indistinguishable from one
	// that never ran, and "端口没被占用" is exactly the reassurance somebody
	// opens this page for.
	launchLevelOK = "ok"
)

// launchFix is a change the form can apply on the reader's behalf.
//
// Patch is a loose map so a new check can propose a new field without the
// browser learning anything about it: the page merges whatever arrives into
// its draft, which keeps every "and here is the button that fixes it" on this
// side of the wire.
type launchFix struct {
	Label string         `json:"label"`
	Patch map[string]any `json:"patch"`
}

type launchIssue struct {
	Level   string     `json:"level"`
	Code    string     `json:"code"`
	Message string     `json:"message"`
	Fix     *launchFix `json:"fix,omitempty"`
}

type launchCheckResponse struct {
	// Mode is "jar" or "argfile", the two shapes of command line the panel
	// builds. The UI branches on it: in argfile mode the heap is not on the
	// command line at all.
	Mode   string        `json:"mode"`
	Issues []launchIssue `json:"issues"`
}

func (s *Server) handleLaunchCheck(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.checkLaunch(inst))
}

func (s *Server) checkLaunch(inst *instance.Instance) launchCheckResponse {
	cfg := inst.Config()
	return launchCheckResponse{Mode: launchMode(cfg), Issues: s.checkLaunchConfig(inst, cfg)}
}

// checkLaunchConfig is the check against a config that may not be saved yet.
//
// Split from checkLaunch so the launch settings page can ask about the draft
// in its form rather than about what is on disk. A page that showed a command
// built from the draft next to problems found in the stored config would
// disagree with itself on screen, which is worse than either half alone.
func (s *Server) checkLaunchConfig(inst *instance.Instance, cfg instance.Config) []launchIssue {
	if cfg.NeedsLaunchSetup {
		return []launchIssue{{
			Level: launchLevelFatal,
			Code:  "needs-setup",
			Message: "这个实例原来由它自己的启动脚本启动，面板已经不再执行脚本，也没能从那个脚本里" +
				"拆出启动参数。在上面指定核心和参数之后它才能开起来——原来的启动命令就列在下面，供对照。",
		}}
	}
	if len(cfg.ArgFiles) > 0 {
		return s.checkArgFileLaunch(inst, cfg)
	}
	return s.checkJarLaunch(inst, cfg)
}

// launchPreviewRequest is the half of the config the 启动方式 page owns.
//
// Deliberately not instanceRequest: the console encoding and the process
// behaviour moved to the other settings page, and a page that cannot edit a
// field has no business asserting a value for it. Everything absent here is
// read off what is stored.
type launchPreviewRequest struct {
	Java        string   `json:"java"`
	Jar         string   `json:"jar"`
	ArgFiles    []string `json:"argFiles"`
	MinMemoryMB int      `json:"minMemoryMB"`
	MaxMemoryMB int      `json:"maxMemoryMB"`
	JVMArgs     []string `json:"jvmArgs"`
	ServerArgs  []string `json:"serverArgs"`
	Loader      string   `json:"loader"`
}

// overlay puts the draft on top of what is on record.
func (req launchPreviewRequest) overlay(stored instance.Config) instance.Config {
	cfg := stored
	cfg.Java = strings.TrimSpace(req.Java)
	cfg.Jar = strings.TrimSpace(req.Jar)
	cfg.ArgFiles = cleanArgs(req.ArgFiles)
	cfg.MinMemoryMB = req.MinMemoryMB
	cfg.MaxMemoryMB = req.MaxMemoryMB
	cfg.JVMArgs = cleanArgs(req.JVMArgs)
	cfg.ServerArgs = cleanArgs(req.ServerArgs)
	if loader := strings.TrimSpace(req.Loader); loader != "" {
		cfg.Loader = plugin.NormaliseLoader(loader)
	}
	return cfg
}

type launchPreviewResponse struct {
	Mode     string             `json:"mode"`
	Program  string             `json:"program"`
	Segments []instance.Segment `json:"segments"`
	Issues   []launchIssue      `json:"issues"`
}

// handleLaunchPreview answers "what exactly will you run, and what do you
// already know is wrong with it" for settings nobody has saved yet.
//
// One endpoint for both because they are one answer. It writes nothing: the
// draft is echoed back with the panel's own additions made visible, which is
// the whole reason the page can claim the command it shows is the real one.
func (s *Server) handleLaunchPreview(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	var req launchPreviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	cfg := req.overlay(inst.Config())
	resp := launchPreviewResponse{
		Mode:     launchMode(cfg),
		Segments: []instance.Segment{},
		Issues:   s.checkLaunchConfig(inst, cfg),
	}
	// A config with no launch target cannot produce a command line, and that
	// is not an error to report twice: the issues already say so.
	if program, segments, err := cfg.PreviewSegments(); err == nil {
		resp.Program = program
		resp.Segments = segments
	}
	writeJSON(w, http.StatusOK, resp)
}

// aikarStyle reports whether these flags are the preset that needs a fixed
// heap. G1NewSizePercent is the tell: it is in Aikar's set and in nothing a
// person types by hand.
func aikarStyle(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-XX:G1NewSizePercent") {
			return true
		}
	}
	return false
}

// heapIssues reports on -Xms against -Xmx, but only where the flags in use
// actually care. Plenty of servers run a small Xms on purpose.
//
// This used to be a warning banner living inside the JVM arguments card. As a
// banner it was there whether or not it applied to you; as a check it appears
// only when it bites, and it can carry the button that fixes it.
func heapIssues(cfg instance.Config) []launchIssue {
	if !aikarStyle(cfg.JVMArgs) || cfg.MaxMemoryMB <= 0 {
		return nil
	}
	if cfg.MinMemoryMB == cfg.MaxMemoryMB {
		return []launchIssue{{
			Level:   launchLevelOK,
			Code:    "heap-mismatch",
			Message: fmt.Sprintf("最小和最大内存都是 %d MB，符合这套参数的前提。", cfg.MaxMemoryMB),
		}}
	}
	return []launchIssue{{
		Level: launchLevelWarn,
		Code:  "heap-mismatch",
		Message: fmt.Sprintf(
			"这套参数的前提是最小和最大内存一样大，现在是 %d / %d MB。堆区大小固定能避免运行中扩容造成的停顿。",
			cfg.MinMemoryMB, cfg.MaxMemoryMB),
		Fix: &launchFix{
			Label: fmt.Sprintf("把 Xms 改成 %d", cfg.MaxMemoryMB),
			Patch: map[string]any{"minMemoryMB": cfg.MaxMemoryMB},
		},
	}}
}

// jarCountIssue is the sentence that used to sit under the jar dropdown. It is
// a check and not a hint: "目录下找到 1 个 jar" is how the reader confirms the
// panel is looking in the directory they think it is.
func (s *Server) jarCountIssue(cfg instance.Config) []launchIssue {
	if cfg.Directory == "" {
		return nil
	}
	entries, err := unconfinedBrowser(cfg.Directory).List("")
	if err != nil {
		// The directory not being there yet is already reported by
		// missingFileIssues; saying it twice helps nobody.
		return nil
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir && strings.HasSuffix(strings.ToLower(entry.Name), ".jar") {
			count++
		}
	}
	if count == 0 {
		return []launchIssue{{
			Level:   launchLevelWarn,
			Code:    "jar-count",
			Message: "实例目录下没有 jar 文件。从核心库装一个，或者自己传一个进去。",
		}}
	}
	return []launchIssue{{
		Level:   launchLevelOK,
		Code:    "jar-count",
		Message: fmt.Sprintf("实例目录下找到 %d 个 jar 文件。", count),
	}}
}

// serverPort is what this server's own server.properties says it listens on.
//
// Empty means "not a fact yet": a server that has never run has no
// server.properties, and vanilla's 25565 is a default rather than a decision.
// Guessing it would flag every pair of fresh instances against each other.
func serverPort(cfg instance.Config) string {
	if cfg.Directory == "" {
		return ""
	}
	// Stat first: mcprops.Load answers a missing file with an empty one and no
	// error, which would turn "never run" into vanilla's default below and
	// flag every pair of fresh instances against each other.
	path := filepath.Join(cfg.Directory, "server.properties")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	file, err := mcprops.Load(path)
	if err != nil {
		return ""
	}
	value, ok := file.Get("server-port")
	if !ok {
		// The file exists and does not say, which is vanilla's default.
		return "25565"
	}
	return strings.TrimSpace(value)
}

// portIssue cross-checks this server's port against every other one's.
//
// Read-only on purpose: the port is edited in 服务器配置 and this page gets no
// field for it. Two servers on one port is a boot failure whose log message
// does not mention the other server, so it is worth saying here even though
// the fix is somewhere else.
//
// Proxies sit this out: their bind address is in velocity.toml, a different
// file with a different shape, and a check that silently only half-works is
// worse than one that is not offered.
func (s *Server) portIssue(self *instance.Instance, cfg instance.Config) []launchIssue {
	if cfg.IsProxy() {
		return nil
	}
	port := serverPort(cfg)
	if port == "" {
		return nil
	}
	// allInstances rather than visibleInstances, for the reason scope.go gives
	// for exactly this case: a port already taken is taken whoever took it,
	// and hiding a clash with a server the reader cannot see would just let
	// them break it. Naming it is the accepted cost.
	clashes := make([]string, 0)
	for _, other := range s.allInstances() {
		if other.ID() == self.ID() {
			continue
		}
		otherCfg := other.Config()
		if otherCfg.IsProxy() {
			continue
		}
		if serverPort(otherCfg) == port {
			clashes = append(clashes, otherCfg.Name)
		}
	}
	if len(clashes) > 0 {
		return []launchIssue{{
			Level:   launchLevelWarn,
			Code:    "port-conflict",
			Message: "端口 " + port + " 和这些实例撞了：" + strings.Join(clashes, "、") + "。在「服务器配置」里改端口。",
		}}
	}
	return []launchIssue{{
		Level:   launchLevelOK,
		Code:    "port-conflict",
		Message: "端口 " + port + " 没有和别的实例撞。",
	}}
}

func launchMode(cfg instance.Config) string {
	if len(cfg.ArgFiles) > 0 {
		return "argfile"
	}
	return "jar"
}

// checkJarLaunch is the one way to get the jar form wrong.
func (s *Server) checkJarLaunch(inst *instance.Instance, cfg instance.Config) []launchIssue {
	issues := make([]launchIssue, 0)
	switch jar := strings.TrimSpace(cfg.Jar); {
	case jar == "":
		issues = append(issues, launchIssue{
			Level:   launchLevelFatal,
			Code:    "jar-unset",
			Message: "还没有指定服务端 jar。从核心库装一个，或者把 jar 传进目录后在上面填它的文件名。",
		})
	default:
		issues = append(issues, missingFileIssues(cfg, "jar-missing", jar)...)
	}
	issues = append(issues, s.jarCountIssue(cfg)...)
	issues = append(issues, heapIssues(cfg)...)
	issues = append(issues, s.portIssue(inst, cfg)...)
	return append(issues, s.checkLoaderKnown(inst, cfg)...)
}

// checkArgFileLaunch is the same question for Forge and NeoForge, where the
// launch is a list of files the installer wrote rather than a jar. A modpack
// update that rewrites libraries/ renames the version directory with it, so
// the argfile the config names is a real thing to go missing.
func (s *Server) checkArgFileLaunch(inst *instance.Instance, cfg instance.Config) []launchIssue {
	issues := make([]launchIssue, 0)
	for _, file := range cfg.ArgFiles {
		issues = append(issues, missingFileIssues(cfg, "argfile-missing", file)...)
	}
	// No heap check here: in argfile mode the panel does not put -Xms/-Xmx on
	// the command line at all, so there is no mismatch of its making to
	// report. See Config.commandSegments.
	issues = append(issues, s.portIssue(inst, cfg)...)
	return append(issues, s.checkLoaderKnown(inst, cfg)...)
}

// missingFileIssues reports a launch target that is not where the config says.
func missingFileIssues(cfg instance.Config, code, path string) []launchIssue {
	// Unconfined on purpose: these sit at the instance root, and "is it there"
	// is a diagnostic rather than a read the file manager's rule governs.
	if _, err := unconfinedBrowser(cfg.Directory).Stat(path); err == nil {
		return nil
	} else if errors.Is(err, serverfiles.ErrNotFound) && !directoryExists(cfg.Directory) {
		return []launchIssue{{
			Level:   launchLevelFatal,
			Code:    code,
			Message: "实例目录还不存在，开服时会创建；里面也还没有 " + path + "。",
		}}
	}
	return []launchIssue{{
		Level:   launchLevelFatal,
		Code:    code,
		Message: "目录里没有 " + path + "。",
	}}
}

func (s *Server) checkLoaderKnown(inst *instance.Instance, cfg instance.Config) []launchIssue {
	if cfg.IsProxy() || cfg.Loader != "" {
		return nil
	}
	if target := s.detectTarget(cfg); target.Loader != "" {
		return nil
	}
	return []launchIssue{{
		Level: launchLevelWarn,
		Code:  "unknown-loader",
		Message: "面板认不出这是什么服务端——Forge 这类没有可读的 jar 名，version_history.json 也只有 Paper 系才会写。" +
			"结果是 mod 会被装进 plugins/ 而不是 mods/，插件市场里每一条也都标成「未知」。在上面的「服务端类型」里选一下就好了。",
	}}
}

func directoryExists(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}
