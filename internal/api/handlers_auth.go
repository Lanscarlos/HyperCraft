package api

import (
	"context"
	"net/http"
	"time"

	"github.com/lanscarlos/hypercraft/internal/auth"
)

func withSession(ctx context.Context, sess auth.Session) context.Context {
	return context.WithValue(ctx, sessionKey, sess)
}

func sessionFrom(ctx context.Context) (auth.Session, bool) {
	sess, ok := ctx.Value(sessionKey).(auth.Session)
	return sess, ok
}

func (s *Server) credential() auth.Credential {
	s.panelMu.RLock()
	defer s.panelMu.RUnlock()
	return s.panel.Credential
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": s.version,
	})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type userResponse struct {
	Username string `json:"username"`
	Version  string `json:"version"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	if err := s.credential().Verify(req.Username, req.Password); err != nil {
		s.log.Warn("failed login", "username", req.Username, "remote", r.RemoteAddr)
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}

	sess, err := s.sessions.Create(req.Username)
	if err != nil {
		s.writeDomainError(w, err)
		return
	}
	s.setSessionCookie(w, r, sess)
	writeJSON(w, http.StatusOK, userResponse{Username: sess.Username, Version: s.version})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		s.sessions.Revoke(cookie.Value)
	}
	s.clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	sess, ok := sessionFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	writeJSON(w, http.StatusOK, userResponse{Username: sess.Username, Version: s.version})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFrom(r.Context())

	var req changePasswordRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if err := s.credential().Verify(sess.Username, req.CurrentPassword); err != nil {
		writeError(w, http.StatusUnauthorized, "当前密码不正确")
		return
	}

	cred, err := auth.NewCredential(sess.Username, req.NewPassword)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.panelMu.Lock()
	panel := s.panel
	panel.Credential = cred
	s.panel = panel
	s.panelMu.Unlock()

	if err := s.store.SavePanel(panel); err != nil {
		s.writeDomainError(w, err)
		return
	}

	// Every existing session was issued against the old password.
	s.sessions.RevokeAll()
	s.clearSessionCookie(w, r)
	s.log.Info("panel password changed", "username", sess.Username)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sess.Token,
		Path:     "/",
		Expires:  sess.ExpiresAt,
		MaxAge:   int(time.Until(sess.ExpiresAt).Seconds()),
		HttpOnly: true,
		// Strict is safe here: the panel is a standalone app, never linked
		// into from elsewhere, so there is no cross-site navigation to break.
		SameSite: http.SameSiteStrictMode,
		Secure:   isTLS(r),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   isTLS(r),
	})
}

// isTLS reports whether the browser reached us over HTTPS, directly or through
// a terminating reverse proxy. Marking the cookie Secure on a plain-HTTP
// localhost setup would make it undeliverable, so it has to be conditional.
func isTLS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https"
}
