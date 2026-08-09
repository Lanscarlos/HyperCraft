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

// awaitDownload polls the library endpoint the way the UI does.
func (e *testEnv) awaitDownload() serverjar.Job {
	e.t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var library coreLibraryResponse
		decodeBody(e.t, e.do(http.MethodGet, "/api/cores", nil), &library)
		if library.Job != nil && library.Job.State != serverjar.JobDownloading {
			return *library.Job
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("download did not finish in time")
	return serverjar.Job{}
}

// downloadCore fetches one core into the library and returns its ID.
func (e *testEnv) downloadCore(project, version string) string {
	e.t.Helper()

	resp := e.do(http.MethodPost, "/api/cores", startDownloadRequest{Project: project, Version: version})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		e.t.Fatalf("expected 202 starting a download, got %d", resp.StatusCode)
	}
	job := e.awaitDownload()
	if job.State != serverjar.JobDone {
		e.t.Fatalf("download failed: %s / %s", job.State, job.Error)
	}
	return job.CoreID
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

func TestDownloadedCoreLandsInTheLibrary(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	id := env.downloadCore("paper", "1.21.11")
	if id != "paper-1.21.11-132.jar" {
		t.Fatalf("unexpected core id %q", id)
	}

	var library coreLibraryResponse
	decodeBody(t, env.do(http.MethodGet, "/api/cores", nil), &library)
	if len(library.Cores) != 1 {
		t.Fatalf("library holds %d cores, want 1", len(library.Cores))
	}
	core := library.Cores[0]
	if core.Version != "1.21.11" || core.Build != 132 || core.ProjectName != "Paper" {
		t.Errorf("core metadata is wrong: %+v", core)
	}
	if len(core.UsedBy) != 0 {
		t.Errorf("nothing uses it yet, got %v", core.UsedBy)
	}

	data, err := os.ReadFile(filepath.Join(library.Root, "paper-1.21.11-132.jar"))
	if err != nil {
		t.Fatalf("read downloaded jar: %v", err)
	}
	if len(data) != len(env.fill.body) {
		t.Errorf("jar is %d bytes, want %d", len(data), len(env.fill.body))
	}

	// Downloading the same build again must not silently replace it.
	again := env.do(http.MethodPost, "/api/cores",
		startDownloadRequest{Project: "paper", Version: "1.21.11"})
	again.Body.Close()
	if again.StatusCode != http.StatusConflict {
		t.Errorf("expected 409 when the core is already in the library, got %d", again.StatusCode)
	}
}

func TestApplyCoreCopiesItIntoTheInstance(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	id := env.downloadCore("paper", "1.21.11")
	created := env.createInstance("paper-from-library")

	resp := env.do(http.MethodPost, "/api/instances/"+created.ID+"/core",
		applyCoreRequest{CoreID: id, SetAsJar: true})
	var applied applyCoreResponse
	decodeBody(t, resp, &applied)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if applied.FileName != id {
		t.Errorf("copied %q, want %q", applied.FileName, id)
	}
	if applied.Instance.Jar != id {
		t.Errorf("launch jar is %q, want the copied core", applied.Instance.Jar)
	}

	data, err := os.ReadFile(filepath.Join(created.Directory, id))
	if err != nil {
		t.Fatalf("read copied jar: %v", err)
	}
	if len(data) != len(env.fill.body) {
		t.Errorf("copy is %d bytes, want %d", len(data), len(env.fill.body))
	}
	if _, err := os.Stat(filepath.Join(created.Directory, id+".hypercraft-part")); err == nil {
		t.Errorf("the copy left its part file behind")
	}

	// The library now knows the instance is running a copy of this core.
	var library coreLibraryResponse
	decodeBody(t, env.do(http.MethodGet, "/api/cores", nil), &library)
	if len(library.Cores) != 1 || len(library.Cores[0].UsedBy) != 1 {
		t.Fatalf("expected the core to be in use: %+v", library.Cores)
	}
}

// Copying twice would be an accident on a jar that may be the one currently
// running, so it takes an explicit overwrite.
func TestApplyCoreRefusesToClobber(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	id := env.downloadCore("paper", "1.21.11")
	created := env.createInstance("clobber")

	first := env.do(http.MethodPost, "/api/instances/"+created.ID+"/core", applyCoreRequest{CoreID: id})
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first copy: expected 200, got %d", first.StatusCode)
	}

	second := env.do(http.MethodPost, "/api/instances/"+created.ID+"/core", applyCoreRequest{CoreID: id})
	second.Body.Close()
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 on the second copy, got %d", second.StatusCode)
	}

	third := env.do(http.MethodPost, "/api/instances/"+created.ID+"/core",
		applyCoreRequest{CoreID: id, Overwrite: true})
	third.Body.Close()
	if third.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with overwrite, got %d", third.StatusCode)
	}
}

// Velocity is a proxy: it has no world and rejects the --nogui the default
// launch config passes to a Minecraft server.
func TestApplyingVelocityClearsServerArgs(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	id := env.downloadCore("velocity", "1.21.11")
	created := env.createInstance("proxy")

	resp := env.do(http.MethodPost, "/api/instances/"+created.ID+"/core",
		applyCoreRequest{CoreID: id, SetAsJar: true})
	var applied applyCoreResponse
	decodeBody(t, resp, &applied)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if applied.Instance.Jar != "velocity-1.21.11-132.jar" {
		t.Errorf("launch jar is %q", applied.Instance.Jar)
	}
	if len(applied.Instance.ServerArgs) != 0 {
		t.Errorf("a proxy should not keep %v as server args", applied.Instance.ServerArgs)
	}
}

func TestApplyUnknownCoreIs404(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("missing-core")

	for _, id := range []string{"nope.jar", "../escape.jar"} {
		resp := env.do(http.MethodPost, "/api/instances/"+created.ID+"/core", applyCoreRequest{CoreID: id})
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("coreId %q: expected 404, got %d", id, resp.StatusCode)
		}
	}
}

func TestDeleteCoreLeavesInstanceCopiesAlone(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	id := env.downloadCore("paper", "1.21.11")
	created := env.createInstance("keeps-its-jar")

	resp := env.do(http.MethodPost, "/api/instances/"+created.ID+"/core",
		applyCoreRequest{CoreID: id, SetAsJar: true})
	resp.Body.Close()

	deleted := env.do(http.MethodDelete, "/api/cores/"+id, nil)
	deleted.Body.Close()
	if deleted.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", deleted.StatusCode)
	}

	var library coreLibraryResponse
	decodeBody(t, env.do(http.MethodGet, "/api/cores", nil), &library)
	if len(library.Cores) != 0 {
		t.Errorf("core survived deletion: %+v", library.Cores)
	}
	if _, err := os.Stat(filepath.Join(created.Directory, id)); err != nil {
		t.Errorf("the instance's own copy was deleted too: %v", err)
	}
}

func TestDownloadRejectsUnknownVersion(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/cores",
		startDownloadRequest{Project: "paper", Version: "9.9.9"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown version, got %d", resp.StatusCode)
	}
}

func TestLibraryIsEmptyBeforeAnyDownload(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodGet, "/api/cores", nil)
	var library coreLibraryResponse
	decodeBody(t, resp, &library)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if len(library.Cores) != 0 || library.Job != nil {
		t.Errorf("expected an empty library, got %+v", library)
	}
}

func TestCancelWithoutADownloadIsAConflict(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/cores/cancel", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("expected 409, got %d", resp.StatusCode)
	}
}
