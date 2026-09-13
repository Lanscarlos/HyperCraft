package javaruntime

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/download"
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

// Attempts is where to look for a release, most preferred first, in the shape
// the download kernel walks.
//
// Which routes exist is the release's own business: it carries the distribution
// it came from, so nothing upstream has to pass that along.
func (c *Client) Attempts(release Release, sourceID string) ([]download.Attempt, error) {
	up := upstreamFor(release)
	routes := download.RouteOrder(routeSetFor(release.Distribution), sourceID, up)
	if len(routes) == 0 {
		return nil, fmt.Errorf("%w: unusable download URL %q", ErrUpstream, release.URL)
	}
	out := make([]download.Attempt, 0, len(routes))
	for _, route := range routes {
		url, name := route.Link(up), route.Name
		out = append(out, download.Attempt{
			Route: route.ID,
			Open: func(ctx context.Context) (io.ReadCloser, error) {
				body, err := c.http.open(ctx, url)
				if err != nil {
					// Named, because "it failed" and "清华 failed, and so did
					// 南大" are different things to read in a job's error.
					return nil, fmt.Errorf("%s: %w", name, err)
				}
				return body, nil
			},
		})
	}
	return out, nil
}
