package javaruntime

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// Client is the panel's way in to every distribution it can install from.
//
// It owns no metadata logic of its own: the shapes these APIs return have
// nothing in common, so all this layer does is pick the provider and enforce
// what every request has to satisfy whoever serves it.
type Client struct {
	providers map[string]provider
	http      *httpClient
}

// NewClient builds a client for every distribution.
//
// bases overrides a distribution's metadata endpoint by id and exists for the
// tests, which serve fake APIs over httptest; nil means the real ones. A plain
// HTTP override also lets downloads come over plain HTTP, which is the only
// way a test server can serve one.
func NewClient(userAgent string, bases map[string]string) *Client {
	insecure := false
	for _, base := range bases {
		if strings.HasPrefix(base, "http://") {
			insecure = true
		}
	}
	shared := newHTTPClient(userAgent, insecure)
	return &Client{
		http: shared,
		providers: map[string]provider{
			DistZulu:    newAzulProvider(bases[DistZulu], shared),
			DistTemurin: newAdoptiumProvider(bases[DistTemurin], shared),
		},
	}
}

func (c *Client) provider(dist string) (provider, string, error) {
	dist, err := ResolveDistribution(dist)
	if err != nil {
		return nil, "", err
	}
	return c.providers[dist], dist, nil
}

// Majors lists the feature releases a distribution currently ships, newest
// first, with the long-term-support ones flagged.
func (c *Client) Majors(ctx context.Context, dist string) ([]Major, error) {
	p, _, err := c.provider(dist)
	if err != nil {
		return nil, err
	}
	return p.Majors(ctx)
}

// LatestRelease resolves the newest build of a major version for a platform.
func (c *Client) LatestRelease(ctx context.Context, dist string, major int, imageType string, platform Platform) (Release, error) {
	p, _, err := c.provider(dist)
	if err != nil {
		return Release{}, err
	}
	if major <= 0 || major > 999 {
		return Release{}, fmt.Errorf("%w: %d is not a Java version", ErrUnknownRelease, major)
	}
	if !validImageType(imageType) {
		return Release{}, fmt.Errorf("%w: image type must be jre or jdk", ErrUnknownRelease)
	}
	return p.LatestRelease(ctx, major, imageType, platform)
}

// Fetch opens the archive body from the requested download source, falling
// back through the rest as described in attempts. It returns the body — which
// the caller closes — and the id of the source that answered.
//
// Which sources exist is the release's own business: it carries the
// distribution it came from, so nothing upstream has to pass that along.
func (c *Client) Fetch(ctx context.Context, release Release, sourceID string) (io.ReadCloser, string, error) {
	tries := attempts(release.Distribution, sourceID, release)
	if len(tries) == 0 {
		return nil, "", fmt.Errorf("%w: unusable download URL %q", ErrUpstream, release.URL)
	}

	var lastErr error
	for _, try := range tries {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		body, err := c.http.open(ctx, try.url)
		if err == nil {
			return body, try.id, nil
		}
		lastErr = fmt.Errorf("%s: %w", SourceName(release.Distribution, try.id), err)
	}
	return nil, "", lastErr
}
