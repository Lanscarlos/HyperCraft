package dbruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/lanscarlos/hypercraft/internal/unpack"
)

// Version is one release of an engine the panel offers to install.
type Version struct {
	Version string `json:"version"`
	// Series groups releases that upgrade within themselves — MySQL 8.0 and
	// 8.4 are separate product lines, not two patches of one. The page groups
	// by it so an operator picks a line first and a patch never.
	Series string `json:"series"`
	// LTS marks the lines upstream supports for years rather than months. It is
	// the answer for anyone who does not have an opinion.
	LTS  bool   `json:"lts"`
	Note string `json:"note"`
}

// Release is a specific build of one version, for one platform.
type Release struct {
	Engine   string `json:"engine"`
	Version  string `json:"version"`
	Series   string `json:"series"`
	FileName string `json:"fileName"`
	URL      string `json:"url"`
	Size     int64  `json:"size"`
	// Checksum is what upstream published for this file, and Algo names how it
	// was computed. An empty checksum means upstream publishes none — see
	// Installer.download for what the panel does about it.
	Checksum string `json:"checksum,omitempty"`
	Algo     string `json:"algo,omitempty"`
	// Inner names an archive nested one level inside the download, by suffix.
	// The PostgreSQL builds are Maven jars wrapping a .txz; unpacking the jar
	// gets you the wrapper, not the server.
	Inner string `json:"inner,omitempty"`
}

// resolver turns "which version" into "which file". One per engine, because
// the three publish their builds in three completely different ways and there
// is no useful abstraction over "a JSON manifest", "a Maven repository" and "a
// predictable path on a CDN".
type resolver interface {
	// Versions lists what can be installed, newest first.
	Versions(ctx context.Context, client *Client, platform Platform) ([]Version, error)
	// Resolve picks the file for one version on this platform.
	Resolve(ctx context.Context, client *Client, version string, platform Platform) (Release, error)
}

var resolvers = map[string]resolver{
	EngineMySQL:      mysqlResolver{},
	EnginePostgreSQL: postgresResolver{},
	EngineMongoDB:    mongoResolver{},
}

// Client fetches release metadata and archives.
//
// One client for all three engines: they share a connection pool, a user agent
// and — importantly — one place where the rule "downloads are HTTPS and nothing
// else" is enforced.
type Client struct {
	userAgent string
	http      *http.Client
	ttl       time.Duration
	// insecureOK allows plain HTTP, for tests that serve a fake upstream.
	insecureOK bool

	mu     sync.Mutex
	cached map[string]versionCache
}

type versionCache struct {
	versions []Version
	expires  time.Time
}

func NewClient(userAgent string) *Client {
	if userAgent == "" {
		userAgent = "HyperCraft"
	}
	return &Client{
		userAgent: userAgent,
		ttl:       time.Hour,
		cached:    map[string]versionCache{},
		http: &http.Client{
			// No overall timeout: this client also streams a 900 MB tarball.
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				TLSHandshakeTimeout:   15 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
				IdleConnTimeout:       60 * time.Second,
			},
		},
	}
}

// AllowInsecure lets the client talk plain HTTP. Only tests call it.
func (c *Client) AllowInsecure() { c.insecureOK = true }

// Versions lists the releases of an engine, cached for an hour. Upstream adds
// a patch release every couple of months, and this is behind a page an operator
// may leave open.
func (c *Client) Versions(ctx context.Context, engine string, platform Platform) ([]Version, error) {
	res, ok := resolvers[engine]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownEngine, engine)
	}

	c.mu.Lock()
	if entry, ok := c.cached[engine]; ok && time.Now().Before(entry.expires) {
		versions := entry.versions
		c.mu.Unlock()
		return versions, nil
	}
	c.mu.Unlock()

	versions, err := res.Versions(ctx, c, platform)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.cached[engine] = versionCache{versions: versions, expires: time.Now().Add(c.ttl)}
	c.mu.Unlock()
	return versions, nil
}

// Resolve picks the build to download.
func (c *Client) Resolve(ctx context.Context, engine, version string, platform Platform) (Release, error) {
	res, ok := resolvers[engine]
	if !ok {
		return Release{}, fmt.Errorf("%w: %q", ErrUnknownEngine, engine)
	}
	if !validVersion(version) {
		return Release{}, fmt.Errorf("%w: %q 不是一个版本号", ErrUnknownRelease, version)
	}

	release, err := res.Resolve(ctx, c, version, platform)
	if err != nil {
		return Release{}, err
	}
	if err := c.checkURL(release.URL); err != nil {
		return Release{}, err
	}
	if !unpack.Supported(release.FileName) {
		return Release{}, fmt.Errorf("%w: 不认识的包格式 %q", ErrUpstream, release.FileName)
	}
	release.Engine = engine
	return release, nil
}

// Fetch opens the archive body, which the caller closes.
func (c *Client) Fetch(ctx context.Context, release Release) (io.ReadCloser, error) {
	if err := c.checkURL(release.URL); err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, http.MethodGet, release.URL, "")
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (c *Client) getJSON(ctx context.Context, link string, dst any) error {
	resp, err := c.do(ctx, http.MethodGet, link, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 32 MiB: MongoDB's manifest lists every download of every supported
	// release and is a few hundred kilobytes, but it only ever grows.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(dst); err != nil {
		return fmt.Errorf("%w: 响应解析失败（%s）：%v", ErrUpstream, link, err)
	}
	return nil
}

// getText reads a small text resource — a Maven checksum, a MySQL .md5 file.
func (c *Client) getText(ctx context.Context, link string) (string, error) {
	resp, err := c.do(ctx, http.MethodGet, link, "")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	return strings.TrimSpace(string(body)), nil
}

// size asks how big a file is without downloading it. Used where upstream
// publishes no manifest, so the progress bar has a total and the download has a
// declared length to be held to.
func (c *Client) size(ctx context.Context, link string) (int64, error) {
	resp, err := c.do(ctx, http.MethodHead, link, "")
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	if resp.ContentLength < 0 {
		return 0, nil
	}
	return resp.ContentLength, nil
}

func (c *Client) do(ctx context.Context, method, link, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, link, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound, resp.StatusCode == http.StatusForbidden:
		// Both mean "no such build" here: the MySQL CDN answers 403 rather than
		// 404 for a version it never published.
		resp.Body.Close()
		return nil, fmt.Errorf("%w: 上游没有这个文件（HTTP %d）：%s",
			ErrUnknownRelease, resp.StatusCode, link)
	case resp.StatusCode == http.StatusTooManyRequests:
		resp.Body.Close()
		return nil, fmt.Errorf("%w: 上游限流了，过一分钟再试", ErrUpstream)
	case resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent:
		resp.Body.Close()
		return nil, fmt.Errorf("%w: HTTP %d from %s", ErrUpstream, resp.StatusCode, link)
	}
	return resp, nil
}

// checkURL keeps every download on plain HTTPS.
//
// It matters more here than it does for Java runtimes. Adoptium publishes a
// SHA-256 for every asset, so a tampered download is caught whatever carried
// it; MySQL publishes only an MD5 and the PostgreSQL builds only a SHA-1, and
// neither is worth much against a deliberate substitution. For those two TLS to
// the publisher is the trust anchor, which is also why this package offers no
// mirrors — see Installer.download.
func (c *Client) checkURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%w: 下载地址不可用 %q", ErrUpstream, raw)
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if c.insecureOK {
			return nil
		}
	}
	return fmt.Errorf("%w: 拒绝通过 %q 下载", ErrUpstream, parsed.Scheme)
}

// validVersion accepts the version strings the three engines use — 8.0.45,
// 17.6, 8.0.28 — and nothing that could steer a URL somewhere else. Versions
// reach Resolve from the operator as well as from a manifest: the page lets one
// be typed in, because a pinned list goes stale between panel releases and
// "install the version my host told me to" should not need a panel update.
func validVersion(version string) bool {
	if version == "" || len(version) > 32 {
		return false
	}
	digits := 0
	for _, r := range version {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '.':
		default:
			return false
		}
	}
	return digits > 0 && !strings.Contains(version, "..") &&
		!strings.HasPrefix(version, ".") && !strings.HasSuffix(version, ".")
}

// seriesOf is the first two components of a version, which is how MySQL and
// MongoDB name their product lines (8.0, 8.4, 7.0).
func seriesOf(version string) string {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return version
	}
	return parts[0] + "." + parts[1]
}

// fileNameFromURL is what to call the archive a download link points at. The
// name decides which unpacker runs and never touches the filesystem — the
// archive is written to a temp file — but it is upstream-supplied either way,
// so it is reduced to a bare base name before anything looks at it.
func fileNameFromURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: 下载地址不可用 %q", ErrUpstream, raw)
	}
	base := path.Base(parsed.Path)
	if base == "" || base == "." || base == "/" || base == ".." || strings.ContainsAny(base, "\x00") {
		return "", fmt.Errorf("%w: 从 %q 里读不出文件名", ErrUpstream, raw)
	}
	return base, nil
}
