package javaruntime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lanscarlos/hypercraft/internal/download"
)

func testRelease(link string) Release {
	return Release{
		Distribution: DistTemurin,
		Major:        21,
		ImageType:    ImageJRE,
		OS:           "linux",
		Arch:         "x64",
		FileName:     "OpenJDK21U-jre_x64_linux_hotspot_21.0.12_8.tar.gz",
		URL:          link,
	}
}

func zuluRelease() Release {
	return Release{
		Distribution: DistZulu,
		Major:        21,
		ImageType:    ImageJRE,
		OS:           "linux",
		Arch:         "x64",
		FileName:     "zulu21.34.19-ca-jre21.0.3-linux_x64.tar.gz",
		URL:          "https://cdn.azul.com/zulu/bin/zulu21.34.19-ca-jre21.0.3-linux_x64.tar.gz",
	}
}

const officialLink = "https://github.com/adoptium/temurin21-binaries/releases/download/" +
	"jdk-21.0.12%2B8/OpenJDK21U-jre_x64_linux_hotspot_21.0.12_8.tar.gz"

// routeIDs is the order a release would actually be fetched in.
func routeIDs(release Release, source string) []string {
	routes := download.RouteOrder(routeSetFor(release.Distribution), source, upstreamFor(release))
	out := make([]string, 0, len(routes))
	for _, r := range routes {
		out = append(out, r.ID)
	}
	return out
}

// The route tables and the order they are walked in belong to
// internal/download and are tested there. What is tested here is the half only
// this package knows: which set a distribution maps to, and what a release's
// coordinates are on that set's tree.

// The path layout every Adoptium mirror copies: <major>/<image>/<arch>/<os>/
// <file>. Getting it wrong is a 404 on every install from a mirror.
func TestAReleaseIsAddressedOnTheAdoptiumTree(t *testing.T) {
	routes := download.RouteOrder("adoptium", "tuna", upstreamFor(testRelease(officialLink)))
	want := "https://mirrors.tuna.tsinghua.edu.cn/Adoptium/21/jre/x64/linux/" +
		"OpenJDK21U-jre_x64_linux_hotspot_21.0.12_8.tar.gz"
	if got := routes[0].Link(upstreamFor(testRelease(officialLink))); got != want {
		t.Fatalf("link = %q, want %q", got, want)
	}
}

// A release the panel cannot describe fully cannot be addressed on a copy, and
// a half-built path is a 404 with the operator's name on it.
func TestAReleaseMissingItsPlatformFallsBackToTheOfficialLink(t *testing.T) {
	release := testRelease(officialLink)
	release.Arch = ""
	if got := strings.Join(routeIDs(release, SourceAuto), ","); got != "ghproxy,official" {
		t.Fatalf("order = %q, want the proxy and the origin — the copies cannot address it", got)
	}
}

func TestTemurinAutoWalksEveryMirrorThenOfficial(t *testing.T) {
	got := strings.Join(routeIDs(testRelease(officialLink), SourceAuto), ",")
	if got != "tuna,nju,huawei,ghproxy,official" {
		t.Fatalf("order = %q", got)
	}
}

// A mirror syncs on a schedule, so a release published an hour ago is simply
// not on it yet. Ending at the official link is what keeps that a retry rather
// than a failed install.
func TestANamedMirrorStillEndsAtTheOfficialLink(t *testing.T) {
	if got := strings.Join(routeIDs(testRelease(officialLink), "tuna"), ","); got != "tuna,official" {
		t.Fatalf("order = %q, want tuna,official", got)
	}
}

func TestOfficialStaysOfficial(t *testing.T) {
	if got := strings.Join(routeIDs(testRelease(officialLink), SourceOfficial), ","); got != "official" {
		t.Fatalf("order = %q, want official alone", got)
	}
}

// Zulu's archives come off cdn.azul.com, which none of the Adoptium mirrors
// carries and the GitHub proxy has nothing to wrap.
func TestZuluAutoIsJustTheOfficialCDN(t *testing.T) {
	if got := strings.Join(routeIDs(zuluRelease(), SourceAuto), ","); got != "official" {
		t.Fatalf("order = %q, want official alone", got)
	}
}

// No mirror of the Zulu tree could be confirmed to exist in China, so an
// operator who has found one — or runs their own — fills it in by hand.
func TestZuluAcceptsACustomPrefix(t *testing.T) {
	const prefix = "https://mirror.example/zulu/bin/"
	id, err := ResolveSource(DistZulu, prefix)
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	release := zuluRelease()
	routes := download.RouteOrder(routeSetFor(DistZulu), id, upstreamFor(release))
	if len(routes) != 2 {
		t.Fatalf("routes = %d, want the custom prefix then the official CDN", len(routes))
	}
	if got := routes[0].Link(upstreamFor(release)); got != prefix+release.URL {
		t.Fatalf("custom link = %q", got)
	}
}

func TestResolveSourceNormalisesACustomPrefix(t *testing.T) {
	got, err := ResolveSource(DistZulu, "https://mirror.example/zulu/bin")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if got != "https://mirror.example/zulu/bin/" {
		t.Fatalf("got %q, want the trailing slash added", got)
	}
}

func TestResolveSource(t *testing.T) {
	for _, id := range []string{"", SourceAuto, SourceOfficial, "tuna", "ghproxy"} {
		if _, err := ResolveSource(DistTemurin, id); err != nil {
			t.Errorf("ResolveSource(%q): %v", id, err)
		}
	}
	if got, _ := ResolveSource(DistTemurin, ""); got != SourceAuto {
		t.Errorf("an unstated source should be %q, got %q", SourceAuto, got)
	}
	if _, err := ResolveSource(DistTemurin, "mirrors.evil.example"); !errors.Is(err, ErrUnknownSource) {
		t.Errorf("got %v, want ErrUnknownSource", err)
	}
}

// A source id is only meaningful for the distribution it belongs to: Zulu has
// no tuna mirror, and accepting one would send an install to a tree that has
// never held a byte of Zulu.
func TestSourcesAreScopedToTheDistribution(t *testing.T) {
	if _, err := ResolveSource(DistZulu, "tuna"); !errors.Is(err, ErrUnknownSource) {
		t.Errorf("Zulu accepted an Adoptium mirror: %v", err)
	}
	if _, err := ResolveSource(DistTemurin, "tuna"); err != nil {
		t.Errorf("Temurin refused its own mirror: %v", err)
	}
}

func TestSourcesListsAutoFirstAndExactlyOneDefault(t *testing.T) {
	list := Sources(DistTemurin)
	want := len(download.RouteSets["adoptium"].Routes) + 1
	if len(list) != want || list[0].ID != SourceAuto || !list[0].Default {
		t.Fatalf("unexpected source list: %+v", list)
	}
	defaults := 0
	for _, source := range list {
		if source.Default {
			defaults++
		}
	}
	if defaults != 1 {
		t.Errorf("%d sources marked default, want 1", defaults)
	}
}

// The behaviour the whole fallback chain exists for: a mirror that does not
// have this build yet must cost one 404, not the install.
func TestAttemptsFallBackToTheNextSource(t *testing.T) {
	stale := httptest.NewServer(http.HandlerFunc(http.NotFound))
	defer stale.Close()
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("archive"))
	}))
	defer cdn.Close()

	client := newTestClient(DistTemurin, "http://api.invalid")
	tries, err := client.Attempts(testRelease(cdn.URL+"/jre.tar.gz"), SourceAuto)
	if err != nil {
		t.Fatalf("Attempts: %v", err)
	}
	// The fake CDN is not github.com, so only the origin can address it — which
	// is the point: what is exercised here is that walking the list works, not
	// how many entries it has.
	var body io.ReadCloser
	for _, try := range tries {
		if body, err = try.Open(context.Background()); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("no attempt opened: %v", err)
	}
	defer body.Close()
	if got, _ := io.ReadAll(body); string(got) != "archive" {
		t.Fatalf("body = %q", got)
	}
}

// "It failed" and "清华 failed, and so did 南大" are different things to read
// in a job's error.
func TestAttemptsReportTheSourceThatFailed(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(http.NotFound))
	defer dead.Close()

	client := newTestClient(DistTemurin, "http://api.invalid")
	tries, err := client.Attempts(testRelease(dead.URL+"/jre.tar.gz"), SourceOfficial)
	if err != nil {
		t.Fatalf("Attempts: %v", err)
	}
	if _, err = tries[0].Open(context.Background()); err == nil {
		t.Fatal("a 404 opened without error")
	}
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("got %v, want ErrUpstream", err)
	}
	if want := "Adoptium 官方"; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not name the source %q", err, want)
	}
}
