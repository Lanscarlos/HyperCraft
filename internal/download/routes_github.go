package download

// Download routes for GitHub release assets.
//
// Used by three shelves that all pull from the same CDN: plugin jars, the
// panel's own release archive, and the Temurin builds Adoptium publishes there.
// Each of those used to carry its own copy of this list — the panel's had
// decayed into a single bare prefix string with no list, no automatic order and
// no fallback.
//
// Unlike the Adoptium and PaperMC mirrors, these are proxies rather than copies:
// they fetch the same GitHub URL on the panel's behalf, so a release published a
// minute ago is available through them immediately. What they cannot offer is a
// checksum to verify against — GitHub publishes none for release assets, so the
// trust in a file from here is the same whichever way it arrived, and picking a
// proxy widens who is trusted with the bytes. An operator with a good line to
// GitHub should pick 直连.
//
// Release metadata is read straight from api.github.com, which none of these
// proxies front, and a private repository never goes through one at all — see
// the plugin package's downloadOrder, which keeps that rule beside the token it
// is about.

// githubHosts is what these proxies can front. Release assets only: an API URL
// prefixed with one of these produces a 404 at best.
var githubHosts = []string{"github.com"}

// The proxies come first because a panel that does not need one is a panel whose
// operator can pick 直连 in one click, while the reverse — a Chinese host
// discovering that downloads simply time out — is a bug report.
func init() {
	register("github", RouteSet{
		Name: "GitHub",
		Routes: []Route{
			{
				ID: "ghfast", Name: "ghfast.top",
				Note:   "国内访问通常最快，面板自身更新也默认走它",
				Kind:   RouteProxy,
				Serves: githubHosts,
				Link:   proxyLink("https://ghfast.top/", githubHosts),
			},
			{
				ID: "ghproxy", Name: "gh-proxy.com",
				Note:   "老牌代理，ghfast 不通时的第一备选",
				Kind:   RouteProxy,
				Serves: githubHosts,
				Link:   proxyLink("https://gh-proxy.com/", githubHosts),
			},
			{
				ID: "moeyy", Name: "github.moeyy.xyz",
				Note:   "再一个备选，用法相同",
				Kind:   RouteProxy,
				Serves: githubHosts,
				Link:   proxyLink("https://github.moeyy.xyz/", githubHosts),
			},
			{
				ID: "direct", Name: "直连 GitHub",
				Note: "不经过任何第三方，境外机器选它",
				Kind: RouteDirect,
				Link: func(u Upstream) string { return u.URL },
			},
		},
	})
}
