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
package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
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
)

// SourceGitHub is the only source kind so far. It is stored rather than
// assumed so a config written today still parses when a second kind (Modrinth,
// Spiget, a plain URL) is added beside it.
const SourceGitHub = "github"

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
	// Kind is always SourceGitHub today. See the constant.
	Kind string `json:"kind"`
	// Repo is "owner/name".
	Repo string `json:"repo"`
	// AssetPattern picks the jar when a release publishes several — a glob
	// matched against the asset name, case-insensitively, e.g. "EssentialsX-*.jar".
	// Empty falls back to the heuristic in pickAsset.
	AssetPattern string `json:"assetPattern,omitempty"`
	// Prerelease includes GitHub prereleases in the version list. Off by
	// default: a plugin marked prerelease is one the author is not ready to
	// have running on someone's server.
	Prerelease bool `json:"prerelease,omitempty"`
}

// Normalise trims and validates a source, returning the cleaned copy.
func (s Source) Normalise() (Source, error) {
	s.Kind = strings.TrimSpace(s.Kind)
	if s.Kind == "" {
		s.Kind = SourceGitHub
	}
	if s.Kind != SourceGitHub {
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
	URL  string `json:"url"`
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
}

// Client reads releases from GitHub.
type Client struct {
	http      *http.Client
	apiBase   string
	userAgent string
	// mirror prefixes asset download URLs, on the same terms as the panel's
	// self-updater: it carries the bytes, never the metadata, because the
	// proxies people use for this do not front api.github.com at all.
	mirror string
}

func NewClient(apiBase, userAgent string) *Client {
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	return &Client{
		http:      &http.Client{Timeout: downloadTimeout},
		apiBase:   strings.TrimSuffix(apiBase, "/"),
		userAgent: userAgent,
	}
}

// SetMirror configures the download proxy, given as a prefix a GitHub URL is
// appended to. It follows the panel's own update mirror so an operator who has
// already said "my line to GitHub is bad" is not asked again per feature.
func (c *Client) SetMirror(prefix string) {
	prefix = strings.TrimSpace(prefix)
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	c.mirror = prefix
}

// Mirror is the configured download proxy, or "" for direct downloads.
func (c *Client) Mirror() string { return c.mirror }

// downloadOrder is where to fetch an asset from, most preferred first. The
// mirror goes first because speed is the entire point of having one, and the
// direct link follows so a mirror that is down or has not synced a release
// published minutes ago does not turn into a failed install.
func (c *Client) downloadOrder(raw string) []string {
	if c.mirror == "" || !strings.HasPrefix(raw, "https://github.com/") {
		return []string{raw}
	}
	return []string{c.mirror + raw, raw}
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
		Name string `json:"name"`
		Size int64  `json:"size"`
		URL  string `json:"browser_download_url"`
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

// Latest is the newest release this source offers.
func (c *Client) Latest(ctx context.Context, src Source) (Release, error) {
	releases, err := c.Releases(ctx, src)
	if err != nil {
		return Release{}, err
	}
	return releases[0], nil
}

// Fetch opens an asset for download, trying the mirror before GitHub.
func (c *Client) Fetch(ctx context.Context, asset Asset) (io.ReadCloser, error) {
	var lastErr error
	for _, url := range c.downloadOrder(asset.URL) {
		body, err := c.open(ctx, url)
		if err == nil {
			return body, nil
		}
		// A cancelled job must not march down the fallback list pretending the
		// mirror was at fault.
		if ctx.Err() != nil {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

func (c *Client) open(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%w: %s returned HTTP %d", ErrUpstream, url, resp.StatusCode)
	}
	return resp.Body, nil
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

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return fmt.Errorf("%w: repository not found, or it has no public releases", ErrNoRelease)
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
func jarAssets(release githubRelease) []Asset {
	out := make([]Asset, 0, len(release.Assets))
	for _, asset := range release.Assets {
		if !strings.EqualFold(path.Ext(asset.Name), ".jar") || asset.URL == "" {
			continue
		}
		out = append(out, Asset{Name: asset.Name, Size: asset.Size, URL: asset.URL})
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
