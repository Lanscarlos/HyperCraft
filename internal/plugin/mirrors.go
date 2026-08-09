package plugin

import (
	"fmt"
	"strings"
)

// Download mirrors for plugin jars.
//
// A mirror only ever carries the bytes. Release metadata is read straight from
// api.github.com, which none of these proxies front, and a private repository
// never goes through one at all — see downloadOrder. So what a mirror changes
// is download speed, which from a mainland Chinese host is the difference
// between a two-second install and a timeout.
//
// Unlike the Java runtime mirrors, these are proxies rather than copies: they
// fetch the same GitHub URL on the panel's behalf, so a release published a
// minute ago is available through them immediately. What they cannot offer is
// a checksum to verify against — GitHub publishes none for release assets, so
// the trust in a plugin jar is the same whichever way it arrived, and picking a
// proxy widens who is trusted with the bytes. An operator with a good line to
// GitHub should pick 直连.
const (
	// MirrorAuto works down the list and only then goes direct. It is what a
	// panel that has never been told otherwise uses, because the common case is
	// a host that needs a proxy and an operator who does not want to test four
	// of them by hand.
	MirrorAuto = "auto"
	// MirrorDirect downloads from GitHub with nothing in between.
	MirrorDirect = "direct"
)

// ErrUnknownMirror rejects a mirror id this build does not have.
var ErrUnknownMirror = fmt.Errorf("unknown download mirror")

// Mirror is a place plugin jars can be downloaded through.
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

// mirrors are tried in this order by MirrorAuto and offered in this order too.
// The proxies come first because a panel that does not need one is a panel
// whose operator can pick 直连 in one click, while the reverse — a Chinese host
// discovering that plugin downloads simply time out — is a bug report.
var mirrors = []Mirror{
	{
		ID:     "ghfast",
		Name:   "ghfast.top",
		Note:   "国内访问通常最快，面板自身更新也默认走它",
		Prefix: "https://ghfast.top/",
	},
	{
		ID:     "ghproxy",
		Name:   "gh-proxy.com",
		Note:   "老牌代理，ghfast 不通时的第一备选",
		Prefix: "https://gh-proxy.com/",
	},
	{
		ID:     "moeyy",
		Name:   "github.moeyy.xyz",
		Note:   "再一个备选，用法相同",
		Prefix: "https://github.moeyy.xyz/",
	},
	{
		ID:   MirrorDirect,
		Name: "直连 GitHub",
		Note: "不经过任何第三方，境外机器选它",
	},
}

// autoMirror is the entry the UI shows for MirrorAuto.
var autoMirror = Mirror{
	ID:      MirrorAuto,
	Name:    "自动",
	Note:    "按上面的顺序挨个试，哪个通用哪个",
	Default: true,
}

// Mirrors lists what an operator can pick, automatic first.
func Mirrors() []Mirror {
	out := make([]Mirror, 0, len(mirrors)+1)
	out = append(out, autoMirror)
	out = append(out, mirrors...)
	return out
}

// MirrorName is the human name of a mirror id, for a log line or a job.
func MirrorName(id string) string {
	switch id {
	case "", MirrorAuto:
		return autoMirror.Name
	}
	for _, mirror := range mirrors {
		if mirror.ID == id {
			return mirror.Name
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
	id = strings.TrimSpace(id)
	switch id {
	case "", MirrorAuto:
		return MirrorAuto, nil
	}
	for _, mirror := range mirrors {
		if mirror.ID == id {
			return id, nil
		}
	}
	if strings.HasPrefix(id, "https://") || strings.HasPrefix(id, "http://") {
		if !strings.HasSuffix(id, "/") {
			id += "/"
		}
		return id, nil
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownMirror, id)
}

// mirrorOrder is the prefixes to try for one GitHub download link, most
// preferred first, where "" means the direct link.
//
// Every choice ends at the direct link. A proxy that is down, blocked or
// rate-limiting would otherwise turn a working install into a failure, and
// unlike a mirror with its own copy there is nothing a proxy has that GitHub
// does not — falling through to the origin can only be more correct.
func mirrorOrder(id, url string) []string {
	if !strings.HasPrefix(url, "https://github.com/") {
		// Nothing else is a GitHub release link, and these proxies front
		// nothing else, so a prefix could only produce a 404.
		return []string{""}
	}

	var prefixes []string
	switch id {
	case "", MirrorAuto:
		for _, mirror := range mirrors {
			prefixes = append(prefixes, mirror.Prefix)
		}
	case MirrorDirect:
		return []string{""}
	default:
		prefixes = append(prefixes, mirrorPrefix(id))
	}

	out := make([]string, 0, len(prefixes)+1)
	seen := make(map[string]bool, len(prefixes)+1)
	for _, prefix := range append(prefixes, "") {
		if seen[prefix] {
			continue
		}
		seen[prefix] = true
		out = append(out, prefix)
	}
	return out
}

// mirrorID names the mirror a prefix belongs to, so a finished job can say
// which one served the bytes. A prefix that is not one of the known proxies is
// the operator's own, and the chosen setting is its name.
func mirrorID(chosen, prefix string) string {
	for _, mirror := range mirrors {
		if mirror.Prefix == prefix {
			return mirror.ID
		}
	}
	return chosen
}

// mirrorPrefix is the URL prefix behind an id, or the id itself when it is
// already a custom prefix.
func mirrorPrefix(id string) string {
	for _, mirror := range mirrors {
		if mirror.ID == id {
			return mirror.Prefix
		}
	}
	return id
}
