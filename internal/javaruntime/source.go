package javaruntime

import (
	"fmt"
	"net/url"
	"strings"
)

// Download sources.
//
// A distribution's metadata API is only consulted for metadata — a few
// kilobytes of JSON naming the build, its size and its SHA-256. The archive
// itself is 50–200 MB, and where it comes from is the only thing a source
// changes: whichever one serves the bytes, they are still checked against the
// checksum the distribution published, so a mirror cannot hand back a
// different JDK than the official link would have (see Installer.download).
//
// The two distributions need separate lists because they have nothing in
// common. Temurin's archives sit on GitHub's release CDN, which from a
// mainland Chinese host is anywhere between slow and hopeless, and the mirrors
// below exist to fix exactly that. Zulu's come off cdn.azul.com, which none of
// those mirrors carries and the GitHub proxy cannot wrap.
const (
	// SourceAuto works down the list and only then falls back to the official
	// link. It is what an install without a stated source uses.
	SourceAuto = "auto"
	// SourceOfficial downloads straight from the distribution's own CDN, and
	// is the fallback every other source ends at.
	SourceOfficial = "official"
)

// Source is a place the panel can download a Java archive from.
type Source struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Note string `json:"note"`
	// Default marks the source an install that names none gets.
	Default bool `json:"default,omitempty"`
}

// source is a Source plus how to build a download link for it.
type source struct {
	Source
	// link returns where this source serves a release, or "" when it cannot
	// serve that release at all.
	link func(Release) string
}

// temurinSources are tried in this order by SourceAuto, and offered to the
// operator in this order too. The China mirrors come first because that is
// where the problem is: everywhere else the official link is already the fast
// one, and picking it is one click away.
//
// The first three are byte-for-byte rsync copies of Adoptium's release tree,
// which is why one path template covers all of them.
var temurinSources = []source{
	{
		Source: Source{ID: "tuna", Name: "清华大学 TUNA", Note: "教育网镜像，国内一般最快"},
		link:   mirrorLink("https://mirrors.tuna.tsinghua.edu.cn/Adoptium"),
	},
	{
		Source: Source{ID: "nju", Name: "南京大学", Note: "另一个教育网镜像，清华不通时用"},
		link:   mirrorLink("https://mirror.nju.edu.cn/adoptium"),
	},
	{
		Source: Source{ID: "huawei", Name: "华为云", Note: "商业 CDN，非教育网线路上通常更稳"},
		link:   mirrorLink("https://mirrors.huaweicloud.com/adoptium"),
	},
	{
		Source: Source{ID: "ghproxy", Name: "GitHub 加速", Note: "代理官方发布页，镜像还没同步的新版本走这个"},
		link:   proxyLink("https://ghfast.top/"),
	},
	{
		Source: Source{ID: SourceOfficial, Name: "Adoptium 官方", Note: "直连 GitHub，境外机器选它"},
		link:   func(release Release) string { return release.URL },
	},
}

// zuluSources is one entry long for a reason. Zulu's archives come off
// cdn.azul.com rather than GitHub, so none of the Adoptium mirrors above
// carries a byte of it and the GitHub proxy has nothing to wrap. No mirror of
// the Zulu tree inside China could be confirmed to exist — NJU, Aliyun, BFSU
// and Tencent all answer 404 for it — so rather than ship a guess that 404s on
// every install, an operator who knows a working accelerator (or runs their
// own) fills it in as a custom prefix; see ResolveSource.
var zuluSources = []source{
	{
		Source: Source{
			ID:   SourceOfficial,
			Name: "Azul 官方 CDN",
			Note: "cdn.azul.com 直连，商业 CDN，国内一般比 GitHub 好走",
		},
		link: func(release Release) string { return release.URL },
	},
}

// sourcesFor is the list a distribution can download from.
func sourcesFor(dist string) []source {
	if dist == DistTemurin {
		return temurinSources
	}
	return zuluSources
}

// autoSource is the entry the UI shows for SourceAuto.
var autoSource = Source{
	ID:      SourceAuto,
	Name:    "自动",
	Note:    "按上面的顺序挨个试，哪个通用哪个",
	Default: true,
}

// Sources lists what an operator can pick for a distribution, automatic first.
func Sources(dist string) []Source {
	list := sourcesFor(dist)
	out := make([]Source, 0, len(list)+1)
	out = append(out, autoSource)
	for _, entry := range list {
		out = append(out, entry.Source)
	}
	return out
}

// SourceName is the human name of a source id, for a log line or an error.
func SourceName(dist, id string) string {
	if id == SourceAuto || id == "" {
		return autoSource.Name
	}
	for _, entry := range sourcesFor(dist) {
		if entry.ID == id {
			return entry.Name
		}
	}
	// A custom prefix is its own name; there is nothing better to call it.
	return id
}

// ResolveSource normalises a requested source id for a distribution. An empty
// one is the default; anything unrecognised is refused rather than quietly
// turned into a default, because silently downloading from somewhere other
// than what was asked for is exactly the surprise this feature exists to
// remove.
//
// Zulu additionally accepts a custom "https://…/" prefix, the same way
// plugin.ResolveMirror does: no mirror of the Zulu tree could be confirmed to
// exist in China, and an operator who has found one — or runs their own — is
// exactly the person this should not stand in the way of.
func ResolveSource(dist, id string) (string, error) {
	id = strings.TrimSpace(id)
	switch id {
	case "", SourceAuto:
		return SourceAuto, nil
	}
	for _, entry := range sourcesFor(dist) {
		if entry.ID == id {
			return id, nil
		}
	}
	if dist == DistZulu && isCustomPrefix(id) {
		if !strings.HasSuffix(id, "/") {
			id += "/"
		}
		return id, nil
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownSource, id)
}

func isCustomPrefix(id string) bool {
	return strings.HasPrefix(id, "https://") || strings.HasPrefix(id, "http://")
}

// attempt is one source-and-URL pair to try.
type attempt struct {
	id  string
	url string
}

// attempts lists where to look for a release, most preferred first.
//
// Every choice except the official link itself ends with the official link:
// mirrors sync on a schedule and a release published an hour ago is simply not
// on them yet, which would otherwise turn a working install into a 404 for
// whoever picked a mirror. The checksum makes falling back safe, and the job
// reports which source actually served the bytes.
func attempts(dist, id string, release Release) []attempt {
	list := sourcesFor(dist)

	var chosen []source
	switch id {
	case SourceAuto, "":
		chosen = list
	default:
		for _, entry := range list {
			if entry.ID == id {
				chosen = []source{entry}
				break
			}
		}
		if len(chosen) == 0 && dist == DistZulu && isCustomPrefix(id) {
			chosen = []source{{Source: Source{ID: id, Name: id}, link: flatLink(id)}}
		}
		if id != SourceOfficial {
			chosen = append(chosen, officialSource(dist))
		}
	}

	out := make([]attempt, 0, len(chosen))
	seen := make(map[string]bool, len(chosen))
	for _, entry := range chosen {
		link := entry.link(release)
		if link == "" || seen[link] {
			continue
		}
		seen[link] = true
		out = append(out, attempt{id: entry.ID, url: link})
	}
	return out
}

func officialSource(dist string) source {
	for _, entry := range sourcesFor(dist) {
		if entry.ID == SourceOfficial {
			return entry
		}
	}
	panic("javaruntime: the official source is missing from the source list")
}

// mirrorLink builds the path an Adoptium mirror stores a build under:
//
//	<base>/<major>/<jre|jdk>/<arch>/<os>/<file name>
//
// e.g. .../Adoptium/21/jre/x64/linux/OpenJDK21U-jre_x64_linux_hotspot_21.0.12_8.tar.gz.
// The tree is a copy of Adoptium's own, so the file name and its contents are
// the ones the API named.
func mirrorLink(base string) func(Release) string {
	base = strings.TrimSuffix(base, "/")
	return func(release Release) string {
		if release.Major <= 0 || release.ImageType == "" ||
			release.Arch == "" || release.OS == "" || release.FileName == "" {
			return ""
		}
		return fmt.Sprintf("%s/%d/%s/%s/%s/%s", base,
			release.Major,
			url.PathEscape(release.ImageType),
			url.PathEscape(release.Arch),
			url.PathEscape(release.OS),
			url.PathEscape(release.FileName))
	}
}

// flatLink builds the path a mirror of Zulu's CDN stores a build under.
// cdn.azul.com/zulu/bin holds every archive in one directory rather than the
// nested tree Adoptium's mirrors copy, so the file name is the whole path —
// which is also why a custom Zulu prefix needs nothing but the prefix.
func flatLink(base string) func(Release) string {
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return func(release Release) string {
		if release.FileName == "" {
			return ""
		}
		return base + url.PathEscape(release.FileName)
	}
}

// proxyLink puts a GitHub proxy in front of the official link. Unlike a mirror
// it carries no copy of its own, so it has whatever GitHub has — including a
// release published minutes ago — at the cost of trusting a third party with
// the bytes, which the checksum already covers. The panel's self-updater uses
// the same proxy by default; see config.DefaultUpdateMirror.
func proxyLink(prefix string) func(Release) string {
	return func(release Release) string {
		if !strings.HasPrefix(release.URL, "https://github.com/") {
			return ""
		}
		return prefix + release.URL
	}
}
