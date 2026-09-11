package javaruntime

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// Client is the panel's way in to the upstreams Java builds come from.
//
// It owns no metadata logic of its own: the shapes upstream APIs return have
// nothing in common, so all this layer does is validate what every request has
// to satisfy and hand the rest to whoever serves that build.
type Client struct {
	adoptium *adoptiumProvider
	http     *httpClient
}

// NewClient builds a client. baseURL overrides the Adoptium API and exists for
// the tests, which serve a fake one over httptest; empty means the real one.
func NewClient(baseURL, userAgent string) *Client {
	shared := newHTTPClient(userAgent, strings.HasPrefix(baseURL, "http://"))
	return &Client{
		adoptium: newAdoptiumProvider(baseURL, shared),
		http:     shared,
	}
}

// Majors lists the feature releases currently on offer, newest first, with the
// long-term-support ones flagged.
func (c *Client) Majors(ctx context.Context) ([]Major, error) {
	return c.adoptium.Majors(ctx)
}

// LatestRelease resolves the newest build of a major version for a platform.
func (c *Client) LatestRelease(ctx context.Context, major int, imageType string, platform Platform) (Release, error) {
	if major <= 0 || major > 999 {
		return Release{}, fmt.Errorf("%w: %d is not a Java version", ErrUnknownRelease, major)
	}
	if !validImageType(imageType) {
		return Release{}, fmt.Errorf("%w: image type must be jre or jdk", ErrUnknownRelease)
	}
	return c.adoptium.LatestRelease(ctx, major, imageType, platform)
}

// Fetch opens the archive body from the requested download source, falling
// back through the rest as described in attempts. It returns the body — which
// the caller closes — and the id of the source that answered.
func (c *Client) Fetch(ctx context.Context, release Release, sourceID string) (io.ReadCloser, string, error) {
	tries := attempts(sourceID, release)
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
		lastErr = fmt.Errorf("%s: %w", SourceName(try.id), err)
	}
	return nil, "", lastErr
}
