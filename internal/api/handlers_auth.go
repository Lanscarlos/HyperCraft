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
	"github.com/lanscarlos/hypercraft/internal/authz"
	"github.com/lanscarlos/hypercraft/internal/users"
)

// errDerivationBusy reports that every password-derivation slot was taken for
// as long as the caller was willing to wait. It is not a failed login and must
// never be charged as one.
var errDerivationBusy = errors.New("no derivation slot available")

// principal is whoever is behind the current request. Exactly one of the two
// credential kinds is set: a browser authenticates with a session cookie, a
// native client with a device token.
type principal struct {
	// user is the account, resolved from the registry on this request. Not a
	// copy of what the credential said: see requireAuth.
	user users.User
	// caps is what the account's role allows, resolved once per request.
	caps map[authz.Cap]bool
	// session is the cookie-borne session; its zero value means the request
	// arrived with a device token instead.
	session auth.Session
	// device is the paired client, nil for a browser session.
	device *auth.DeviceToken
}

// username is what log lines and the credential trail call this principal.
// A method rather than a field so there is no second copy to go stale when an
// account is renamed.
func (p principal) username() string { return p.user.Username }

func (p principal) can(cap authz.Cap) bool { return p.caps[cap] }

func withPrincipal(ctx context.Context, who principal) context.Context {
	return context.WithValue(ctx, sessionKey, who)
}

func principalFrom(ctx context.Context) (principal, bool) {
	who, ok := ctx.Value(sessionKey).(principal)
	return who, ok
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
//
// The derivation slot is held across the lookup as well as the comparison,
// because the registry spends one on a username that does not exist too — that
// is what stops the clock from answering "does this account exist".
func (s *Server) verifyCredential(ctx context.Context, username, password string) (users.User, error) {
	if !s.kdf.enter(ctx) {
		return users.User{}, errDerivationBusy
	}
	defer s.kdf.leave()
	return s.accounts.Authenticate(username, password)
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
	Username    string `json:"username"`
	DisplayName string `json:"displayName,omitempty"`
	// RoleID and RoleName say which role the account holds; Capabilities is
	// what that resolves to. The browser needs the resolved set rather than the
	// role name: hiding a button is a question about one capability, and
	// re-deriving the answer from a role would put a second copy of the role
	// table in the front end.
	RoleID   string `json:"roleId"`
	RoleName string `json:"roleName"`
	// Instances is the caller's server grant, null for "every server". The
	// browser needs it to know whether to offer a server picker at all.
	Instances    []string    `json:"instances"`
	Capabilities []authz.Cap `json:"capabilities"`
	Version      string      `json:"version"`
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

	user, err := s.verifyCredential(r.Context(), req.Username, req.Password)
	switch {
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

	sess, err := s.sessions.Create(user.ID, user.Username)
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
	writeJSON(w, http.StatusOK, s.describeUser(principal{user: user, caps: s.accounts.Capabilities(user)}, key, peerHost(r)))
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
			s.recordAuth(r, eventUnpaired, who.username(), who.device.Name)
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
	resp := s.describeUser(who, s.clientAddr(r), peerHost(r))
	if who.device != nil {
		resp.Device = who.device.Name
	}
	writeJSON(w, http.StatusOK, resp)
}

// describeUser is what both signing in and asking "who am I" return, so the two
// can never disagree about what the browser was told.
//
// Capabilities come out in the vocabulary's own order rather than the role's,
// so a role edited by hand does not change how the list reads.
func (s *Server) describeUser(who principal, client, remote string) userResponse {
	caps := make([]authz.Cap, 0, len(who.caps))
	for _, info := range authz.All() {
		if who.caps[info.Cap] {
			caps = append(caps, info.Cap)
		}
	}
	roleName := who.user.RoleID
	for _, role := range s.accounts.Roles() {
		if role.ID == who.user.RoleID {
			roleName = role.Name
			break
		}
	}
	return userResponse{
		Username:     who.user.Username,
		DisplayName:  who.user.DisplayName,
		RoleID:       who.user.RoleID,
		RoleName:     roleName,
		Instances:    who.user.Instances,
		Capabilities: caps,
		Version:      s.version,
		Client:       client,
		Remote:       remote,
	}
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

	// Throttled and gated on the same budget as login, for the two reasons
	// handleCreateDevice is. Being behind requireAuth does not exempt it: this
	// endpoint checks the same password, so leaving it open would give anyone
	// holding a borrowed session an unmetered oracle for the credential while
	// the front door was throttled — locked out of one door, free to knock on
	// the other. And the check costs the same tenth of a second of CPU whoever
	// asks for it, which is CPU the Minecraft servers on this machine are not
	// getting.
	key, ok := s.beginCredentialCheck(w, r)
	if !ok {
		return
	}

	var req changePasswordRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	switch _, err := s.verifyCredential(r.Context(), who.username(), req.CurrentPassword); {
	case errors.Is(err, errDerivationBusy):
		writeBusy(w)
		return
	case err != nil:
		s.loginLimit.penalise(key)
		s.log.Warn("failed password change", "username", who.username(), "remote", r.RemoteAddr, "client", key)
		s.recordAuth(r, eventSignInFailed, who.username(), "修改密码")
		writeError(w, http.StatusUnauthorized, "当前密码不正确")
		return
	}
	s.loginLimit.reset(key)

	if _, err := s.accounts.SetPassword(who.user.ID, req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Everything issued against the old password stops working, paired devices
	// included: changing the password is usually the account's owner saying the
	// old credential should not open anything any more, and a device token was
	// minted by presenting exactly that credential.
	//
	// Only this account's, though. It used to be everybody's, which was the
	// right behaviour when there was only ever one account and is the wrong one
	// now: one person rotating their password must not sign the whole team out.
	s.sessions.RevokeUser(who.user.ID)
	unpaired := s.devices.RevokeUser(who.user.ID)

	if err := s.persistUsers(); err != nil {
		s.writeDomainError(w, err)
		return
	}
	if unpaired > 0 {
		if err := s.persistPanel(); err != nil {
			s.writeDomainError(w, err)
			return
		}
	}

	s.clearSessionCookie(w, r)
	s.log.Info("password changed", "username", who.username(), "devicesUnpaired", unpaired)
	s.recordAuth(r, eventPasswordChanged, who.username(), fmt.Sprintf("解除了 %d 台设备", unpaired))
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

	user, err := s.verifyCredential(r.Context(), req.Username, req.Password)
	switch {
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

	// The pairing belongs to the account whose password just opened it, and
	// inherits that account's role: a token is another way in, not a way round.
	dev, token, err := s.devices.Issue(user.ID, req.Name)
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

	// One account's own pairings. An administrator does not get everybody's
	// here — that belongs on the user management page, next to the account it
	// is about, rather than mixed into the list somebody manages their own
	// phone from.
	devices := s.devices.ListUser(who.user.ID)
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
	who, _ := principalFrom(r.Context())

	id := r.PathValue("id")
	dev, ok := s.devices.Get(id)
	// Somebody else's pairing is reported as missing rather than refused: this
	// route is "my devices", and an id that is not in that list does not exist
	// as far as the caller is concerned.
	if !ok || dev.UserID != who.user.ID {
		writeError(w, http.StatusNotFound, "device not found")
		return
	}
	s.devices.Revoke(id)
	if err := s.persistPanel(); err != nil {
		s.writeDomainError(w, err)
		return
	}

	s.log.Info("device unpaired", "device", dev.Name, "id", dev.ID)
	s.recordAuth(r, eventUnpaired, who.username(), dev.Name)
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
