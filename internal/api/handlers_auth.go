package api

import (
	"context"
	"net/http"
	"time"

	"github.com/lanscarlos/hypercraft/internal/auth"
)

// principal is whoever is behind the current request. Exactly one of the two
// credential kinds is set: a browser authenticates with a session cookie, a
// native client with a device token.
type principal struct {
	username string
	// session is the cookie-borne session; its zero value means the request
	// arrived with a device token instead.
	session auth.Session
	// device is the paired client, nil for a browser session.
	device *auth.DeviceToken
}

func withPrincipal(ctx context.Context, who principal) context.Context {
	return context.WithValue(ctx, sessionKey, who)
}

func principalFrom(ctx context.Context) (principal, bool) {
	who, ok := ctx.Value(sessionKey).(principal)
	return who, ok
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
	// Device names the pairing when the request authenticated with a device
	// token, and is absent for a browser session. It gives an app somewhere to
	// show which pairing it is running under.
	Device string `json:"device,omitempty"`
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
	who, _ := principalFrom(r.Context())

	// For a paired client, signing out means unpairing. The app holds one
	// long-lived token and has no other way to make it stop working, and
	// leaving a live credential behind on a device the operator just signed
	// out of would be the wrong default.
	if who.device != nil {
		if s.devices.Revoke(who.device.ID) {
			if err := s.persistPanel(); err != nil {
				s.writeDomainError(w, err)
				return
			}
			s.log.Info("device signed out", "device", who.device.Name, "id", who.device.ID)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if who.session.Token != "" {
		s.sessions.Revoke(who.session.Token)
	}
	s.clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	who, ok := principalFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	resp := userResponse{Username: who.username, Version: s.version}
	if who.device != nil {
		resp.Device = who.device.Name
	}
	writeJSON(w, http.StatusOK, resp)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	who, _ := principalFrom(r.Context())

	var req changePasswordRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if err := s.credential().Verify(who.username, req.CurrentPassword); err != nil {
		writeError(w, http.StatusUnauthorized, "当前密码不正确")
		return
	}

	cred, err := auth.NewCredential(who.username, req.NewPassword)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.panelMu.Lock()
	panel := s.panel
	panel.Credential = cred
	s.panel = panel
	s.panelMu.Unlock()

	// Everything issued against the old password stops working, paired devices
	// included: changing the password is usually the operator saying the old
	// credential should not open anything any more, and a device token was
	// minted by presenting exactly that credential. Both are revoked before the
	// write, so the file that lands on disk already reflects it.
	s.sessions.RevokeAll()
	unpaired := s.devices.RevokeAll()

	if err := s.persistPanel(); err != nil {
		s.writeDomainError(w, err)
		return
	}

	s.clearSessionCookie(w, r)
	s.log.Info("panel password changed", "username", who.username, "devicesUnpaired", unpaired)
	w.WriteHeader(http.StatusNoContent)
}

// --------------------------------------------------------------- devices

type createDeviceRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

type createDeviceResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	// Token is returned here and nowhere else, ever: the panel keeps only a
	// digest of it, so a client that loses it has to pair again.
	Token string `json:"token"`
}

type deviceResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	// LastUsed is a pointer so "never used" is an absent field rather than a
	// zero timestamp a client would have to recognise. It never carries the
	// stored digest — that is not usable on its own, but it would let someone
	// who read this response verify a guessed token offline.
	LastUsed *time.Time `json:"lastUsed,omitempty"`
	// Current marks the device that made this request, so an app can show
	// "this device" and a browser can warn before revoking the phone it is not
	// holding.
	Current bool `json:"current"`
}

// handleCreateDevice pairs a native client.
//
// It is authenticated by the password rather than by an existing session on
// purpose: minting a credential that outlives every session should require the
// credential itself, so someone who has borrowed a browser session cannot
// quietly turn it into permanent access.
func (s *Server) handleCreateDevice(w http.ResponseWriter, r *http.Request) {
	var req createDeviceRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	if err := s.credential().Verify(req.Username, req.Password); err != nil {
		s.log.Warn("failed device pairing", "username", req.Username, "remote", r.RemoteAddr)
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}

	dev, token, err := s.devices.Issue(req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.persistPanel(); err != nil {
		// Never hand out a token the panel failed to record: it would work
		// until the next restart and then die for no visible reason.
		s.devices.Revoke(dev.ID)
		s.writeDomainError(w, err)
		return
	}

	// A device token is long-lived and does not expire on its own, so on a
	// plain-HTTP panel it crosses the network in the clear on every single
	// request. That is the operator's call to make, but it should not be a
	// silent one.
	if !isTLS(r) {
		s.log.Warn("device paired over plain HTTP: its token will cross the network in clear text on every request",
			"device", dev.Name, "remote", r.RemoteAddr)
	}
	s.log.Info("device paired", "device", dev.Name, "id", dev.ID)

	writeJSON(w, http.StatusCreated, createDeviceResponse{
		ID:        dev.ID,
		Name:      dev.Name,
		CreatedAt: dev.CreatedAt,
		Token:     token,
	})
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	who, _ := principalFrom(r.Context())

	devices := s.devices.List()
	out := make([]deviceResponse, 0, len(devices))
	for _, dev := range devices {
		out = append(out, deviceResponse{
			ID:        dev.ID,
			Name:      dev.Name,
			CreatedAt: dev.CreatedAt,
			LastUsed:  dev.LastUsed,
			Current:   who.device != nil && who.device.ID == dev.ID,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	dev, ok := s.devices.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "device not found")
		return
	}
	s.devices.Revoke(id)
	if err := s.persistPanel(); err != nil {
		s.writeDomainError(w, err)
		return
	}

	s.log.Info("device unpaired", "device", dev.Name, "id", dev.ID)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------- cookies

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
