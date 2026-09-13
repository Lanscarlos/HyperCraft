package download

import (
	"fmt"
	"net/url"
	"strings"
)

// Download routes for OpenJDK archives.
//
// A distribution's metadata API is only consulted for metadata — a few kilobytes
// of JSON naming the build, its size and its SHA-256. The archive itself is
// 50–200 MB, and where it comes from is the only thing a route changes:
// whichever one serves the bytes, they are checked against the checksum the
// distribution published (see transfer), so a mirror cannot hand back a
// different JDK than the official link would have.
//
// The two distributions need separate sets because they have nothing in common.
// Temurin's archives sit on GitHub's release CDN, which from a mainland Chinese
// host is anywhere between slow and hopeless, and the mirrors below exist to fix
// exactly that. Zulu's come off cdn.azul.com, which none of those mirrors
// carries and the GitHub proxy cannot wrap.

// adoptiumParts are the keys an Adoptium copy reads from an Upstream: the
// coordinates of one build in a tree laid out by major version. A caller that
// omits any of them gets the origin, because a half-built path is a 404.
const (
	partMajor     = "major"
	partImageType = "imageType"
	partArch      = "arch"
	partOS        = "os"
	partFileName  = "fileName"
)

// adoptiumLink addresses a build on an rsync copy of Adoptium's release tree.
// The first three mirrors below are byte-for-byte copies of it, which is why
// one path template covers all of them.
func adoptiumLink(base string) func(Upstream) string {
	base = strings.TrimSuffix(base, "/")
	return func(u Upstream) string {
		major := u.part(partMajor)
		image := u.part(partImageType)
		arch := u.part(partArch)
		os := u.part(partOS)
		name := u.part(partFileName)
		if major == "" || image == "" || arch == "" || os == "" || name == "" {
			return ""
		}
		return fmt.Sprintf("%s/%s/%s/%s/%s/%s", base,
			url.PathEscape(major),
			url.PathEscape(image),
			url.PathEscape(arch),
			url.PathEscape(os),
			url.PathEscape(name))
	}
}

// The China mirrors come first because that is where the problem is: everywhere
// else the official link is already the fast one, and picking it is one click
// away.
func init() {
	register("adoptium", RouteSet{
		Name: "Eclipse Temurin",
		Routes: []Route{
			{
				ID: "tuna", Name: "清华大学 TUNA", Note: "教育网镜像，国内一般最快",
				Kind: RouteCopy, Serves: githubHosts,
				Link: adoptiumLink("https://mirrors.tuna.tsinghua.edu.cn/Adoptium"),
			},
			{
				ID: "nju", Name: "南京大学", Note: "另一个教育网镜像，清华不通时用",
				Kind: RouteCopy, Serves: githubHosts,
				Link: adoptiumLink("https://mirror.nju.edu.cn/adoptium"),
			},
			{
				ID: "huawei", Name: "华为云", Note: "商业 CDN，非教育网线路上通常更稳",
				Kind: RouteCopy, Serves: githubHosts,
				Link: adoptiumLink("https://mirrors.huaweicloud.com/adoptium"),
			},
			{
				ID: "ghproxy", Name: "GitHub 加速", Note: "代理官方发布页，镜像还没同步的新版本走这个",
				Prefix: "https://ghfast.top/",
				Kind:   RouteProxy, Serves: githubHosts,
				Link: proxyLink("https://ghfast.top/", githubHosts),
			},
			{
				ID: "official", Name: "Adoptium 官方", Note: "直连 GitHub，境外机器选它",
				Kind: RouteDirect,
				Link: func(u Upstream) string { return u.URL },
			},
		},
	})

	// One entry long, for a reason. Zulu's archives come off cdn.azul.com rather
	// than GitHub, so none of the Adoptium mirrors above carries a byte of it and
	// the GitHub proxy has nothing to wrap. No mirror of the Zulu tree inside
	// China could be confirmed to exist — NJU, Aliyun, BFSU and Tencent all
	// answer 404 for it — so rather than ship a guess that 404s on every install,
	// an operator who knows a working accelerator (or runs their own) fills it in
	// as a custom prefix; see ResolveRoute.
	register("azul", RouteSet{
		Name: "Azul Zulu",
		Routes: []Route{
			{
				ID: "official", Name: "Azul 官方 CDN",
				Note: "cdn.azul.com 直连，商业 CDN，国内一般比 GitHub 好走",
				Kind: RouteDirect,
				Link: func(u Upstream) string { return u.URL },
			},
		},
	})
}
