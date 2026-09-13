package plugin

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lanscarlos/hypercraft/internal/download"
	"time"
)

// downloadStub is a GitHub that publishes one release per plugin and serves the
// jar from the same host, with a hook for holding transfers open — which is the
// only way to observe a queue: everything else finishes too fast to overlap.
type downloadStub struct {
	server *httptest.Server
	// inFlight counts transfers currently sitting inside the handler, and peak
	// is the most that were ever there at once. peak is the assertion: the
	// concurrency limit is a claim about simultaneity, not about totals.
	inFlight atomic.Int32
	peak     atomic.Int32
	// release is closed to let held transfers finish.
	release chan struct{}
	hold    bool
	started chan string
}

func newDownloadStub(t *testing.T, hold bool) *downloadStub {
	t.Helper()
	stub := &downloadStub{
		release: make(chan struct{}),
		hold:    hold,
		started: make(chan string, 64),
	}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases") {
			owner, name, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/repos/"), "/")
			name = strings.TrimSuffix(name, "/releases")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `[{"tag_name":"v1.0.0","name":"One","draft":false,"prerelease":false,
			  "published_at":"2026-01-01T00:00:00Z",
			  "assets":[{"name":"%s-1.0.0.jar","size":4,
			             "browser_download_url":"%s/dl/%s"}]}]`, name, stub.server.URL, owner+"-"+name)
			return
		}

		now := stub.inFlight.Add(1)
		for {
			peak := stub.peak.Load()
			if now <= peak || stub.peak.CompareAndSwap(peak, now) {
				break
			}
		}
		defer stub.inFlight.Add(-1)

		select {
		case stub.started <- r.URL.Path:
		default:
		}
		if stub.hold {
			select {
			case <-stub.release:
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Second):
			}
		}
		_, _ = io.WriteString(w, "jar!")
	}))
	t.Cleanup(func() {
		stub.releaseAll()
		stub.server.Close()
	})
	return stub
}

func (s *downloadStub) releaseAll() {
	select {
	case <-s.release:
	default:
		close(s.release)
	}
}

// downloaderFixture wires a downloader to the stub with `count` tracked
// plugins, all of which have exactly one release to fetch.
func downloaderFixture(t *testing.T, hold bool, count int) (*Downloader, *downloadStub, []Plugin) {
	t.Helper()
	stub := newDownloadStub(t, hold)
	client := NewClient(stub.server.URL, "test")
	library := newLibrary(t)

	items := make([]Plugin, 0, count)
	for i := range count {
		items = append(items, addPlugin(t, library, fmt.Sprintf("Plug%d", i), fmt.Sprintf("owner%d/plug%d", i, i)))
	}
	queue := download.NewQueue(slog.New(slog.DiscardHandler))
	t.Cleanup(queue.Close)
	downloader := NewDownloader(client, library, queue, slog.New(slog.DiscardHandler))
	return downloader, stub, items
}

// waitFor polls until the condition holds, so a test never depends on how long
// a goroutine took to get going.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func countState(jobs []Job, state string) int {
	n := 0
	for _, job := range jobs {
		if job.State == state {
			n++
		}
	}
	return n
}

// The queue, its concurrency, its history and its checksum policy are
// internal/download's now and are tested there. What is left here is what only
// this package knows.

// An empty tag means "whatever is newest". Once the release resolves the job
// says v1.0.0 — but a second request for "newest" is still the same request,
// and matching it against the resolved tag is how the panel ends up
// downloading the same jar twice.
func TestARepeatOfNewestIsNotADifferentRequestOnceItResolves(t *testing.T) {
	downloader, stub, items := downloaderFixture(t, true, 1)
	defer stub.releaseAll()

	first, err := downloader.Start(items[0].ID, "", "")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	waitFor(t, "the job to resolve its tag", func() bool {
		jobs := downloader.Jobs()
		return len(jobs) == 1 && jobs[0].FileName != ""
	})

	second, err := downloader.Start(items[0].ID, "", "")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("asking for 最新 twice made two jobs (%s, %s)", first.ID, second.ID)
	}
}

// Naming the tag the job resolved to is a different request from "newest", and
// the panel must not collapse them: one pins a version, the other tracks.
func TestNamingTheResolvedTagIsItsOwnRequest(t *testing.T) {
	downloader, stub, items := downloaderFixture(t, true, 1)
	defer stub.releaseAll()

	newest, err := downloader.Start(items[0].ID, "", "")
	if err != nil {
		t.Fatalf("newest: %v", err)
	}
	pinned, err := downloader.Start(items[0].ID, "v1.0.0", "")
	if err != nil {
		t.Fatalf("pinned: %v", err)
	}
	if pinned.ID == newest.ID {
		t.Fatal("asking for 最新 and for v1.0.0 collapsed onto one job")
	}
}

// A private asset has exactly one route — the API, authenticated — and no
// fallback. The public link cannot serve it, and a mirror must not be told
// about it: the URL alone identifies a repository its owner chose not to
// publish, and the token would be useless to the proxy anyway.
//
// This invariant is why route selection did not move to internal/download with
// the rest of the queue. Nothing had been testing it.
func TestAPrivateAssetIsNeverOfferedThroughAProxy(t *testing.T) {
	client := NewClient("https://api.github.com", "test")
	client.SetMirror("ghfast")
	client.SetTokens([]Token{{ID: "t1", Name: "test", Secret: "ghp_x"}})

	src := Source{Kind: "github", Repo: "owner/secret", Private: true, TokenID: "t1"}
	asset := Asset{
		Name:   "plug.jar",
		URL:    "https://github.com/owner/secret/releases/download/v1/plug.jar",
		APIURL: "https://api.github.com/repos/owner/secret/releases/assets/1",
	}

	attempts, err := client.Attempts(src, asset)
	if err != nil {
		t.Fatalf("attempts: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("a private asset got %d routes, want exactly one", len(attempts))
	}
	if attempts[0].Route != MirrorDirect {
		t.Fatalf("private route = %q, want %q", attempts[0].Route, MirrorDirect)
	}
}

// The public case is the one the proxies exist for, and it still ends at GitHub
// so a proxy that is down costs a retry rather than the install.
func TestAPublicAssetWalksTheProxiesThenGitHub(t *testing.T) {
	client := NewClient("https://api.github.com", "test")
	client.SetMirror(MirrorAuto)

	src := Source{Kind: "github", Repo: "owner/open"}
	asset := Asset{
		Name: "plug.jar",
		URL:  "https://github.com/owner/open/releases/download/v1/plug.jar",
	}

	attempts, err := client.Attempts(src, asset)
	if err != nil {
		t.Fatalf("attempts: %v", err)
	}
	var routes []string
	for _, a := range attempts {
		routes = append(routes, a.Route)
	}
	if len(routes) < 2 || routes[len(routes)-1] != MirrorDirect {
		t.Fatalf("routes = %v, want several ending at %q", routes, MirrorDirect)
	}
}

// The digest a registry published must reach the kernel, or the download is
// verified against nothing at all.
//
// This is not hypothetical: it was broken for a whole commit. The asset's
// checksum is only known once the release resolves, which happens on the
// worker, and the migration to the shared queue simply never passed it along.
// Nothing failed — downloads kept working, unverified.
func TestTheSourcesChecksumReachesTheKernel(t *testing.T) {
	cases := []struct {
		name  string
		asset Asset
		algo  string
	}{
		{"modrinth publishes sha512", Asset{SHA512: strings.Repeat("a", 128)}, "sha512"},
		{"a source publishing sha256", Asset{SHA256: strings.Repeat("b", 64)}, "sha256"},
		{"github publishes neither", Asset{}, ""},
		{
			"both published takes the stronger",
			Asset{SHA256: strings.Repeat("b", 64), SHA512: strings.Repeat("a", 128)},
			"sha512",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Through describeFor, not assetDigest directly: what broke was the
			// plumbing between them, and a test of the leaf would have passed
			// throughout.
			asset := tc.asset
			asset.Name = "plug.jar"
			asset.Size = 10
			got := describeFor(Release{Tag: "v1", Version: "1"}, asset)
			if got.Digest.Algo != tc.algo {
				t.Fatalf("algo = %q, want %q", got.Digest.Algo, tc.algo)
			}
			if tc.algo != "" && got.Digest.Value == "" {
				t.Fatal("the algorithm survived but the checksum did not")
			}
			if got.FileName != "plug.jar" || got.Total != 10 {
				t.Fatalf("the rest of the description did not survive: %+v", got)
			}
		})
	}
}
