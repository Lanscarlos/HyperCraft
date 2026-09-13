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

func paperUp(project, version, build string) Upstream {
	up := Origin("https://fill-data.papermc.io/v1/objects/abc/paper-1.21.4-232.jar")
	up.Parts = map[string]string{
		partProject: project, partVersion: version, partBuild: build,
	}
	return up
}

// FastMirror serves Paper and Velocity byte-for-byte: build 232 of Paper 1.21.4
// came back with the same SHA-256 and the same 51437498 bytes as the official
// CDN. That is what makes it safe to offer — the panel verifies against the
// checksum fill.papermc.io published, not against anything the mirror says.
func TestPaperMCOffersTheMirrorThenTheOrigin(t *testing.T) {
	order := RouteOrder("papermc", RouteAuto, paperUp("paper", "1.21.4", "232"))
	if got := strings.Join(ids(order), ","); got != "fastmirror,official" {
		t.Fatalf("order = %q, want fastmirror,official", got)
	}
}

// FastMirror capitalises the project segment where PaperMC's API does not.
func TestTheFastMirrorPathIsRebuiltFromTheCoordinates(t *testing.T) {
	up := paperUp("paper", "1.21.4", "232")
	order := RouteOrder("papermc", RouteAuto, up)
	want := "https://download.fastmirror.net/download/Paper/1.21.4/build232"
	if got := order[0].Link(up); got != want {
		t.Fatalf("link = %q, want %q", got, want)
	}
}

// A copy cannot address an artifact it was given no coordinates for, and a
// half-built path is a 404 with the operator's name on it. The origin is the
// honest answer.
func TestPaperMCFallsBackToTheOriginWithoutCoordinates(t *testing.T) {
	bare := Origin("https://fill-data.papermc.io/v1/objects/abc/paper.jar")
	order := RouteOrder("papermc", RouteAuto, bare)
	if got := strings.Join(ids(order), ","); got != "official" {
		t.Fatalf("order = %q, want official alone", got)
	}
}

// Zulu's archives come off cdn.azul.com, which none of the Adoptium mirrors
// carries and the GitHub proxy has nothing to wrap. Shipping a guess that 404s
// on every install is worse than shipping one route.
func TestAzulOffersOnlyItsOwnCDN(t *testing.T) {
	order := RouteOrder("azul", RouteAuto, Origin("https://cdn.azul.com/zulu/bin/x.tar.gz"))
	if got := ids(order); len(got) != 1 || got[0] != "official" {
		t.Fatalf("order = %v, want just official", got)
	}
}

func TestAdoptiumRebuildsTheMirrorPath(t *testing.T) {
	up := Origin("https://github.com/adoptium/temurin21-binaries/releases/download/x/OpenJDK21.tar.gz")
	up.Parts = map[string]string{
		partMajor: "21", partImageType: "jdk", partArch: "x64",
		partOS: "linux", partFileName: "OpenJDK21.tar.gz",
	}
	order := RouteOrder("adoptium", RouteAuto, up)
	if got := strings.Join(ids(order), ","); got != "tuna,nju,huawei,ghproxy,official" {
		t.Fatalf("order = %q", got)
	}
	want := "https://mirrors.tuna.tsinghua.edu.cn/Adoptium/21/jdk/x64/linux/OpenJDK21.tar.gz"
	if got := order[0].Link(up); got != want {
		t.Fatalf("link = %q, want %q", got, want)
	}
}

// The rsync copies need coordinates; the GitHub proxy in the same set does not,
// because it fronts the origin URL verbatim. Without coordinates the copies
// drop out and the proxy stays.
func TestAdoptiumKeepsTheProxyWhenTheCopiesCannotAddressTheBuild(t *testing.T) {
	bare := Origin("https://github.com/adoptium/temurin21-binaries/releases/download/x/OpenJDK21.tar.gz")
	order := RouteOrder("adoptium", RouteAuto, bare)
	if got := strings.Join(ids(order), ","); got != "ghproxy,official" {
		t.Fatalf("order = %q, want ghproxy,official", got)
	}
}

// A copy holds one upstream's tree and nothing else. FastMirror's path template
// will happily build a URL for any project, version and build it is handed, so
// without a host check a download from somewhere else entirely would be served
// from there — the wrong file, from a third party, silently.
func TestACopyIsSkippedWhenTheOriginIsNotItsUpstream(t *testing.T) {
	elsewhere := Origin("http://127.0.0.1:9999/artifact.jar")
	elsewhere.Parts = map[string]string{
		partProject: "paper", partVersion: "1.21.11", partBuild: "132",
	}
	order := RouteOrder("papermc", RouteAuto, elsewhere)
	if got := strings.Join(ids(order), ","); got != "official" {
		t.Fatalf("order = %q, want official alone — a copy must not serve another host's download", got)
	}
}

// The same rule for the Adoptium copies, whose path template is equally willing.
func TestAnAdoptiumCopyIsSkippedForAnotherHost(t *testing.T) {
	elsewhere := Origin("https://example.invalid/OpenJDK21.tar.gz")
	elsewhere.Parts = map[string]string{
		partMajor: "21", partImageType: "jdk", partArch: "x64",
		partOS: "linux", partFileName: "OpenJDK21.tar.gz",
	}
	order := RouteOrder("adoptium", RouteAuto, elsewhere)
	if got := strings.Join(ids(order), ","); got != "official" {
		t.Fatalf("order = %q, want official alone", got)
	}
}
