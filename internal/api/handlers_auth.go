package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/lanscarlos/hypercraft/internal/auth"
)

// errDerivationBusy reports that every password-derivation slot was taken for
// as long as the caller was willing to wait. It is not a failed login and must
// never be charged as one.
var errDerivationBusy = errors.New("no derivation slot available")

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

// beginCredentialCheck throttles a public endpoint that is about to verify the
// panel password, returning the key any failure should be charged to.
//
// It runs before the request body is read: refusing a throttled caller should
// cost the panel as close to nothing as possible, and the check itself is one
// map lookup.
func (s *Server) beginCredentialCheck(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := s.clientAddr(r)
	retry, ok := s.loginLimit.allow(key)
	if ok {
		return key, true
	}

	seconds := max(1, int(math.Ceil(retry.Seconds())))
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	// Debug rather than Warn: this fires for every refused request, so a flood
	// would bury everything else. The bounded stream of "failed login"
	// warnings — bounded precisely because of this limiter — is what tells an
	// operator that someone is trying.
	s.log.Debug("credential check throttled", "client", key, "remote", r.RemoteAddr)
	// The in-memory view does keep these: repeats collapse into one row, so a
	// flood costs a single line there while telling the operator the thing the
	// suppressed log lines would have.
	s.recordAuth(r, eventThrottled, "", "")
	writeError(w, http.StatusTooManyRequests, fmt.Sprintf("尝试过于频繁，请 %d 秒后再试", seconds))
	return "", false
}

// verifyCredential checks a username and password with only a few derivations
// running at once, so an unauthenticated flood cannot take the machine's CPU
// away from the Minecraft servers sharing it.
func (s *Server) verifyCredential(ctx context.Context, username, password string) error {
	if !s.kdf.enter(ctx) {
		return errDerivationBusy
	}
	defer s.kdf.leave()
	return s.credential().Verify(username, password)
}

// writeBusy refuses a request that could not get a derivation slot. 503 rather
// than 429, because nothing is wrong with this caller: the panel is simply out
// of the CPU it is prepared to spend on password checks.
func writeBusy(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	writeError(w, http.StatusServiceUnavailable, "服务器繁忙，请稍后再试")
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
	// Client and Remote are the two addresses this very request arrived on:
	// who the panel believes you are, and the peer it actually spoke to. They
	// are the same until a trusted proxy is configured, and telling them apart
	// is the whole question behind "what address does my panel see?" — which
	// otherwise has no answer short of reading journald.
	Client string `json:"client"`
	Remote string `json:"remote"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	key, ok := s.beginCredentialCheck(w, r)
	if !ok {
		return
	}

	var req loginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	switch err := s.verifyCredential(r.Context(), req.Username, req.Password); {
	case errors.Is(err, errDerivationBusy):
		writeBusy(w)
		return
	case err != nil:
		s.loginLimit.penalise(key)
		s.log.Warn("failed login", "username", req.Username, "remote", r.RemoteAddr, "client", key)
		s.recordAuth(r, eventSignInFailed, req.Username, "")
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	// Signing in correctly clears the address's history: a run of typos
	// followed by the right password is an operator, not an attack.
	s.loginLimit.reset(key)

	sess, err := s.sessions.Create(req.Username)
	if err != nil {
		s.writeDomainError(w, err)
		return
	}
	s.setSessionCookie(w, r, sess)
	// A successful sign-in is the one event that proves the panel's credential
	// was used, and it was the only one going unrecorded — every failed guess
	// was logged while the guess that worked left no trace. It also answers
	// "which address actually reaches this panel", which is not obvious once
	// there is an accelerator or a reverse proxy in front: remote is the peer,
	// client is who the panel believes is behind it.
	s.log.Info("signed in", "username", sess.Username, "remote", r.RemoteAddr, "client", key)
	s.recordAuth(r, eventSignIn, sess.Username, "")
	writeJSON(w, http.StatusOK, userResponse{
		Username: sess.Username,
		Version:  s.version,
		Client:   key,
		Remote:   peerHost(r),
	})
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
			s.recordAuth(r, eventUnpaired, who.username, who.device.Name)
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
	resp := userResponse{
		Username: who.username,
		Version:  s.version,
		Client:   s.clientAddr(r),
		Remote:   peerHost(r),
	}
	if who.device != nil {
		resp.Device = who.device.Name
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleAuthEvents serves the in-memory credential trail. It is behind
// requireAuth like everything else under /api: the addresses in it are exactly
// what someone probing the panel would like to know about the panel's traffic.
func (s *Server) handleAuthEvents(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.authLog.list())
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
	s.recordAuth(r, eventPasswordChanged, who.username, fmt.Sprintf("解除了 %d 台设备", unpaired))
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
	// Throttled on the same budget as login, and deliberately so: this
	// endpoint is public, checks the same password, and mints a credential
	// that outlives every session. Giving it a separate allowance would leave
	// an attacker locked out of one door and free to knock on the other.
	key, ok := s.beginCredentialCheck(w, r)
	if !ok {
		return
	}

	var req createDeviceRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	switch err := s.verifyCredential(r.Context(), req.Username, req.Password); {
	case errors.Is(err, errDerivationBusy):
		writeBusy(w)
		return
	case err != nil:
		s.loginLimit.penalise(key)
		s.log.Warn("failed device pairing", "username", req.Username, "remote", r.RemoteAddr, "client", key)
		s.recordAuth(r, eventPairFailed, req.Username, "")
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	s.loginLimit.reset(key)

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
	s.recordAuth(r, eventPaired, req.Username, dev.Name)

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
	who, _ := principalFrom(r.Context())
	s.recordAuth(r, eventUnpaired, who.username, dev.Name)
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
