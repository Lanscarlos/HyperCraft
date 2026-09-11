package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lanscarlos/hypercraft/internal/selfupdate"
)

// applyTimeout bounds a whole update: reaching GitHub, downloading a few
// megabytes and unpacking it. Generous, because a game server's uplink is not
// always fast, but finite so a stalled download cannot leave the panel wedged
// in the "updating" state forever.
const applyTimeout = 30 * time.Minute

// handleUpdateStatus reports what the panel knows without touching the network,
// so the UI can poll it while an update runs.
func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	if !s.updaterReady(w) {
		return
	}
	writeJSON(w, http.StatusOK, s.updater.Status())
}

// handleUpdateCheck forces a check now rather than waiting for the timer.
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if !s.updaterReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	status, err := s.updater.Check(ctx)
	if err != nil {
		if errors.Is(err, selfupdate.ErrBusy) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		// The check failed but the cached status still carries the reason, so
		// the UI can show "could not reach GitHub" instead of a bare error.
		s.log.Warn("update check failed", "err", err)
		writeJSON(w, http.StatusOK, status)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// applyRequest is the optional body of an apply. No version means the one the
// last check offered, which is what the update button sends.
type applyRequest struct {
	Version string `json:"version"`
}

// handleUpdateApply starts an update and returns immediately. The work outlives
// this request on purpose: it ends by stopping every managed server and
// replacing the process, which cannot be reported over the connection that
// asked for it. The UI follows progress on GET /api/update.
//
// With a version, the target is whatever the operator picked from the version
// list instead. Whether that version may be installed at all is settled by the
// updater, against the channel's own listing — see selfupdate.Updater.Find.
// That check reaches GitHub and so happens with the rest of the work, which is
// why a version this panel cannot install is reported through the status rather
// than in this response.
func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if !s.updaterReady(w) {
		return
	}

	var req applyRequest
	if r.ContentLength > 0 {
		if err := decodeJSON(w, r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "请求格式错误")
			return
		}
	}
	req.Version = strings.TrimSpace(req.Version)

	status := s.updater.Status()
	switch {
	case !status.Eligible:
		writeError(w, http.StatusBadRequest, status.IneligibleWhy)
		return
	case req.Version == "" && !status.UpdateAvailable:
		writeError(w, http.StatusBadRequest, "已经是最新版本")
		return
	case status.Phase != selfupdate.PhaseIdle:
		writeError(w, http.StatusConflict, "更新正在进行中")
		return
	}

	target := req.Version
	if target == "" {
		target = status.LatestVersion
	}
	s.log.Info("update requested", "from", status.CurrentVersion, "to", target, "chosen", req.Version != "")

	// Not r.Context(): that is cancelled as soon as this response is written.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), applyTimeout)
		defer cancel()
		var err error
		if req.Version == "" {
			err = s.updater.Apply(ctx)
		} else {
			err = s.updater.ApplyVersion(ctx, req.Version)
		}
		if err != nil {
			s.log.Error("update failed", "err", err)
		}
	}()

	writeJSON(w, http.StatusAccepted, s.updater.Status())
}

// handleUpdateVersions lists what this panel's channel offers, so the operator
// can install something other than the newest — an older release when a new one
// misbehaves, or a specific snapshot.
//
// Fetched on demand rather than cached: this page is opened rarely, and a
// stale list is one that offers a release since pruned.
func (s *Server) handleUpdateVersions(w http.ResponseWriter, r *http.Request) {
	if !s.updaterReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	versions, err := s.updater.Versions(ctx)
	if err != nil {
		s.log.Warn("could not list versions", "err", err)
		writeError(w, http.StatusBadGateway, "没能从 GitHub 取到版本列表："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, versions)
}

// handleUpdateRollback puts the panel back on the build the last update
// replaced, which is sitting next to the running binary. Like an update it
// outlives this request: it ends by stopping every server and replacing the
// process. The UI follows it on GET /api/update, the same as an update.
func (s *Server) handleUpdateRollback(w http.ResponseWriter, r *http.Request) {
	if !s.updaterReady(w) {
		return
	}

	status := s.updater.Status()
	switch {
	case !status.RollbackAvailable:
		// The status already carries a reason in the operator's language —
		// nothing recorded, the file is gone, it reports a different version.
		why := status.RollbackWhy
		if why == "" {
			why = "现在没有可以退回的版本"
		}
		writeError(w, http.StatusBadRequest, why)
		return
	case status.Phase != selfupdate.PhaseIdle:
		writeError(w, http.StatusConflict, "更新正在进行中")
		return
	}

	s.log.Info("rollback requested", "from", status.CurrentVersion, "to", status.PreviousVersion)

	// Not r.Context(): that is cancelled as soon as this response is written.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), applyTimeout)
		defer cancel()
		if err := s.updater.Rollback(ctx); err != nil {
			s.log.Error("rollback failed", "err", err)
		}
	}()

	writeJSON(w, http.StatusAccepted, s.updater.Status())
}

type mirrorRequest struct {
	// Mirror is a URL prefix, or "" to download straight from GitHub.
	Mirror string `json:"mirror"`
}

// handleUpdateMirror changes which proxy release downloads go through and
// persists it, so the choice survives the restart an update performs.
func (s *Server) handleUpdateMirror(w http.ResponseWriter, r *http.Request) {
	if !s.updaterReady(w) {
		return
	}
	var req mirrorRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}

	mirror := strings.TrimSpace(req.Mirror)
	if err := validateMirror(mirror); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if mirror != "" && !strings.HasSuffix(mirror, "/") {
		mirror += "/"
	}

	if err := s.updater.SetMirror(mirror); err != nil {
		writeError(w, http.StatusConflict, "更新正在进行中，无法修改镜像源")
		return
	}
	s.panelMu.Lock()
	panel := s.panel
	panel.UpdateMirror = &mirror
	s.panel = panel
	s.panelMu.Unlock()

	if err := s.store.SavePanel(panel); err != nil {
		s.log.Error("could not persist the update mirror", "err", err)
		writeError(w, http.StatusInternalServerError, "保存失败")
		return
	}
	s.log.Info("update mirror changed", "mirror", mirror)
	writeJSON(w, http.StatusOK, s.updater.Status())
}

type channelRequest struct {
	// Channel is "stable" or "snapshot".
	Channel string `json:"channel"`
}

// handleUpdateChannel switches between release channels and persists the
// choice. The cached check is dropped by the switch — it describes the channel
// just left — so the caller is expected to follow this with a check.
func (s *Server) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	if !s.updaterReady(w) {
		return
	}
	var req channelRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}

	channel := selfupdate.Channel(strings.TrimSpace(req.Channel))
	if channel != selfupdate.ChannelStable && channel != selfupdate.ChannelSnapshot {
		writeError(w, http.StatusBadRequest, "更新通道只能是 stable 或 snapshot")
		return
	}

	if err := s.updater.SetChannel(channel); err != nil {
		writeError(w, http.StatusConflict, "更新正在进行中，无法切换更新通道")
		return
	}

	s.panelMu.Lock()
	panel := s.panel
	panel.UpdateChannel = string(channel)
	s.panel = panel
	s.panelMu.Unlock()

	if err := s.store.SavePanel(panel); err != nil {
		s.log.Error("could not persist the update channel", "err", err)
		writeError(w, http.StatusInternalServerError, "保存失败")
		return
	}
	s.log.Info("update channel changed", "channel", channel)
	writeJSON(w, http.StatusOK, s.updater.Status())
}

// validateMirror rejects anything that is not an absolute http(s) prefix. The
// operator already has full control of the panel, so this is not a privilege
// boundary — it is there to turn a typo into a clear message instead of a
// confusing download failure during an update.
func validateMirror(mirror string) error {
	if mirror == "" {
		return nil
	}
	parsed, err := url.Parse(mirror)
	if err != nil {
		return errors.New("镜像源不是合法的 URL")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return errors.New("镜像源必须以 https:// 或 http:// 开头")
	}
	if parsed.Host == "" {
		return errors.New("镜像源缺少主机名")
	}
	return nil
}

// updaterReady guards the endpoints against a Server built without an updater,
// which is how the API tests construct one.
func (s *Server) updaterReady(w http.ResponseWriter) bool {
	if s.updater == nil {
		writeError(w, http.StatusServiceUnavailable, "更新功能未启用")
		return false
	}
	return true
}
