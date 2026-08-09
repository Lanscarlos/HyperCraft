package api

import (
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/lanscarlos/hypercraft/internal/selfupdate"
)

// withUpdater wires a real update service into the test panel. It never reaches
// the network in these tests: every path exercised here is decided from the
// cached status alone.
func withUpdater(version string) func(*Options) {
	return func(o *Options) {
		o.Version = version
		o.Updater = selfupdate.NewService(
			"owner/repo", version, "", selfupdate.Hooks{},
			slog.New(slog.NewTextHandler(io.Discard, nil)),
		)
	}
}

func TestUpdateEndpointsRequireASession(t *testing.T) {
	env := newTestEnv(t, withUpdater("v1.0.0"))

	// Installing a new binary is the single most powerful thing the panel can
	// be told to do; none of it may be reachable without signing in.
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/update"},
		{http.MethodPost, "/api/update/check"},
		{http.MethodPost, "/api/update/apply"},
	} {
		resp := env.do(c.method, c.path, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s without a session = %d, want 401", c.method, c.path, resp.StatusCode)
		}
	}
}

func TestUpdateApplyRequiresTheCSRFHeader(t *testing.T) {
	env := newTestEnv(t, withUpdater("v1.0.0"))
	env.login()

	resp := env.doRaw(http.MethodPost, "/api/update/apply", nil, false)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("apply without the CSRF header = %d, want 403", resp.StatusCode)
	}
}

func TestUpdateStatusReportsCurrentVersion(t *testing.T) {
	env := newTestEnv(t, withUpdater("v1.0.0"))
	env.login()

	resp := env.do(http.MethodGet, "/api/update", nil)
	var status selfupdate.Status
	decodeBody(t, resp, &status)

	if status.CurrentVersion != "v1.0.0" {
		t.Errorf("CurrentVersion = %q, want v1.0.0", status.CurrentVersion)
	}
	if status.Phase != selfupdate.PhaseIdle {
		t.Errorf("Phase = %q, want idle", status.Phase)
	}
	// Nothing has been checked yet, so there is nothing to install.
	if status.UpdateAvailable {
		t.Error("UpdateAvailable = true before any check ran")
	}
	if !status.Eligible {
		t.Errorf("Eligible = false for a release build: %s", status.IneligibleWhy)
	}
}

func TestUpdateIsDisabledForDevBuilds(t *testing.T) {
	env := newTestEnv(t, withUpdater("dev"))
	env.login()

	resp := env.do(http.MethodGet, "/api/update", nil)
	var status selfupdate.Status
	decodeBody(t, resp, &status)

	if status.Eligible {
		t.Error("a dev build reports itself as updatable; it would overwrite a local build")
	}
	if status.IneligibleWhy == "" {
		t.Error("no reason given for refusing to update a dev build")
	}
}

func TestUpdateApplyRejectedWhenNothingToInstall(t *testing.T) {
	env := newTestEnv(t, withUpdater("v1.0.0"))
	env.login()

	resp := env.do(http.MethodPost, "/api/update/apply", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("apply with no known update = %d, want 400", resp.StatusCode)
	}
}

func TestUpdateMirrorRoundTripsAndPersists(t *testing.T) {
	env := newTestEnv(t, withUpdater("v1.0.0"))
	env.login()

	resp := env.do(http.MethodPut, "/api/update/mirror", mirrorRequest{Mirror: "https://gh-proxy.com"})
	var status selfupdate.Status
	decodeBody(t, resp, &status)

	// A prefix without the trailing slash is the obvious typo; it is fixed up
	// rather than rejected, because gluing the two URLs together would only
	// fail later, mid-update.
	if status.Mirror != "https://gh-proxy.com/" {
		t.Errorf("Mirror = %q, want a trailing slash added", status.Mirror)
	}

	// It has to survive a restart: an update restarts the panel, and a mirror
	// that reverted afterwards would be useless for the next one.
	panel, err := env.store.LoadPanel()
	if err != nil {
		t.Fatalf("LoadPanel: %v", err)
	}
	if panel.Mirror() != "https://gh-proxy.com/" {
		t.Errorf("persisted mirror = %q", panel.Mirror())
	}
}

func TestUpdateMirrorCanBeTurnedOff(t *testing.T) {
	env := newTestEnv(t, withUpdater("v1.0.0"))
	env.login()

	resp := env.do(http.MethodPut, "/api/update/mirror", mirrorRequest{Mirror: ""})
	var status selfupdate.Status
	decodeBody(t, resp, &status)
	if status.Mirror != "" {
		t.Errorf("Mirror = %q, want empty for direct downloads", status.Mirror)
	}

	// Empty must persist as a deliberate choice, not be refilled with the
	// default on the next load.
	panel, err := env.store.LoadPanel()
	if err != nil {
		t.Fatalf("LoadPanel: %v", err)
	}
	if panel.Mirror() != "" {
		t.Errorf("persisted mirror = %q, want the operator's choice of direct", panel.Mirror())
	}
}

func TestUpdateMirrorRejectsNonHTTPPrefixes(t *testing.T) {
	env := newTestEnv(t, withUpdater("v1.0.0"))
	env.login()

	for _, bad := range []string{"ghfast.top", "javascript:alert(1)", "ftp://example.com/", "://"} {
		resp := env.do(http.MethodPut, "/api/update/mirror", mirrorRequest{Mirror: bad})
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("mirror %q = %d, want 400", bad, resp.StatusCode)
		}
	}
}

func TestUpdateMirrorRequiresASessionAndCSRF(t *testing.T) {
	env := newTestEnv(t, withUpdater("v1.0.0"))

	resp := env.do(http.MethodPut, "/api/update/mirror", mirrorRequest{Mirror: ""})
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("without a session = %d, want 401", resp.StatusCode)
	}

	env.login()
	resp = env.doRaw(http.MethodPut, "/api/update/mirror", mirrorRequest{Mirror: ""}, false)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("without the CSRF header = %d, want 403", resp.StatusCode)
	}
}

func TestUpdateEndpointsReportUnavailableWithoutAnUpdater(t *testing.T) {
	// The panel can be built without the updater wired in; the endpoints must
	// say so rather than panicking on a nil service.
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodGet, "/api/update", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status without an updater = %d, want 503", resp.StatusCode)
	}
}
