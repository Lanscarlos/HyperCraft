package selfupdate

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// mirrorProxy stands in for ghfast.top and friends: a prefix proxy that serves
// GitHub URLs appended to its own. It records what was asked of it and can be
// told to fail, or to lie about the bytes.
type mirrorProxy struct {
	t      *testing.T
	server *httptest.Server
	origin *httptest.Server

	mu      sync.Mutex
	asked   []string
	broken  bool
	payload map[string][]byte // path suffix -> replacement body
}

func newMirrorProxy(t *testing.T, origin *httptest.Server) *mirrorProxy {
	t.Helper()
	m := &mirrorProxy{t: t, origin: origin, payload: map[string][]byte{}}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Requests arrive as /<full origin URL>; the proxied target is the path.
		target := strings.TrimPrefix(r.URL.RequestURI(), "/")

		m.mu.Lock()
		m.asked = append(m.asked, target)
		broken := m.broken
		var replacement []byte
		for suffix, body := range m.payload {
			if strings.HasSuffix(target, suffix) {
				replacement = body
			}
		}
		m.mu.Unlock()

		if broken {
			http.Error(w, "mirror down", http.StatusBadGateway)
			return
		}
		if replacement != nil {
			_, _ = w.Write(replacement)
			return
		}
		resp, err := http.Get(target) //nolint:gosec // target is the test origin
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(m.server.Close)
	return m
}

func (m *mirrorProxy) prefix() string { return m.server.URL + "/" }

func (m *mirrorProxy) requests() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.asked...)
}

func (m *mirrorProxy) setBroken(broken bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.broken = broken
}

func TestMirrorRewritesOnlyGitHubURLs(t *testing.T) {
	u := New("owner/repo", "v1.0.0")
	u.SetMirror("https://ghfast.top")

	// A missing trailing slash is the obvious way to mis-type a prefix, and
	// would otherwise glue the two URLs together.
	if got := u.Mirror(); got != "https://ghfast.top/" {
		t.Errorf("Mirror() = %q, want a trailing slash", got)
	}

	gh := "https://github.com/o/r/releases/download/v1/x.tar.gz"
	if got := u.mirrored(gh); got != "https://ghfast.top/"+gh {
		t.Errorf("mirrored(github) = %q", got)
	}
	// These proxies only front GitHub; prefixing anything else yields a 404 at
	// best, and at worst sends a request somewhere the operator did not intend.
	for _, other := range []string{
		"https://api.github.com/repos/o/r/releases/latest",
		"https://example.invalid/x.tar.gz",
	} {
		if got := u.mirrored(other); got != "" {
			t.Errorf("mirrored(%q) = %q, want no rewrite", other, got)
		}
	}

	u.SetMirror("")
	if got := u.mirrored(gh); got != "" {
		t.Errorf("mirrored with no mirror = %q, want no rewrite", got)
	}
}

func TestArchiveUsesTheMirrorAndChecksumsDoNot(t *testing.T) {
	// The whole point of the split, with both sides reachable: the megabytes go
	// through the proxy, while the bytes that decide what is legitimate are
	// taken from the origin. If the checksums ever started coming from the
	// mirror by preference, a mirror could serve its own binary with a matching
	// digest and this would be a plain code-execution hole.
	f := newFakeRelease(t, "1.2.0", []byte("new binary"))
	mirror := newMirrorProxy(t, f.server)

	u, exe := f.updaterFor(t, "v1.0.0")
	// Let the mirror front the test origin, which stands in for github.com.
	u.downloadPrefix = f.server.URL
	u.SetMirror(mirror.prefix())

	rel, err := u.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	staged, err := u.Prepare(context.Background(), rel, nil)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	asked := mirror.requests()
	var sawArchive, sawSums bool
	for _, target := range asked {
		if strings.HasSuffix(target, "/dl/archive") {
			sawArchive = true
		}
		if strings.HasSuffix(target, "/dl/sums") {
			sawSums = true
		}
	}
	if !sawArchive {
		t.Errorf("the archive did not go through the mirror; requests: %v", asked)
	}
	if sawSums {
		t.Errorf("the checksums went through the mirror; requests: %v", asked)
	}
	if staged.ChecksumFromMirror() {
		t.Error("ChecksumFromMirror = true although the origin served them")
	}

	if err := staged.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "new binary" {
		t.Errorf("binary = %q, want the new one", got)
	}
}

func TestMirrorCannotSubstituteTheBinary(t *testing.T) {
	// A hostile mirror serving a different binary must be caught, because the
	// digest it is checked against came from the origin, not from the mirror.
	f := newFakeRelease(t, "1.2.0", []byte("new binary"))
	mirror := newMirrorProxy(t, f.server)

	u, exe := f.updaterFor(t, "v1.0.0")
	u.downloadPrefix = f.server.URL
	u.SetMirror(mirror.prefix())

	mirror.mu.Lock()
	mirror.payload["/dl/archive"] = []byte("a completely different payload")
	mirror.mu.Unlock()

	rel, err := u.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if _, err := u.Prepare(context.Background(), rel, nil); err == nil {
		t.Fatal("Prepare installed a binary the mirror substituted")
	} else if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error = %v, want a checksum mismatch", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "old binary" {
		t.Errorf("executable = %q, want it untouched", got)
	}
}

func TestPrepareFallsBackToGitHubWhenTheMirrorIsDown(t *testing.T) {
	// A dead mirror must be a slowdown, not an outage: the download retries
	// against the origin and the update still lands.
	f := newFakeRelease(t, "1.2.0", []byte("new binary"))
	mirror := newMirrorProxy(t, f.server)
	mirror.setBroken(true)

	u, exe := f.updaterFor(t, "v1.0.0")
	u.downloadPrefix = f.server.URL
	u.SetMirror(mirror.prefix())

	rel, err := u.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	staged, err := u.Prepare(context.Background(), rel, nil)
	if err != nil {
		t.Fatalf("Prepare with a dead mirror: %v", err)
	}
	if staged.ChecksumFromMirror() {
		t.Error("ChecksumFromMirror = true although the checksums came from the origin")
	}
	if asked := mirror.requests(); len(asked) == 0 {
		t.Error("the mirror was never tried, so the fallback proves nothing")
	}
	if err := staged.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "new binary" {
		t.Errorf("binary = %q, want the new one", got)
	}
}

func TestNoMirrorMeansEveryRequestGoesDirect(t *testing.T) {
	f := newFakeRelease(t, "1.2.0", []byte("new binary"))
	mirror := newMirrorProxy(t, f.server)

	u, _ := f.updaterFor(t, "v1.0.0")
	u.downloadPrefix = f.server.URL
	u.SetMirror("")

	rel, err := u.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if _, err := u.Prepare(context.Background(), rel, nil); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if asked := mirror.requests(); len(asked) != 0 {
		t.Errorf("mirror received %v with mirroring turned off", asked)
	}
}

func TestGetFirstPrefersTheFirstURLAndFallsBack(t *testing.T) {
	var hits []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/broken" {
			http.Error(w, "no", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	u := New("owner/repo", "v1.0.0")

	body, _, used, err := u.getFirst(context.Background(), []string{srv.URL + "/good", srv.URL + "/broken"})
	if err != nil {
		t.Fatalf("getFirst: %v", err)
	}
	body.Close()
	if used != srv.URL+"/good" {
		t.Errorf("used = %q, want the first URL", used)
	}
	mu.Lock()
	first := append([]string(nil), hits...)
	mu.Unlock()
	if len(first) != 1 {
		t.Errorf("tried %v, want only the first URL when it works", first)
	}

	body, _, used, err = u.getFirst(context.Background(), []string{srv.URL + "/broken", srv.URL + "/good"})
	if err != nil {
		t.Fatalf("getFirst with a broken first URL: %v", err)
	}
	body.Close()
	if used != srv.URL+"/good" {
		t.Errorf("used = %q, want the fallback", used)
	}

	if _, _, _, err := u.getFirst(context.Background(), []string{srv.URL + "/broken"}); err == nil {
		t.Error("getFirst succeeded with every URL failing")
	}
}

func TestServiceMirrorIsReportedAndSettable(t *testing.T) {
	svc := NewService("owner/repo", "v1.0.0", "https://ghfast.top/", ChannelStable, Hooks{},
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	if got := svc.Status().Mirror; got != "https://ghfast.top/" {
		t.Errorf("Status().Mirror = %q", got)
	}
	if err := svc.SetMirror(""); err != nil {
		t.Fatalf("SetMirror: %v", err)
	}
	if got := svc.Status().Mirror; got != "" {
		t.Errorf("Status().Mirror after clearing = %q, want empty", got)
	}
}

func TestChecksumFromMirrorIsFlaggedWhenGitHubIsUnreachable(t *testing.T) {
	// Degraded mode: with the origin unreachable for the checksums, the mirror
	// supplies both halves and so is trusted for this run. The panel must know,
	// because that is the one case a mirror could substitute a binary.
	f := newFakeRelease(t, "1.2.0", []byte("new binary"))
	u, _ := f.updaterFor(t, "v1.0.0")

	rel, err := u.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	mirror := newMirrorProxy(t, f.server)
	// Rewrite both assets to unreachable github.com-shaped URLs whose mirrored
	// form the proxy can still resolve back to the live test server.
	sums := rel.assets["SHA256SUMS.txt"]
	archive := rel.assets[AssetName(rel.Version)]
	rel.assets["SHA256SUMS.txt"] = "https://github.com/unreachable/sums"
	rel.assets[AssetName(rel.Version)] = "https://github.com/unreachable/archive"

	mirror.mu.Lock()
	mirror.payload["/unreachable/sums"] = fetch(t, sums)
	mirror.payload["/unreachable/archive"] = fetch(t, archive)
	mirror.mu.Unlock()

	u.SetMirror(mirror.prefix())

	staged, err := u.Prepare(context.Background(), rel, nil)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !staged.ChecksumFromMirror() {
		t.Error("ChecksumFromMirror = false although GitHub was unreachable and the mirror served them")
	}

	// And the request order proves the origin was tried first for the sums.
	asked := mirror.requests()
	if len(asked) == 0 || !strings.Contains(asked[0], "/unreachable/sums") {
		t.Errorf("mirror requests = %v, want the checksums attempted first", asked)
	}
	staged.Discard()
	_ = filepath.Dir(staged.Path())
}

func fetch(t *testing.T, url string) []byte {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec // test server URL
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
