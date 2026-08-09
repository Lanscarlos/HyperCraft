package javaruntime

import (
	"fmt"
	"net/url"
	"strings"
)

// Download sources.
//
// The Adoptium API is only consulted for metadata — a few kilobytes of JSON
// naming the build, its size and its SHA-256. The archive itself is 50–200 MB
// and comes off GitHub's release CDN, which from a mainland Chinese host is
// anywhere between slow and hopeless. That download is the only thing a source
// changes: whichever one serves the bytes, they are still checked against the
// checksum Adoptium published, so a mirror cannot hand back a different JDK
// than the official link would have (see Installer.download).
//
// The mirrors below are byte-for-byte rsync copies of Adoptium's release tree,
// which is why one path template covers all of them.
const (
	// SourceAuto works down the list of mirrors and only then falls back to
	// the official link. It is what an install without a stated source uses.
	SourceAuto = "auto"
	// SourceOfficial downloads straight from Adoptium's GitHub releases, and
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

// mirrors are tried in this order by SourceAuto, and offered to the operator in
// this order too. The China mirrors come first because that is where the
// problem is: everywhere else the official link is already the fast one, and
// picking it is one click away.
var mirrors = []source{
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

// autoSource is the entry the UI shows for SourceAuto.
var autoSource = Source{
	ID:      SourceAuto,
	Name:    "自动",
	Note:    "按上面的顺序挨个试，哪个通用哪个",
	Default: true,
}

// Sources lists what an operator can pick, automatic first.
func Sources() []Source {
	out := make([]Source, 0, len(mirrors)+1)
	out = append(out, autoSource)
	for _, mirror := range mirrors {
		out = append(out, mirror.Source)
	}
	return out
}

// SourceName is the human name of a source id, for a log line or an error.
func SourceName(id string) string {
	if id == SourceAuto || id == "" {
		return autoSource.Name
	}
	for _, mirror := range mirrors {
		if mirror.ID == id {
			return mirror.Name
		}
	}
	return id
}

// ResolveSource normalises a requested source id. An empty one is the default;
// anything unrecognised is refused rather than quietly turned into a default,
// because silently downloading from somewhere other than what was asked for is
// exactly the surprise this feature exists to remove.
func ResolveSource(id string) (string, error) {
	switch id {
	case "", SourceAuto:
		return SourceAuto, nil
	}
	for _, mirror := range mirrors {
		if mirror.ID == id {
			return id, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownSource, id)
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
func attempts(id string, release Release) []attempt {
	var chosen []source
	switch id {
	case SourceAuto, "":
		chosen = mirrors
	default:
		for _, mirror := range mirrors {
			if mirror.ID == id {
				chosen = []source{mirror}
				break
			}
		}
		if id != SourceOfficial {
			chosen = append(chosen, officialSource())
		}
	}

	out := make([]attempt, 0, len(chosen))
	seen := make(map[string]bool, len(chosen))
	for _, source := range chosen {
		link := source.link(release)
		if link == "" || seen[link] {
			continue
		}
		seen[link] = true
		out = append(out, attempt{id: source.ID, url: link})
	}
	return out
}

func officialSource() source {
	for _, mirror := range mirrors {
		if mirror.ID == SourceOfficial {
			return mirror
		}
	}
	panic("javaruntime: the official source is missing from the mirror list")
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
