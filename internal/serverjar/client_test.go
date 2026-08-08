package serverjar

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

const versionsPayload = `{"versions":[
  {"version":{"id":"1.21.11","support":{"status":"SUPPORTED"},"java":{"version":{"minimum":21}}},"builds":[130,131,132]},
  {"version":{"id":"1.21.11-pre2","support":{"status":"UNSUPPORTED"},"java":{"version":{"minimum":21}}},"builds":[1]},
  {"version":{"id":"1.20.6","support":{"status":"UNSUPPORTED"},"java":{"version":{"minimum":17}}},"builds":[]}
]}`

func buildPayload(url, sha string, size int64) string {
	return `{"id":132,"time":"2026-05-11T11:43:09Z","channel":"STABLE","downloads":{"server:default":{` +
		`"name":"paper-1.21.11-132.jar","url":"` + url + `","size":` + strconv.FormatInt(size, 10) +
		`,"checksums":{"sha256":"` + sha + `"}}}}`
}

func TestVersionsParsesAndFilters(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/projects/paper/versions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		hits.Add(1)
		w.Write([]byte(versionsPayload))
	}))
	defer upstream.Close()

	client := NewClient(upstream.URL, "test")
	versions, err := client.Versions(context.Background(), "paper")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}

	// 1.20.6 has no builds, so it cannot be downloaded and is not offered.
	if len(versions) != 2 {
		t.Fatalf("got %d versions, want 2: %+v", len(versions), versions)
	}
	if versions[0].ID != "1.21.11" || !versions[0].Stable || versions[0].JavaMinimum != 21 {
		t.Errorf("unexpected first version: %+v", versions[0])
	}
	if versions[0].Support != "SUPPORTED" || versions[0].Builds != 3 {
		t.Errorf("unexpected support/build data: %+v", versions[0])
	}
	if versions[1].Stable {
		t.Errorf("%s should be flagged unstable", versions[1].ID)
	}

	// The second call must come from the cache: the launch settings page asks
	// on every open and upstream rate-limits per IP.
	if _, err := client.Versions(context.Background(), "paper"); err != nil {
		t.Fatalf("second Versions: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("upstream hit %d times, want 1", got)
	}
}

func TestVersionsRejectsUnknownProject(t *testing.T) {
	client := NewClient("http://127.0.0.1:1", "test")
	if _, err := client.Versions(context.Background(), "forge"); !errors.Is(err, ErrUnknownProject) {
		t.Fatalf("got %v, want ErrUnknownProject", err)
	}
}

func TestLatestBuildParsesDownload(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/projects/paper/versions/1.21.11/builds/latest" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(buildPayload("https://cdn.example/paper.jar", "ABCDEF", 42)))
	}))
	defer upstream.Close()

	build, err := NewClient(upstream.URL, "test").LatestBuild(context.Background(), "paper", "1.21.11")
	if err != nil {
		t.Fatalf("LatestBuild: %v", err)
	}
	if build.Build != 132 || build.FileName != "paper-1.21.11-132.jar" || build.Size != 42 {
		t.Errorf("unexpected build: %+v", build)
	}
	if build.SHA256 != "abcdef" {
		t.Errorf("checksum should be lowercased for comparison, got %q", build.SHA256)
	}
	if !build.Recommended() {
		t.Errorf("STABLE build should be recommended")
	}
}

func TestLatestBuildRejectsBadVersionID(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream should not have been called for %s", r.URL.Path)
	}))
	defer upstream.Close()

	client := NewClient(upstream.URL, "test")
	for _, bad := range []string{"../../secret", "1.21/../..", "", strings.Repeat("1", 100)} {
		if _, err := client.LatestBuild(context.Background(), "paper", bad); !errors.Is(err, ErrUnknownVersion) {
			t.Errorf("version %q: got %v, want ErrUnknownVersion", bad, err)
		}
	}
}

func TestLatestBuildMapsNotFound(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	_, err := NewClient(upstream.URL, "test").LatestBuild(context.Background(), "paper", "9.9.9")
	if !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("got %v, want ErrUnknownVersion", err)
	}
}

// A download URL comes from a response but drives an outbound request from the
// operator's machine, so anything that is not plain HTTP(S) has to be refused.
func TestLatestBuildRejectsNonHTTPDownload(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(buildPayload("file:///etc/passwd", "abc", 1)))
	}))
	defer upstream.Close()

	_, err := NewClient(upstream.URL, "test").LatestBuild(context.Background(), "paper", "1.21.11")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("got %v, want ErrUpstream", err)
	}
}

func TestLatestBuildRejectsEscapingFileName(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":1,"channel":"STABLE","downloads":{"server:default":{` +
			`"name":"../../evil.jar","url":"https://cdn.example/x.jar","size":1,` +
			`"checksums":{"sha256":"ab"}}}}`))
	}))
	defer upstream.Close()

	_, err := NewClient(upstream.URL, "test").LatestBuild(context.Background(), "paper", "1.21.11")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("got %v, want ErrUpstream", err)
	}
}

func TestUpstreamErrorStatus(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	_, err := NewClient(upstream.URL, "test").Versions(context.Background(), "paper")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("got %v, want ErrUpstream", err)
	}
}

func TestLookupProject(t *testing.T) {
	paper, ok := LookupProject("paper")
	if !ok || paper.IsProxy() {
		t.Errorf("paper should be a known server core, got %+v ok=%v", paper, ok)
	}
	velocity, ok := LookupProject("velocity")
	if !ok || !velocity.IsProxy() {
		t.Errorf("velocity should be a known proxy, got %+v ok=%v", velocity, ok)
	}
	if _, ok := LookupProject("purpur"); ok {
		t.Errorf("purpur is not in the catalogue")
	}
}
