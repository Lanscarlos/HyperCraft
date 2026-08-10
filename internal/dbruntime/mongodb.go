package dbruntime

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// mongoManifest is MongoDB's own release feed. It lists the current build of
// every supported series together with a SHA-256 for each file, which makes it
// the one engine here whose downloads can be verified as strongly as a Java
// runtime's.
var mongoManifest = "https://downloads.mongodb.org/current.json"

type mongoResolver struct{}

type mongoPayload struct {
	Versions []mongoVersion `json:"versions"`
}

type mongoVersion struct {
	Version            string       `json:"version"`
	LTS                bool         `json:"lts_release"`
	Production         bool         `json:"production_release"`
	ReleaseCandidate   bool         `json:"release_candidate"`
	DevelopmentRelease bool         `json:"development_release"`
	Downloads          []mongoAsset `json:"downloads"`
}

type mongoAsset struct {
	Arch    string `json:"arch"`
	Edition string `json:"edition"`
	Target  string `json:"target"`
	Archive struct {
		URL    string `json:"url"`
		SHA256 string `json:"sha256"`
	} `json:"archive"`
}

func (mongoResolver) Versions(ctx context.Context, client *Client, platform Platform) ([]Version, error) {
	payload, err := fetchMongo(ctx, client)
	if err != nil {
		return nil, err
	}

	var out []Version
	for _, entry := range payload.Versions {
		if !entry.Production || entry.ReleaseCandidate || entry.DevelopmentRelease {
			continue
		}
		// A series with no build for this machine is not worth offering: the
		// operator would pick it and get a 404 dressed up as a failed install.
		if _, err := pickMongoDownload(entry.Downloads, platform); err != nil {
			continue
		}
		out = append(out, Version{
			Version: entry.Version,
			Series:  seriesOf(entry.Version),
			LTS:     entry.LTS,
			Note:    mongoNote(seriesOf(entry.Version)),
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: MongoDB 没有 %s/%s 的社区版构建",
			ErrUnsupported, platform.OS, platform.Arch)
	}
	sort.Slice(out, func(a, b int) bool { return compareVersions(out[a].Version, out[b].Version) > 0 })
	return out, nil
}

func (mongoResolver) Resolve(ctx context.Context, client *Client, version string, platform Platform) (Release, error) {
	payload, err := fetchMongo(ctx, client)
	if err != nil {
		return Release{}, err
	}

	for _, entry := range payload.Versions {
		if entry.Version != version {
			continue
		}
		download, err := pickMongoDownload(entry.Downloads, platform)
		if err != nil {
			return Release{}, err
		}
		name, err := fileNameFromURL(download.url)
		if err != nil {
			return Release{}, err
		}
		return Release{
			Version:  version,
			Series:   seriesOf(version),
			FileName: name,
			URL:      download.url,
			Checksum: strings.ToLower(download.sha256),
			Algo:     "sha256",
		}, nil
	}
	// current.json only carries the newest build of each series, so a version
	// typed by hand — or one that was current when the page was opened and has
	// been superseded since — lands here rather than in a 404 halfway through
	// a download.
	return Release{}, fmt.Errorf("%w: MongoDB 的发布清单里没有 %s，"+
		"清单只列每条产品线的当前版本，旧的补丁版本装不了", ErrUnknownRelease, version)
}

type mongoDownload struct {
	url    string
	sha256 string
}

// pickMongoDownload chooses the community build for this machine.
//
// MongoDB stopped publishing a generic Linux tarball at 4.0: every Linux build
// is compiled against one distribution's libraries, so picking the wrong one
// gets a server that unpacks fine and dies on a missing symbol. The exact
// distribution is tried first and a deliberately old build second — a binary
// linked against an older glibc runs on a newer system, never the reverse.
func pickMongoDownload(downloads []mongoAsset, platform Platform) (mongoDownload, error) {
	arches := mongoArches(platform)
	byTarget := map[string]mongoDownload{}
	for _, entry := range downloads {
		// "enterprise" is the licensed build and is not ours to install.
		if entry.Edition == "enterprise" || entry.Archive.URL == "" {
			continue
		}
		if !arches[strings.ToLower(entry.Arch)] {
			continue
		}
		if _, seen := byTarget[entry.Target]; seen {
			continue
		}
		byTarget[entry.Target] = mongoDownload{url: entry.Archive.URL, sha256: entry.Archive.SHA256}
	}

	for _, target := range mongoTargets(platform) {
		if found, ok := byTarget[target]; ok {
			return found, nil
		}
	}
	return mongoDownload{}, fmt.Errorf("%w: MongoDB 没有 %s/%s 的社区版构建",
		ErrUnsupported, platform.OS, platform.Arch)
}

// mongoArches maps our arch onto the several names MongoDB uses for it —
// aarch64 on Linux, arm64 on macOS.
func mongoArches(platform Platform) map[string]bool {
	if platform.Arch == "arm64" {
		return map[string]bool{"aarch64": true, "arm64": true}
	}
	return map[string]bool{"x86_64": true}
}

// mongoTargets is the order to try build targets in, best match first.
func mongoTargets(platform Platform) []string {
	switch platform.OS {
	case "windows":
		return []string{"windows"}
	case "darwin":
		return []string{"macos"}
	}

	var exact []string
	major, _, _ := strings.Cut(platform.DistroVersion, ".")
	compact := strings.ReplaceAll(platform.DistroVersion, ".", "")
	switch platform.Distro {
	case "ubuntu":
		exact = append(exact, "ubuntu"+compact)
	case "debian":
		exact = append(exact, "debian"+major)
	case "rhel", "centos", "rocky", "almalinux", "ol", "fedora":
		// The rhel targets are named inconsistently across releases — rhel8,
		// rhel93, rhel90 — so both the bare major and the two-digit forms are
		// worth asking for.
		exact = append(exact, "rhel"+major, "rhel"+major+"3", "rhel"+major+"0")
	case "amzn":
		exact = append(exact, "amazon"+platform.DistroVersion)
	case "sles", "opensuse", "opensuse-leap":
		exact = append(exact, "suse"+major)
	}

	// The fallback runs oldest-first on purpose: these are the builds most
	// likely to load on a distribution we did not recognise.
	return append(exact,
		"ubuntu2004", "debian11", "rhel8", "ubuntu2204",
		"debian12", "ubuntu2404", "rhel93", "rhel10", "amazon2023", "suse15")
}

func mongoNote(series string) string {
	switch series {
	case "8.0":
		return "长期支持线，插件用它最稳妥"
	case "7.0", "6.0":
		return "老一些的长期支持线，只有明确要求时才选"
	case "5.0", "4.4":
		return "已停止维护，除非插件写死了版本否则别装"
	}
	return "较新的产品线，维护期比长期支持线短"
}

func fetchMongo(ctx context.Context, client *Client) (mongoPayload, error) {
	var payload mongoPayload
	if err := client.getJSON(ctx, mongoManifest, &payload); err != nil {
		return mongoPayload{}, err
	}
	if len(payload.Versions) == 0 {
		return mongoPayload{}, fmt.Errorf("%w: MongoDB 的发布清单是空的", ErrUpstream)
	}
	return payload, nil
}

// compareVersions orders dotted numeric versions. Missing components count as
// zero, so 17 sorts below 17.6.
func compareVersions(a, b string) int {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(left) || i < len(right); i++ {
		var x, y int
		if i < len(left) {
			x = atoiSafe(left[i])
		}
		if i < len(right) {
			y = atoiSafe(right[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func atoiSafe(s string) int {
	value := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return value
		}
		value = value*10 + int(r-'0')
	}
	return value
}
