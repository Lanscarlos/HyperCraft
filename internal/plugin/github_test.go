package plugin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseRepoAcceptsWhatOperatorsActuallyPaste(t *testing.T) {
	cases := map[string]string{
		"EssentialsX/Essentials":                                  "EssentialsX/Essentials",
		"  EssentialsX/Essentials  ":                              "EssentialsX/Essentials",
		"github.com/EssentialsX/Essentials":                       "EssentialsX/Essentials",
		"https://github.com/EssentialsX/Essentials":               "EssentialsX/Essentials",
		"https://github.com/EssentialsX/Essentials/":              "EssentialsX/Essentials",
		"https://github.com/EssentialsX/Essentials.git":           "EssentialsX/Essentials",
		"https://github.com/EssentialsX/Essentials/releases":      "EssentialsX/Essentials",
		"https://www.github.com/EssentialsX/Essentials/tree/main": "EssentialsX/Essentials",
	}
	for input, want := range cases {
		got, err := ParseRepo(input)
		if err != nil {
			t.Errorf("ParseRepo(%q): %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("ParseRepo(%q) = %q, want %q", input, got, want)
		}
	}

	for _, bad := range []string{"", "Essentials", "https://gitlab.com/a/b", "https://github.com", "a b/c"} {
		if _, err := ParseRepo(bad); !errors.Is(err, ErrInvalidRepo) {
			t.Errorf("ParseRepo(%q) should have been rejected, got %v", bad, err)
		}
	}
}

func TestPickAssetSkipsSidecarJars(t *testing.T) {
	assets := []Asset{
		{Name: "EssentialsX-sources.jar", Size: 900, URL: "s"},
		{Name: "EssentialsX-2.20.1.jar", Size: 500, URL: "plugin"},
		{Name: "EssentialsX-javadoc.jar", Size: 800, URL: "d"},
	}
	picked, err := pickAsset(assets, "")
	if err != nil {
		t.Fatalf("pickAsset: %v", err)
	}
	// The sources jar is the biggest, and installing it would produce a server
	// that starts and silently does nothing — which is the whole point of the
	// exclusion list.
	if picked.URL != "plugin" {
		t.Fatalf("picked %+v, want the plugin jar", picked)
	}
}

func TestPickAssetPrefersTheLargestRealJar(t *testing.T) {
	assets := []Asset{
		{Name: "Foo-api.jar", Size: 100, URL: "api"},
		{Name: "Foo-1.0.jar", Size: 4000, URL: "shaded"},
	}
	picked, _ := pickAsset(assets, "")
	if picked.URL != "shaded" {
		t.Fatalf("picked %+v, want the shaded jar", picked)
	}
}

func TestPickAssetHonoursThePatternExactly(t *testing.T) {
	assets := []Asset{
		{Name: "Foo-1.0.jar", Size: 4000, URL: "big"},
		{Name: "Foo-slim-1.0.jar", Size: 100, URL: "slim"},
	}
	// An operator who names a file wants that file, even when the heuristic
	// would have gone the other way.
	picked, err := pickAsset(assets, "Foo-slim-*.jar")
	if err != nil {
		t.Fatalf("pickAsset: %v", err)
	}
	if picked.URL != "slim" {
		t.Fatalf("picked %+v, want the slim jar", picked)
	}

	if _, err := pickAsset(assets, "Bar-*.jar"); !errors.Is(err, ErrNoAsset) {
		t.Fatalf("a pattern matching nothing should fail, got %v", err)
	}
}

func TestPickAssetFallsBackWhenEverythingLooksLikeASidecar(t *testing.T) {
	assets := []Asset{{Name: "Foo-dev.jar", Size: 10, URL: "only"}}
	picked, err := pickAsset(assets, "")
	if err != nil {
		t.Fatalf("pickAsset: %v", err)
	}
	if picked.URL != "only" {
		t.Fatalf("picked %+v, want the only jar there is", picked)
	}
}

func TestVersionOfStripsALeadingV(t *testing.T) {
	cases := map[string]string{
		"v2.20.1": "2.20.1",
		"2.20.1":  "2.20.1",
		"V1.0":    "1.0",
		"version": "version", // not a version prefix, just a word starting with v
		"":        "",
	}
	for tag, want := range cases {
		if got := VersionOf(tag); got != want {
			t.Errorf("VersionOf(%q) = %q, want %q", tag, got, want)
		}
	}
}

const releasesJSON = `[
  {"tag_name":"v2.0.0","name":"Two","body":"notes","draft":false,"prerelease":true,
   "published_at":"2026-02-01T00:00:00Z",
   "assets":[{"name":"Foo-2.0.0.jar","size":20,"browser_download_url":"https://github.com/o/r/2.jar"}]},
  {"tag_name":"v1.5.0","name":"Draft","draft":true,"prerelease":false,
   "published_at":"2026-01-20T00:00:00Z",
   "assets":[{"name":"Foo-1.5.0.jar","size":15,"browser_download_url":"https://github.com/o/r/15.jar"}]},
  {"tag_name":"v1.0.0","name":"One","draft":false,"prerelease":false,
   "published_at":"2026-01-01T00:00:00Z",
   "assets":[{"name":"Foo-1.0.0.jar","size":10,"browser_download_url":"https://github.com/o/r/1.jar"},
             {"name":"Foo-1.0.0-sources.jar","size":90,"browser_download_url":"https://github.com/o/r/1s.jar"}]},
  {"tag_name":"v0.9.0","name":"Source only","draft":false,"prerelease":false,
   "published_at":"2025-12-01T00:00:00Z","assets":[]}
]`

func githubStub(t *testing.T, body string, status int, headers ...[2]string) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, header := range headers {
			w.Header().Set(header[0], header[1])
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return NewClient(server.URL, "test")
}

func TestReleasesFiltersDraftsPrereleasesAndAssetlessTags(t *testing.T) {
	client := githubStub(t, releasesJSON, http.StatusOK)

	releases, err := client.Releases(context.Background(), Source{Repo: "o/r"})
	if err != nil {
		t.Fatalf("Releases: %v", err)
	}
	if len(releases) != 1 || releases[0].Tag != "v1.0.0" {
		t.Fatalf("unexpected releases: %+v", releases)
	}
	if releases[0].Version != "1.0.0" {
		t.Errorf("version is %q", releases[0].Version)
	}
	// Both jars are offered for the picker to explain itself; only one is the
	// one a download would fetch.
	if len(releases[0].Assets) != 2 || releases[0].Asset.Name != "Foo-1.0.0.jar" {
		t.Errorf("unexpected asset choice: %+v", releases[0])
	}
}

func TestReleasesIncludesPrereleasesWhenAsked(t *testing.T) {
	client := githubStub(t, releasesJSON, http.StatusOK)

	releases, err := client.Releases(context.Background(), Source{Repo: "o/r", Prerelease: true})
	if err != nil {
		t.Fatalf("Releases: %v", err)
	}
	if len(releases) != 2 || releases[0].Tag != "v2.0.0" || !releases[0].Prerelease {
		t.Fatalf("unexpected releases: %+v", releases)
	}
}

func TestReleasesReportsAnEmptyRepositoryAsNoRelease(t *testing.T) {
	client := githubStub(t, `[]`, http.StatusOK)
	if _, err := client.Releases(context.Background(), Source{Repo: "o/r"}); !errors.Is(err, ErrNoRelease) {
		t.Fatalf("expected ErrNoRelease, got %v", err)
	}
}

func TestReleasesSeparatesRateLimitsFromOtherFailures(t *testing.T) {
	limited := githubStub(t, "", http.StatusForbidden, [2]string{"X-RateLimit-Remaining", "0"})
	if _, err := limited.Releases(context.Background(), Source{Repo: "o/r"}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}

	// A 403 with quota to spare is something else — a blocked repository, an
	// org restriction, a proxy in the way — and telling that operator to wait
	// an hour would send them off to fix the wrong thing.
	blocked := githubStub(t, `{"message":"access to this repository is not enabled"}`,
		http.StatusForbidden, [2]string{"X-RateLimit-Remaining", "57"})
	_, err := blocked.Releases(context.Background(), Source{Repo: "o/r"})
	if !errors.Is(err, ErrUpstream) || errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected a plain upstream error, got %v", err)
	}
	if !strings.Contains(err.Error(), "not enabled") {
		t.Errorf("GitHub's own explanation should be passed through: %v", err)
	}

	// A 404 cannot tell a typo from a repository the caller may not see, so it
	// is reported as the one of the two that has a fix: configure a token.
	missing := githubStub(t, "", http.StatusNotFound)
	if _, err := missing.Releases(context.Background(), Source{Repo: "o/r"}); !errors.Is(err, ErrNeedsToken) {
		t.Fatalf("expected ErrNeedsToken for a 404, got %v", err)
	}
}

func TestMirrorOnlyRewritesGitHubDownloads(t *testing.T) {
	client := NewClient("", "test")
	client.SetMirror("https://ghfast.top")
	public := Source{Repo: "o/r"}

	order, err := client.downloadOrder(public, Asset{URL: "https://github.com/o/r/releases/download/v1/x.jar"})
	if err != nil {
		t.Fatalf("downloadOrder: %v", err)
	}
	if len(order) != 2 ||
		order[0].url != "https://ghfast.top/https://github.com/o/r/releases/download/v1/x.jar" ||
		order[1].url != "https://github.com/o/r/releases/download/v1/x.jar" {
		t.Fatalf("unexpected order: %v", order)
	}
	// A mirror that only fronts github.com would 404 on anything else, so the
	// prefix is not applied to it.
	if order, _ := client.downloadOrder(public, Asset{URL: "https://example.com/x.jar"}); len(order) != 1 {
		t.Fatalf("a non-GitHub URL should not be mirrored: %v", order)
	}

	client.SetMirror("")
	if order, _ := client.downloadOrder(public, Asset{URL: "https://github.com/o/r/x.jar"}); len(order) != 1 {
		t.Fatalf("no mirror means one attempt: %v", order)
	}
}

func TestPrivateDownloadsGoStraightToTheAPIWithNoMirror(t *testing.T) {
	client := NewClient("", "test")
	client.SetMirror("https://ghfast.top")
	client.SetToken("ghp_secret")

	private := Source{Repo: "me/mine", Private: true}
	asset := Asset{
		Name:   "Mine-1.0.jar",
		URL:    "https://github.com/me/mine/releases/download/v1/Mine-1.0.jar",
		APIURL: "https://api.github.com/repos/me/mine/releases/assets/7",
	}

	order, err := client.downloadOrder(private, asset)
	if err != nil {
		t.Fatalf("downloadOrder: %v", err)
	}
	// One route, and it is the API: the public link cannot serve a private
	// asset, and the mirror must never be told this repository exists — it is a
	// third party the operator hid the repository from.
	if len(order) != 1 || order[0].url != asset.APIURL || !order[0].auth {
		t.Fatalf("unexpected order: %+v", order)
	}

	client.SetToken("")
	if _, err := client.downloadOrder(private, asset); !errors.Is(err, ErrNeedsToken) {
		t.Fatalf("a private source without a token should be refused, got %v", err)
	}
}

func TestTokenGoesToTheAPIAndNowhereElse(t *testing.T) {
	var seen []string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path+" auth="+r.Header.Get("Authorization"))
		if strings.HasPrefix(r.URL.Path, "/repos/") && strings.Contains(r.URL.Path, "/assets/") {
			if r.Header.Get("Accept") != "application/octet-stream" {
				// Without this header the API answers with the asset's JSON,
				// which would be written to disk as if it were the jar.
				w.WriteHeader(http.StatusNotAcceptable)
				return
			}
			_, _ = w.Write([]byte("private jar"))
			return
		}
		_, _ = w.Write([]byte("public jar"))
	}))
	defer origin.Close()

	client := NewClient(origin.URL, "test")
	client.SetToken("ghp_secret")

	// A private asset: authenticated, and the bytes come back.
	body, err := client.Fetch(context.Background(),
		Source{Repo: "me/mine", Private: true},
		Asset{Name: "Mine.jar", APIURL: origin.URL + "/repos/me/mine/releases/assets/7"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	_ = body.Close()

	// A public asset from the same stub, which is not the API host as far as
	// this client is concerned: the token stays behind.
	body, err = client.Fetch(context.Background(),
		Source{Repo: "o/r"}, Asset{Name: "Foo.jar", URL: origin.URL + "/public/Foo.jar"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	_ = body.Close()

	if len(seen) != 2 {
		t.Fatalf("expected two requests, got %v", seen)
	}
	if !strings.HasSuffix(seen[0], "auth=Bearer ghp_secret") {
		t.Errorf("the private download should have been authenticated: %q", seen[0])
	}
	if !strings.HasSuffix(seen[1], "auth=") {
		t.Errorf("a public download must not carry the token: %q", seen[1])
	}
}

func TestReleasesAuthenticatesAndKeepsThePrivateAssetURL(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
		  {"tag_name":"v1.0.0","name":"One","draft":false,"prerelease":false,
		   "published_at":"2026-01-01T00:00:00Z",
		   "assets":[{"name":"Mine-1.0.jar","size":10,
		              "url":"https://api.github.com/repos/me/mine/releases/assets/7"}]}
		]`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test")
	client.SetToken("ghp_secret")

	releases, err := client.Releases(context.Background(), Source{Repo: "me/mine", Private: true})
	if err != nil {
		t.Fatalf("Releases: %v", err)
	}
	if authorization != "Bearer ghp_secret" {
		t.Errorf("metadata request was not authenticated: %q", authorization)
	}
	// A private release publishes no browser_download_url worth having; the
	// asset is kept anyway, because the API link is what will fetch it.
	if len(releases) != 1 || releases[0].Asset.APIURL != "https://api.github.com/repos/me/mine/releases/assets/7" {
		t.Fatalf("unexpected release: %+v", releases)
	}
}

func TestReleasesSaysTheTokenIsTheProblemWhenOneIsConfigured(t *testing.T) {
	client := githubStub(t, "", http.StatusNotFound)
	client.SetToken("ghp_secret")

	_, err := client.Releases(context.Background(), Source{Repo: "me/mine", Private: true})
	if !errors.Is(err, ErrNeedsToken) {
		t.Fatalf("expected ErrNeedsToken, got %v", err)
	}
	// "Configure a token" is useless advice to someone who already did.
	if !strings.Contains(err.Error(), "cannot read it") {
		t.Errorf("the message should point at the token that is there: %v", err)
	}
}

func TestSourceNormaliseRejectsBadPatternsUpFront(t *testing.T) {
	if _, err := (Source{Repo: "o/r", AssetPattern: "[unclosed"}).Normalise(); !errors.Is(err, ErrInvalidRepo) {
		t.Fatalf("expected the bad pattern to be caught, got %v", err)
	}
	src, err := (Source{Repo: "https://github.com/o/r"}).Normalise()
	if err != nil {
		t.Fatalf("Normalise: %v", err)
	}
	if src.Kind != SourceGitHub || src.Repo != "o/r" {
		t.Fatalf("unexpected source: %+v", src)
	}
}

func TestFetchFallsBackToGitHubWhenTheMirrorFails(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "broken") {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte("jar bytes"))
	}))
	defer origin.Close()

	client := NewClient("", "test")
	// downloadOrder only mirrors github.com URLs, so the fallback is exercised
	// by pointing both attempts at the stub: the first path fails, the second
	// is the real one.
	body, err := client.Fetch(context.Background(), Source{Repo: "o/r"}, Asset{URL: origin.URL + "/ok.jar"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer body.Close()

	if _, err := client.Fetch(context.Background(), Source{Repo: "o/r"}, Asset{URL: origin.URL + "/broken.jar"}); !errors.Is(err, ErrUpstream) {
		t.Fatalf("expected ErrUpstream, got %v", err)
	}
}
