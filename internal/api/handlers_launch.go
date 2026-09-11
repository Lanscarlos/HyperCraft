package api

import (
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/serverfiles"
)

// The launch check answers one question the console cannot: will this instance
// start, and will the panel still be holding it afterwards.
//
// The second half is the one worth an endpoint. A start script that puts the
// JVM in the background — nohup, screen, tmux, a systemctl call — starts the
// server perfectly well and hands the panel a process that exits within the
// second. The panel then reports "已停止, exit 0" for a server everyone can
// see is running, the console goes nowhere, 停止 does nothing, and AutoRestart
// cheerfully starts a second copy on the same world. Nothing in that chain
// reads as "your script backgrounds the JVM", and no amount of staring at the
// console reveals it.
//
// So this looks at the script before any of that happens and says so in
// advance. It cannot fix someone's script — it does not try — but the two
// mechanical failures around it, a missing file and a missing execute bit, it
// can name exactly and repair on request.

const (
	launchLevelFatal = "fatal"
	launchLevelWarn  = "warn"
	launchLevelInfo  = "info"
)

// launchFixChmod is the one repair the panel offers here.
const launchFixChmod = "chmod"

type launchIssue struct {
	Level   string `json:"level"`
	Code    string `json:"code"`
	Message string `json:"message"`
	// Detail is the offending line, verbatim, so the operator can find it in
	// their own file instead of taking the panel's word for it.
	Detail string `json:"detail,omitempty"`
	Line   int    `json:"line,omitempty"`
	// Fix names a repair the panel can carry out; empty when there is none.
	Fix string `json:"fix,omitempty"`
}

type launchCheckResponse struct {
	// Mode is "jar" when the panel builds the command line, "script" when the
	// operator's own does.
	Mode    string   `json:"mode"`
	Command []string `json:"command,omitempty"`
	// Script is the instance-relative path of the file that was read, empty
	// when nothing in the command line turned out to be one.
	Script string        `json:"script,omitempty"`
	Issues []launchIssue `json:"issues"`
}

func (s *Server) handleLaunchCheck(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.checkLaunch(inst))
}

type launchFixRequest struct {
	Action string `json:"action"`
}

func (s *Server) handleLaunchFix(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	var req launchFixRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if strings.TrimSpace(req.Action) != launchFixChmod {
		writeError(w, http.StatusBadRequest, "未知的修复动作")
		return
	}

	cfg := inst.Config()
	target, kind := resolveExecutable(cfg)
	if kind != execInsideInstance {
		// Only ever inside the instance directory. The panel is not in the
		// business of changing permissions on /usr/bin/java, and a file it
		// cannot find is not one to change the permissions of either.
		reason := "只能给实例目录里的文件加执行位"
		if kind == execNotFound {
			reason = "找不到 " + target + "，没什么可以加执行位的"
		}
		writeError(w, http.StatusConflict, reason)
		return
	}
	if err := s.browserFor(inst).MakeExecutable(target); err != nil {
		s.writeFileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.checkLaunch(inst))
}

// Where Command[0] was found, which decides both the diagnosis and whether the
// panel may touch it.
type execKind int

const (
	execNotFound execKind = iota
	execInsideInstance
	execOnHost // an absolute path or something on PATH: /bin/sh, java
)

// resolveExecutable works out what Command[0] names. The instance-relative
// path is returned for the inside case, since that is what the file browser
// speaks.
func resolveExecutable(cfg instance.Config) (string, execKind) {
	if len(cfg.Command) == 0 {
		return "", execNotFound
	}
	bin := strings.TrimSpace(cfg.Command[0])
	if bin == "" {
		return "", execNotFound
	}

	if filepath.IsAbs(bin) {
		if info, err := os.Stat(bin); err == nil && !info.IsDir() {
			return bin, execOnHost
		}
		return bin, execNotFound
	}

	// The order here is os/exec's, not the one that reads most naturally. A
	// name with no separator never means "the file in the working directory":
	// exec.Command hands it to LookPath, which searches the panel's own PATH
	// and nothing else. So "run.sh" fails for a run.sh sitting right there,
	// and it is "./run.sh" that works.
	if !strings.ContainsAny(bin, `/\`) {
		if _, err := exec.LookPath(bin); err == nil {
			return bin, execOnHost
		}
		return bin, execNotFound
	}

	// With a separator it is a path, resolved against Dir — the instance
	// directory — when the process is started.
	rel := filepath.ToSlash(filepath.Clean(bin))
	if !strings.HasPrefix(rel, "../") && rel != ".." {
		if info, err := os.Stat(filepath.Join(cfg.Directory, rel)); err == nil && !info.IsDir() {
			return rel, execInsideInstance
		}
	}
	return bin, execNotFound
}

func (s *Server) checkLaunch(inst *instance.Instance) launchCheckResponse {
	cfg := inst.Config()
	if !cfg.UsesScript() {
		return launchCheckResponse{Mode: "jar", Issues: s.checkJarLaunch(inst, cfg)}
	}

	out := launchCheckResponse{Mode: "script", Command: cfg.Command, Issues: []launchIssue{}}
	browser := s.browserFor(inst)

	target, kind := resolveExecutable(cfg)
	switch kind {
	case execNotFound:
		out.Issues = append(out.Issues, missingExecutableIssue(cfg, browser, target))
	case execInsideInstance:
		if mode, err := browser.Mode(target); err == nil && mode&0o111 == 0 {
			out.Issues = append(out.Issues, launchIssue{
				Level:   launchLevelFatal,
				Code:    "not-executable",
				Message: target + " 没有执行位，开服会直接得到 permission denied。上传的压缩包和从 Windows 拷过来的文件都会丢掉这一位。",
				Fix:     launchFixChmod,
			})
		}
	}

	// The file to read is not always Command[0]: "/bin/sh start.sh" puts the
	// interpreter first and the script second.
	if script, text, ok := readLaunchScript(cfg, browser); ok {
		out.Script = script
		out.Issues = append(out.Issues, scanScript(text)...)
	}

	out.Issues = append(out.Issues, s.checkLoaderKnown(inst, cfg)...)
	return out
}

// missingExecutableIssue distinguishes "there is no such file" from the one
// mistake that looks identical and is not: naming a script that is right there
// in the directory, without the "./" that makes it a path instead of a PATH
// lookup.
func missingExecutableIssue(cfg instance.Config, browser *serverfiles.Browser, bin string) launchIssue {
	if !filepath.IsAbs(bin) && !strings.ContainsAny(bin, `/\`) {
		if _, err := browser.Stat(bin); err == nil {
			return launchIssue{
				Level: launchLevelFatal,
				Code:  "needs-dot-slash",
				Message: "目录里有 " + bin + "，但第一行得写成 ./" + bin +
					"。不带 ./ 的名字会被当成 PATH 里的命令去找，而不是这个目录里的文件。",
			}
		}
	}
	return launchIssue{
		Level:   launchLevelFatal,
		Code:    "not-found",
		Message: "找不到 " + bin + "。启动命令的第一行是可执行文件，相对路径从实例目录算起。",
	}
}

// readLaunchScript finds the first element of the command line that is a
// readable text file inside the instance directory.
func readLaunchScript(cfg instance.Config, browser *serverfiles.Browser) (string, string, bool) {
	for _, arg := range cfg.Command {
		arg = strings.TrimSpace(arg)
		if arg == "" || strings.HasPrefix(arg, "-") {
			continue
		}
		rel := arg
		if filepath.IsAbs(arg) {
			// An absolute path that happens to point back into the instance
			// directory is still this instance's script.
			inside, err := filepath.Rel(cfg.Directory, arg)
			if err != nil || strings.HasPrefix(inside, "..") {
				continue
			}
			rel = inside
		}
		rel = filepath.ToSlash(filepath.Clean(rel))
		if strings.HasPrefix(rel, "../") {
			continue
		}
		text, err := browser.ReadText(rel)
		if err != nil {
			continue
		}
		// A jar or any other binary read as text is not a script; the check
		// below is cheap and keeps the scanner off megabytes of noise.
		if strings.ContainsRune(text, 0) {
			continue
		}
		return rel, text, true
	}
	return "", "", false
}

// backgrounding are the ways a script hands the JVM to something that is not
// the panel. Each is named with what it breaks, because "不推荐" teaches
// nobody anything.
var backgrounding = []struct {
	needle  string
	message string
}{
	{"nohup", "nohup 会让 JVM 脱离面板：脚本秒退，面板显示「已停止」，而服务器还在跑。之后控制台发不出命令，停止按钮也停不掉它。"},
	{"screen", "在 screen 里开服，面板拿到的是 screen 的进程：控制台和停止按钮都会落空，日志也进不来。"},
	{"tmux", "在 tmux 里开服，面板拿到的是 tmux 的进程：控制台和停止按钮都会落空，日志也进不来。"},
	{"systemctl", "systemctl 把服务端交给 systemd 管，那面板这边就只是个空壳——两边都以为自己在管这台服。"},
	{"start-stop-daemon", "start-stop-daemon 会把进程转到后台，面板会立刻认为它退出了。"},
	{"docker", "在脚本里起容器，面板管的是 docker 命令而不是服务端；容器停了面板不知道，面板停了容器还在。"},
}

// scanScript reads a start script for the things that would take the server
// out of the panel's hands.
//
// Deliberately shallow: this is a text scan, not a shell parser, and it says
// so by quoting the line it objects to rather than claiming to understand it.
// A false positive costs one warning the operator can read and dismiss; a
// missed nohup costs an afternoon.
func scanScript(text string) []launchIssue {
	issues := make([]launchIssue, 0)
	seen := make(map[string]bool)
	sawExec := false
	javaLine := 0
	javaText := ""

	for number, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "::") {
			continue
		}
		lower := strings.ToLower(line)

		for _, pattern := range backgrounding {
			if seen[pattern.needle] || !strings.Contains(lower, pattern.needle) {
				continue
			}
			seen[pattern.needle] = true
			issues = append(issues, launchIssue{
				Level:   launchLevelWarn,
				Code:    "background-" + pattern.needle,
				Message: pattern.message,
				Detail:  line,
				Line:    number + 1,
			})
		}

		// A trailing & is the same problem without a keyword to name it.
		// "&&" is not it, and neither is a & inside a quoted string, which is
		// why only a line ending in a lone & counts.
		if strings.HasSuffix(line, "&") && !strings.HasSuffix(line, "&&") && !seen["&"] {
			seen["&"] = true
			issues = append(issues, launchIssue{
				Level:   launchLevelWarn,
				Code:    "background-amp",
				Message: "行尾的 & 会把这条命令放到后台，脚本随即结束，面板会认为服务器已经退出。",
				Detail:  line,
				Line:    number + 1,
			})
		}

		if slices.Contains(strings.Fields(lower), "exec") {
			sawExec = true
		}
		if javaLine == 0 && looksLikeJavaCall(lower) {
			javaLine, javaText = number+1, line
		}
	}

	if javaLine > 0 && !sawExec && len(issues) == 0 {
		issues = append(issues, launchIssue{
			Level: launchLevelInfo,
			Code:  "no-exec",
			Message: "脚本没有用 exec 启动 JVM，面板管到的是外层 shell。这样也能跑——" +
				"停止和监控都按进程树处理——但写成 exec java … 之后进程就是 JVM 本身，少一层中转。",
			Detail: javaText,
			Line:   javaLine,
		})
	}
	return issues
}

// looksLikeJavaCall reports whether a line starts the JVM. Only the start of
// the command counts: JAVA_HOME=… on its own line is configuration, not a
// launch.
func looksLikeJavaCall(lower string) bool {
	fields := strings.Fields(lower)
	for _, field := range fields {
		if strings.Contains(field, "=") {
			continue // leading VAR=value assignments
		}
		base := field
		if index := strings.LastIndexAny(base, `/\`); index >= 0 {
			base = base[index+1:]
		}
		base = strings.Trim(base, `"'`)
		return base == "java" || base == "java.exe" || base == "javaw" || base == "javaw.exe"
	}
	return false
}

// checkLoaderKnown warns when the panel cannot tell what server software this
// is, because the consequence is silent and specific: mods get installed into
// plugins/, where no mod loader will ever look at them.
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
		Message: "面板认不出这是什么服务端——脚本启动没有 jar 名可读，version_history.json 也只有 Paper 系才会写。" +
			"结果是 mod 会被装进 plugins/ 而不是 mods/，插件市场里每一条也都标成「未知」。在上面的「服务端类型」里选一下就好了。",
	}}
}

// checkJarLaunch is the same question for the mode the panel does build the
// command line for, where there is exactly one way to get it wrong.
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
		if _, err := s.browserFor(inst).Stat(jar); err != nil {
			level, message := launchLevelFatal, "目录里没有 "+jar+"。"
			if errors.Is(err, serverfiles.ErrNotFound) && !directoryExists(cfg.Directory) {
				message = "实例目录还不存在，开服时会创建；里面也还没有 " + jar + "。"
			}
			issues = append(issues, launchIssue{Level: level, Code: "jar-missing", Message: message})
		}
	}
	return append(issues, s.checkLoaderKnown(inst, cfg)...)
}

func directoryExists(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}
