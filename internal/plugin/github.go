// Package plugin manages server plugins as a panel-wide asset.
//
// The shape mirrors internal/serverjar: a plugin is downloaded once into a
// panel-wide library and then copied into as many instance directories as the
// operator wants. The difference is that a plugin has a history — servers pin
// versions, and rolling one back is a routine repair — so the library keeps
// every version it has downloaded rather than a single current file.
//
// What this trusts: releases come from GitHub over HTTPS and are recorded with
// the SHA-256 the panel computed while downloading. GitHub releases carry no
// published checksum to verify against, so unlike a Java runtime or a Paper
// jar there is nothing to compare the bytes to — the trust anchor is GitHub's
// TLS and whoever can publish to the repository the operator named. The
// recorded digest is there so the same file can be recognised later, not so a
// tampered one can be rejected on the way in.
//
// A repository does not have to be public. An operator who publishes their own
// plugin to a private repository can give the panel a GitHub access token, and
// a source marked Private is then read and downloaded through the authenticated
// API. The token is only ever sent to the API host this client was built with —
// never to a download mirror, which is a third party the operator's credential
// has no business reaching.
package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	// ErrUpstream covers anything GitHub said that the panel cannot act on.
	ErrUpstream = errors.New("plugin source is unavailable")
	// ErrRateLimited is the one upstream failure worth its own message: an
	// unauthenticated panel gets 60 API calls an hour, and "try again later"
	// is a very different instruction from "check the repository name".
	ErrRateLimited = errors.New("GitHub API rate limit reached")
	// ErrNoRelease means the repository has no release this plugin can use.
	ErrNoRelease = errors.New("no usable release found")
	// ErrNoAsset means a release published nothing that looks like a plugin.
	ErrNoAsset = errors.New("this release publishes no jar")
	// ErrInvalidRepo rejects anything that is not an "owner/name" pair.
	ErrInvalidRepo = errors.New("invalid GitHub repository")
	// ErrNeedsToken means GitHub answered as if the repository did not exist,
	// which is also what it says about a private repository nobody has proved
	// they can see. It is its own error because the fix is specific and would
	// otherwise be buried under "not found": configure an access token, or
	// check that the one configured can reach this repository.
	ErrNeedsToken = errors.New("this repository needs a GitHub access token")
)

// SourceGitHub is a repository's releases, read through the GitHub API. It is
// the kind this file implements; the three registry kinds live in registry.go
// and meet this one at Source, Release and Client.
const SourceGitHub = "github"

// SourceLocal is a jar the operator uploaded, with no upstream at all.
//
// It is the kind for everything the four catalogues cannot reach: a plugin
// bought from a marketplace, one compiled from a fork, one a friend sent over.
// Those jars used to have exactly one home — dropped into a server's plugins
// directory by hand, where the panel would notice the file and be able to say
// nothing else about it, once per server. As a library entry the same jar is
// one upload, one checksum, and the same 装到实例 as everything else.
//
// What it does not get is update checking. There is nowhere to check.
const SourceLocal = "local"

// releasePage is how many releases one check looks at. Plugins that publish a
// release per commit would otherwise push the useful versions off the end,
// and nobody picks a version out of a list longer than this by scrolling.
const releasePage = 30

// metadataTimeout bounds one call to the GitHub API. The archive download has
// its own, much longer, budget.
const metadataTimeout = 30 * time.Second

// maxMetadataBytes caps the JSON a release listing may make this process read.
// Thirty releases with notes is tens of kilobytes; the cap is there so a
// hostile or broken response cannot exhaust memory.
const maxMetadataBytes = 8 << 20

// Source describes where a plugin's releases come from.
type Source struct {
	// Kind is SourceGitHub, or one of the registry kinds in registry.go.
	Kind string `json:"kind"`
	// Repo is "owner/name" for GitHub. For a registry it is whatever that
	// registry calls the plugin — a Modrinth slug, a Hangar project name, a
	// numeric SpigotMC resource id — and is opaque to everything but the
	// reader for that kind.
	Repo string `json:"repo"`
	// AssetPattern picks the jar when a release publishes several — a glob
	// matched against the asset name, case-insensitively, e.g. "EssentialsX-*.jar".
	// Empty falls back to the heuristic in pickAsset.
	AssetPattern string `json:"assetPattern,omitempty"`
	// Prerelease includes GitHub prereleases in the version list. Off by
	// default: a plugin marked prerelease is one the author is not ready to
	// have running on someone's server.
	Prerelease bool `json:"prerelease,omitempty"`
	// Private marks a repository only an authenticated account can see —
	// typically the operator's own plugin, published where nobody else can read
	// it. It is a stored flag rather than something detected per request for two
	// reasons: a private release's jar has to be fetched through the API rather
	// than from the public download host, and it must skip the download mirror,
	// which would otherwise be handed the name of a repository the operator has
	// deliberately kept off the public internet.
	Private bool `json:"private,omitempty"`
}

// IsRegistry reports whether this source is one of the plugin catalogues
// rather than a GitHub repository.
//
// An empty Kind is GitHub — it is what every source stored before the
// registries existed says, and Normalise fills it in — so this asks which
// registry it is rather than whether it is not GitHub. Getting that the wrong
// way round would send every pre-existing plugin's download down the registry
// path, where the mirror it was configured with is not applied.
func (s Source) IsRegistry() bool {
	switch s.Kind {
	case SourceModrinth, SourceHangar, SourceSpigot:
		return true
	default:
		return false
	}
}

// Normalise trims and validates a source, returning the cleaned copy.
func (s Source) Normalise() (Source, error) {
	s.Kind = strings.TrimSpace(s.Kind)
	if s.Kind == "" {
		s.Kind = SourceGitHub
	}
	switch s.Kind {
	case SourceGitHub:
	case SourceLocal:
		// A jar the operator handed the panel. There is no upstream to address,
		// so Repo is only an identity — a slug of the plugin's own name, kept
		// so a second upload of the same plugin joins the entry that already
		// exists instead of starting a second one beside it.
		s.Repo = strings.Trim(strings.TrimSpace(s.Repo), "/")
		if s.Repo == "" {
			return Source{}, fmt.Errorf("%w: 导入的插件得有个名字", ErrInvalidRepo)
		}
		s.Private = false
		s.AssetPattern = ""
		s.Prerelease = false
		return s, nil
	case SourceModrinth, SourceHangar, SourceSpigot:
		// A registry addresses a plugin by an id of its own choosing — a
		// Modrinth slug, a Hangar project name, a numeric SpigotMC resource —
		// so there is no owner/name pair to parse. What has to hold is that it
		// is a single non-empty token: it goes into a URL path, and everything
		// downstream treats Repo as opaque.
		s.Repo = strings.Trim(strings.TrimSpace(s.Repo), "/")
		if s.Repo == "" || strings.ContainsAny(s.Repo, " \t\n?#%\x00") {
			return Source{}, fmt.Errorf("%w: %q is not a valid %s id", ErrInvalidRepo, s.Repo, s.Kind)
		}
		// None of the three has private plugins the panel could authenticate
		// for, and Private is what routes a download through the GitHub API.
		s.Private = false
		s.AssetPattern = ""
		return s, nil
	default:
		return Source{}, fmt.Errorf("%w: unknown source kind %q", ErrInvalidRepo, s.Kind)
	}

	repo, err := ParseRepo(s.Repo)
	if err != nil {
		return Source{}, err
	}
	s.Repo = repo
	s.AssetPattern = strings.TrimSpace(s.AssetPattern)
	if s.AssetPattern != "" {
		// path.Match only reports a bad pattern when it is actually matched
		// against something, so it is checked here rather than at download
		// time, when the operator is no longer looking at the form.
		if _, err := path.Match(s.AssetPattern, "probe.jar"); err != nil {
			return Source{}, fmt.Errorf("%w: bad asset pattern %q", ErrInvalidRepo, s.AssetPattern)
		}
	}
	return s, nil
}

// ParseRepo accepts what an operator is likely to paste — "owner/name", a
// browser URL, or a clone URL — and returns the canonical "owner/name".
//
// Taking the URL matters more than it looks: the way anyone finds a plugin's
// releases is by opening the page, so the URL bar is what is on the clipboard.
func ParseRepo(raw string) (string, error) {
	repo := strings.TrimSpace(raw)
	repo = strings.TrimSuffix(repo, "/")

	// Strip a scheme and host, then any trailing /releases, /tree/main, .git …
	if i := strings.Index(repo, "://"); i >= 0 {
		rest := repo[i+3:]
		slash := strings.Index(rest, "/")
		if slash < 0 {
			return "", fmt.Errorf("%w: %q", ErrInvalidRepo, raw)
		}
		host := strings.ToLower(rest[:slash])
		if host != "github.com" && host != "www.github.com" {
			return "", fmt.Errorf("%w: %q is not a github.com URL", ErrInvalidRepo, raw)
		}
		repo = rest[slash+1:]
	}
	repo = strings.TrimPrefix(repo, "github.com/")
	repo = strings.TrimSuffix(repo, ".git")

	parts := strings.Split(strings.Trim(repo, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("%w: %q, expected owner/name", ErrInvalidRepo, raw)
	}
	owner, name := parts[0], parts[1]
	for _, part := range []string{owner, name} {
		if strings.ContainsAny(part, ` /\?#%`) {
			return "", fmt.Errorf("%w: %q", ErrInvalidRepo, raw)
		}
	}
	return owner + "/" + name, nil
}

// Asset is one file published with a release.
type Asset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	// URL is the public download link. It needs no credential and is what a
	// mirror can be pointed at, but it is only good for a public repository:
	// GitHub serves it from a host that does not accept API tokens.
	URL string `json:"url"`
	// APIURL is the same asset through the REST API, which is the only way to
	// download one out of a private repository. Asked for with an
	// "Accept: application/octet-stream" header it answers with the bytes
	// instead of the asset's metadata.
	APIURL string `json:"apiUrl,omitempty"`
}

// Release is one version a plugin could be updated to.
type Release struct {
	Tag         string    `json:"tag"`
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	Notes       string    `json:"notes"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"publishedAt"`
	// Asset is the jar a download would fetch, chosen by pickAsset.
	Asset Asset `json:"asset"`
	// Assets lists every jar in the release, so the UI can explain why the
	// pattern picked what it did — and what else was on offer.
	Assets []Asset `json:"assets"`

	// The rest is what a plugin registry publishes and a GitHub release does
	// not. All optional: a release with none of it is judged 未知兼容性 rather
	// than assumed to fit, which is the whole point of the field being absent
	// rather than defaulted. See Judge.

	// GameVersions and Loaders are the two halves of the compatibility check.
	GameVersions []string `json:"gameVersions,omitempty"`
	Loaders      []string `json:"loaders,omitempty"`
	// Dependencies are the other plugins this version needs. Shown in the
	// drawer before installing, which is the moment it costs nothing to know.
	Dependencies []Dependency `json:"dependencies,omitempty"`
	Downloads    int64        `json:"downloads,omitempty"`
	// SHA256 is the digest the source published, when it published one. It is
	// not what the library records — that is always computed from the bytes
	// that actually arrived — but it is something to compare against.
	SHA256 string `json:"sha256,omitempty"`
	// Unverified marks compatibility metadata that describes the plugin rather
	// than this specific version, which is all SpigotMC offers for anything
	// but its newest release.
	Unverified bool `json:"unverified,omitempty"`
}

// Client reads releases from GitHub, and — through the registry it owns —
// from the three plugin catalogues.
//
// One client rather than two because everything downstream of it should not
// have to care which kind of source a plugin came from. Releases, Latest and
// Fetch all dispatch on Source.Kind, so the downloader, the library and the
// instance installer are the same code for a Modrinth plugin as for a jar out
// of somebody's private GitHub repository.
type Client struct {
	http      *http.Client
	apiBase   string
	userAgent string
	// registry reads Modrinth, Hangar and SpigotMC. Built here rather than
	// injected: it holds no configuration an operator can set, so there is
	// nothing for the wiring to decide.
	registry *Registry
	// mirror is the chosen download proxy, by id or as a custom prefix. It
	// carries the bytes and never the metadata, because the proxies people use
	// for this do not front api.github.com at all. See mirrors.go.
	//
	// Guarded for the same reason the token is: the settings page can change it
	// while a check is in flight.
	mirrorMu sync.RWMutex
	mirror   string
	// token authenticates API requests. Panel-wide rather than per plugin: it
	// is the operator's own GitHub account either way, and a token per
	// repository would be a secret to rotate per repository.
	//
	// Guarded because the settings page can replace it while a check is in
	// flight; everything else on this client is fixed at construction.
	tokenMu sync.RWMutex
	token   string

	// budgetMu guards the last rate-limit headers GitHub sent back.
	budgetMu sync.RWMutex
	budget   Budget
}

// Budget is what GitHub last said about the panel's remaining API quota.
//
// Read off the headers of requests the panel was making anyway rather than
// asked for: the whole reason this number is worth showing is that it is
// scarce — anonymous callers get sixty an hour, and a panel with twenty
// plugins spends that in three "check all updates" — so spending a call to
// find out how many calls are left would be its own joke.
//
// Zero Limit means nothing has come back yet, which is a different statement
// from "no quota" and is shown as one.
type Budget struct {
	Limit     int       `json:"limit"`
	Remaining int       `json:"remaining"`
	ResetAt   time.Time `json:"resetAt,omitempty"`
	// Authenticated says which ceiling this is measured against: 60 an hour
	// anonymous, 5000 with a token. Without it the number is unreadable.
	Authenticated bool      `json:"authenticated"`
	SeenAt        time.Time `json:"seenAt,omitempty"`
}

// Budget returns the last known GitHub quota.
func (c *Client) Budget() Budget {
	c.budgetMu.RLock()
	defer c.budgetMu.RUnlock()
	budget := c.budget
	budget.Authenticated = c.authToken() != ""
	return budget
}

// noteBudget records the rate-limit headers off a response.
func (c *Client) noteBudget(resp *http.Response) {
	limit, err := strconv.Atoi(resp.Header.Get("X-RateLimit-Limit"))
	if err != nil {
		return
	}
	remaining, err := strconv.Atoi(resp.Header.Get("X-RateLimit-Remaining"))
	if err != nil {
		return
	}

	c.budgetMu.Lock()
	defer c.budgetMu.Unlock()
	c.budget = Budget{Limit: limit, Remaining: remaining, ResetAt: resetTime(resp), SeenAt: time.Now()}
}

func NewClient(apiBase, userAgent string) *Client {
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	return &Client{
		http:      &http.Client{Timeout: downloadTimeout},
		apiBase:   strings.TrimSuffix(apiBase, "/"),
		userAgent: userAgent,
		registry:  NewRegistry(userAgent),
	}
}

// Registry is the reader for the three plugin catalogues, for the discovery
// page — which searches them without any plugin being tracked yet.
func (c *Client) Registry() *Registry { return c.registry }

// SetMirror chooses which proxy plugin downloads go through: a mirror id from
// mirrors.go, a custom "https://…/" prefix, or "" for the automatic order.
// Anything unrecognised leaves the client on automatic rather than silently
// downloading direct, which on the hosts this feature exists for is the failure
// case, not the safe one.
func (c *Client) SetMirror(id string) {
	resolved, err := ResolveMirror(id)
	if err != nil {
		resolved = MirrorAuto
	}
	c.mirrorMu.Lock()
	defer c.mirrorMu.Unlock()
	c.mirror = resolved
}

// Mirror is the configured download mirror's id.
func (c *Client) Mirror() string {
	c.mirrorMu.RLock()
	defer c.mirrorMu.RUnlock()
	if c.mirror == "" {
		return MirrorAuto
	}
	return c.mirror
}

// SetToken stores the GitHub access token, or "" to go back to anonymous
// requests. It takes effect on the next request rather than needing a restart:
// an operator pasting a token into the settings page is usually looking at the
// repository that would not load, and expects to retry it immediately.
func (c *Client) SetToken(token string) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	c.token = strings.TrimSpace(token)
}

// Authenticated reports whether a token is configured, which is what decides
// whether asking GitHub about a repository's visibility can tell the truth. The
// token itself is deliberately not readable back out.
func (c *Client) Authenticated() bool { return c.authToken() != "" }

// authToken is read internally only. There is deliberately no exported getter:
// nothing in the panel needs to show the token back, and a getter is how a
// secret ends up in a JSON response by accident.
func (c *Client) authToken() string {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	return c.token
}

// attempt is one place to try fetching an asset from.
type attempt struct {
	url string
	// mirror names where this attempt goes, for the job to report afterwards:
	// with the automatic order in play, "it downloaded" and "it downloaded from
	// the one you would have picked" are different facts.
	mirror string
	// auth carries the token and asks the API for raw bytes. It is only ever
	// set for a URL on this client's own API host — see downloadOrder.
	auth bool
}

// downloadOrder is where to fetch an asset from, most preferred first.
//
// A public asset walks the chosen mirror order and ends at GitHub itself, so a
// proxy that is down or blocked costs a retry rather than the install. A
// private one has exactly one route — the API, authenticated — and no fallback:
// the public link cannot serve it, and a mirror must not be told about it.
func (c *Client) downloadOrder(src Source, asset Asset) ([]attempt, error) {
	if src.IsRegistry() {
		// A registry serves its own CDN. The mirrors this panel knows about are
		// GitHub proxies — prefixing a Modrinth URL with one produces a 404 at
		// best, and at worst hands a third party a request it has no business
		// seeing — so a registry download has exactly one route.
		if asset.URL == "" {
			return nil, fmt.Errorf("%w: %s 没有可用的下载链接", ErrNoAsset, asset.Name)
		}
		return []attempt{{url: asset.URL, mirror: MirrorDirect}}, nil
	}
	if !src.Private {
		if asset.URL == "" {
			return nil, fmt.Errorf("%w: %s has no public download link", ErrNoAsset, asset.Name)
		}
		chosen := c.Mirror()
		prefixes := mirrorOrder(chosen, asset.URL)
		out := make([]attempt, 0, len(prefixes))
		for _, prefix := range prefixes {
			id := MirrorDirect
			if prefix != "" {
				id = mirrorID(chosen, prefix)
			}
			out = append(out, attempt{url: prefix + asset.URL, mirror: id})
		}
		return out, nil
	}

	if c.authToken() == "" {
		return nil, fmt.Errorf("%w: %s is marked private, so downloading from it needs a token", ErrNeedsToken, src.Repo)
	}
	if !c.isAPIURL(asset.APIURL) {
		// Release metadata is re-read immediately before every download, so this
		// is a stub or a mangled record rather than something an operator hits.
		return nil, fmt.Errorf("%w: %s has no API download URL, so it cannot be fetched privately",
			ErrNoAsset, asset.Name)
	}
	// "direct" is the honest name here: a private download never sees a proxy.
	return []attempt{{url: asset.APIURL, mirror: MirrorDirect, auth: true}}, nil
}

// isAPIURL reports whether a URL belongs to the API host this client talks to.
// It is what keeps the token off every other host, mirrors included.
func (c *Client) isAPIURL(raw string) bool {
	return raw != "" && strings.HasPrefix(raw, c.apiBase+"/")
}

// githubRelease is the subset of GitHub's release JSON this package reads.
type githubRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		URL    string `json:"browser_download_url"`
		APIURL string `json:"url"`
	} `json:"assets"`
}

// Releases lists the versions a plugin can be installed at, newest first.
//
// Releases with no jar are dropped rather than listed and refused later: an
// entry an operator cannot click is worse than one that is not there, and a
// repository that publishes source-only tags would otherwise fill the list.
func (c *Client) Releases(ctx context.Context, src Source) ([]Release, error) {
	src, err := src.Normalise()
	if err != nil {
		return nil, err
	}
	if src.IsRegistry() {
		releases, err := c.registry.Versions(ctx, src.Kind, src.Repo)
		if err != nil {
			return nil, err
		}
		if len(releases) == 0 {
			return nil, fmt.Errorf("%w: %s 上的 %s 没有可安装的版本", ErrNoRelease, src.Kind, src.Repo)
		}
		return releases, nil
	}

	url := fmt.Sprintf("%s/repos/%s/releases?per_page=%d", c.apiBase, src.Repo, releasePage)
	var raw []githubRelease
	if err := c.getJSON(ctx, url, &raw); err != nil {
		return nil, err
	}

	out := make([]Release, 0, len(raw))
	for _, item := range raw {
		// Drafts are invisible to anyone but the repository's own maintainers,
		// and a prerelease is only offered when the operator asked for them.
		if item.Draft || (item.Prerelease && !src.Prerelease) {
			continue
		}
		assets := jarAssets(item)
		if len(assets) == 0 {
			continue
		}
		picked, err := pickAsset(assets, src.AssetPattern)
		if err != nil {
			continue
		}
		out = append(out, Release{
			Tag:         item.TagName,
			Name:        strings.TrimSpace(item.Name),
			Version:     VersionOf(item.TagName),
			Notes:       item.Body,
			Prerelease:  item.Prerelease,
			PublishedAt: item.PublishedAt,
			Asset:       picked,
			Assets:      assets,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: %s publishes no release with a jar the panel can use", ErrNoRelease, src.Repo)
	}
	return out, nil
}

// Visibility reports whether a repository is private, which decides how its
// jars have to be fetched: a public asset comes off the download host and may
// go through the mirror, a private one exists only through the API.
//
// This is asked rather than left to the operator because the two answers are
// not interchangeable and the difference is invisible from the release listing
// — which lists a private repository's releases perfectly happily once a token
// is in play, and then hands out download links that 404. Someone who pasted a
// repository URL should not have to know that.
//
// Only useful with a token: without one, a private repository is a 404 here for
// the same reason it is everywhere else.
func (c *Client) Visibility(ctx context.Context, repo string) (bool, error) {
	repo, err := ParseRepo(repo)
	if err != nil {
		return false, err
	}
	var info struct {
		Private bool `json:"private"`
	}
	if err := c.getJSON(ctx, fmt.Sprintf("%s/repos/%s", c.apiBase, repo), &info); err != nil {
		return false, err
	}
	return info.Private, nil
}

// Latest is the newest release this source offers.
func (c *Client) Latest(ctx context.Context, src Source) (Release, error) {
	releases, err := c.Releases(ctx, src)
	if err != nil {
		return Release{}, err
	}
	return releases[0], nil
}

// Fetch opens an asset for download: down the mirror order for a public
// release, through the authenticated API for a private one. It also returns
// which mirror answered, so a job can say where the bytes came from rather than
// leaving the automatic order a black box.
func (c *Client) Fetch(ctx context.Context, src Source, asset Asset) (io.ReadCloser, string, error) {
	order, err := c.downloadOrder(src, asset)
	if err != nil {
		return nil, "", err
	}

	var lastErr error
	for _, next := range order {
		body, err := c.open(ctx, next)
		if err == nil {
			return body, next.mirror, nil
		}
		// A cancelled job must not march down the fallback list pretending the
		// mirror was at fault.
		if ctx.Err() != nil {
			return nil, "", err
		}
		lastErr = err
	}
	if !src.Private && c.authToken() != "" {
		// The release listing was readable but no download link was. With a
		// token in play the likely cause is a private repository the visibility
		// check could not reach, and that is worth naming: the bare transport
		// error reads like the release is gone.
		return nil, "", fmt.Errorf("%w — if %s is private, check that the token can read it",
			lastErr, src.Repo)
	}
	return nil, "", lastErr
}

func (c *Client) open(ctx context.Context, next attempt) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, next.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	if next.auth {
		// The API answers an asset request with its metadata unless this header
		// says otherwise, and it redirects to a signed storage URL rather than
		// serving the bytes itself. Go drops the Authorization header on a
		// cross-host redirect, which is both what storage wants — a signed URL
		// carrying a second credential is rejected — and what the panel wants.
		req.Header.Set("Accept", "application/octet-stream")
		c.authorize(req)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		if next.auth && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized) {
			return nil, fmt.Errorf("%w: GitHub refused the download (HTTP %d) — check that the token is valid and can read this repository",
				ErrNeedsToken, resp.StatusCode)
		}
		return nil, fmt.Errorf("%w: %s returned HTTP %d", ErrUpstream, next.url, resp.StatusCode)
	}
	return resp.Body, nil
}

// authorize attaches the token. Callers must have established that the request
// is going to this client's own API host first.
func (c *Client) authorize(req *http.Request) {
	if token := c.authToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func (c *Client) getJSON(ctx context.Context, url string, dst any) error {
	ctx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/vnd.github+json")
	// Every metadata URL is built from apiBase, so this is the API host by
	// construction; the check is here so it stays true if that ever changes.
	if c.isAPIURL(url) {
		c.authorize(req)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	defer resp.Body.Close()
	// Recorded before the status is judged: the refusal that spends the last
	// call is the one where the remaining count matters most.
	if c.isAPIURL(url) {
		c.noteBudget(resp)
	}

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusUnauthorized:
		// GitHub answers "not found" for a repository the caller may not see, so
		// this status cannot tell a typo from a private repository. Which of the
		// two is worth suggesting depends on whether a token was sent at all.
		if c.authToken() == "" {
			return fmt.Errorf("%w: repository not found — if it is private, configure a GitHub access token in the plugin library",
				ErrNeedsToken)
		}
		return fmt.Errorf("%w: repository not found, or the configured token cannot read it", ErrNeedsToken)
	case http.StatusForbidden, http.StatusTooManyRequests:
		// 403 is not automatically a rate limit: a blocked repository, an
		// org-level restriction and a proxy in the way all answer with it too,
		// and telling those operators to "wait an hour" sends them off to fix
		// the wrong thing. The rate limit headers are what distinguishes them.
		if exhausted(resp) {
			if reset := resetTime(resp); !reset.IsZero() {
				return fmt.Errorf("%w: try again after %s", ErrRateLimited, reset.Local().Format("15:04"))
			}
			return fmt.Errorf("%w: GitHub refused the request (HTTP %d)", ErrRateLimited, resp.StatusCode)
		}
		return fmt.Errorf("%w: GitHub refused the request (HTTP %d) — %s",
			ErrUpstream, resp.StatusCode, forbiddenReason(resp))
	default:
		return fmt.Errorf("%w: GitHub returned HTTP %d", ErrUpstream, resp.StatusCode)
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, maxMetadataBytes)).Decode(dst); err != nil {
		return fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	return nil
}

// exhausted reports whether a refusal was GitHub saying the quota is spent.
//
// 429 always is. A 403 only counts when the headers say so — either the
// remaining count has hit zero, or GitHub named the rate limiter in its
// documentation link, which is what it does when the body is the only clue.
func exhausted(resp *http.Response) bool {
	if resp.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		return true
	}
	return strings.Contains(strings.ToLower(resp.Header.Get("X-GitHub-Media-Type")), "rate") ||
		resp.Header.Get("Retry-After") != ""
}

// forbiddenReason pulls the human explanation out of a refusal so the operator
// sees what GitHub actually said rather than only a status code.
func forbiddenReason(resp *http.Response) string {
	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<10)).Decode(&body); err != nil {
		return "没有更多信息"
	}
	message := strings.TrimSpace(body.Message)
	if message == "" {
		return "没有更多信息"
	}
	// Long enough for a real explanation, short enough not to fill a card with
	// somebody else's paragraph.
	if len(message) > 300 {
		message = message[:300] + "…"
	}
	return message
}

// resetTime reads GitHub's rate limit reset header, or the zero time when it
// is absent or unparseable.
func resetTime(resp *http.Response) time.Time {
	raw := resp.Header.Get("X-RateLimit-Reset")
	if raw == "" {
		return time.Time{}
	}
	var seconds int64
	if _, err := fmt.Sscanf(raw, "%d", &seconds); err != nil || seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0)
}

// jarAssets keeps the release assets that could be a plugin.
//
// An asset needs somewhere to be fetched from, but which of the two links that
// is depends on the repository: a public release is taken from the download
// host, a private one only exists through the API. So either one is enough to
// keep the entry, and downloadOrder decides which is usable.
func jarAssets(release githubRelease) []Asset {
	out := make([]Asset, 0, len(release.Assets))
	for _, asset := range release.Assets {
		if !strings.EqualFold(path.Ext(asset.Name), ".jar") || (asset.URL == "" && asset.APIURL == "") {
			continue
		}
		out = append(out, Asset{Name: asset.Name, Size: asset.Size, URL: asset.URL, APIURL: asset.APIURL})
	}
	return out
}

// sidecarMarkers name the jars a build publishes alongside the plugin itself.
// Installing one of these produces a server that starts and then behaves as if
// the plugin were not there, which is a much harder failure to diagnose than a
// download that refused to guess.
var sidecarMarkers = []string{"-sources", "-javadoc", "-dev", "-original", "-slim", "-api"}

// pickAsset chooses the jar to install.
//
// A pattern, when given, is the whole answer: the operator said which file
// they want and guessing past that would defeat the point of asking. Without
// one, the sidecar jars are dropped and the largest of what remains wins —
// a shaded plugin jar is bigger than its own api or slim variant, which is
// what a repository publishing several of them is distinguishing between.
func pickAsset(assets []Asset, pattern string) (Asset, error) {
	if len(assets) == 0 {
		return Asset{}, ErrNoAsset
	}
	if pattern != "" {
		for _, asset := range assets {
			ok, err := path.Match(strings.ToLower(pattern), strings.ToLower(asset.Name))
			if err != nil {
				return Asset{}, fmt.Errorf("%w: bad asset pattern %q", ErrInvalidRepo, pattern)
			}
			if ok {
				return asset, nil
			}
		}
		return Asset{}, fmt.Errorf("%w: nothing matches %q", ErrNoAsset, pattern)
	}

	best := Asset{}
	for _, asset := range assets {
		if isSidecar(asset.Name) {
			continue
		}
		if best.URL == "" || asset.Size > best.Size {
			best = asset
		}
	}
	if best.URL == "" {
		// Everything looked like a sidecar. Rather than refuse, fall back to
		// the largest jar: a plugin whose only artifact happens to be called
		// "Foo-dev.jar" is still the plugin.
		for _, asset := range assets {
			if best.URL == "" || asset.Size > best.Size {
				best = asset
			}
		}
	}
	return best, nil
}

func isSidecar(name string) bool {
	lower := strings.ToLower(strings.TrimSuffix(name, path.Ext(name)))
	for _, marker := range sidecarMarkers {
		if strings.HasSuffix(lower, marker) {
			return true
		}
	}
	return false
}

// VersionOf is the human version behind a tag: "v2.20.1" and "2.20.1" are the
// same release to everyone but the tag itself, and the UI should say so.
func VersionOf(tag string) string {
	trimmed := strings.TrimSpace(tag)
	if len(trimmed) > 1 && (trimmed[0] == 'v' || trimmed[0] == 'V') &&
		trimmed[1] >= '0' && trimmed[1] <= '9' {
		return trimmed[1:]
	}
	return trimmed
}
