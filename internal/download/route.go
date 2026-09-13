package download

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// Download routes.
//
// A route is a line to the bytes, not a shelf to pick from. Which routes exist
// for a given download depends on where its origin lives, so they are grouped
// into sets — one per upstream — rather than offered as one flat list: ghfast
// cannot front cdn.azul.com, and the Adoptium mirrors carry no Paper jar. That
// grouping is what the Java installer used to do by hand with two separate
// source lists.
//
// What a route changes is download speed, which from a mainland Chinese host is
// the difference between a two-second install and a timeout. What it must never
// change is what arrives: every route in every set is checked against the digest
// the *origin's* metadata published (see transfer), so a mirror cannot hand back
// a different file than the official link would have. Where an upstream
// publishes no digest — GitHub release assets — there is nothing to check
// against, and picking a proxy widens who is trusted with the bytes. An operator
// with a good line to the origin should pick it.

// RouteAuto works down its set and only then goes to the origin. It is what a
// panel that has never been told otherwise uses, because the common case is a
// host that needs help and an operator who does not want to test four of them
// by hand.
const RouteAuto = "auto"

// ErrUnknownRoute rejects a route id that is not in the named set.
var ErrUnknownRoute = fmt.Errorf("unknown download route")

// RouteKind is how a route reaches the bytes, which decides what it can serve.
type RouteKind int

const (
	// RouteProxy fetches the origin URL on the panel's behalf, so anything the
	// origin publishes is available through it immediately. It can only front
	// the hosts it was built for.
	RouteProxy RouteKind = iota
	// RouteCopy is a separate tree holding its own copy, reachable only by
	// rebuilding the path for that tree's layout. A copy can lag the origin,
	// which is why every set still ends at the origin.
	RouteCopy
	// RouteDirect is the origin itself. Always available, because it is the URL
	// the metadata gave us.
	RouteDirect
)

// Upstream is the origin download plus whatever a copy needs to rebuild the
// path on its own tree.
//
// A proxy only ever reads URL — it fronts the origin verbatim. A copy of a tree
// laid out differently needs the parts: FastMirror serves
// /download/{project}/{version}/build{n} against PaperMC's content-addressed
// /v1/objects/{sha256}/{name}, and no amount of prefixing turns one into the
// other.
//
// Parts is a map rather than named fields because the coordinates differ per
// upstream and have nothing in common: Adoptium's tree is addressed by major,
// image type, arch, os and file name, PaperMC's by project, version and build.
// Named fields would make Upstream the union of every upstream's schema, where
// each caller fills three of eight and the next upstream adds more. Each
// routes_*.go documents the keys it reads and treats a missing one as "cannot
// serve this", which is the same answer as a host mismatch.
type Upstream struct {
	URL   string
	Host  string
	Parts map[string]string
}

// part is one coordinate, or "" when the caller did not supply it.
func (u Upstream) part(key string) string { return u.Parts[key] }

// Origin is an Upstream for a set whose routes need nothing but the URL, with
// Host filled in from it. Serves is matched against Host, so a caller building
// an Upstream by hand must set it.
func Origin(rawURL string) Upstream {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return Upstream{URL: rawURL}
	}
	return Upstream{URL: rawURL, Host: parsed.Host}
}

// Route is one place the bytes can come from.
type Route struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Note string `json:"note"`
	// Prefix is what a RouteProxy prepends to the origin URL, empty for every
	// other kind. It travels to the UI so an operator can see where a name
	// actually points rather than having to trust the label.
	Prefix string    `json:"prefix,omitempty"`
	Kind   RouteKind `json:"-"`
	// Serves names the upstream hosts this route can address. Meaningless for
	// RouteDirect, which is the upstream.
	Serves []string `json:"-"`
	// Link is where this route serves the given upstream, or "" when it cannot
	// serve that one at all. Not serialisable, and nothing outside this package
	// needs it — the UI picks a route by id and the daemon does the rest.
	Link func(Upstream) string `json:"-"`
}

// RouteSet is the routes for one upstream, most preferred first and ending at
// the origin.
type RouteSet struct {
	// Name is what the UI calls this set. Not shown today; it is here so the
	// settings page can say which shelf a route belongs to without the frontend
	// carrying its own copy of the mapping.
	Name   string
	Routes []Route
}

// RouteSets holds every set, keyed by the name a Request asks for.
//
// Populated by each routes_*.go through register() rather than written out as
// one literal here: the sets are added by different tasks and touch nothing
// they do not own, and a single literal would make every new upstream an edit
// to a file that already works.
var RouteSets = map[string]RouteSet{}

func register(key string, set RouteSet) {
	RouteSets[key] = set
}

// proxyLink fronts the origin URL with a prefix, for the hosts a proxy covers.
func proxyLink(prefix string, serves []string) func(Upstream) string {
	return func(u Upstream) string {
		if !slices.Contains(serves, u.Host) {
			return ""
		}
		return prefix + u.URL
	}
}

// ResolveRoute normalises a stored or requested route id against its set.
//
// A custom "https://…/" prefix is accepted alongside the known ids: these
// proxies come and go, and an operator who is running their own is exactly the
// person this should not stand in the way of. Anything else is refused rather
// than quietly turned into the default — silently downloading from somewhere
// other than what was asked for is the surprise this whole feature removes.
func ResolveRoute(set, id string) (string, error) {
	routes, ok := RouteSets[set]
	if !ok {
		return "", fmt.Errorf("%w: no route set %q", ErrUnknownRoute, set)
	}
	id = strings.TrimSpace(id)
	switch id {
	case "", RouteAuto:
		return RouteAuto, nil
	}
	for _, route := range routes.Routes {
		if route.ID == id {
			return id, nil
		}
	}
	if strings.HasPrefix(id, "https://") || strings.HasPrefix(id, "http://") {
		if !strings.HasSuffix(id, "/") {
			id += "/"
		}
		return id, nil
	}
	return "", fmt.Errorf("%w: %q is not a route of %q", ErrUnknownRoute, id, set)
}

// RouteOrder is the routes to try for one download, most preferred first.
//
// Every order ends at the origin. A route that is down, blocked or rate-limiting
// would otherwise turn a working install into a failure, and a copy that has not
// synced yet simply does not have the file — falling through to the origin can
// only be more correct. The one exception is an operator who named the origin:
// asking for it and being given three proxies first is not a fallback, it is
// ignoring the setting.
func RouteOrder(set, pref string, up Upstream) []Route {
	routes, ok := RouteSets[set]
	if !ok {
		return nil
	}

	origin := Route{}
	var usable []Route
	for _, route := range routes.Routes {
		if route.Kind == RouteDirect {
			origin = route
			continue
		}
		// A route that cannot address this upstream is not a fallback, it is a
		// guaranteed 404 with the operator's name on it.
		if route.Link(up) == "" {
			continue
		}
		usable = append(usable, route)
	}

	pref = strings.TrimSpace(pref)
	switch {
	case pref == "" || pref == RouteAuto:
		return append(usable, origin)
	case pref == origin.ID:
		return []Route{origin}
	case strings.HasPrefix(pref, "https://") || strings.HasPrefix(pref, "http://"):
		// An operator's own proxy. Nothing is known about what it fronts, so it
		// is offered for whatever they pointed it at and the origin catches the
		// case where they were wrong.
		prefix := pref
		if !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		custom := Route{
			ID: pref, Name: pref, Note: "自定义代理", Prefix: prefix, Kind: RouteProxy,
			Serves: []string{up.Host},
			Link:   func(u Upstream) string { return prefix + u.URL },
		}
		return []Route{custom, origin}
	}
	for _, route := range usable {
		if route.ID == pref {
			return []Route{route, origin}
		}
	}
	// The named route exists in this set but cannot serve this upstream, or the
	// id is not one of ours at all. Either way the origin is the honest answer.
	return []Route{origin}
}
