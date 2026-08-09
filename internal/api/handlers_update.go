package api

import (
	"context"
	"errors"
	"net/http"
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

// updaterReady guards the endpoints against a Server built without an updater,
// which is how the API tests construct one.
func (s *Server) updaterReady(w http.ResponseWriter) bool {
	if s.updater == nil {
		writeError(w, http.StatusServiceUnavailable, "更新功能未启用")
		return false
	}
	return true
}
