package download

import (
	"strings"
	"testing"
)

// Every set ends at its origin. A proxy that is down, blocked or rate-limiting
// would otherwise turn a working install into a failure, and unlike a copy
// there is nothing a proxy has that the origin does not.
func TestEveryRouteSetEndsAtItsOrigin(t *testing.T) {
	for name, set := range RouteSets {
		if len(set.Routes) == 0 {
			t.Fatalf("%s has no routes", name)
		}
		last := set.Routes[len(set.Routes)-1]
		if last.Kind != RouteDirect {
			t.Fatalf("%s ends at %q (kind %v), want a direct route", name, last.ID, last.Kind)
		}
	}
}

// A route only appears for an upstream it can actually serve: ghfast fronts
// github.com and nothing else, so offering it for cdn.azul.com would produce a
// 404 with the operator's name on it. The origin is always offered, because it
// is the URL itself.
func TestARouteIsSkippedForAnUpstreamItCannotServe(t *testing.T) {
	order := RouteOrder("github", RouteAuto, Origin("https://cdn.azul.com/zulu/bin/x.tar.gz"))
	if len(order) != 1 || order[0].Kind != RouteDirect {
		t.Fatalf("order = %v, want just the direct route", ids(order))
	}
}

func TestAutoWalksEveryProxyThenTheOrigin(t *testing.T) {
	order := RouteOrder("github", RouteAuto, Origin("https://github.com/o/r/releases/download/v1/x.jar"))
	if got := strings.Join(ids(order), ","); got != "ghfast,ghproxy,moeyy,direct" {
		t.Fatalf("order = %q, want ghfast,ghproxy,moeyy,direct", got)
	}
}

// Naming one route still ends at the origin, for the same reason auto does.
func TestNamingOneRouteStillFallsBackToTheOrigin(t *testing.T) {
	order := RouteOrder("github", "moeyy", Origin("https://github.com/o/r/releases/download/v1/x.jar"))
	if got := strings.Join(ids(order), ","); got != "moeyy,direct" {
		t.Fatalf("order = %q, want moeyy,direct", got)
	}
}

// Picking the origin means the origin, not the origin plus three proxies that
// were not asked for.
func TestNamingTheOriginTriesNothingElse(t *testing.T) {
	order := RouteOrder("github", "direct", Origin("https://github.com/o/r/releases/download/v1/x.jar"))
	if got := strings.Join(ids(order), ","); got != "direct" {
		t.Fatalf("order = %q, want direct alone", got)
	}
}

// These proxies come and go; an operator running their own is exactly the
// person this should not stand in the way of.
func TestACustomPrefixIsAccepted(t *testing.T) {
	id, err := ResolveRoute("github", "https://my-proxy.example/")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if id != "https://my-proxy.example/" {
		t.Fatalf("id = %q, want the prefix back", id)
	}
}

// A prefix without its trailing slash is the same prefix, not a different one.
func TestACustomPrefixGetsItsTrailingSlash(t *testing.T) {
	id, err := ResolveRoute("github", "https://my-proxy.example")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if id != "https://my-proxy.example/" {
		t.Fatalf("id = %q, want the trailing slash added", id)
	}
}

// A custom prefix has to actually be used, not merely accepted.
func TestACustomPrefixIsWalkedBeforeTheOrigin(t *testing.T) {
	up := Origin("https://github.com/o/r/releases/download/v1/x.jar")
	order := RouteOrder("github", "https://my-proxy.example/", up)
	if len(order) != 2 {
		t.Fatalf("order = %v, want the custom prefix then the origin", ids(order))
	}
	if got := order[0].Link(up); got != "https://my-proxy.example/"+up.URL {
		t.Fatalf("custom link = %q", got)
	}
	if order[1].Kind != RouteDirect {
		t.Fatalf("order = %v, want it to end at the origin", ids(order))
	}
}

// Anything else is refused rather than quietly turned into the default —
// silently downloading from somewhere other than what was asked for is the
// surprise this whole feature removes.
func TestAnUnknownRouteIsRefusedRatherThanDefaulted(t *testing.T) {
	if _, err := ResolveRoute("github", "not-a-route"); err == nil {
		t.Fatal("resolve accepted an unknown route id")
	}
}

// A route id is only meaningful inside its own set: the Adoptium mirrors are
// not GitHub proxies and accepting one here would send a plugin jar to a
// university that has never heard of it.
func TestARouteIDFromAnotherSetIsRefused(t *testing.T) {
	if _, err := ResolveRoute("github", "tuna"); err == nil {
		t.Fatal("resolve accepted an Adoptium mirror as a GitHub route")
	}
}

func TestAnUnknownSetIsRefused(t *testing.T) {
	if _, err := ResolveRoute("no-such-set", RouteAuto); err == nil {
		t.Fatal("resolve accepted an unknown route set")
	}
}

func ids(routes []Route) []string {
	out := make([]string, 0, len(routes))
	for _, r := range routes {
		out = append(out, r.ID)
	}
	return out
}
