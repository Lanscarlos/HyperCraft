package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/javaruntime"
)

// fakeAdoptium serves the Adoptium endpoints the panel uses plus the tarball,
// so an install can be driven end to end without leaving the test.
type fakeAdoptium struct {
	server  *httptest.Server
	archive []byte
}

func newFakeAdoptium(t *testing.T) *fakeAdoptium {
	t.Helper()
	fake := &fakeAdoptium{archive: fakeJREArchive(t)}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /info/available_releases", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"available_releases":[8,17,21],"available_lts_releases":[8,17,21]}`)
	})
	mux.HandleFunc("GET /assets/latest/{major}/hotspot", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("major") == "99" {
			fmt.Fprint(w, `[]`)
			return
		}
		sum := sha256.Sum256(fake.archive)
		fmt.Fprintf(w, `[{"binary":{"image_type":%q,"os":"linux","architecture":"x64","package":{
			"name":"OpenJDK21U-jre_x64_linux_hotspot_21.0.1_12.tar.gz","link":"%s/jre.tar.gz",
			"size":%d,"checksum":%q}},"release_name":"jdk-21.0.1+12",
			"version":{"major":21,"openjdk_version":"21.0.1+12"}}]`,
			r.URL.Query().Get("image_type"), fake.URL(), len(fake.archive), hex.EncodeToString(sum[:]))
	})
	mux.HandleFunc("GET /jre.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		w.Write(fake.archive)
	})

	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeAdoptium) URL() string { return f.server.URL }

// fakeJREArchive builds a tarball shaped like a Temurin JRE: one wrapper
// directory holding release and bin/java.
func fakeJREArchive(t *testing.T) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	writer := tar.NewWriter(gz)
	files := []struct {
		name, body string
		mode       int64
	}{
		{"jdk-21.0.1+12-jre/release", "JAVA_VERSION=\"21.0.1\"\nIMPLEMENTOR=\"Eclipse Adoptium\"\nIMAGE_TYPE=\"JRE\"\n", 0o644},
		{"jdk-21.0.1+12-jre/bin/java", "#!/bin/sh\necho openjdk\n", 0o755},
	}
	for _, file := range files {
		header := &tar.Header{Name: file.name, Mode: file.mode, Size: int64(len(file.body)), Typeflag: tar.TypeReg}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := writer.Write([]byte(file.body)); err != nil {
			t.Fatalf("tar body: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

// awaitInstall polls the overview endpoint the way the Java page does.
func (e *testEnv) awaitInstall() javaruntime.Job {
	e.t.Helper()

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var overview javaOverview
		decodeBody(e.t, e.do(http.MethodGet, "/api/java", nil), &overview)
		if overview.Job != nil &&
			overview.Job.State != javaruntime.JobDownloading &&
			overview.Job.State != javaruntime.JobExtracting {
			return *overview.Job
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("install did not finish in time")
	return javaruntime.Job{}
}

func TestJavaOverviewOnAFreshPanel(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	var overview javaOverview
	decodeBody(t, env.do(http.MethodGet, "/api/java", nil), &overview)

	if len(overview.Runtimes) != 0 {
		t.Errorf("expected no runtimes, got %+v", overview.Runtimes)
	}
	if overview.Platform.OS == "" {
		t.Errorf("the platform should be reported so the UI can offer a download")
	}
	if overview.Job != nil {
		t.Errorf("expected no install job, got %+v", overview.Job)
	}
}

func TestListJavaMajors(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	var majors []availableMajor
	decodeBody(t, env.do(http.MethodGet, "/api/java/available", nil), &majors)

	if len(majors) != 3 || majors[0].Major != 21 {
		t.Fatalf("expected newest first, got %+v", majors)
	}
	if !majors[0].LTS || majors[0].Installed {
		t.Errorf("21 should be LTS and not yet installed: %+v", majors[0])
	}
}

func TestInstallJavaThenDelete(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/java/install", installJavaRequest{
		Major: 21, ImageType: "jre", Source: javaruntime.SourceOfficial,
	})
	var started javaruntime.Job
	decodeBody(t, resp, &started)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
	if started.RuntimeID != "temurin-21.0.1-12-jre" {
		t.Fatalf("unexpected job: %+v", started)
	}

	if job := env.awaitInstall(); job.State != javaruntime.JobDone {
		t.Fatalf("install failed: %s / %s", job.State, job.Error)
	}

	var overview javaOverview
	decodeBody(t, env.do(http.MethodGet, "/api/java", nil), &overview)
	if len(overview.Runtimes) != 1 {
		t.Fatalf("expected one runtime, got %+v", overview.Runtimes)
	}
	runtime := overview.Runtimes[0]
	if runtime.Major != 21 || runtime.Version != "21.0.1" {
		t.Errorf("unexpected runtime: %+v", runtime)
	}
	if _, err := os.Stat(runtime.JavaPath); err != nil {
		t.Errorf("javaPath does not exist: %v", err)
	}
	if len(runtime.UsedBy) != 0 {
		t.Errorf("nothing uses it yet: %+v", runtime.UsedBy)
	}

	// Installing the same build again has nothing to do.
	again := env.do(http.MethodPost, "/api/java/install", installJavaRequest{
		Major: 21, ImageType: "jre", Source: javaruntime.SourceOfficial,
	})
	again.Body.Close()
	if again.StatusCode != http.StatusConflict {
		t.Errorf("expected 409 for an already-installed runtime, got %d", again.StatusCode)
	}

	// And now it shows up as installed in the picker.
	var majors []availableMajor
	decodeBody(t, env.do(http.MethodGet, "/api/java/available", nil), &majors)
	if !majors[0].Installed {
		t.Errorf("21 should be marked installed: %+v", majors[0])
	}

	deleted := env.do(http.MethodDelete, "/api/java/"+runtime.ID, nil)
	deleted.Body.Close()
	if deleted.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", deleted.StatusCode)
	}
	if _, err := os.Stat(runtime.Path); err == nil {
		t.Errorf("runtime directory survived the delete")
	}
}

// An instance pointed at a runtime is listed against it, so the operator can
// see what a delete would break before doing it.
func TestRuntimeReportsTheInstancesUsingIt(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/java/install", installJavaRequest{
		Major: 21, ImageType: "jre", Source: javaruntime.SourceOfficial,
	})
	resp.Body.Close()
	if job := env.awaitInstall(); job.State != javaruntime.JobDone {
		t.Fatalf("install failed: %s / %s", job.State, job.Error)
	}

	var overview javaOverview
	decodeBody(t, env.do(http.MethodGet, "/api/java", nil), &overview)
	javaPath := overview.Runtimes[0].JavaPath

	created := env.createInstance("uses-java-21")
	updated := env.do(http.MethodPut, "/api/instances/"+created.ID, instanceRequest{
		Name: created.Name, Directory: created.Directory, Java: javaPath, Jar: "server.jar",
	})
	var refreshed instance.Status
	decodeBody(t, updated, &refreshed)
	if refreshed.Java != javaPath {
		t.Fatalf("instance java is %q", refreshed.Java)
	}

	decodeBody(t, env.do(http.MethodGet, "/api/java", nil), &overview)
	if got := overview.Runtimes[0].UsedBy; len(got) != 1 || got[0] != "uses-java-21" {
		t.Errorf("usedBy = %+v, want the instance name", got)
	}
	if overview.Runtimes[0].Live {
		t.Errorf("the instance is not running")
	}
}

func TestInstallUnknownMajorIsRejected(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/java/install", installJavaRequest{
		Major: 99, ImageType: "jre", Source: javaruntime.SourceOfficial,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// The download source is remembered from one install to the next: the reason
// to be off the official link — this machine's route to GitHub — does not
// change between them.
func TestInstallRemembersTheDownloadSource(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	var fresh javaOverview
	decodeBody(t, env.do(http.MethodGet, "/api/java", nil), &fresh)
	if fresh.Source != javaruntime.SourceAuto {
		t.Errorf("a panel that has never installed should default to %q, got %q",
			javaruntime.SourceAuto, fresh.Source)
	}
	if len(fresh.Sources) < 2 || fresh.Sources[0].ID != javaruntime.SourceAuto {
		t.Errorf("the page needs the source list, got %+v", fresh.Sources)
	}

	resp := env.do(http.MethodPost, "/api/java/install", installJavaRequest{
		Major: 21, ImageType: "jre", Source: javaruntime.SourceOfficial,
	})
	resp.Body.Close()
	if job := env.awaitInstall(); job.State != javaruntime.JobDone {
		t.Fatalf("install failed: %s / %s", job.State, job.Error)
	}

	var overview javaOverview
	decodeBody(t, env.do(http.MethodGet, "/api/java", nil), &overview)
	if overview.Source != javaruntime.SourceOfficial {
		t.Errorf("source = %q, want the one that was just used", overview.Source)
	}
	if overview.Job == nil || overview.Job.Source != javaruntime.SourceOfficial {
		t.Errorf("the job should report the source that served it: %+v", overview.Job)
	}

	// And it outlives the process, not just this server's memory.
	panel, err := env.store.LoadPanel()
	if err != nil {
		t.Fatalf("LoadPanel: %v", err)
	}
	if panel.JavaSource != javaruntime.SourceOfficial {
		t.Errorf("panel.json holds %q", panel.JavaSource)
	}
}

func TestInstallRejectsAnUnknownDownloadSource(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/java/install", installJavaRequest{
		Major: 21, ImageType: "jre", Source: "mirrors.evil.example",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestDeleteUnknownRuntime(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodDelete, "/api/java/does-not-exist", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

// The ID lands in a filesystem path that gets deleted recursively, so a
// traversal attempt must not reach outside the runtimes directory.
func TestDeleteRejectsTraversalIDs(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	victim := filepath.Join(t.TempDir(), "important")
	if err := os.WriteFile(victim, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("write victim: %v", err)
	}

	resp := env.do(http.MethodDelete, "/api/java/..%2F..%2F..%2Fetc", nil)
	resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		t.Errorf("a traversal id was accepted")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("the file outside the runtimes root is gone: %v", err)
	}
}

func TestCancelJavaInstallWithoutOne(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/java/install/cancel", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("expected 409, got %d", resp.StatusCode)
	}
}
