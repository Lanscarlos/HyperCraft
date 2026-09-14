package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
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
	mux.HandleFunc("GET /projects/{project}/versions/{version}/builds", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("version") != "1.21.11" {
			http.NotFound(w, r)
			return
		}
		sum := sha256.Sum256(fill.body)
		// Oldest first, the way upstream returns them: the client is what puts
		// them the right way round.
		fmt.Fprintf(w, `[
			{"id":131,"channel":"STABLE","time":"2026-05-10T11:43:09Z","commits":[{"message":"Earlier build"}],
			 "downloads":{"server:default":{"name":"%[1]s-1.21.11-131.jar","url":"%[2]s/artifact","size":%[3]d,"checksums":{"sha256":"%[4]s"}}}},
			{"id":132,"channel":"STABLE","time":"2026-05-11T11:43:09Z","commits":[{"message":"Newer build"}],
			 "downloads":{"server:default":{"name":"%[1]s-1.21.11-132.jar","url":"%[2]s/artifact","size":%[3]d,"checksums":{"sha256":"%[4]s"}}}}
		]`, r.PathValue("project"), fill.URL(), len(fill.body), hex.EncodeToString(sum[:]))
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
	// The jar is what makes it a proxy, and the panel has to remember: every
	// page that differs between the two reads this and nothing else.
	if applied.Instance.Kind != instance.KindProxy {
		t.Errorf("kind = %q, want proxy", applied.Instance.Kind)
	}
	if applied.Instance.StopCommand != "end" {
		t.Errorf("stopCommand = %q, want end", applied.Instance.StopCommand)
	}
}

// A stop command the operator typed themselves is theirs. Switching the jar
// changes what the instance is, not what they decided.
func TestApplyingVelocityKeepsACustomStopCommand(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	id := env.downloadCore("velocity", "1.21.11")
	created := env.createInstance("proxy-with-opinions")

	resp := env.do(http.MethodPut, "/api/instances/"+created.ID,
		instanceRequest{Name: created.Name, Directory: created.Directory, StopCommand: "save-all"})
	resp.Body.Close()

	resp = env.do(http.MethodPost, "/api/instances/"+created.ID+"/core",
		applyCoreRequest{CoreID: id, SetAsJar: true})
	var applied applyCoreResponse
	decodeBody(t, resp, &applied)

	if applied.Instance.Kind != instance.KindProxy {
		t.Errorf("kind = %q, want proxy", applied.Instance.Kind)
	}
	if applied.Instance.StopCommand != "save-all" {
		t.Errorf("stopCommand = %q, want the operator's own", applied.Instance.StopCommand)
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

// Cancelling is /api/downloads/{id}/cancel now — one route for every shelf,
// naming the job rather than meaning "whatever this shelf is doing". An id the
// panel does not hold answers 404; see handlers_downloads_queue_test.go for the
// rest of that route, including why it is 404 and not 403.
func TestCancellingANonexistentDownloadIsNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	if got := env.status(http.MethodPost, "/api/downloads/no-such-job/cancel", nil); got != http.StatusNotFound {
		t.Errorf("expected 404, got %d", got)
	}
}

func TestDownloadedCoreRecordsItsRunningRequirements(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	env.downloadCore("paper", "1.21.11")

	var library coreLibraryResponse
	decodeBody(t, env.do(http.MethodGet, "/api/cores", nil), &library)
	if len(library.Cores) != 1 {
		t.Fatalf("library holds %d cores, want 1", len(library.Cores))
	}
	// Read off the version listing at download time, so the row can say what it
	// needs on a machine that is offline afterwards.
	if got := library.Cores[0].JavaMinimum; got != 21 {
		t.Errorf("javaMinimum = %d, want 21", got)
	}
	if got := library.Cores[0].Minecraft; got != "1.21.11" {
		t.Errorf("minecraft = %q, want the version id for a world server", got)
	}
}

// uploadCore posts a jar the way the 添加核心 dialog's upload branch does.
func (e *testEnv) uploadCore(name string, fields map[string]string) *http.Response {
	e.t.Helper()

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := form.WriteField(key, value); err != nil {
			e.t.Fatalf("write field %s: %v", key, err)
		}
	}
	part, err := form.CreateFormFile("file", name)
	if err != nil {
		e.t.Fatalf("create file part: %v", err)
	}
	part.Write([]byte("not really a jar, but bytes are bytes"))
	if err := form.Close(); err != nil {
		e.t.Fatalf("close form: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, e.server.URL+"/api/cores/upload", &body)
	if err != nil {
		e.t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set(csrfHeader, "1")
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("upload: %v", err)
	}
	return resp
}

func TestUploadedCoreKeepsTheMetadataTheOperatorFilledIn(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.uploadCore("forge-1.20.1-47.2.0.jar", map[string]string{
		"kind":        "server",
		"version":     "1.20.1",
		"javaMinimum": "17",
		"minecraft":   "1.20.1",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload returned %d, want 201", resp.StatusCode)
	}

	var library coreLibraryResponse
	decodeBody(t, env.do(http.MethodGet, "/api/cores", nil), &library)
	if len(library.Cores) != 1 {
		t.Fatalf("library holds %d cores, want 1", len(library.Cores))
	}
	core := library.Cores[0]
	// Nothing upstream knows this jar, so every one of these came off the form.
	if core.Version != "1.20.1" || core.JavaMinimum != 17 || core.Kind != "server" {
		t.Errorf("uploaded core lost its metadata: %+v", core)
	}
	// The panel holds the bytes but did not choose them, which is what the
	// 手动放入 chip on the row reads.
	if !core.Imported {
		t.Errorf("an uploaded core should still read as imported: %+v", core)
	}
	if core.SHA256 == "" {
		t.Errorf("an uploaded core should be checksummed: %+v", core)
	}
}

func TestUploadedCoreCanBeFetchedBack(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	env.uploadCore("custom.jar", map[string]string{"kind": "server"}).Body.Close()

	resp := env.do(http.MethodGet, "/api/cores/custom.jar/file", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fetch returned %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "not really a jar, but bytes are bytes" {
		t.Errorf("fetched %q", body)
	}
}

func TestUploadRejectsSomethingThatIsNotAJar(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.uploadCore("notes.txt", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("uploading a non-jar returned %d", resp.StatusCode)
	}
}

func TestCoreBuildsAreListedNewestFirst(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodGet, "/api/downloads/projects/paper/versions/1.21.11/builds", nil)
	var builds []serverjar.Build
	decodeBody(t, resp, &builds)
	if len(builds) != 2 || builds[0].Build != 132 || builds[1].Build != 131 {
		t.Fatalf("unexpected builds: %+v", builds)
	}
}
