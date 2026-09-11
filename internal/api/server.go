// Package api exposes the panel over HTTP: a JSON API for managing instances
// and a websocket per instance carrying the live console.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lanscarlos/hypercraft/internal/auth"
	"github.com/lanscarlos/hypercraft/internal/authz"
	"github.com/lanscarlos/hypercraft/internal/config"
	"github.com/lanscarlos/hypercraft/internal/confighist"
	"github.com/lanscarlos/hypercraft/internal/dbruntime"
	"github.com/lanscarlos/hypercraft/internal/hostterm"
	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/javaruntime"
	"github.com/lanscarlos/hypercraft/internal/metrics"
	"github.com/lanscarlos/hypercraft/internal/plugin"
	"github.com/lanscarlos/hypercraft/internal/schemlib"
	"github.com/lanscarlos/hypercraft/internal/selfupdate"
	"github.com/lanscarlos/hypercraft/internal/serverjar"
	"github.com/lanscarlos/hypercraft/internal/store"
	"github.com/lanscarlos/hypercraft/internal/users"
)

// sessionCookie is the browser cookie holding the session token. It is also
// what authenticates the console websocket for a browser, which cannot set
// headers on a WebSocket handshake. A native client has no such limit and
// presents its device token in the Authorization header there like anywhere
// else; see bearerScheme.
const sessionCookie = "hypercraft_session"

// csrfHeader must be present on every state-changing request. A custom header
// cannot be sent cross-origin without the server opting in via CORS preflight,
// so requiring one blocks form-based CSRF without any token plumbing.
const csrfHeader = "X-HyperCraft"

// bearerScheme prefixes the Authorization header a native client sends. Only a
// device token is accepted there, never a session token: keeping the two kinds
// apart means a browser session can never be lifted out and replayed as a
// long-lived credential. See internal/auth.DeviceToken.
const bearerScheme = "Bearer "

// Server wires the HTTP surface to the instance manager.
type Server struct {
	log      *slog.Logger
	mgr      *instance.Manager
	store    *store.Store
	sessions *auth.SessionStore
	// devices holds the paired native clients. It is seeded from the panel
	// config and is the runtime owner of that list from then on.
	devices *auth.DeviceStore
	// accounts holds who exists and what they may do. Like devices, it is the
	// runtime owner of its list; users.json is where it is parked between runs.
	accounts *users.Registry
	// loginLimit throttles the two endpoints that check the panel password,
	// and kdf caps how many of those checks run at once. Both are public and
	// both are expensive; see ratelimit.go.
	loginLimit *rateLimiter
	kdf        *kdfGate
	// consoleSockets bounds how many console websockets one client may hold on
	// one instance at a time. Unlike the two above it guards an authenticated
	// endpoint: what it stops is a client that reconnects without closing,
	// piling up subscriptions on a server process that must keep running.
	consoleSockets *streamGate
	// authLog is the in-memory view of recent credential events behind
	// GET /api/auth/events. The slog lines remain the system of record; see
	// authlog.go.
	authLog *authLog
	// trustedProxies decides whether X-Forwarded-For is believed when working
	// out which client a request belongs to. See config.Panel.TrustedProxies.
	trustedProxies []netip.Prefix
	metrics        *metrics.Collector
	// paths is the panel's on-disk layout, used to seed the path picker with
	// the directories an operator is most likely to want.
	paths config.Paths
	// jars fetches server cores from PaperMC into the panel-wide library.
	// Optional: a nil downloader turns the feature off and leaves uploading a
	// jar as the only way in.
	jars *serverjar.Downloader
	// java manages the Java runtimes servers are launched with. Optional, on
	// the same terms as jars.
	java *javaruntime.Installer
	// databaseInstalls downloads database engines and databases runs the
	// databases built on them. Optional as a pair, like the plugin services:
	// neither half is useful without the other.
	databaseInstalls *dbruntime.Installer
	databases        *dbruntime.Manager
	// plugins fetches plugin releases into the panel-wide plugin library, and
	// instancePlugins hands copies out to servers. Optional as a pair: both
	// nil turns plugin management off, and neither is useful without the
	// other.
	plugins         *plugin.Downloader
	instancePlugins *plugin.Instances
	// pendingPlugins records changes a running server has not seen yet, which
	// is every plugin change: the directory is read once, at startup. Optional
	// like the pair above — without it the page loses its banner, not its
	// ability to install anything.
	pendingPlugins *plugin.Pending
	// schematics is the panel-wide building library and schemMarket is where
	// new builds come from. Optional as a pair, like the plugin services:
	// without the library there is nothing for the market to download into.
	schematics  *schemlib.Library
	schemMarket *schemlib.Market
	// configHistory keeps a Git timeline of each server's configuration.
	// Optional: nil turns the 配置历史 tab off and takes every lifecycle
	// snapshot with it, which is what a panel that cannot write its data
	// directory should do rather than failing every start.
	configHistory *confighist.Service
	// updater installs new panel releases. Optional in the same way: nil turns
	// in-panel updates off.
	updater *selfupdate.Service
	// terminal runs shells on the host. Optional, and even when present it
	// does nothing until the operator flips config.Terminal.Enabled.
	terminal *hostterm.Service
	version  string

	// The system java is found by forking one, so the answer is cached.
	systemJavaMu    sync.Mutex
	systemJavaCache javaruntime.SystemJava
	systemJavaFound bool
	systemJavaAt    time.Time

	panelMu sync.RWMutex
	panel   config.Panel

	handler  http.Handler
	upgrader websocket.Upgrader
}

// Options configures a Server.
type Options struct {
	Manager  *instance.Manager
	Store    *store.Store
	Sessions *auth.SessionStore
	Metrics  *metrics.Collector
	Paths    config.Paths
	Jars     *serverjar.Downloader
	Java     *javaruntime.Installer
	Updater  *selfupdate.Service
	Terminal *hostterm.Service
	Panel    config.Panel
	Users    *users.Registry
	Version  string
	Logger   *slog.Logger

	Plugins         *plugin.Downloader
	InstancePlugins *plugin.Instances
	PendingPlugins  *plugin.Pending

	Schematics      *schemlib.Library
	SchematicMarket *schemlib.Market

	ConfigHistory *confighist.Service

	DatabaseInstalls *dbruntime.Installer
	Databases        *dbruntime.Manager
}

func NewServer(opts Options) *Server {
	trusted, bad := parseTrustedProxies(opts.Panel.TrustedProxies)
	if len(bad) > 0 {
		opts.Logger.Warn("ignoring unparseable trustedProxies entries",
			"entries", strings.Join(bad, ", "))
	}

	s := &Server{
		log:      opts.Logger,
		mgr:      opts.Manager,
		store:    opts.Store,
		sessions: opts.Sessions,
		devices:  auth.NewDeviceStore(opts.Panel.Devices),
		accounts: opts.Users,

		loginLimit:     newRateLimiter(loginBurst, loginRefill),
		kdf:            newKDFGate(defaultKDFSlots(), kdfWait),
		consoleSockets: newStreamGate(maxConsoleSockets),
		authLog:        newAuthLog(),
		trustedProxies: trusted,

		metrics:  opts.Metrics,
		paths:    opts.Paths,
		jars:     opts.Jars,
		java:     opts.Java,
		plugins:  opts.Plugins,
		updater:  opts.Updater,
		terminal: opts.Terminal,
		panel:    opts.Panel,
		version:  opts.Version,

		instancePlugins: opts.InstancePlugins,
		pendingPlugins:  opts.PendingPlugins,
		configHistory:   opts.ConfigHistory,

		schematics:  opts.Schematics,
		schemMarket: opts.SchematicMarket,

		databaseInstalls: opts.DatabaseInstalls,
		databases:        opts.Databases,
		upgrader: websocket.Upgrader{
			HandshakeTimeout: 10 * time.Second,
			ReadBufferSize:   4096,
			WriteBufferSize:  4096,
			CheckOrigin:      sameOrigin,
		},
	}
	s.handler = s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.handler }

// persistPanel writes panel.json, folding in the current device list.
//
// The server is the sole writer of that file once it is running: a caller
// saving its own copy would race a concurrent password change and could put the
// old credential back.
func (s *Server) persistPanel() error {
	s.panelMu.Lock()
	panel := s.panel
	panel.Devices = s.devices.Snapshot()
	s.panel = panel
	s.panelMu.Unlock()
	return s.store.SavePanel(panel)
}

// persistUsers writes users.json.
//
// The server is the sole writer once it is running, for the same reason it is
// the sole writer of panel.json: a caller saving its own copy would race a
// concurrent edit and could put a deleted account back.
func (s *Server) persistUsers() error {
	return s.store.SaveUsers(s.accounts.Snapshot())
}

// FlushDevices persists the device list if a token has been used since the last
// write. LastUsed moves on every authenticated request, which is far too often
// to touch the disk, so the panel flushes it on a slow timer and accepts losing
// up to one interval of precision if it is killed outright.
func (s *Server) FlushDevices() error {
	if !s.devices.Dirty() {
		return nil
	}
	return s.persistPanel()
}

// SweepRateLimits drops login-throttle buckets for addresses that have gone
// quiet. Nothing depends on it for correctness — an idle bucket has refilled
// and would allow the next attempt anyway — so it runs on the same slow timer
// as the session GC purely to keep the table from growing.
func (s *Server) SweepRateLimits() { s.loginLimit.sweep() }

func (s *Server) routes() http.Handler {
	api := http.NewServeMux()

	// Both tables are registered the same way and differ only in which mux
	// they land on. Keeping registration to this one loop is what makes the
	// tables the single description of the API surface — see routes.go.
	for _, rt := range s.publicRoutes() {
		api.HandleFunc(rt.pattern, rt.handler)
	}

	// Capabilities are enforced by wrapping each handler here rather than by a
	// middleware in front of the mux: the mux is what decides which route
	// matched, and a middleware sitting before it would have to work that out a
	// second time — a second implementation of routing, which is a second place
	// to get it wrong.
	protected := http.NewServeMux()
	for _, rt := range s.protectedRoutes() {
		handler := s.requireCaps(rt.need, rt.handler)
		// The grant is checked outside the capability, so a server the caller
		// may not see answers 404 rather than telling them which capability
		// they would have needed for it. See scope.go.
		if instanceScopedPattern(rt.pattern) {
			handler = s.requireInstance(handler)
		}
		protected.HandleFunc(rt.pattern, handler)
	}

	api.Handle("/api/", s.requireAuth(s.requireCSRF(protected)))

	root := http.NewServeMux()
	root.Handle("/api/", api)
	root.Handle("/", s.staticHandler())

	return withRecover(s.log, withNoStore(root))
}

// ------------------------------------------------------------- middleware

type ctxKey int

const sessionKey ctxKey = iota

// requireAuth accepts either of the panel's two credentials: a device token in
// an Authorization header, which is how native clients authenticate, or the
// session cookie the browser UI uses. They are not interchangeable — see
// bearerScheme.
//
// Either way the account is resolved from the registry on every request rather
// than taken from the credential. A name carried in a session goes stale on a
// rename, and — the reason that matters — an account that has just been deleted
// or switched off must stop working now, not when its session happens to
// expire.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token, ok := bearerToken(r); ok {
			dev, valid := s.devices.Validate(token)
			if !valid {
				// Worth recording even though guessing a device token is
				// hopeless — 32 bytes from crypto/rand has no shorter path than
				// exhaustion. What this catches is the other case: an app still
				// presenting a token the operator revoked, which looks exactly
				// like an intrusion attempt from the outside and is the one
				// thing the credential trail could not previously tell them
				// apart from silence.
				s.recordAuth(r, eventTokenRejected, "", "")
				writeError(w, http.StatusUnauthorized, "invalid or revoked device token")
				return
			}
			user, ok := s.accounts.ByID(dev.UserID)
			if !ok || user.Disabled {
				// The pairing outlived its owner. Recorded as a rejected token
				// because that is what it is from the outside, and because the
				// app will keep presenting it until somebody notices.
				s.recordAuth(r, eventTokenRejected, "", dev.Name)
				writeError(w, http.StatusUnauthorized, "该设备所属的账号已停用或删除")
				return
			}
			next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), s.principalFor(user, auth.Session{}, &dev))))
			return
		}

		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not signed in")
			return
		}
		sess, ok := s.sessions.Validate(cookie.Value)
		if !ok {
			s.clearSessionCookie(w, r)
			writeError(w, http.StatusUnauthorized, "session expired")
			return
		}
		user, ok := s.accounts.ByID(sess.UserID)
		if !ok || user.Disabled {
			s.sessions.Revoke(sess.Token)
			s.clearSessionCookie(w, r)
			writeError(w, http.StatusUnauthorized, "账号已停用或删除")
			return
		}
		next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), s.principalFor(user, sess, nil))))
	})
}

// principalFor resolves an account's capabilities once per request, so a
// handler asking twice is a map lookup rather than a second walk of the role.
func (s *Server) principalFor(user users.User, sess auth.Session, dev *auth.DeviceToken) principal {
	return principal{
		user:    user,
		caps:    s.accounts.Capabilities(user),
		session: sess,
		device:  dev,
	}
}

// requireCaps refuses a request whose account lacks any of the capabilities its
// route declared. See routes.go for the table those come from.
func (s *Server) requireCaps(need []authz.Cap, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		who, ok := principalFrom(r.Context())
		if !ok {
			// requireAuth runs first and never passes a request through
			// without one, so this is a wiring mistake rather than a caller's.
			writeError(w, http.StatusUnauthorized, "not signed in")
			return
		}
		for _, cap := range need {
			if cap == authz.CapSignedIn || who.can(cap) {
				continue
			}
			// Naming the missing capability is safe — the caller is
			// authenticated, and the vocabulary is in the docs — and it is the
			// difference between "403" and knowing which box to tick.
			s.log.Debug("refused for want of a capability",
				"user", who.username(), "capability", cap, "path", r.URL.Path)
			writeError(w, http.StatusForbidden, "当前角色没有这项权限："+capTitle(cap))
			return
		}
		next(w, r)
	}
}

// capTitle names a capability the way the role editor does, falling back to the
// id for one this build does not know.
func capTitle(cap authz.Cap) string {
	if info, ok := authz.Lookup(cap); ok {
		return info.Title
	}
	return string(cap)
}

// bearerToken pulls a credential out of the Authorization header. RFC 7235
// makes the scheme case-insensitive, so it is matched that way.
func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if len(header) <= len(bearerScheme) || !strings.EqualFold(header[:len(bearerScheme)], bearerScheme) {
		return "", false
	}
	token := strings.TrimSpace(header[len(bearerScheme):])
	return token, token != ""
}

// requireCSRF rejects state-changing requests that did not come from the panel
// UI. GET and the websocket upgrade are exempt: neither can be abused to change
// state, and a browser cannot add headers to a WebSocket handshake.
func (s *Server) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			// A bearer credential is never attached by the browser on its own,
			// so a request carrying one cannot have been forged by another
			// site. The header only has to guard the cookie path.
			if _, bearer := bearerToken(r); bearer {
				break
			}
			if r.Header.Get(csrfHeader) == "" {
				writeError(w, http.StatusForbidden, "missing "+csrfHeader+" header")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// withNoStore keeps the API and the SPA shell out of intermediary caches; a
// stale instance list is worse than an extra round trip.
func withNoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func withRecover(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("panic serving request", "path", r.URL.Path, "panic", rec)
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// sameOrigin only accepts websocket upgrades whose Origin matches the Host the
// request arrived on, which stops another site from opening a console socket
// with the operator's cookie.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Non-browser clients (curl, scripts) send no Origin.
		return true
	}
	if i := strings.Index(origin, "://"); i >= 0 {
		origin = origin[i+3:]
	}
	return strings.EqualFold(origin, r.Host)
}

// ---------------------------------------------------------------- helpers

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already out; nothing left to do but note it.
		return
	}
}

type errorBody struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

// writeDomainError maps package-level sentinel errors onto HTTP statuses so
// handlers do not each repeat the mapping.
func (s *Server) writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, instance.ErrNotFound):
		writeError(w, http.StatusNotFound, "instance not found")
	case errors.Is(err, instance.ErrInvalidConfig):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, instance.ErrAlreadyRunning),
		errors.Is(err, instance.ErrNotRunning),
		errors.Is(err, instance.ErrBusy):
		writeError(w, http.StatusConflict, err.Error())
	default:
		s.log.Error("request failed", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// decodeJSON reads a request body with a size cap and rejects unknown fields,
// so a typo in a field name fails loudly instead of being silently ignored.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	return decodeJSONBody(r, dst)
}

// decodeJSONBody is decodeJSON without the cap, for handlers that set their
// own larger limit (the file editor, whose payload is a whole config file).
func decodeJSONBody(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func (s *Server) instanceFromPath(w http.ResponseWriter, r *http.Request) (*instance.Instance, bool) {
	inst, err := s.mgr.Get(r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, err)
		return nil, false
	}
	return inst, true
}
