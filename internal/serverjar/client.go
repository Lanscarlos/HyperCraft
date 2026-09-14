// Package serverjar downloads server cores straight onto the machine the panel
// runs on, so the operator does not have to fetch a jar locally and upload it
// over their own uplink.
//
// The catalogue is deliberately small: Paper and Velocity, both served by
// PaperMC's Fill API. Every other core still works the way it always did —
// upload the jar, or point the instance at a directory that already has one.
package serverjar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is PaperMC's Fill API. The older api.papermc.io/v2 endpoints
// were sunset and now answer 410 for every request.
const DefaultBaseURL = "https://fill.papermc.io/v3"

var (
	// ErrUnknownProject is returned for a project outside the catalogue.
	ErrUnknownProject = errors.New("unknown project")
	// ErrUnknownVersion is returned when the upstream API has no such version.
	ErrUnknownVersion = errors.New("unknown version")
	// ErrUpstream wraps anything the PaperMC API did that we cannot act on.
	ErrUpstream = errors.New("papermc api")
)

// Project is one downloadable server core.
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Kind is "server" for something that runs a world, "proxy" for a front
	// door that forwards players to other servers. The UI needs the difference:
	// a proxy has no EULA, no server.properties and takes no --nogui.
	Kind        string `json:"kind"`
	Description string `json:"description"`
}

// IsProxy reports whether this core is a proxy rather than a world server.
func (p Project) IsProxy() bool { return p.Kind == "proxy" }

// Projects is the catalogue offered by the panel, newest-first in the UI.
var Projects = []Project{
	{
		ID:          "paper",
		Name:        "Paper",
		Kind:        "server",
		Description: "高性能 Minecraft 服务端，兼容 Spigot / Bukkit 插件，绝大多数服都用它。",
	},
	{
		ID:          "velocity",
		Name:        "Velocity",
		Kind:        "proxy",
		Description: "现代 Minecraft 代理端，把多个子服连成群组。它本身不开世界，也没有 EULA。",
	},
}

// LookupProject returns the catalogue entry for an ID.
func LookupProject(id string) (Project, bool) {
	for _, project := range Projects {
		if project.ID == id {
			return project, true
		}
	}
	return Project{}, false
}

// Version is one release line of a project, as offered to the operator.
type Version struct {
	ID string `json:"id"`
	// Support mirrors the upstream status, e.g. SUPPORTED or UNSUPPORTED.
	Support string `json:"support"`
	// JavaMinimum is the lowest Java major version this build runs on, 0 when
	// upstream does not say. Running Paper 1.21 on Java 17 fails with a stack
	// trace that says nothing about Java, so it is worth showing up front.
	JavaMinimum int `json:"javaMinimum"`
	// Stable is false for pre-releases, release candidates and snapshots. The
	// UI hides those unless the operator asks for them.
	Stable bool `json:"stable"`
	// Builds is how many builds exist for this version.
	Builds int `json:"builds"`
	// Minecraft is which game versions this one runs, as a label. For a world
	// server the version id *is* the game version, so it repeats it; for a
	// proxy it is a range off proxyMinecraft, and empty when nothing here
	// knows. Empty means the UI says 未知 rather than showing a guess — a
	// wrong range here is worse than no range, because it is the answer to
	// "will my players be able to join".
	Minecraft string `json:"minecraft"`
}

// Build is a single build of a version, together with its artifact.
type Build struct {
	Build int `json:"build"`
	// Channel is STABLE, RECOMMENDED, ALPHA, BETA or EXPERIMENTAL.
	Channel  string    `json:"channel"`
	Time     time.Time `json:"time"`
	FileName string    `json:"fileName"`
	URL      string    `json:"url"`
	SHA256   string    `json:"sha256"`
	Size     int64     `json:"size"`
	// Changelog is the subject line of this build's first commit, which is as
	// much as the one-line column it lands in can hold. Empty when upstream
	// reports no commits.
	Changelog string `json:"changelog"`
}

// Recommended reports whether this build is one upstream considers fit for
// production. Anything else gets a warning in the UI before it is downloaded.
func (b Build) Recommended() bool {
	switch strings.ToUpper(b.Channel) {
	case "STABLE", "RECOMMENDED", "DEFAULT":
		return true
	default:
		return false
	}
}

// versionPattern is what may be interpolated into an upstream URL path. Version
// IDs are operator-supplied, so they are matched against this rather than
// escaped: an unexpected shape means a bug or an attack, not a version.
var versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

// unstableMarkers appear in the IDs of versions that are not releases.
var unstableMarkers = []string{"-pre", "-rc", "snapshot", "-exp"}

// Client talks to the PaperMC Fill API.
//
// Version listings are cached: the UI asks for them every time the operator
// opens the launch settings, and upstream rate-limits per IP for everyone
// behind it.
type Client struct {
	baseURL   string
	userAgent string
	http      *http.Client
	ttl       time.Duration

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	versions []Version
	expires  time.Time
}

// NewClient returns a client for baseURL, or for PaperMC when it is blank.
func NewClient(baseURL, userAgent string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if userAgent == "" {
		userAgent = "HyperCraft"
	}
	return &Client{
		baseURL:   strings.TrimSuffix(baseURL, "/"),
		userAgent: userAgent,
		ttl:       10 * time.Minute,
		cache:     make(map[string]cacheEntry),
		http: &http.Client{
			// No overall timeout: the same client streams the jar itself, and
			// a 60 MB body over a slow uplink is not a stuck request. The
			// per-stage timeouts below are what catch a dead server.
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				TLSHandshakeTimeout:   15 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
				IdleConnTimeout:       60 * time.Second,
			},
		},
	}
}

// Versions lists the versions of a project that have at least one build,
// newest first — the order upstream returns them in.
func (c *Client) Versions(ctx context.Context, projectID string) ([]Version, error) {
	if _, ok := LookupProject(projectID); !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownProject, projectID)
	}
	if cached, ok := c.cached(projectID); ok {
		return cached, nil
	}

	var payload struct {
		Versions []struct {
			Version struct {
				ID      string `json:"id"`
				Support struct {
					Status string `json:"status"`
				} `json:"support"`
				Java struct {
					Version struct {
						Minimum int `json:"minimum"`
					} `json:"version"`
				} `json:"java"`
			} `json:"version"`
			Builds []int `json:"builds"`
		} `json:"versions"`
	}
	if err := c.getJSON(ctx, "/projects/"+projectID+"/versions", &payload); err != nil {
		return nil, err
	}

	versions := make([]Version, 0, len(payload.Versions))
	for _, entry := range payload.Versions {
		id := entry.Version.ID
		// A version with no builds cannot be downloaded, and an ID we would
		// refuse to put in a URL is not one we can offer either.
		if len(entry.Builds) == 0 || !versionPattern.MatchString(id) {
			continue
		}
		versions = append(versions, Version{
			ID:          id,
			Support:     entry.Version.Support.Status,
			JavaMinimum: entry.Version.Java.Version.Minimum,
			Stable:      isStable(id),
			Builds:      len(entry.Builds),
			Minecraft:   MinecraftOf(projectID, id),
		})
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("%w: no downloadable versions for %s", ErrUpstream, projectID)
	}

	c.store(projectID, versions)
	return versions, nil
}

// buildJSON is one build as the Fill API reports it. Shared by LatestBuild
// and Builds: the two endpoints differ only in whether the body is one of
// these or an array of them.
type buildJSON struct {
	ID      int       `json:"id"`
	Time    time.Time `json:"time"`
	Channel string    `json:"channel"`
	Commits []struct {
		Message string `json:"message"`
	} `json:"commits"`
	Downloads map[string]struct {
		Name      string `json:"name"`
		URL       string `json:"url"`
		Size      int64  `json:"size"`
		Checksums struct {
			SHA256 string `json:"sha256"`
		} `json:"checksums"`
	} `json:"downloads"`
}

func (c *Client) toBuild(payload buildJSON) (Build, error) {
	download, ok := payload.Downloads["server:default"]
	if !ok {
		// Fill names the primary artifact "server:default" for both projects we
		// offer. Fall back to a lone entry so a rename upstream degrades to a
		// working download instead of a hard failure.
		if len(payload.Downloads) != 1 {
			return Build{}, fmt.Errorf("%w: build %d has no server:default download", ErrUpstream, payload.ID)
		}
		for _, only := range payload.Downloads {
			download = only
		}
	}

	name, err := safeFileName(download.Name)
	if err != nil {
		return Build{}, err
	}
	if err := c.checkDownloadURL(download.URL); err != nil {
		return Build{}, err
	}

	return Build{
		Build:     payload.ID,
		Channel:   payload.Channel,
		Time:      payload.Time,
		FileName:  name,
		URL:       download.URL,
		SHA256:    strings.ToLower(download.Checksums.SHA256),
		Size:      download.Size,
		Changelog: subject(payload.Commits),
	}, nil
}

// subject is the first commit's first line. Upstream messages carry a body
// under a blank line, and the column this lands in is one line tall.
func subject(commits []struct {
	Message string `json:"message"`
}) string {
	if len(commits) == 0 {
		return ""
	}
	line, _, _ := strings.Cut(commits[0].Message, "\n")
	return strings.TrimSpace(line)
}

// LatestBuild resolves the newest build of a version and the artifact to fetch.
func (c *Client) LatestBuild(ctx context.Context, projectID, versionID string) (Build, error) {
	if err := c.checkTarget(projectID, versionID); err != nil {
		return Build{}, err
	}

	var payload buildJSON
	err := c.getJSON(ctx, "/projects/"+projectID+"/versions/"+versionID+"/builds/latest", &payload)
	if err != nil {
		return Build{}, err
	}
	return c.toBuild(payload)
}

// Builds is every build of one version, newest first.
//
// The library page's 添加核心 dialog needs the whole list rather than only the
// newest: a version and its builds are parent and child, and picking a build
// is the second half of picking what to download. Upstream returns them
// oldest-first, which is the wrong end to read a changelog from.
func (c *Client) Builds(ctx context.Context, projectID, versionID string) ([]Build, error) {
	if err := c.checkTarget(projectID, versionID); err != nil {
		return nil, err
	}

	var payload []buildJSON
	if err := c.getJSON(ctx, "/projects/"+projectID+"/versions/"+versionID+"/builds", &payload); err != nil {
		return nil, err
	}

	builds := make([]Build, 0, len(payload))
	for _, entry := range payload {
		build, err := c.toBuild(entry)
		if err != nil {
			// One unusable build does not make the list unusable: the operator
			// is choosing among the others, and a hard failure here would take
			// the whole dialog down over a build nobody asked for.
			continue
		}
		builds = append(builds, build)
	}
	if len(builds) == 0 {
		return nil, fmt.Errorf("%w: no downloadable builds for %s %s", ErrUpstream, projectID, versionID)
	}

	sort.Slice(builds, func(a, b int) bool { return builds[a].Build > builds[b].Build })
	return builds, nil
}

// checkTarget rejects a project or version we would not put in an upstream URL.
// Version IDs are operator-supplied, so an unexpected shape means a bug or an
// attack, not a version.
func (c *Client) checkTarget(projectID, versionID string) error {
	if _, ok := LookupProject(projectID); !ok {
		return fmt.Errorf("%w: %s", ErrUnknownProject, projectID)
	}
	if !versionPattern.MatchString(versionID) {
		return fmt.Errorf("%w: %q is not a valid version", ErrUnknownVersion, versionID)
	}
	return nil
}

// Fetch opens the artifact body. The caller closes it.
// Opener fetches one URL, for the download kernel to call when it reaches this
// route. The URL may be a mirror's rather than the origin's, which is why the
// HTTPS check runs on whatever is handed in rather than on the build: a route
// is a line to the same bytes, not a licence to leave TLS.
func (c *Client) Opener(rawURL string) func(context.Context) (io.ReadCloser, error) {
	return func(ctx context.Context) (io.ReadCloser, error) {
		if rawURL == "" {
			return nil, fmt.Errorf("%w: this route cannot serve that build", ErrUpstream)
		}
		return c.fetchURL(ctx, rawURL)
	}
}

func (c *Client) Fetch(ctx context.Context, build Build) (io.ReadCloser, error) {
	return c.fetchURL(ctx, build.URL)
}

func (c *Client) fetchURL(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	if err := c.checkDownloadURL(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
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
		return nil, fmt.Errorf("%w: %s returned HTTP %d", ErrUpstream, rawURL, resp.StatusCode)
	}
	return resp.Body, nil
}

func (c *Client) getJSON(ctx context.Context, endpoint string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrUnknownVersion, endpoint)
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w: rate limited, try again in a minute", ErrUpstream)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%w: HTTP %d from %s", ErrUpstream, resp.StatusCode, endpoint)
	}

	// Metadata responses are tens of kilobytes; the cap is a guard against a
	// proxy or captive portal handing back something enormous.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(dst); err != nil {
		return fmt.Errorf("%w: malformed response from %s: %v", ErrUpstream, endpoint, err)
	}
	return nil
}

// checkDownloadURL rejects an artifact URL that does not look like the CDN.
//
// The URL comes from a response, not from the operator, but it ends up driving
// an outbound request from inside their network — so it has to be a plain
// absolute HTTPS URL. Plain HTTP is allowed only when the API itself is being
// served over HTTP, which in practice means a test server.
func (c *Client) checkDownloadURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%w: unusable download URL %q", ErrUpstream, raw)
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if strings.HasPrefix(c.baseURL, "http://") {
			return nil
		}
	}
	return fmt.Errorf("%w: refusing to download over %q", ErrUpstream, parsed.Scheme)
}

// safeFileName keeps an upstream-supplied name from naming anything but a file
// in the instance root. serverfiles jails it too; this is the earlier, clearer
// of the two rejections.
func safeFileName(name string) (string, error) {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	base := path.Base(name)
	if name == "" || name != base || base == "." || base == ".." || strings.ContainsAny(base, "/\x00") {
		return "", fmt.Errorf("%w: unusable file name %q", ErrUpstream, name)
	}
	return base, nil
}

func isStable(versionID string) bool {
	lower := strings.ToLower(versionID)
	for _, marker := range unstableMarkers {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}

func (c *Client) cached(projectID string) ([]Version, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.cache[projectID]
	if !ok || time.Now().After(entry.expires) {
		return nil, false
	}
	return entry.versions, true
}

func (c *Client) store(projectID string, versions []Version) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[projectID] = cacheEntry{versions: versions, expires: time.Now().Add(c.ttl)}
}

// proxyMinecraft is which Minecraft versions each Velocity release line speaks.
//
// A world server's version id is the game version, so it needs no table. A
// proxy's is its own — Velocity 3.4.0 is not a Minecraft version — and
// upstream's API does not carry the range anywhere, so this is maintained by
// hand against Velocity's release notes. Keyed on the major line, because that
// is the granularity the range actually changes at.
//
// An entry missing from here is not guessed at. The column shows 未知 and the row
// gets an 信息不全 chip, which is the honest answer: a wrong range here is the
// answer to "can my players join", and being confidently wrong about that
// costs more than saying nothing.
var proxyMinecraft = map[string]string{
	"1": "1.7 – 1.12.2",
	"3": "1.7 – 1.21.x",
	"4": "1.20 – 1.21.x",
}

// MinecraftOf is which game versions a project's version runs, as a label, or
// empty when nothing here knows.
func MinecraftOf(projectID, versionID string) string {
	project, ok := LookupProject(projectID)
	if !ok {
		return ""
	}
	if !project.IsProxy() {
		// The version id of a world server *is* the Minecraft version.
		return versionID
	}
	major, _, _ := strings.Cut(versionID, ".")
	return proxyMinecraft[major]
}
