package download

import (
	"fmt"
	"net/url"
	"unicode"
)

// Download routes for PaperMC server cores.
//
// New in this release: the core was the one shelf with no accelerator at all,
// while a Paper jar is 50 MB and the panel downloads one per instance.
//
// The official URL is content-addressed —
// https://fill-data.papermc.io/v1/objects/<sha256>/<name>.jar — and the build
// metadata publishes that same SHA-256, which is what makes a copy safe to use:
// whichever route serves the bytes, they are checked against what
// fill.papermc.io said they would be (see transfer).
//
// FastMirror was verified byte-for-byte before it was added: Paper 1.21.4 build
// 232 came back with the same digest and the same 51437498 bytes as the origin.
// It publishes a SHA-1 of its own, which is deliberately not used — the origin's
// SHA-256 is the stronger claim and the one already in hand.
//
// Two mirrors that look like candidates are not. The university mirrors that
// carry Adoptium do not carry PaperMC: TUNA and NJU both answer 404 for
// /papermc/. Huawei's mirror portal answers 200 for *every* path including ones
// that do not exist, so its apparent /papermc/ tree is an artifact of the portal
// being a single-page app, not a tree.

// paperMCHosts is the CDN the build metadata points downloads at. The API host
// (fill.papermc.io) serves metadata only and is never routed.
var paperMCHosts = []string{"fill-data.papermc.io"}

// FastMirror's own coordinates, which the content-addressed origin URL does not
// contain — hence Parts.
const (
	partProject = "project"
	partVersion = "version"
	partBuild   = "build"
)

// fastMirrorLink rebuilds the path on FastMirror's tree. It returns "" for
// anything it cannot address, so a caller that did not supply the coordinates
// gets the origin rather than a URL that 404s.
func fastMirrorLink(u Upstream) string {
	project, version, build := u.part(partProject), u.part(partVersion), u.part(partBuild)
	if project == "" || version == "" || build == "" {
		return ""
	}
	// FastMirror capitalises the project segment (Paper, Velocity) where the
	// panel and PaperMC's own API use lower case.
	//
	// Written to be total rather than to lean on the guard above: this runs on a
	// worker goroutine inside the daemon that holds every server process, so a
	// panic here takes the whole fleet down with it.
	name := project
	if runes := []rune(project); len(runes) > 0 {
		runes[0] = unicode.ToUpper(runes[0])
		name = string(runes)
	}
	return fmt.Sprintf("https://download.fastmirror.net/download/%s/%s/build%s",
		url.PathEscape(name), url.PathEscape(version), url.PathEscape(build))
}

func init() {
	register("papermc", RouteSet{
		Name: "PaperMC",
		Routes: []Route{
			{
				ID: "fastmirror", Name: "FastMirror",
				Note:   "国内镜像，与官方字节一致",
				Kind:   RouteCopy,
				Serves: paperMCHosts,
				Link:   fastMirrorLink,
			},
			{
				ID: "official", Name: "PaperMC 官方",
				Note: "直连官方 CDN，境外机器选它",
				Kind: RouteDirect,
				Link: func(u Upstream) string { return u.URL },
			},
		},
	})
}
