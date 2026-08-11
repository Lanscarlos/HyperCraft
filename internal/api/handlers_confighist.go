package api

import (
	"errors"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/confighist"
	"github.com/lanscarlos/hypercraft/internal/instance"
)

// The 配置历史 endpoints.
//
// Everything here is scoped to one instance and reads or writes exactly one
// thing: the timeline, one commit's change list, one file's diff, one restore.
// There is deliberately no endpoint that hands over the repository — the
// history holds the rcon password and every plugin token on the server, and
// the design's §7 makes "it cannot leave this machine in one piece" a property
// of the API rather than a policy someone has to remember.

// configHistoryOverview is everything the tab needs for its first paint.
type configHistoryOverview struct {
	// Available is false when the module cannot run for this instance at all —
	// it is not wired into this panel, or the instance shares its directory.
	Available bool   `json:"available"`
	Enabled   bool   `json:"enabled"`
	Reason    string `json:"reason,omitempty"`
	Running   bool   `json:"running"`

	Timeline []confighist.Snapshot       `json:"timeline"`
	Pending  []confighist.FileChange     `json:"pending"`
	Stats    confighist.Stats            `json:"stats"`
	Coverage confighist.Coverage         `json:"coverage"`
	Settings confighist.InstanceSettings `json:"settings"`
	// RepoPath is where the repository lives, shown in the footer and in the
	// delete confirmation. Read-only information: there is no download.
	RepoPath string `json:"repoPath"`
}

// configHistoryFor resolves the instance and checks the module can run for it.
func (s *Server) configHistoryFor(w http.ResponseWriter, r *http.Request) (*instance.Instance, bool) {
	if s.configHistory == nil {
		writeError(w, http.StatusServiceUnavailable, "这个面板没有启用配置历史")
		return nil, false
	}
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return nil, false
	}
	if reason := s.configHistoryBlocked(inst); reason != "" {
		writeError(w, http.StatusConflict, reason)
		return nil, false
	}
	return inst, true
}

// configHistoryBlocked reports why the module cannot run for an instance.
//
// One reason today, and it is the design's §10: two instances pointed at the
// same directory (or at one inside the other) would each record the other's
// edits and each try to restore over the other. Detecting it and switching the
// module off is the honest answer; pretending the history belongs to one of
// them is not.
func (s *Server) configHistoryBlocked(inst *instance.Instance) string {
	other, shared := s.mgr.DirectoryConflict(inst.ID())
	if !shared {
		return ""
	}
	return fmt.Sprintf("这个实例和「%s」共用同一个目录，配置历史无法分辨改动来自哪一台，已停用", other)
}

func (s *Server) handleConfigHistory(w http.ResponseWriter, r *http.Request) {
	if s.configHistory == nil {
		writeJSON(w, http.StatusOK, configHistoryOverview{})
		return
	}
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}

	cfg := inst.Config()
	out := configHistoryOverview{
		Available: true,
		Running:   inst.State().Running(),
		Settings:  s.configHistory.Settings(cfg.ID),
		RepoPath:  s.configHistory.ExportPath(cfg.ID),
	}
	out.Enabled = !out.Settings.Disabled
	if reason := s.configHistoryBlocked(inst); reason != "" {
		out.Available, out.Enabled, out.Reason = false, false, reason
		writeJSON(w, http.StatusOK, out)
		return
	}

	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	timeline, err := s.configHistory.History(cfg.ID, cfg.Directory, limit)
	if err != nil && !errors.Is(err, confighist.ErrNoHistory) {
		s.writeConfigHistoryError(w, err)
		return
	}
	out.Timeline = timeline

	if pending, err := s.configHistory.Pending(cfg.ID, cfg.Directory); err == nil {
		out.Pending = pending
	} else if !errors.Is(err, confighist.ErrNoHistory) {
		s.log.Warn("配置历史待提交变更读取失败", "instance", cfg.Name, "err", err)
	}
	if stats, err := s.configHistory.Stats(cfg.ID, cfg.Directory); err == nil {
		out.Stats = stats
	}
	if coverage, err := s.configHistory.Coverage(cfg.ID, cfg.Directory); err == nil {
		out.Coverage = coverage
	}

	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleConfigHistoryCommit(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.configHistoryFor(w, r)
	if !ok {
		return
	}
	cfg := inst.Config()

	changes, err := s.configHistory.Changes(cfg.ID, cfg.Directory, r.PathValue("ref"))
	if err != nil {
		s.writeConfigHistoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, changes)
}

func (s *Server) handleConfigHistoryDiff(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.configHistoryFor(w, r)
	if !ok {
		return
	}
	cfg := inst.Config()

	query := r.URL.Query()
	ref, target := query.Get("ref"), query.Get("path")
	if strings.TrimSpace(ref) == "" || strings.TrimSpace(target) == "" {
		writeError(w, http.StatusBadRequest, "ref 和 path 都是必填的")
		return
	}

	var (
		diff confighist.FileDiff
		err  error
	)
	if query.Get("against") == "current" {
		diff, err = s.configHistory.DiffAgainstCurrent(cfg.ID, cfg.Directory, ref, target)
	} else {
		diff, err = s.configHistory.Diff(cfg.ID, cfg.Directory, ref, target)
	}
	if err != nil {
		s.writeConfigHistoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

// handleConfigHistoryFile serves one recorded file. Single files only — see
// the note at the top of this file.
func (s *Server) handleConfigHistoryFile(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.configHistoryFor(w, r)
	if !ok {
		return
	}
	cfg := inst.Config()

	query := r.URL.Query()
	content, err := s.configHistory.FileAt(cfg.ID, cfg.Directory, query.Get("ref"), query.Get("path"))
	if err != nil {
		s.writeConfigHistoryError(w, err)
		return
	}

	name := path.Base(query.Get("path"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", contentDisposition(name))
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	_, _ = w.Write(content)
}

type snapshotRequest struct {
	Message string `json:"message"`
}

func (s *Server) handleConfigHistorySnapshot(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.configHistoryFor(w, r)
	if !ok {
		return
	}

	var req snapshotRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	cfg := inst.Config()
	running := inst.State().Running()
	message := strings.TrimSpace(req.Message)
	if message == "" {
		message = "手动快照"
	}

	// A hand-made snapshot is the one case where recording a running server is
	// allowed. It is also the one case where the result may be a torn state —
	// plugins write their own files while they run — so the commit carries the
	// caveat and the timeline shows it.
	result, err := s.configHistory.Commit(confighist.CommitRequest{
		InstanceID: cfg.ID,
		Directory:  cfg.Directory,
		Message:    message,
		Trigger:    confighist.TriggerUser,
		Actor:      actorOf(r),
		Running:    running,
	})
	if err != nil {
		s.writeConfigHistoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type restoreRequest struct {
	Ref string `json:"ref"`
	// Path empty means the whole tree, which the UI keeps behind an advanced
	// section and a second confirmation.
	Path      string `json:"path"`
	Confirmed bool   `json:"confirmed"`
}

func (s *Server) handleConfigHistoryRestorePreview(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.configHistoryFor(w, r)
	if !ok {
		return
	}

	var req restoreRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	cfg := inst.Config()
	plan, err := s.configHistory.PlanRestore(confighist.RestoreRequest{
		InstanceID: cfg.ID,
		Directory:  cfg.Directory,
		Ref:        req.Ref,
		Path:       req.Path,
		Actor:      actorOf(r),
		Running:    inst.State().Running(),
	})
	if err != nil {
		s.writeConfigHistoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// restoreResponse pairs the commit that was made with the plan it came from,
// so a refused restore can still show the operator what it was going to do.
type restoreResponse struct {
	Result confighist.CommitResult `json:"result"`
	Plan   confighist.RestorePlan  `json:"plan"`
}

func (s *Server) handleConfigHistoryRestore(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.configHistoryFor(w, r)
	if !ok {
		return
	}

	var req restoreRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	cfg := inst.Config()
	result, plan, err := s.configHistory.Restore(confighist.RestoreRequest{
		InstanceID: cfg.ID,
		Directory:  cfg.Directory,
		Ref:        req.Ref,
		Path:       req.Path,
		Actor:      actorOf(r),
		Running:    inst.State().Running(),
		Confirmed:  req.Confirmed,
	})
	if err != nil {
		// A version mismatch is not a failure, it is the second question. The
		// plan travels with the 409 so the dialog can list what differs instead
		// of asking the operator to confirm something it will not name.
		if confighist.IsVersionMismatch(err) {
			writeJSON(w, http.StatusConflict, restoreResponse{Plan: plan})
			return
		}
		s.writeConfigHistoryError(w, err)
		return
	}
	s.log.Info("配置已还原", "instance", cfg.Name, "ref", req.Ref, "path", req.Path, "by", actorOf(r))
	writeJSON(w, http.StatusOK, restoreResponse{Result: result, Plan: plan})
}

type compactRequest struct {
	Keep int `json:"keep"`
}

func (s *Server) handleConfigHistoryCompact(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.configHistoryFor(w, r)
	if !ok {
		return
	}

	var req compactRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	cfg := inst.Config()
	result, err := s.configHistory.Compact(cfg.ID, cfg.Directory, req.Keep)
	if err != nil {
		s.writeConfigHistoryError(w, err)
		return
	}
	// Compacting rebuilds the repository, so every surviving commit has a new
	// id — and the plugin ledger holds refs into it. Fixing them up here rather
	// than inside the compaction keeps the config history from having to know
	// what a plugin snapshot is.
	if s.instancePlugins != nil && len(result.Remap) > 0 {
		if err := s.instancePlugins.RemapConfigRefs(cfg.ID, result.Remap); err != nil {
			s.log.Warn("插件快照的配置引用没能更新", "instance", cfg.Name, "err", err)
		}
	}
	writeJSON(w, http.StatusOK, result)
}

// configSettingsRequest is the settings form. Every field is a pointer so a
// request that only flips the switch does not silently reset the limits.
type configSettingsRequest struct {
	Enabled   *bool     `json:"enabled"`
	FileBytes *int64    `json:"fileBytes"`
	FileCount *int      `json:"fileCount"`
	RepoBytes *int64    `json:"repoBytes"`
	Allow     *[]string `json:"allow"`
	Exclude   *[]string `json:"exclude"`
}

func (s *Server) handleConfigHistorySettings(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.configHistoryFor(w, r)
	if !ok {
		return
	}

	var req configSettingsRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	settings, err := s.configHistory.UpdateSettings(inst.ID(), func(entry *confighist.InstanceSettings) {
		if req.Enabled != nil {
			entry.Disabled = !*req.Enabled
		}
		if req.FileBytes != nil {
			entry.Limits.FileBytes = *req.FileBytes
		}
		if req.FileCount != nil {
			entry.Limits.FileCount = *req.FileCount
		}
		if req.RepoBytes != nil {
			entry.Limits.RepoBytes = *req.RepoBytes
		}
		if req.Allow != nil {
			entry.Allow = *req.Allow
		}
		if req.Exclude != nil {
			entry.Exclude = *req.Exclude
		}
	})
	if err != nil {
		s.writeConfigHistoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// snapshotAfter records a configuration change the panel just made.
//
// Fire-and-forget on purpose, and never on the caller's own error path: the
// save succeeded, and a history that could not be written must not turn a
// successful save into a failed request. What a blocked gate costs is a row on
// the timeline, and the tab is where the operator is told about it.
func (s *Server) snapshotAfter(inst *instance.Instance, trigger confighist.Trigger, actor, message string) {
	if s.configHistory == nil || inst == nil {
		return
	}
	if s.configHistoryBlocked(inst) != "" {
		return
	}

	cfg := inst.Config()
	running := inst.State().Running()
	go func() {
		_, err := s.configHistory.Commit(confighist.CommitRequest{
			InstanceID: cfg.ID,
			Directory:  cfg.Directory,
			Message:    message,
			Trigger:    trigger,
			Actor:      actor,
			Running:    running,
		})
		if err != nil && !errors.Is(err, confighist.ErrDisabled) {
			s.log.Warn("配置历史没能记录", "instance", cfg.Name, "message", message, "err", err)
		}
	}()
}

// writeConfigHistoryError maps the module's errors onto statuses. A tripped
// gate is a 409 carrying the whole report, because "which files, and how big"
// is the entire content of the decision the operator now has to make.
func (s *Server) writeConfigHistoryError(w http.ResponseWriter, err error) {
	if gate, blocked := confighist.AsGateError(err); blocked {
		writeJSON(w, http.StatusConflict, struct {
			Error string                `json:"error"`
			Gate  *confighist.GateError `json:"gate"`
		}{Error: gate.Message, Gate: gate})
		return
	}
	switch {
	case errors.Is(err, confighist.ErrNotFound), errors.Is(err, confighist.ErrNoHistory):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, confighist.ErrDisabled):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, confighist.ErrRunning):
		writeError(w, http.StatusConflict, err.Error())
	default:
		s.writeDomainError(w, err)
	}
}
