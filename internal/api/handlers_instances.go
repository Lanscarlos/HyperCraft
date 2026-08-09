package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/instance"
)

// instanceRequest is the editable subset of instance.Config. ID and CreatedAt
// are server-owned and deliberately absent.
type instanceRequest struct {
	Name           string   `json:"name"`
	Directory      string   `json:"directory"`
	Java           string   `json:"java"`
	Jar            string   `json:"jar"`
	MinMemoryMB    int      `json:"minMemoryMB"`
	MaxMemoryMB    int      `json:"maxMemoryMB"`
	JVMArgs        []string `json:"jvmArgs"`
	ServerArgs     []string `json:"serverArgs"`
	Command        []string `json:"command"`
	Encoding       string   `json:"encoding"`
	ForceColor     *bool    `json:"forceColor"`
	AutoStart      bool     `json:"autoStart"`
	AutoRestart    bool     `json:"autoRestart"`
	StopCommand    string   `json:"stopCommand"`
	StopTimeoutSec int      `json:"stopTimeoutSec"`
}

func (req instanceRequest) toConfig() instance.Config {
	return instance.Config{
		Name:        strings.TrimSpace(req.Name),
		Directory:   strings.TrimSpace(req.Directory),
		Java:        strings.TrimSpace(req.Java),
		Jar:         strings.TrimSpace(req.Jar),
		MinMemoryMB: req.MinMemoryMB,
		MaxMemoryMB: req.MaxMemoryMB,
		JVMArgs:     cleanArgs(req.JVMArgs),
		ServerArgs:  cleanArgs(req.ServerArgs),
		Command:     cleanArgs(req.Command),
		Encoding:    strings.TrimSpace(req.Encoding),
		// Absent means "unset": applyDefaults turns it into colours-on rather
		// than silently taking Go's zero value for a bool.
		ForceColor:     req.ForceColor,
		AutoStart:      req.AutoStart,
		AutoRestart:    req.AutoRestart,
		StopCommand:    strings.TrimSpace(req.StopCommand),
		StopTimeoutSec: req.StopTimeoutSec,
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

	inst, err := s.mgr.Update(r.PathValue("id"), req.toConfig())
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

	if err := s.mgr.Delete(r.PathValue("id"), deleteFiles); err != nil {
		s.writeDomainError(w, err)
		return
	}
	if s.jars != nil {
		// Stop a core download that is still writing into a directory nobody
		// owns any more, and drop the finished-job record with it.
		s.jars.Forget(r.PathValue("id"))
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
		writeJSON(w, http.StatusAccepted, inst.Status())
	}
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
