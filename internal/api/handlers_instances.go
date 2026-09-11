package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/plugin"
)

// instanceRequest is the editable subset of instance.Config. ID and CreatedAt
// are server-owned and deliberately absent.
type instanceRequest struct {
	Name      string `json:"name"`
	Directory string `json:"directory"`
	// Kind is "server" or "proxy". Blank is not "server" here: on update it
	// means "leave it alone", so a client that saves the launch settings
	// without echoing the field back cannot turn a proxy into a server.
	Kind string `json:"kind"`
	// Loader and GameVersion are what the operator says this server is, for
	// the cases nothing on disk can answer — chiefly a script-launched Forge
	// server, which has no jar to read a name off.
	Loader          string   `json:"loader"`
	GameVersion     string   `json:"gameVersion"`
	Java            string   `json:"java"`
	Jar             string   `json:"jar"`
	MinMemoryMB     int      `json:"minMemoryMB"`
	MaxMemoryMB     int      `json:"maxMemoryMB"`
	JVMArgs         []string `json:"jvmArgs"`
	ServerArgs      []string `json:"serverArgs"`
	Command         []string `json:"command"`
	Encoding        string   `json:"encoding"`
	TTY             *bool    `json:"tty"`
	ForceColor      *bool    `json:"forceColor"`
	JavaToolOptions *bool    `json:"javaToolOptions"`
	AutoStart       bool     `json:"autoStart"`
	AutoRestart     bool     `json:"autoRestart"`
	StopCommand     string   `json:"stopCommand"`
	StopTimeoutSec  int      `json:"stopTimeoutSec"`
}

// The two halves of instanceRequest.
//
// One route carries both "rename this server" and "run this command instead",
// so CapInstanceLaunch is checked against the fields present in the body rather
// than against the route — see routes.go. Splitting the route instead would
// break every client that saves the whole form at once.
//
// They are written out rather than derived because the question each field
// answers is "does setting this decide what code runs", and only a person can
// answer it. TestInstanceRequestFieldsAreClassified fails when a new field
// belongs to neither list, which is the moment whoever added it knows.
var (
	// launchFields decide what the machine executes: an argv, the program that
	// runs it, or the directory it runs in. Every one of them is a way to run
	// arbitrary code as the account the panel runs as.
	launchFields = []string{"directory", "java", "jar", "jvmArgs", "serverArgs", "command"}

	// settingsFields are the rest: labels, console behaviour, and what to do
	// when the server stops. None of them changes what runs.
	settingsFields = []string{
		"name", "kind", "loader", "gameVersion", "encoding", "tty", "forceColor",
		"javaToolOptions", "autoStart", "autoRestart", "stopCommand", "stopTimeoutSec",
		"minMemoryMB", "maxMemoryMB",
	}
)

func (req instanceRequest) toConfig() instance.Config {
	return instance.Config{
		Name:      strings.TrimSpace(req.Name),
		Directory: strings.TrimSpace(req.Directory),
		Kind:      strings.TrimSpace(req.Kind),
		// Normalised here rather than trusted: the field is read back by
		// plugin.Judge, which only knows one spelling of each loader.
		Loader:      plugin.NormaliseLoader(req.Loader),
		GameVersion: strings.TrimSpace(req.GameVersion),
		Java:        strings.TrimSpace(req.Java),
		Jar:         strings.TrimSpace(req.Jar),
		MinMemoryMB: req.MinMemoryMB,
		MaxMemoryMB: req.MaxMemoryMB,
		JVMArgs:     cleanArgs(req.JVMArgs),
		ServerArgs:  cleanArgs(req.ServerArgs),
		Command:     cleanArgs(req.Command),
		Encoding:    strings.TrimSpace(req.Encoding),
		// Absent means "unset" for both of these: applyDefaults turns them on
		// rather than silently taking Go's zero value for a bool.
		TTY:             req.TTY,
		ForceColor:      req.ForceColor,
		JavaToolOptions: req.JavaToolOptions,
		AutoStart:       req.AutoStart,
		AutoRestart:     req.AutoRestart,
		StopCommand:     strings.TrimSpace(req.StopCommand),
		StopTimeoutSec:  req.StopTimeoutSec,
	}
}

// cleanArgs drops blank entries so an empty textarea line does not become an
// empty argv element, which some launchers choke on.
func cleanArgs(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, arg := range in {
		if arg = strings.TrimSpace(arg); arg != "" {
			out = append(out, arg)
		}
	}
	return out
}

func (s *Server) handleListInstances(w http.ResponseWriter, _ *http.Request) {
	instances := s.mgr.List()
	out := make([]instance.Status, 0, len(instances))
	for _, inst := range instances {
		out = append(out, inst.Status())
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetInstance(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, inst.Status())
}

func (s *Server) handleCreateInstance(w http.ResponseWriter, r *http.Request) {
	var req instanceRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	inst, err := s.mgr.Create(req.toConfig())
	if err != nil {
		s.writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, inst.Status())
}

func (s *Server) handleUpdateInstance(w http.ResponseWriter, r *http.Request) {
	var req instanceRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	cfg := req.toConfig()
	// Whether this instance is a proxy is decided when it is created and when
	// a core is applied to it, not by whoever last saved the launch settings.
	// An omitted kind keeps the one on record.
	if cfg.Kind == "" {
		if current, err := s.mgr.Get(r.PathValue("id")); err == nil {
			cfg.Kind = current.Config().Kind
		}
	}

	inst, err := s.mgr.Update(r.PathValue("id"), cfg)
	if err != nil {
		s.writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, inst.Status())
}

func (s *Server) handleDeleteInstance(w http.ResponseWriter, r *http.Request) {
	// Removing the world files is destructive and irreversible, so it only
	// happens when the caller opts in explicitly.
	deleteFiles := r.URL.Query().Get("deleteFiles") == "true"

	id := r.PathValue("id")
	if err := s.mgr.Delete(id, deleteFiles); err != nil {
		s.writeDomainError(w, err)
		return
	}
	// The plugin records describe an instance that no longer exists. Dropping
	// them after the delete succeeded — never before — means a refused delete
	// leaves the instance exactly as it was, plugins included.
	if s.instancePlugins != nil {
		if err := s.instancePlugins.Forget(id); err != nil {
			s.log.Warn("could not drop the instance's plugin records", "instance", id, "err", err)
		}
		// The upgrade snapshots go with them. They are full copies of jars and
		// config for a server that no longer exists, and nothing left could
		// ever restore them.
		s.instancePlugins.ForgetBackups(id)
	}
	// The config history goes too, and the delete dialog says so before it is
	// clicked — see the design's §9. Same ordering rule as the plugin records:
	// only once the delete itself has succeeded.
	if s.configHistory != nil {
		if err := s.configHistory.Forget(id); err != nil {
			s.log.Warn("could not remove the instance's config history", "instance", id, "err", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

type powerAction int

const (
	powerStart powerAction = iota
	powerStop
	powerRestart
	powerKill
)

func (s *Server) handlePower(action powerAction) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inst, ok := s.instanceFromPath(w, r)
		if !ok {
			return
		}
		// An update stops every server and then replaces the panel's own
		// binary; a server started in that window would be counted as down,
		// left out of the resume list, and killed by the restart moments later.
		if (action == powerStart || action == powerRestart) && s.updater != nil && s.updater.Applying() {
			writeError(w, http.StatusConflict, "面板正在更新，服务器会在更新完成后自动恢复运行")
			return
		}

		var err error
		switch action {
		case powerStart:
			err = inst.Start()
		case powerStop:
			err = inst.Stop()
		case powerRestart:
			err = inst.Restart()
		case powerKill:
			err = inst.Kill()
		}
		if err != nil {
			s.writeDomainError(w, err)
			return
		}
		if action == powerStart || action == powerRestart {
			s.reconcileInBackground(inst.Config().ID, inst.Config().Directory, inst.Config().Name)
		}
		writeJSON(w, http.StatusAccepted, inst.Status())
	}
}

// reconcileInBackground compares the ledger with the directory a server is
// about to read.
//
// A start is one of the three moments the answer can have changed — the other
// two are an operator asking and an upgrade finishing — and it is the moment
// that catches what happened while the panel was not looking: an SFTP upload
// last night, a restored backup, a jar deleted by hand. Off the request,
// because it reads every jar on the server end to end and a start button that
// waited for that would feel broken on a server with forty plugins.
func (s *Server) reconcileInBackground(id, directory, name string) {
	if s.instancePlugins == nil {
		return
	}
	go func() {
		report, err := s.instancePlugins.Reconcile(id, directory)
		switch {
		case err != nil:
			s.log.Warn("could not reconcile plugins at startup", "instance", name, "err", err)
		case !report.Clean():
			s.log.Info("plugin ledger and directory disagree", "instance", name,
				"drift", report.Drift, "missing", report.Missing, "foreign", report.Foreign)
		}
	}()
}

type commandRequest struct {
	Command string `json:"command"`
}

func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}

	var req commandRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if err := inst.SendCommand(req.Command); err != nil {
		s.writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type logsResponse struct {
	Lines   []instance.Line `json:"lines"`
	LastSeq uint64          `json:"lastSeq"`
}

// handleLogs serves the scrollback buffer. The websocket is the normal path;
// this exists for reconnects that need to fill a gap and for scripting.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}

	var since uint64
	if raw := r.URL.Query().Get("since"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "since must be a number")
			return
		}
		since = parsed
	}

	lines := inst.LinesSince(since)
	last := since
	if n := len(lines); n > 0 {
		last = lines[n-1].Seq
	}
	writeJSON(w, http.StatusOK, logsResponse{Lines: lines, LastSeq: last})
}
