package javaruntime

import (
	"fmt"
	"strconv"

	"github.com/lanscarlos/hypercraft/internal/download"
)

// Download sources for OpenJDK archives.
//
// The tables themselves live in internal/download as the "adoptium" and "azul"
// route sets, shared with every other shelf's routing. What stays here is the
// vocabulary this package's API speaks — these ids are in stored configs
// (config.JavaSource) and in the panel's own requests — and the mapping from a
// distribution to its set, which is the one fact only this package knows.
//
// A distribution's metadata API is only consulted for metadata: a few kilobytes
// of JSON naming the build, its size and its SHA-256. The archive is 50–200 MB,
// and where it comes from is the only thing a source changes — whichever one
// serves the bytes, they are checked against the checksum the distribution
// published, so a mirror cannot hand back a different JDK than the official
// link would have.

const (
	// SourceAuto works down the list and only then falls back to the official
	// link. It is what an install without a stated source uses.
	SourceAuto = download.RouteAuto
	// SourceOfficial downloads straight from the distribution's own CDN, and is
	// the fallback every other source ends at.
	SourceOfficial = "official"
)

// ErrUnknownSource rejects a source id a distribution does not have.
var ErrUnknownSource = fmt.Errorf("unknown java download source")

// Source is a place the panel can download a Java archive from, in the shape
// this package's API has always published.
type Source struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Note string `json:"note"`
	// Default marks the source an install that names none gets.
	Default bool `json:"default,omitempty"`
}

// routeSetFor is which set in internal/download a distribution downloads
// through. Temurin's archives sit on GitHub's release CDN; Zulu's come off
// cdn.azul.com, which none of the Adoptium mirrors carries and the GitHub proxy
// cannot wrap.
func routeSetFor(dist string) string {
	if dist == DistTemurin {
		return "adoptium"
	}
	return "azul"
}

// autoSource is the entry the UI shows for SourceAuto. It is not a route — it
// is the instruction to walk them all — so it has no counterpart in the set.
var autoSource = Source{
	ID:      SourceAuto,
	Name:    "自动",
	Note:    "按上面的顺序挨个试，哪个通用哪个",
	Default: true,
}

// Sources lists what an operator can pick for a distribution, automatic first.
func Sources(dist string) []Source {
	routes := download.RouteSets[routeSetFor(dist)].Routes
	out := make([]Source, 0, len(routes)+1)
	out = append(out, autoSource)
	for _, route := range routes {
		out = append(out, Source{ID: route.ID, Name: route.Name, Note: route.Note})
	}
	return out
}

// SourceName is the human name of a source id, for a log line or an error.
func SourceName(dist, id string) string {
	if id == "" || id == SourceAuto {
		return autoSource.Name
	}
	for _, route := range download.RouteSets[routeSetFor(dist)].Routes {
		if route.ID == id {
			return route.Name
		}
	}
	// A custom prefix is its own name; there is nothing better to call it.
	return id
}

// ResolveSource normalises a requested source id for a distribution.
//
// Anything unrecognised is refused rather than quietly turned into a default,
// because silently downloading from somewhere other than what was asked for is
// exactly the surprise this feature exists to remove. A custom "https://…/"
// prefix is accepted, which matters most for Zulu: no mirror of its tree could
// be confirmed to exist in China, and an operator who has found one — or runs
// their own — is exactly the person this should not stand in the way of.
func ResolveSource(dist, id string) (string, error) {
	resolved, err := download.ResolveRoute(routeSetFor(dist), id)
	if err != nil {
		// Re-wrapped so callers matching on this package's sentinel keep
		// working; the message from download names the offending id.
		return "", fmt.Errorf("%w: %v", ErrUnknownSource, err)
	}
	return resolved, nil
}

// upstreamFor describes one release to the router: the official URL, plus the
// coordinates the rsync copies of Adoptium's tree need to address it.
func upstreamFor(release Release) download.Upstream {
	up := download.Origin(release.URL)
	up.Parts = map[string]string{
		"major":     strconv.Itoa(release.Major),
		"imageType": release.ImageType,
		"arch":      release.Arch,
		"os":        release.OS,
		"fileName":  release.FileName,
	}
	return up
}
