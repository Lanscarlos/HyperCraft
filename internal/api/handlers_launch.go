package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/instance"
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
