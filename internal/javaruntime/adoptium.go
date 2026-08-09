package javaruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the Adoptium API. Temurin is the reference OpenJDK build:
// no account, no click-through licence, and every asset comes with a checksum.
const DefaultBaseURL = "https://api.adoptium.net/v3"

var (
	// ErrUnsupported is returned for a platform Adoptium does not build for.
	ErrUnsupported = errors.New("unsupported platform")
	// ErrUnknownRelease is returned when no build matches the request.
	ErrUnknownRelease = errors.New("no matching java build")
	// ErrUpstream wraps anything the Adoptium API did that we cannot act on.
	ErrUpstream = errors.New("adoptium api")
)

// ImageType selects how much of the JDK to install.
const (
	// ImageJRE is enough to run a server and is about a third smaller.
	ImageJRE = "jre"
	// ImageJDK adds the compiler and tools; some plugins and profilers want it.
	ImageJDK = "jdk"
)

func validImageType(kind string) bool { return kind == ImageJRE || kind == ImageJDK }

// Major is one Java feature release on offer.
type Major struct {
	Major int  `json:"major"`
	LTS   bool `json:"lts"`
}

// Release is a specific build of a major version, for one platform.
type Release struct {
	Major     int    `json:"major"`
	Version   string `json:"version"`   // e.g. 21.0.12+8
	Name      string `json:"name"`      // upstream release name, e.g. jdk-21.0.12+8
	ImageType string `json:"imageType"` // jre or jdk
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	FileName  string `json:"fileName"`
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
}

// Client talks to the Adoptium API.
type Client struct {
	baseURL   string
	userAgent string
	http      *http.Client
	ttl       time.Duration

	mu      sync.Mutex
	majors  []Major
	expires time.Time
}

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
		ttl:       time.Hour,
		http: &http.Client{
			// No overall timeout: this client also streams a 50 MB tarball.
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				TLSHandshakeTimeout:   15 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
				IdleConnTimeout:       60 * time.Second,
			},
		},
	}
}

// Majors lists the feature releases Adoptium currently ships, newest first,
// with the long-term-support ones flagged.
//
// The list changes a few times a year, so it is cached for an hour.
func (c *Client) Majors(ctx context.Context) ([]Major, error) {
	c.mu.Lock()
	if c.majors != nil && time.Now().Before(c.expires) {
		cached := c.majors
		c.mu.Unlock()
		return cached, nil
	}
	c.mu.Unlock()

	var payload struct {
		AvailableReleases    []int `json:"available_releases"`
		AvailableLTSReleases []int `json:"available_lts_releases"`
	}
	if err := c.getJSON(ctx, "/info/available_releases", &payload); err != nil {
		return nil, err
	}
	if len(payload.AvailableReleases) == 0 {
		return nil, fmt.Errorf("%w: no releases listed", ErrUpstream)
	}

	lts := make(map[int]bool, len(payload.AvailableLTSReleases))
	for _, major := range payload.AvailableLTSReleases {
		lts[major] = true
	}
	majors := make([]Major, 0, len(payload.AvailableReleases))
	for _, major := range payload.AvailableReleases {
		majors = append(majors, Major{Major: major, LTS: lts[major]})
	}
	sort.Slice(majors, func(a, b int) bool { return majors[a].Major > majors[b].Major })

	c.mu.Lock()
	c.majors, c.expires = majors, time.Now().Add(c.ttl)
	c.mu.Unlock()
	return majors, nil
}

// LatestRelease resolves the newest build of a major version for a platform.
func (c *Client) LatestRelease(ctx context.Context, major int, imageType string, platform Platform) (Release, error) {
	if major <= 0 || major > 999 {
		return Release{}, fmt.Errorf("%w: %d is not a Java version", ErrUnknownRelease, major)
	}
	if !validImageType(imageType) {
		return Release{}, fmt.Errorf("%w: image type must be jre or jdk", ErrUnknownRelease)
	}

	query := url.Values{
		"architecture": {platform.Arch},
		"image_type":   {imageType},
		"os":           {platform.OS},
		"vendor":       {"eclipse"},
	}
	endpoint := "/assets/latest/" + strconv.Itoa(major) + "/hotspot?" + query.Encode()

	var payload []struct {
		Binary struct {
			ImageType string `json:"image_type"`
			OS        string `json:"os"`
			Arch      string `json:"architecture"`
			Package   struct {
				Name     string `json:"name"`
				Link     string `json:"link"`
				Size     int64  `json:"size"`
				Checksum string `json:"checksum"`
			} `json:"package"`
		} `json:"binary"`
		ReleaseName string `json:"release_name"`
		Version     struct {
			Major          int    `json:"major"`
			OpenJDKVersion string `json:"openjdk_version"`
		} `json:"version"`
	}
	if err := c.getJSON(ctx, endpoint, &payload); err != nil {
		return Release{}, err
	}
	if len(payload) == 0 {
		return Release{}, fmt.Errorf("%w: Adoptium 没有 Java %d 的 %s（%s/%s）",
			ErrUnknownRelease, major, imageType, platform.OS, platform.Arch)
	}

	entry := payload[0]
	name, err := safeFileName(entry.Binary.Package.Name)
	if err != nil {
		return Release{}, err
	}
	if err := c.checkDownloadURL(entry.Binary.Package.Link); err != nil {
		return Release{}, err
	}
	if !isSupportedArchive(name) {
		return Release{}, fmt.Errorf("%w: 不认识的包格式 %q", ErrUpstream, name)
	}

	version := entry.Version.OpenJDKVersion
	if version == "" {
		version = entry.ReleaseName
	}
	return Release{
		Major:     major,
		Version:   version,
		Name:      entry.ReleaseName,
		ImageType: imageType,
		OS:        entry.Binary.OS,
		Arch:      entry.Binary.Arch,
		FileName:  name,
		URL:       entry.Binary.Package.Link,
		SHA256:    strings.ToLower(entry.Binary.Package.Checksum),
		Size:      entry.Binary.Package.Size,
	}, nil
}

// Fetch opens the archive body. The caller closes it.
func (c *Client) Fetch(ctx context.Context, release Release) (io.ReadCloser, error) {
	if err := c.checkDownloadURL(release.URL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, release.URL, nil)
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
		return nil, fmt.Errorf("%w: download returned HTTP %d", ErrUpstream, resp.StatusCode)
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
		return fmt.Errorf("%w: %s", ErrUnknownRelease, endpoint)
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w: rate limited, try again in a minute", ErrUpstream)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%w: HTTP %d from %s", ErrUpstream, resp.StatusCode, endpoint)
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(dst); err != nil {
		return fmt.Errorf("%w: malformed response from %s: %v", ErrUpstream, endpoint, err)
	}
	return nil
}

// checkDownloadURL keeps an upstream-supplied URL to plain HTTPS. Adoptium
// serves assets from GitHub releases, so the host varies and is not worth
// pinning, but the scheme is.
func (c *Client) checkDownloadURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%w: unusable download URL %q", ErrUpstream, raw)
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		// Only in tests, where the API itself is served over plain HTTP.
		if strings.HasPrefix(c.baseURL, "http://") {
			return nil
		}
	}
	return fmt.Errorf("%w: refusing to download over %q", ErrUpstream, parsed.Scheme)
}

func safeFileName(name string) (string, error) {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	base := path.Base(name)
	if name == "" || name != base || base == "." || base == ".." || strings.ContainsAny(base, "/\x00") {
		return "", fmt.Errorf("%w: unusable file name %q", ErrUpstream, name)
	}
	return base, nil
}

func isSupportedArchive(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".zip")
}
