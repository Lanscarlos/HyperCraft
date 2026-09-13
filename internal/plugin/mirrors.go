package plugin

import (
	"fmt"

	"github.com/lanscarlos/hypercraft/internal/download"
)

// Download mirrors for plugin jars.
//
// The table itself lives in internal/download as the "github" route set, shared
// with the panel's own updater and with the Temurin builds that come off the
// same CDN — ghfast used to be written out three separate times, and the copy
// the updater held had decayed into a bare prefix string with no list, no
// automatic order and no fallback.
//
// What stays here is the vocabulary the plugin API speaks. These ids are in
// stored configs (config.PluginMirror) and in the panel's own requests, so they
// keep their names and their shape; only where the list comes from has changed.
//
// A mirror only ever carries the bytes. Release metadata is read straight from
// api.github.com, which none of these proxies front, and a private repository
// never goes through one at all — see downloadOrder, which keeps that rule
// beside the token it is about.

const (
	// MirrorAuto works down the list and only then goes direct. It is what a
	// panel that has never been told otherwise uses, because the common case is
	// a host that needs a proxy and an operator who does not want to test four
	// of them by hand.
	MirrorAuto = download.RouteAuto
	// MirrorDirect downloads from GitHub with nothing in between.
	MirrorDirect = "direct"
)

// routeSet is the set in internal/download these mirrors are drawn from.
const routeSet = "github"

// ErrUnknownMirror rejects a mirror id this build does not have.
var ErrUnknownMirror = fmt.Errorf("unknown download mirror")

// Mirror is a place plugin jars can be downloaded through, in the shape the
// panel API has always published.
type Mirror struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Note string `json:"note"`
	// Prefix is the URL a GitHub link is appended to, empty for a direct
	// download. It travels to the UI so the operator can see where a name
	// actually points rather than having to trust the label.
	Prefix string `json:"prefix,omitempty"`
	// Default marks the mirror a panel that has chosen none uses.
	Default bool `json:"default,omitempty"`
}

// autoMirror is the entry the UI shows for MirrorAuto. It is not a route — it
// is the instruction to walk them all — so it has no counterpart in the set.
var autoMirror = Mirror{
	ID:      MirrorAuto,
	Name:    "自动",
	Note:    "按上面的顺序挨个试，哪个通用哪个",
	Default: true,
}

// Mirrors lists what an operator can pick, automatic first.
func Mirrors() []Mirror {
	routes := download.RouteSets[routeSet].Routes
	out := make([]Mirror, 0, len(routes)+1)
	out = append(out, autoMirror)
	for _, route := range routes {
		out = append(out, Mirror{
			ID:     route.ID,
			Name:   route.Name,
			Note:   route.Note,
			Prefix: route.Prefix,
		})
	}
	return out
}

// MirrorName is the human name of a mirror id, for a log line or a job.
func MirrorName(id string) string {
	if id == "" || id == MirrorAuto {
		return autoMirror.Name
	}
	for _, route := range download.RouteSets[routeSet].Routes {
		if route.ID == id {
			return route.Name
		}
	}
	// A custom prefix is its own name; there is nothing better to call it.
	return id
}

// ResolveMirror normalises a stored or requested mirror.
//
// A custom "https://…/" prefix is accepted alongside the known ids: these
// proxies come and go, and an operator who is running their own is exactly the
// person this should not stand in the way of. Anything else is refused rather
// than quietly turned into the default — silently downloading through somewhere
// other than what was asked for is the surprise this whole feature removes.
func ResolveMirror(id string) (string, error) {
	resolved, err := download.ResolveRoute(routeSet, id)
	if err != nil {
		// Re-wrapped so callers that already match on this package's sentinel
		// keep working; the message from download names the offending id.
		return "", fmt.Errorf("%w: %v", ErrUnknownMirror, err)
	}
	return resolved, nil
}
