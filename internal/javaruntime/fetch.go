package javaruntime

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
)

// httpClient is the transport every distribution shares: a metadata GET that
// returns a few kilobytes of JSON, and an archive GET that streams 50–200 MB.
type httpClient struct {
	userAgent string
	// allowInsecure lets a download come over plain HTTP. It is on only when
	// the metadata API itself is plain HTTP, which in practice means a test
	// serving a fake one over httptest.
	allowInsecure bool
	http          *http.Client
}

func newHTTPClient(userAgent string, allowInsecure bool) *httpClient {
	if userAgent == "" {
		userAgent = "HyperCraft"
	}
	return &httpClient{
		userAgent:     userAgent,
		allowInsecure: allowInsecure,
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

func (c *httpClient) getJSON(ctx context.Context, endpoint string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
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

// open starts the archive download. The caller closes the body.
func (c *httpClient) open(ctx context.Context, link string) (io.ReadCloser, error) {
	if err := c.checkDownloadURL(link); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
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

// checkDownloadURL keeps an upstream-supplied URL to plain HTTPS. Assets come
// off a CDN whose host varies and is not worth pinning, but the scheme is.
func (c *httpClient) checkDownloadURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%w: unusable download URL %q", ErrUpstream, raw)
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		// Only in tests, where the API itself is served over plain HTTP.
		if c.allowInsecure {
			return nil
		}
	}
	return fmt.Errorf("%w: refusing to download over %q", ErrUpstream, parsed.Scheme)
}

// majorCache holds a distribution's feature-release list. The list changes a
// few times a year, so an hour is plenty and keeps the page snappy.
//
// One per provider rather than one map on the client: a cache that has to be
// keyed by its owner is a cache in the wrong place, and getting the key wrong
// would serve one distribution's version list for another for a whole hour.
type majorCache struct {
	mu      sync.Mutex
	majors  []Major
	expires time.Time
}

const majorCacheTTL = time.Hour

func (c *majorCache) get() ([]Major, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.majors != nil && time.Now().Before(c.expires) {
		return c.majors, true
	}
	return nil, false
}

func (c *majorCache) put(majors []Major) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.majors, c.expires = majors, time.Now().Add(majorCacheTTL)
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
