package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/serverjar"
)

// fakeFill serves the two PaperMC endpoints the panel uses plus the artifact
// itself, so the download path is exercised end to end without the network.
type fakeFill struct {
	server *httptest.Server
	body   []byte
}

func newFakeFill(t *testing.T) *fakeFill {
	t.Helper()
	fill := &fakeFill{body: []byte(strings.Repeat("jar", 2048))}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /projects/{project}/versions", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"versions":[
			{"version":{"id":"1.21.11","support":{"status":"SUPPORTED"},"java":{"version":{"minimum":21}}},"builds":[131,132]},
			{"version":{"id":"1.21.11-pre1","support":{"status":"UNSUPPORTED"},"java":{"version":{"minimum":21}}},"builds":[1]}
		]}`)
	})
	mux.HandleFunc("GET /projects/{project}/versions/{version}/builds/latest", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("version") != "1.21.11" {
			http.NotFound(w, r)
			return
		}
		sum := sha256.Sum256(fill.body)
		fmt.Fprintf(w, `{"id":132,"channel":"STABLE","time":"2026-05-11T11:43:09Z","downloads":{"server:default":{
			"name":"%s-1.21.11-132.jar","url":"%s/artifact","size":%d,"checksums":{"sha256":"%s"}}}}`,
			r.PathValue("project"), fill.URL(), len(fill.body), hex.EncodeToString(sum[:]))
	})
	mux.HandleFunc("GET /artifact", func(w http.ResponseWriter, r *http.Request) {
		w.Write(fill.body)
	})

	fill.server = httptest.NewServer(mux)
	t.Cleanup(fill.server.Close)
	return fill
}

func (f *fakeFill) URL() string { return f.server.URL }

// awaitDownload polls the status endpoint the way the UI does.
func (e *testEnv) awaitDownload(instanceID string) serverjar.Job {
	e.t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp := e.do(http.MethodGet, "/api/instances/"+instanceID+"/jars/download", nil)
		var job *serverjar.Job
		decodeBody(e.t, resp, &job)
		if job != nil && job.State != serverjar.JobDownloading {
			return *job
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("download did not finish in time")
	return serverjar.Job{}
}

func TestCoreCatalogueIsOffered(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodGet, "/api/downloads/projects", nil)
	var projects []serverjar.Project
	decodeBody(t, resp, &projects)

	if len(projects) != 2 || projects[0].ID != "paper" || projects[1].ID != "velocity" {
		t.Fatalf("unexpected catalogue: %+v", projects)
	}
	if projects[1].Kind != "proxy" {
		t.Errorf("velocity should be marked as a proxy: %+v", projects[1])
	}
}

func TestCoreVersionsAreListed(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodGet, "/api/downloads/projects/paper/versions", nil)
	var versions []serverjar.Version
	decodeBody(t, resp, &versions)

	if len(versions) != 2 || versions[0].ID != "1.21.11" || !versions[0].Stable {
		t.Fatalf("unexpected versions: %+v", versions)
	}
	if versions[1].Stable {
		t.Errorf("a -pre version must not be offered as stable: %+v", versions[1])
	}
}

func TestCoreVersionsRejectUnknownProject(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodGet, "/api/downloads/projects/forge/versions", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown project, got %d", resp.StatusCode)
	}
}

func TestDownloadCoreLandsInTheInstanceDirectory(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("paper-dl")

	resp := env.do(http.MethodPost, "/api/instances/"+created.ID+"/jars/download",
		startDownloadRequest{Project: "paper", Version: "1.21.11", SetAsJar: true})
	var started serverjar.Job
	decodeBody(t, resp, &started)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
	if started.FileName != "paper-1.21.11-132.jar" {
		t.Fatalf("unexpected job: %+v", started)
	}

	job := env.awaitDownload(created.ID)
	if job.State != serverjar.JobDone {
		t.Fatalf("download failed: %s / %s", job.State, job.Error)
	}

	onDisk := filepath.Join(created.Directory, "paper-1.21.11-132.jar")
	data, err := os.ReadFile(onDisk)
	if err != nil {
		t.Fatalf("read downloaded jar: %v", err)
	}
	if len(data) != len(env.fill.body) {
		t.Errorf("jar is %d bytes, want %d", len(data), len(env.fill.body))
	}

	// setAsJar has to be applied by the daemon: the operator may well have
	// closed the tab before the transfer finished.
	var refreshed instance.Status
	decodeBody(t, env.do(http.MethodGet, "/api/instances/"+created.ID, nil), &refreshed)
	if refreshed.Jar != "paper-1.21.11-132.jar" {
		t.Errorf("launch jar is %q, want the downloaded file", refreshed.Jar)
	}

	// Downloading the same build again must not silently replace it.
	again := env.do(http.MethodPost, "/api/instances/"+created.ID+"/jars/download",
		startDownloadRequest{Project: "paper", Version: "1.21.11"})
	again.Body.Close()
	if again.StatusCode != http.StatusConflict {
		t.Errorf("expected 409 when the jar is already there, got %d", again.StatusCode)
	}
}

// Velocity is a proxy: it has no world and rejects the --nogui the default
// launch config passes to a Minecraft server.
func TestDownloadingVelocityClearsServerArgs(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("proxy")

	resp := env.do(http.MethodPost, "/api/instances/"+created.ID+"/jars/download",
		startDownloadRequest{Project: "velocity", Version: "1.21.11", SetAsJar: true})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
	if job := env.awaitDownload(created.ID); job.State != serverjar.JobDone {
		t.Fatalf("download failed: %s / %s", job.State, job.Error)
	}

	var refreshed instance.Status
	decodeBody(t, env.do(http.MethodGet, "/api/instances/"+created.ID, nil), &refreshed)
	if refreshed.Jar != "velocity-1.21.11-132.jar" {
		t.Errorf("launch jar is %q", refreshed.Jar)
	}
	if len(refreshed.ServerArgs) != 0 {
		t.Errorf("a proxy should not keep %v as server args", refreshed.ServerArgs)
	}
}

func TestDownloadRejectsUnknownVersion(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("bad-version")

	resp := env.do(http.MethodPost, "/api/instances/"+created.ID+"/jars/download",
		startDownloadRequest{Project: "paper", Version: "9.9.9"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown version, got %d", resp.StatusCode)
	}
}

func TestDownloadStatusIsNullBeforeAnyDownload(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("fresh")

	resp := env.do(http.MethodGet, "/api/instances/"+created.ID+"/jars/download", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var job *serverjar.Job
	decodeBody(t, resp, &job)
	if job != nil {
		t.Errorf("expected null, got %+v", job)
	}
}

func TestCancelWithoutADownloadIsAConflict(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("nothing-running")

	resp := env.do(http.MethodPost, "/api/instances/"+created.ID+"/jars/download/cancel", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("expected 409, got %d", resp.StatusCode)
	}
}
