package javaruntime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testRelease(link string) Release {
	return Release{
		Major:     21,
		ImageType: ImageJRE,
		OS:        "linux",
		Arch:      "x64",
		FileName:  "OpenJDK21U-jre_x64_linux_hotspot_21.0.12_8.tar.gz",
		URL:       link,
	}
}

const officialLink = "https://github.com/adoptium/temurin21-binaries/releases/download/" +
	"jdk-21.0.12%2B8/OpenJDK21U-jre_x64_linux_hotspot_21.0.12_8.tar.gz"

// TestMirrorLinkFollowsTheAdoptiumTree pins the path layout every mirror
// copies: <major>/<image>/<arch>/<os>/<file>. Getting it wrong is a 404 on
// every install from a mirror.
func TestMirrorLinkFollowsTheAdoptiumTree(t *testing.T) {
	link := mirrorLink("https://mirrors.example/Adoptium/")(testRelease(officialLink))
	want := "https://mirrors.example/Adoptium/21/jre/x64/linux/" +
		"OpenJDK21U-jre_x64_linux_hotspot_21.0.12_8.tar.gz"
	if link != want {
		t.Errorf("mirror link = %q, want %q", link, want)
	}
}

// A release we have no platform details for cannot be found on a mirror, and
// asking for a path built out of blanks would be a 404 at best.
func TestMirrorLinkNeedsThePlatform(t *testing.T) {
	if link := mirrorLink("https://mirrors.example/Adoptium")(Release{Major: 21}); link != "" {
		t.Errorf("expected no link for an incomplete release, got %q", link)
	}
}

func TestProxyLinkOnlyWrapsGitHub(t *testing.T) {
	wrap := proxyLink("https://proxy.example/")
	if got := wrap(testRelease(officialLink)); got != "https://proxy.example/"+officialLink {
		t.Errorf("proxy link = %q", got)
	}
	// A GitHub proxy has nothing to offer for a link that is not on GitHub.
	if got := wrap(testRelease("https://cdn.example/jre.tar.gz")); got != "" {
		t.Errorf("expected no link for a non-GitHub release, got %q", got)
	}
}

func TestAttemptsAutoTriesEveryMirrorThenOfficial(t *testing.T) {
	tries := attempts(SourceAuto, testRelease(officialLink))
	if len(tries) != len(mirrors) {
		t.Fatalf("got %d attempts, want %d", len(tries), len(mirrors))
	}
	if tries[0].id != "tuna" {
		t.Errorf("first attempt is %q, want the first mirror", tries[0].id)
	}
	if last := tries[len(tries)-1]; last.id != SourceOfficial || last.url != officialLink {
		t.Errorf("last attempt = %+v, want the official link", last)
	}
}

// A named mirror still ends at the official link: mirrors sync on a schedule,
// and a release they have not picked up yet must not fail the install.
func TestAttemptsNamedMirrorFallsBackToOfficial(t *testing.T) {
	tries := attempts("tuna", testRelease(officialLink))
	if len(tries) != 2 || tries[0].id != "tuna" || tries[1].id != SourceOfficial {
		t.Fatalf("unexpected attempts: %+v", tries)
	}
	want := "https://mirrors.tuna.tsinghua.edu.cn/Adoptium/21/jre/x64/linux/" +
		"OpenJDK21U-jre_x64_linux_hotspot_21.0.12_8.tar.gz"
	if tries[0].url != want {
		t.Errorf("mirror url = %q, want %q", tries[0].url, want)
	}
}

// Picking the official source is a decision to stay off the mirrors — usually
// because the machine is not in China — so nothing else is tried.
func TestAttemptsOfficialStaysOfficial(t *testing.T) {
	tries := attempts(SourceOfficial, testRelease(officialLink))
	if len(tries) != 1 || tries[0].id != SourceOfficial {
		t.Fatalf("unexpected attempts: %+v", tries)
	}
}

func TestResolveSource(t *testing.T) {
	for _, id := range []string{"", SourceAuto, SourceOfficial, "tuna", "ghproxy"} {
		if _, err := ResolveSource(id); err != nil {
			t.Errorf("ResolveSource(%q): %v", id, err)
		}
	}
	if got, _ := ResolveSource(""); got != SourceAuto {
		t.Errorf("an unstated source should be %q, got %q", SourceAuto, got)
	}
	if _, err := ResolveSource("mirrors.evil.example"); !errors.Is(err, ErrUnknownSource) {
		t.Errorf("got %v, want ErrUnknownSource", err)
	}
}

func TestSourcesListsAutoFirstAndExactlyOneDefault(t *testing.T) {
	list := Sources()
	if len(list) != len(mirrors)+1 || list[0].ID != SourceAuto || !list[0].Default {
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

// TestFetchFallsBackToTheNextSource is the behaviour the whole fallback chain
// exists for: a mirror that does not have this build yet must cost one 404,
// not the install.
func TestFetchFallsBackToTheNextSource(t *testing.T) {
	stale := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer stale.Close()
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("archive"))
	}))
	defer cdn.Close()

	restore := swapMirrors(t, []source{
		{
			Source: Source{ID: "stale", Name: "落后的镜像"},
			link:   func(Release) string { return stale.URL + "/jre.tar.gz" },
		},
		{
			Source: Source{ID: SourceOfficial, Name: "Adoptium 官方"},
			link:   func(release Release) string { return release.URL },
		},
	})
	defer restore()

	// The client's base URL is http, which is what lets these test servers be
	// downloaded from at all; see checkDownloadURL.
	client := NewClient("http://api.invalid", "test")
	body, served, err := client.Fetch(context.Background(), testRelease(cdn.URL+"/jre.tar.gz"), "stale")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer body.Close()

	if served != SourceOfficial {
		t.Errorf("served by %q, want the fallback %q", served, SourceOfficial)
	}
	if data, _ := io.ReadAll(body); string(data) != "archive" {
		t.Errorf("got %q from the fallback", data)
	}
}

// When nothing answers, the error names the last source tried rather than
// leaving the operator with a bare HTTP status.
func TestFetchReportsTheSourceThatFailed(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer dead.Close()

	restore := swapMirrors(t, []source{{
		Source: Source{ID: SourceOfficial, Name: "Adoptium 官方"},
		link:   func(release Release) string { return release.URL },
	}})
	defer restore()

	client := NewClient("http://api.invalid", "test")
	_, _, err := client.Fetch(context.Background(), testRelease(dead.URL+"/jre.tar.gz"), SourceOfficial)
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("got %v, want ErrUpstream", err)
	}
	if want := "Adoptium 官方"; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not name the source %q", err, want)
	}
}

func swapMirrors(t *testing.T, replacement []source) func() {
	t.Helper()
	previous := mirrors
	mirrors = replacement
	return func() { mirrors = previous }
}
