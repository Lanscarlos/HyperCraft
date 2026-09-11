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

// fakeAzul serves the Azul metadata shape over the same archive fakeAdoptium
// hands out, so an install through either distribution drives the same unpack.
type fakeAzul struct {
	server  *httptest.Server
	archive []byte
}

func newFakeAzul(t *testing.T) *fakeAzul {
	t.Helper()
	fake := &fakeAzul{archive: fakeJREArchive(t)}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /packages/", func(w http.ResponseWriter, r *http.Request) {
		// The real API has one endpoint for both questions; only the feature
		// lookup names a platform. The version listing is per package, so a
		// feature release repeats — which is what Majors has to fold down.
		if r.URL.Query().Get("os") == "" {
			fmt.Fprint(w, `[{"java_version":[21,0,12,1],"support_term":"lts"},
				{"java_version":[21,0,11],"support_term":"lts"},
				{"java_version":[17,0,13],"support_term":"lts"},
				{"java_version":[8,0,432],"support_term":"lts"}]`)
			return
		}
		sum := sha256.Sum256(fake.archive)
		fmt.Fprintf(w, `[{"name":"zulu21.52.203-ca-jre21.0.12.1-linux_x64.tar.gz",
			"java_version":[21,0,12,1],"download_url":"%s/zulu.tar.gz",
			"sha256_hash":%q,"size":%d,"support_term":"lts"}]`,
			fake.URL(), hex.EncodeToString(sum[:]), len(fake.archive))
	})
	mux.HandleFunc("GET /zulu.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		w.Write(fake.archive)
	})

	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeAzul) URL() string { return f.server.URL }

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
		Distribution: javaruntime.DistTemurin,
		Major:        21, ImageType: "jre", Source: javaruntime.SourceOfficial,
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
		Distribution: javaruntime.DistTemurin,
		Major:        21, ImageType: "jre", Source: javaruntime.SourceOfficial,
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
		Distribution: javaruntime.DistTemurin,
		Major:        21, ImageType: "jre", Source: javaruntime.SourceOfficial,
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
		Distribution: javaruntime.DistTemurin,
		Major:        99, ImageType: "jre", Source: javaruntime.SourceOfficial,
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
		Distribution: javaruntime.DistTemurin,
		Major:        21, ImageType: "jre", Source: javaruntime.SourceOfficial,
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

// A panel that has never installed anything offers Zulu, and says so.
func TestJavaOverviewDefaultsToZulu(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	var overview javaOverview
	decodeBody(t, env.do(http.MethodGet, "/api/java", nil), &overview)

	if overview.Distribution != javaruntime.DistZulu {
		t.Errorf("default distribution = %q, want %q", overview.Distribution, javaruntime.DistZulu)
	}
	if len(overview.Distributions) != 2 || overview.Distributions[0].ID != javaruntime.DistZulu {
		t.Errorf("unexpected distribution list: %+v", overview.Distributions)
	}
	// The source list belongs to the selected distribution: the Adoptium
	// mirrors carry no Zulu, so offering them here is a guaranteed 404.
	for _, source := range overview.Sources {
		if source.ID == "tuna" {
			t.Error("the Adoptium mirrors must not be offered for Zulu")
		}
	}
}

// Installing from the default distribution works end to end, and the runtime
// lands under its own prefix so a Temurin build of the same version could sit
// beside it.
func TestInstallZuluByDefault(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/java/install", installJavaRequest{
		Major: 21, ImageType: "jre",
	})
	var started javaruntime.Job
	decodeBody(t, resp, &started)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
	if started.Distribution != javaruntime.DistZulu {
		t.Errorf("job distribution = %q", started.Distribution)
	}
	if started.RuntimeID != "zulu-21.0.12.1-jre" {
		t.Errorf("runtime id = %q, want zulu-21.0.12.1-jre", started.RuntimeID)
	}
	if job := env.awaitInstall(); job.State != javaruntime.JobDone {
		t.Fatalf("install ended %s: %s", job.State, job.Error)
	}
}

// The distribution an install named is remembered, the same way the source is.
func TestInstallRemembersTheDistribution(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/java/install", installJavaRequest{
		Distribution: javaruntime.DistTemurin,
		Major:        21, ImageType: "jre", Source: javaruntime.SourceOfficial,
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
	if job := env.awaitInstall(); job.State != javaruntime.JobDone {
		t.Fatalf("install ended %s: %s", job.State, job.Error)
	}

	var overview javaOverview
	decodeBody(t, env.do(http.MethodGet, "/api/java", nil), &overview)
	if overview.Distribution != javaruntime.DistTemurin {
		t.Errorf("overview distribution = %q", overview.Distribution)
	}
	// The source list follows it, so the Adoptium mirrors are back on offer.
	if len(overview.Sources) < 3 {
		t.Errorf("expected the Temurin mirrors, got %+v", overview.Sources)
	}

	panel, err := env.store.LoadPanel()
	if err != nil {
		t.Fatalf("LoadPanel: %v", err)
	}
	if panel.JavaDistribution != javaruntime.DistTemurin {
		t.Errorf("panel.json holds %q", panel.JavaDistribution)
	}
}

// An unknown distribution is a bad request, not a silent fallback.
func TestInstallRefusesAnUnknownDistribution(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/java/install", installJavaRequest{
		Distribution: "graalvm", Major: 21, ImageType: "jre",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for an unknown distribution, got %d", resp.StatusCode)
	}
}
