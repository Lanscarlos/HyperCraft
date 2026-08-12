package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lanscarlos/hypercraft/internal/auth"
	"github.com/lanscarlos/hypercraft/internal/config"
	"github.com/lanscarlos/hypercraft/internal/dbruntime"
	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/javaruntime"
	"github.com/lanscarlos/hypercraft/internal/mcprops"
	"github.com/lanscarlos/hypercraft/internal/metrics"
	"github.com/lanscarlos/hypercraft/internal/plugin"
	"github.com/lanscarlos/hypercraft/internal/schemlib"
	"github.com/lanscarlos/hypercraft/internal/serverjar"
	"github.com/lanscarlos/hypercraft/internal/store"
)

const (
	testUser = "admin"
	testPass = "correct-horse-battery"
)

type testEnv struct {
	t      *testing.T
	server *httptest.Server
	client *http.Client
	// api is the panel behind server, for the few assertions that read state
	// no endpoint exposes — how many console sockets are held, say.
	api   *Server
	mgr   *instance.Manager
	store *store.Store
	paths config.Paths
	// fill stands in for the PaperMC API and its CDN; see handlers_downloads_test.go.
	fill *fakeFill
	// adoptium stands in for the Java download API; see handlers_java_test.go.
	adoptium *fakeAdoptium
	// github stands in for the GitHub releases API; see handlers_plugins_test.go.
	github *fakeGitHub
}

// newTestEnv builds a panel backed by a temporary data directory. Tests that
// need a component the default wiring leaves out — the updater, say — pass an
// option to fill it in.
func newTestEnv(t *testing.T, opts ...func(*Options)) *testEnv {
	t.Helper()

	paths := config.NewPaths(t.TempDir())
	st, err := store.New(paths)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	cred, err := auth.NewCredential(testUser, testPass)
	if err != nil {
		t.Fatalf("NewCredential: %v", err)
	}
	panel := config.Defaults()
	panel.Credential = cred
	// Small enough that an upload test can exceed it without moving megabytes.
	panel.MaxUploadMB = 1

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := instance.NewManager(st, paths.ServersRoot(), logger)
	fill := newFakeFill(t)
	adoptium := newFakeAdoptium(t)
	gh := newFakeGitHub(t)
	pluginLibrary := plugin.NewLibrary(paths.PluginsRoot())
	databases, err := dbruntime.NewManager(
		paths.DatabaseRoot(), dbruntime.NewStore(paths.DatabaseEnginesRoot()), st, nil, logger)
	if err != nil {
		t.Fatalf("dbruntime.NewManager: %v", err)
	}

	options := Options{
		Manager:  mgr,
		Store:    st,
		Sessions: auth.NewSessionStore(time.Hour),
		Metrics:  metrics.New(time.Second, time.Minute, t.TempDir(), logger),
		Paths:    paths,
		Jars: serverjar.NewDownloader(
			serverjar.NewClient(fill.URL(), "test"),
			serverjar.NewLibrary(paths.CoresRoot()),
			logger,
		),
		Java: javaruntime.NewInstaller(
			javaruntime.NewClient(adoptium.URL(), "test"),
			javaruntime.NewStore(paths.JavaRoot()),
			logger,
		),
		Plugins: plugin.NewDownloader(
			plugin.NewClient(gh.URL(), "test"),
			pluginLibrary,
			logger,
		),
		InstancePlugins: plugin.NewInstances(pluginLibrary, paths.InstancePluginsFile()),
		PendingPlugins:  plugin.NewPending(paths.PendingPluginsFile()),

		Schematics: schemlib.NewLibrary(paths.SchematicsRoot()),
		// No mirrors and no token: the market's own sources are exercised in
		// internal/schemlib against a test server, and nothing here should be
		// able to reach the real internet by accident.
		SchematicMarket: schemlib.NewMarket(paths.SchematicsRoot(), "test"),

		DatabaseInstalls: dbruntime.NewInstaller(
			dbruntime.NewClient("test"),
			dbruntime.NewStore(paths.DatabaseEnginesRoot()),
			logger,
		),
		Databases: databases,
		Panel:     panel,
		Version:   "test",
		Logger:    logger,
	}
	for _, opt := range opts {
		opt(&options)
	}

	api := NewServer(options)
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &testEnv{
		t: t, server: srv, client: &http.Client{Jar: jar}, api: api,
		mgr: mgr, store: st, paths: paths, fill: fill, adoptium: adoptium, github: gh,
	}
}

// waitFor blocks until cond holds, for the handful of assertions that land on
// another goroutine — a websocket handler unwinding after its peer hung up,
// say. Polling rather than a fixed sleep so a slow CI box does not decide the
// test's outcome.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition never became true")
}

// do issues a request with the CSRF header the UI always sends.
func (e *testEnv) do(method, path string, body any) *http.Response {
	e.t.Helper()
	return e.doRaw(method, path, body, true)
}

func (e *testEnv) doRaw(method, path string, body any, csrf bool) *http.Response {
	e.t.Helper()

	req := e.request(method, path, body)
	if !csrf {
		req.Header.Del(csrfHeader)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// request builds a request the way the UI sends them — JSON body, CSRF header
// — for callers that need to adjust it before it goes out.
func (e *testEnv) request(method, path string, body any) *http.Request {
	e.t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, e.server.URL+path, reader)
	if err != nil {
		e.t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(csrfHeader, "1")
	return req
}

func (e *testEnv) login() {
	e.t.Helper()

	resp := e.do(http.MethodPost, "/api/auth/login", loginRequest{Username: testUser, Password: testPass})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("login failed: %d", resp.StatusCode)
	}
}

func decodeBody(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func TestHealthIsPublic(t *testing.T) {
	env := newTestEnv(t)

	resp := env.do(http.MethodGet, "/api/health", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestAPIRequiresASession(t *testing.T) {
	env := newTestEnv(t)

	resp := env.do(http.MethodGet, "/api/instances", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without a session, got %d", resp.StatusCode)
	}
}

func TestLoginRejectsAWrongPassword(t *testing.T) {
	env := newTestEnv(t)

	resp := env.do(http.MethodPost, "/api/auth/login", loginRequest{Username: testUser, Password: "nope"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
	if len(resp.Cookies()) != 0 {
		t.Error("a failed login must not set a session cookie")
	}
}

func TestLoginThenListInstances(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodGet, "/api/instances", nil)
	var instances []instance.Status
	decodeBody(t, resp, &instances)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if len(instances) != 0 {
		t.Errorf("expected an empty panel, got %d instances", len(instances))
	}
}

// A logged-in browser must not be usable by another site via a cross-origin
// form post, which cannot set a custom header.
func TestMutationsRequireTheCSRFHeader(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.doRaw(http.MethodPost, "/api/instances", instanceRequest{Name: "csrf"}, false)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 without the CSRF header, got %d", resp.StatusCode)
	}
}

func TestCreateAndFetchInstance(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/instances", instanceRequest{
		Name:        "生存服",
		Jar:         "server.jar",
		MaxMemoryMB: 4096,
	})
	var created instance.Status
	decodeBody(t, resp, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	if created.ID == "" {
		t.Fatal("created instance has no ID")
	}
	if created.State != instance.StateStopped {
		t.Errorf("a new instance should be stopped, got %q", created.State)
	}
	if filepath.Base(created.Directory) == "" || !filepath.IsAbs(created.Directory) {
		t.Errorf("expected an absolute default directory, got %q", created.Directory)
	}

	// Fetching by ID exercises the path wildcard through the nested mux.
	resp = env.do(http.MethodGet, "/api/instances/"+created.ID, nil)
	var fetched instance.Status
	decodeBody(t, resp, &fetched)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if fetched.Name != "生存服" {
		t.Errorf("name did not round trip: %q", fetched.Name)
	}
	if fetched.MaxMemoryMB != 4096 {
		t.Errorf("maxMemoryMB did not round trip: %d", fetched.MaxMemoryMB)
	}
}

func TestUnknownInstanceIs404(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodGet, "/api/instances/does-not-exist", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

// Starting an instance whose jar is missing must fail with a clear 400 rather
// than leaving the panel in a half-started state.
func TestStartWithoutAJarIsRejected(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/instances", instanceRequest{Name: "empty", Jar: "server.jar"})
	var created instance.Status
	decodeBody(t, resp, &created)

	resp = env.do(http.MethodPost, "/api/instances/"+created.ID+"/start", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for a missing jar, got %d", resp.StatusCode)
	}
	if got := env.mgr.List()[0].State(); got != instance.StateStopped {
		t.Errorf("instance should still be stopped, got %q", got)
	}
}

func TestPropertiesEditPreservesUnknownKeys(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/instances", instanceRequest{Name: "props"})
	var created instance.Status
	decodeBody(t, resp, &created)

	// A key the panel has never heard of, as a plugin or a newer Minecraft
	// version would write.
	seed := "some-future-key=42\nmotd=old\n"
	if err := os.WriteFile(filepath.Join(created.Directory, "server.properties"), []byte(seed), 0o644); err != nil {
		t.Fatalf("seed properties: %v", err)
	}

	resp = env.do(http.MethodPut, "/api/instances/"+created.ID+"/properties", putPropertiesRequest{
		Entries: []mcprops.Entry{{Key: "motd", Value: "新服务器"}},
	})
	var out propertiesResponse
	decodeBody(t, resp, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	found := map[string]string{}
	for _, e := range out.Entries {
		found[e.Key] = e.Value
	}
	if found["motd"] != "新服务器" {
		t.Errorf("motd not updated: %q", found["motd"])
	}
	if found["some-future-key"] != "42" {
		t.Error("an unknown key was dropped on save")
	}
}

func TestLogoutInvalidatesTheSession(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/auth/logout", nil)
	resp.Body.Close()

	resp = env.do(http.MethodGet, "/api/instances", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 after logout, got %d", resp.StatusCode)
	}
}
