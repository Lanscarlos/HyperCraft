// Package api exposes the panel over HTTP: a JSON API for managing instances
// and a websocket per instance carrying the live console.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lanscarlos/hypercraft/internal/auth"
	"github.com/lanscarlos/hypercraft/internal/config"
	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/metrics"
	"github.com/lanscarlos/hypercraft/internal/store"
)

// sessionCookie is the browser cookie holding the session token. It is also
// what authenticates the console websocket, since browsers cannot set headers
// on a WebSocket handshake.
const sessionCookie = "hypercraft_session"

// csrfHeader must be present on every state-changing request. A custom header
// cannot be sent cross-origin without the server opting in via CORS preflight,
// so requiring one blocks form-based CSRF without any token plumbing.
const csrfHeader = "X-HyperCraft"

// Server wires the HTTP surface to the instance manager.
type Server struct {
	log      *slog.Logger
	mgr      *instance.Manager
	store    *store.Store
	sessions *auth.SessionStore
	metrics  *metrics.Collector
	version  string

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
	Panel    config.Panel
	Version  string
	Logger   *slog.Logger
}

func NewServer(opts Options) *Server {
	s := &Server{
		log:      opts.Logger,
		mgr:      opts.Manager,
		store:    opts.Store,
		sessions: opts.Sessions,
		metrics:  opts.Metrics,
		panel:    opts.Panel,
		version:  opts.Version,
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

func (s *Server) routes() http.Handler {
	api := http.NewServeMux()

	// Public.
	api.HandleFunc("POST /api/auth/login", s.handleLogin)
	api.HandleFunc("GET /api/health", s.handleHealth)

	// Authenticated.
	protected := http.NewServeMux()
	protected.HandleFunc("POST /api/auth/logout", s.handleLogout)
	protected.HandleFunc("GET /api/auth/me", s.handleMe)
	protected.HandleFunc("POST /api/auth/password", s.handleChangePassword)

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
	protected.HandleFunc("GET /api/instances/{id}/jars", s.handleListJars)

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

	// Resource usage.
	protected.HandleFunc("GET /api/instances/{id}/metrics", s.handleInstanceMetrics)
	protected.HandleFunc("GET /api/system", s.handleSystem)

	api.Handle("/api/", s.requireAuth(s.requireCSRF(protected)))

	root := http.NewServeMux()
	root.Handle("/api/", api)
	root.Handle("/", s.staticHandler())

	return withRecover(s.log, withNoStore(root))
}

// ------------------------------------------------------------- middleware

type ctxKey int

const sessionKey ctxKey = iota

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		next.ServeHTTP(w, r.WithContext(withSession(r.Context(), sess)))
	})
}

// requireCSRF rejects state-changing requests that did not come from the panel
// UI. GET and the websocket upgrade are exempt: neither can be abused to change
// state, and a browser cannot add headers to a WebSocket handshake.
func (s *Server) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
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
