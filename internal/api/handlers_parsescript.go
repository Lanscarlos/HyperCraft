package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/launchscript"
)

// Reading somebody's start script for the launch settings inside it.
//
// A server that has been run by hand for a year has its arguments written down
// already — in run.sh, in 启动.sh, in whatever the last panel left behind. The
// panel does not run those scripts, so the settings in them would otherwise
// have to be copied into the form by hand, one flag at a time.
//
// So it reads them instead. What comes back is a draft: the operator sees
// every field before anything is saved, and can change any of them. That is
// what makes a static parse good enough — a wrong guess is visible in the
// preview rather than silently stored, and the script is never consulted
// again afterwards.
//
// Two routes because there are two moments. Importing a directory has no
// instance to resolve a path against, so it takes an absolute one and is
// guarded like the rest of the host filesystem; an instance that already
// exists is confined to its own directory like every other file read.

type parseScriptRequest struct {
	Path string `json:"path"`
}

// launchDraft is a parsed launch, in the shape the launch settings form takes.
type launchDraft struct {
	Java string `json:"java"`
	// JavaVar names the environment variable the script expected to supply the
	// JVM. When set, Java is empty and the operator has to choose one: the
	// panel does not read its own environment for this, because the answer
	// would be right on the machine that exports it and wrong everywhere else.
	JavaVar     string   `json:"javaVar,omitempty"`
	MinMemoryMB int      `json:"minMemoryMB"`
	MaxMemoryMB int      `json:"maxMemoryMB"`
	JVMArgs     []string `json:"jvmArgs"`
	Jar         string   `json:"jar"`
	ArgFiles    []string `json:"argFiles"`
	ServerArgs  []string `json:"serverArgs"`
	// Javaw records that the script launched the console-less JVM, which the
	// panel cannot use: it needs the console.
	Javaw bool `json:"javaw,omitempty"`
	// Wrappers are what was stripped — exec, nohup, screen, a trailing &. The
	// preview says so rather than dropping them silently.
	Wrappers []string `json:"wrappers,omitempty"`
}

type parseScriptRefusal struct {
	Code   string `json:"code"`
	Reason string `json:"reason"`
	Line   int    `json:"line,omitempty"`
	Text   string `json:"text,omitempty"`
}

type parseScriptResponse struct {
	// OK is false when the script could not be taken apart with certainty. The
	// draft is then meaningless and the refusals say why: half a command line
	// is worse than none, because it starts.
	OK       bool                 `json:"ok"`
	Script   string               `json:"script"`
	Draft    launchDraft          `json:"draft"`
	Refusals []parseScriptRefusal `json:"refusals"`
}

// handleParseHostScript reads a script by absolute path, for 导入现有目录.
func (s *Server) handleParseHostScript(w http.ResponseWriter, r *http.Request) {
	var req parseScriptRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	path := strings.TrimSpace(req.Path)
	if path == "" || !filepath.IsAbs(path) {
		writeError(w, http.StatusBadRequest, "path 必须是绝对路径")
		return
	}

	text, ok := readScriptFile(w, path)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, parseScript(filepath.Base(path), path, text))
}

// handleParseInstanceScript reads a script from inside one instance.
func (s *Server) handleParseInstanceScript(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	var req parseScriptRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	// Confined exactly like the file manager: this reads a file out of the
	// server directory and echoes lines of it back, so it is governed by the
	// rule that governs every other read there, not by a looser one.
	text, err := s.browserFor(r, inst).ReadText(strings.TrimSpace(req.Path))
	if err != nil {
		s.writeFileError(w, err)
		return
	}
	if strings.ContainsRune(text, 0) {
		writeError(w, http.StatusBadRequest, "这不是一个文本文件，读不出启动参数")
		return
	}
	writeJSON(w, http.StatusOK, parseScript(req.Path, req.Path, text))
}

// readScriptFile reads a host path, answering the two ways it can fail.
func readScriptFile(w http.ResponseWriter, path string) (string, bool) {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		writeError(w, http.StatusNotFound, "找不到 "+path)
		return "", false
	case err != nil:
		writeError(w, http.StatusForbidden, "读不了 "+path)
		return "", false
	case info.IsDir():
		writeError(w, http.StatusBadRequest, path+" 是个目录")
		return "", false
	case info.Size() > maxScriptBytes:
		writeError(w, http.StatusBadRequest, "这个文件太大，不像启动脚本")
		return "", false
	}

	data, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusForbidden, "读不了 "+path)
		return "", false
	}
	if strings.ContainsRune(string(data), 0) {
		// A Bedrock binary is also a file with a plausible name.
		writeError(w, http.StatusBadRequest, "这不是一个文本文件，读不出启动参数")
		return "", false
	}
	return string(data), true
}

// maxScriptBytes is well past any real start script and well short of anything
// worth loading into memory to find out it is not one.
const maxScriptBytes = 1 << 20

// parseScript runs the parser and shapes its two possible answers.
func parseScript(name, script, text string) parseScriptResponse {
	dialect := launchscript.Shell
	if ext := strings.ToLower(filepath.Ext(name)); ext == ".bat" || ext == ".cmd" {
		dialect = launchscript.Batch
	}

	result, refusals := launchscript.Parse(text, dialect)
	out := parseScriptResponse{
		Script:   script,
		OK:       len(refusals) == 0,
		Refusals: make([]parseScriptRefusal, 0, len(refusals)),
	}
	for _, refusal := range refusals {
		out.Refusals = append(out.Refusals, parseScriptRefusal{
			Code: refusal.Code, Reason: refusal.Reason, Line: refusal.Line, Text: refusal.Text,
		})
	}
	if !out.OK {
		return out
	}

	out.Draft = launchDraft{
		Java:        result.Java,
		JavaVar:     result.JavaVar,
		MinMemoryMB: result.MinMemoryMB,
		MaxMemoryMB: result.MaxMemoryMB,
		JVMArgs:     orEmpty(result.JVMArgs),
		Jar:         result.Jar,
		ArgFiles:    orEmpty(result.ArgFiles),
		ServerArgs:  orEmpty(result.ServerArgs),
		Javaw:       result.Javaw,
		Wrappers:    orEmpty(result.Wrappers),
	}
	return out
}

// orEmpty keeps a nil slice out of the JSON, where it would arrive as null and
// make every consumer test for it.
func orEmpty(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
