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
	"github.com/lanscarlos/hypercraft/internal/config"
	"github.com/lanscarlos/hypercraft/internal/confighist"
	"github.com/lanscarlos/hypercraft/internal/dbruntime"
	"github.com/lanscarlos/hypercraft/internal/hostterm"
	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/javaruntime"
	"github.com/lanscarlos/hypercraft/internal/metrics"
	"github.com/lanscarlos/hypercraft/internal/plugin"
	"github.com/lanscarlos/hypercraft/internal/selfupdate"
	"github.com/lanscarlos/hypercraft/internal/serverjar"
	"github.com/lanscarlos/hypercraft/internal/store"
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
	Version  string
	Logger   *slog.Logger

	Plugins         *plugin.Downloader
	InstancePlugins *plugin.Instances
	PendingPlugins  *plugin.Pending

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

	// Public.
	api.HandleFunc("POST /api/auth/login", s.handleLogin)
	api.HandleFunc("GET /api/health", s.handleHealth)
	// Pairing is authenticated by the password rather than by a session, so it
	// sits outside requireAuth. See handleCreateDevice for why.
	api.HandleFunc("POST /api/auth/devices", s.handleCreateDevice)

	// Authenticated.
	protected := http.NewServeMux()
	protected.HandleFunc("POST /api/auth/logout", s.handleLogout)
	protected.HandleFunc("GET /api/auth/me", s.handleMe)
	protected.HandleFunc("POST /api/auth/password", s.handleChangePassword)
	protected.HandleFunc("GET /api/auth/devices", s.handleListDevices)
	protected.HandleFunc("DELETE /api/auth/devices/{id}", s.handleDeleteDevice)
	protected.HandleFunc("GET /api/auth/events", s.handleAuthEvents)

	protected.HandleFunc("GET /api/instances", s.handleListInstances)
	protected.HandleFunc("POST /api/instances", s.handleCreateInstance)
	protected.HandleFunc("GET /api/instances/{id}", s.handleGetInstance)
	protected.HandleFunc("PUT /api/instances/{id}", s.handleUpdateInstance)
	protected.HandleFunc("DELETE /api/instances/{id}", s.handleDeleteInstance)

	protected.HandleFunc("POST /api/instances/{id}/start", s.handlePower(powerStart))
	protected.HandleFunc("POST /api/instances/{id}/stop", s.handlePower(powerStop))
	protected.HandleFunc("POST /api/instances/{id}/restart", s.handlePower(powerRestart))
	protected.HandleFunc("POST /api/instances/{id}/kill", s.handlePower(powerKill))
	protected.HandleFunc("POST /api/instances/{id}/command", s.handleCommand)

	protected.HandleFunc("GET /api/instances/{id}/logs", s.handleLogs)
	protected.HandleFunc("GET /api/instances/{id}/properties", s.handleGetProperties)
	protected.HandleFunc("PUT /api/instances/{id}/properties", s.handlePutProperties)
	protected.HandleFunc("GET /api/instances/{id}/eula", s.handleGetEULA)
	protected.HandleFunc("POST /api/instances/{id}/eula", s.handleAcceptEULA)

	// Config history. Two segments deep below the instance for the same reason
	// the plugin config routes are: "commits" must never be reachable as
	// anything else. Notably absent, and staying absent: anything that hands
	// over the repository as a whole. See handlers_confighist.go.
	protected.HandleFunc("GET /api/instances/{id}/config-history", s.handleConfigHistory)
	protected.HandleFunc("GET /api/instances/{id}/config-history/commits/{ref}", s.handleConfigHistoryCommit)
	protected.HandleFunc("GET /api/instances/{id}/config-history/diff", s.handleConfigHistoryDiff)
	protected.HandleFunc("GET /api/instances/{id}/config-history/file", s.handleConfigHistoryFile)
	protected.HandleFunc("POST /api/instances/{id}/config-history/snapshot", s.handleConfigHistorySnapshot)
	protected.HandleFunc("POST /api/instances/{id}/config-history/restore/preview", s.handleConfigHistoryRestorePreview)
	protected.HandleFunc("POST /api/instances/{id}/config-history/restore", s.handleConfigHistoryRestore)
	protected.HandleFunc("POST /api/instances/{id}/config-history/compact", s.handleConfigHistoryCompact)
	protected.HandleFunc("PUT /api/instances/{id}/config-history/settings", s.handleConfigHistorySettings)

	// Server cores. Panel-wide rather than per-instance, for the same reason
	// Java runtimes are: one download serves every server built from it, and
	// an instance is handed its own copy out of the library.
	protected.HandleFunc("GET /api/downloads/projects", s.handleListCoreProjects)
	protected.HandleFunc("GET /api/downloads/projects/{project}/versions", s.handleListCoreVersions)
	protected.HandleFunc("GET /api/downloads/projects/{project}/versions/{version}/build", s.handleLatestCoreBuild)
	protected.HandleFunc("GET /api/cores", s.handleCoreLibrary)
	protected.HandleFunc("POST /api/cores", s.handleStartCoreDownload)
	protected.HandleFunc("POST /api/cores/cancel", s.handleCancelCoreDownload)
	protected.HandleFunc("DELETE /api/cores/{id}", s.handleDeleteCore)
	protected.HandleFunc("POST /api/instances/{id}/core", s.handleApplyCore)

	// Plugins. Panel-wide on purpose, and more strictly so than cores are: a
	// plugin is added, versioned and updated here and nowhere else, and an
	// instance may only take a copy, swap which version it holds, or switch
	// one off. Letting every server manage its own downloads is how a panel
	// ends up with six subtly different copies of the same plugin and nobody
	// able to say which is which.
	protected.HandleFunc("GET /api/plugins", s.handlePluginLibrary)
	protected.HandleFunc("POST /api/plugins", s.handleAddPlugin)
	protected.HandleFunc("POST /api/plugins/check", s.handleCheckPlugins)
	protected.HandleFunc("POST /api/plugins/import", s.handleImportPlugins)
	protected.HandleFunc("GET /api/plugins/source/preview", s.handlePreviewPluginSource)
	// Discovery. Two segments deep for the same reason the config routes are:
	// "browse" must not be reachable as a plugin id.
	protected.HandleFunc("GET /api/plugins/browse", s.handleBrowsePlugins)
	protected.HandleFunc("GET /api/plugins/browse/{source}/{id}", s.handleBrowsePluginDetail)
	protected.HandleFunc("POST /api/plugins/browse/track", s.handleTrackPlugin)
	// The cross-instance view, and the bulk operation it exists to enable.
	protected.HandleFunc("GET /api/plugins/overview", s.handlePluginOverview)
	protected.HandleFunc("POST /api/plugins/bulk/preview", s.handleBulkUpgradePreview)
	protected.HandleFunc("POST /api/plugins/bulk/upgrade", s.handleBulkUpgrade)
	// Two segments deep on purpose: "PUT /api/plugins/{id}" already owns the
	// single-segment shape, and a plugin an operator happened to name "token"
	// would otherwise become the one plugin nobody can edit.
	protected.HandleFunc("PUT /api/plugins/config/token", s.handlePluginToken)
	protected.HandleFunc("POST /api/plugins/config/tokens", s.handlePluginTokens)
	protected.HandleFunc("PUT /api/plugins/config/tokens/{tokenId}", s.handleUpdatePluginToken)
	protected.HandleFunc("DELETE /api/plugins/config/tokens/{tokenId}", s.handleDeletePluginToken)
	protected.HandleFunc("PUT /api/plugins/config/mirror", s.handlePluginMirror)
	protected.HandleFunc("POST /api/plugins/cancel", s.handleCancelPluginDownload)
	protected.HandleFunc("DELETE /api/plugins/downloads", s.handleClearPluginDownloads)
	protected.HandleFunc("PUT /api/plugins/{id}", s.handleUpdatePlugin)
	protected.HandleFunc("DELETE /api/plugins/{id}", s.handleDeletePlugin)
	protected.HandleFunc("GET /api/plugins/{id}/releases", s.handlePluginReleases)
	protected.HandleFunc("GET /api/plugins/{id}/targets", s.handlePluginInstallTargets)
	protected.HandleFunc("POST /api/plugins/{id}/check", s.handleCheckPlugin)
	protected.HandleFunc("POST /api/plugins/{id}/download", s.handleDownloadPlugin)
	protected.HandleFunc("DELETE /api/plugins/{id}/versions", s.handleDeletePluginVersion)
	protected.HandleFunc("PUT /api/plugins/{id}/policy", s.handlePluginPolicy)

	protected.HandleFunc("GET /api/instances/{id}/plugins", s.handleListInstancePlugins)
	protected.HandleFunc("POST /api/instances/{id}/plugins", s.handleInstallInstancePlugin)
	protected.HandleFunc("PUT /api/instances/{id}/plugins", s.handleToggleInstancePlugin)
	protected.HandleFunc("POST /api/instances/{id}/plugins/adopt", s.handleAdoptInstancePlugin)
	protected.HandleFunc("POST /api/instances/{id}/plugins/library", s.handleImportInstancePluginToLibrary)
	protected.HandleFunc("POST /api/instances/{id}/plugins/reconcile", s.handleReconcileInstancePlugins)
	protected.HandleFunc("POST /api/instances/{id}/plugins/rollback", s.handleRollbackInstancePlugin)
	protected.HandleFunc("POST /api/instances/{id}/plugins/accept", s.handleAcceptInstancePlugin)
	protected.HandleFunc("DELETE /api/instances/{id}/plugins", s.handleUninstallInstancePlugin)

	// Java runtimes. Panel-wide rather than per-instance: one download serves
	// every server that needs that version.
	protected.HandleFunc("GET /api/java", s.handleJavaOverview)
	protected.HandleFunc("GET /api/java/available", s.handleListJavaMajors)
	protected.HandleFunc("POST /api/java/install", s.handleInstallJava)
	protected.HandleFunc("POST /api/java/install/cancel", s.handleCancelJavaInstall)
	protected.HandleFunc("DELETE /api/java/{id}", s.handleDeleteJava)

	// Databases. Panel-wide like the Java runtimes and for the same reason —
	// one download of MySQL serves every server that needs one — but with a
	// second layer the runtimes do not have: an engine is the binaries, a
	// service is a data directory and a process. Hence two sets of routes.
	protected.HandleFunc("GET /api/databases", s.handleDatabaseOverview)
	// Two segments deep on purpose, the same way the plugin config routes are:
	// "engines" must never be reachable as a service id.
	protected.HandleFunc("GET /api/databases/engines/{engine}/versions", s.handleListDatabaseVersions)
	protected.HandleFunc("POST /api/databases/engines/install", s.handleInstallDatabase)
	protected.HandleFunc("POST /api/databases/engines/install/cancel", s.handleCancelDatabaseInstall)
	protected.HandleFunc("DELETE /api/databases/engines/{id}", s.handleDeleteDatabaseEngine)

	protected.HandleFunc("POST /api/databases/services", s.handleCreateDatabase)
	protected.HandleFunc("PUT /api/databases/services/{id}", s.handleUpdateDatabase)
	protected.HandleFunc("DELETE /api/databases/services/{id}", s.handleDeleteDatabase)
	protected.HandleFunc("POST /api/databases/services/{id}/start", s.handleDatabasePower(true))
	protected.HandleFunc("POST /api/databases/services/{id}/stop", s.handleDatabasePower(false))
	protected.HandleFunc("GET /api/databases/services/{id}/logs", s.handleDatabaseLogs)

	protected.HandleFunc("GET /api/instances/{id}/console", s.handleConsoleSocket)

	// File manager. Every path here is confined to the instance directory by
	// os.Root; see internal/serverfiles.
	protected.HandleFunc("GET /api/instances/{id}/files", s.handleListFiles)
	protected.HandleFunc("DELETE /api/instances/{id}/files", s.handleDeleteFile)
	protected.HandleFunc("GET /api/instances/{id}/files/content", s.handleReadFile)
	protected.HandleFunc("PUT /api/instances/{id}/files/content", s.handleWriteFile)
	protected.HandleFunc("GET /api/instances/{id}/files/download", s.handleDownloadFile)
	protected.HandleFunc("POST /api/instances/{id}/files/upload", s.handleUploadFile)
	protected.HandleFunc("POST /api/instances/{id}/files/mkdir", s.handleMkdir)
	protected.HandleFunc("POST /api/instances/{id}/files/rename", s.handleRenameFile)
	protected.HandleFunc("GET /api/instances/{id}/files/schematic", s.handleSchematic)

	// Directories on the host, for the instance directory picker. Read-only,
	// and not confined to an instance — see handlers_hostfs.go.
	protected.HandleFunc("GET /api/fs", s.handleBrowseHost)
	protected.HandleFunc("GET /api/fs/inspect", s.handleInspectHost)

	// Resource usage.
	protected.HandleFunc("GET /api/instances/{id}/metrics", s.handleInstanceMetrics)
	protected.HandleFunc("GET /api/system", s.handleSystem)

	// Panel self-update.
	protected.HandleFunc("GET /api/update", s.handleUpdateStatus)
	protected.HandleFunc("POST /api/update/check", s.handleUpdateCheck)
	protected.HandleFunc("POST /api/update/apply", s.handleUpdateApply)
	protected.HandleFunc("PUT /api/update/mirror", s.handleUpdateMirror)
	protected.HandleFunc("PUT /api/update/channel", s.handleUpdateChannel)

	// Host shell. The routes exist whatever the switch says — the status one
	// is what the settings page renders the switch from, and the other two
	// refuse while it is off — so turning the terminal on takes effect
	// immediately instead of waiting for a panel restart.
	protected.HandleFunc("GET /api/terminal", s.handleTerminalStatus)
	protected.HandleFunc("PUT /api/terminal", s.handleTerminalToggle)
	protected.HandleFunc("GET /api/terminal/session", s.handleTerminalSocket)

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
			// A device token authenticates the panel's single operator; the
			// username comes from the credential rather than from the token,
			// so renaming the operator does not strand paired devices.
			who := principal{username: s.credential().Username, device: &dev}
			next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), who)))
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
		next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), principal{username: sess.Username, session: sess})))
	})
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
