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

// handleUpdateApply starts an update and returns immediately. The work outlives
// this request on purpose: it ends by stopping every managed server and
// replacing the process, which cannot be reported over the connection that
// asked for it. The UI follows progress on GET /api/update.
func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if !s.updaterReady(w) {
		return
	}

	status := s.updater.Status()
	switch {
	case !status.Eligible:
		writeError(w, http.StatusBadRequest, status.IneligibleWhy)
		return
	case !status.UpdateAvailable:
		writeError(w, http.StatusBadRequest, "已经是最新版本")
		return
	case status.Phase != selfupdate.PhaseIdle:
		writeError(w, http.StatusConflict, "更新正在进行中")
		return
	}

	s.log.Info("update requested", "from", status.CurrentVersion, "to", status.LatestVersion)

	// Not r.Context(): that is cancelled as soon as this response is written.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), applyTimeout)
		defer cancel()
		if err := s.updater.Apply(ctx); err != nil {
			s.log.Error("update failed", "err", err)
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
	// Plugin downloads come off the same GitHub release CDN, so they follow the
	// same proxy: an operator who has said once that their line to GitHub is
	// bad should not have to say it again per feature.
	if s.plugins != nil {
		s.plugins.Client().SetMirror(mirror)
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
