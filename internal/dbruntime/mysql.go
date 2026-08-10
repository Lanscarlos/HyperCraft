package dbruntime

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// mysqlBase is Oracle's archive CDN. Unlike the other two engines MySQL
// publishes no machine-readable release feed at all — no manifest, no
// repository index, not even a directory listing — so the version list below is
// pinned into the binary and the page lets a version be typed in for when it
// goes stale. The paths themselves are stable and predictable, which is what
// makes that workable.
var mysqlBase = "https://cdn.mysql.com/archives"

type mysqlResolver struct{}

// mysqlSeries is the product lines the panel offers, newest first.
//
// Latest is the newest patch as of this panel release. It is a starting point,
// not a ceiling: Resolve accepts any version and asks the CDN, so an operator
// told to install a specific patch is never blocked by this table being a few
// months old.
var mysqlSeries = []struct {
	Series string
	Latest string
	LTS    bool
	Note   string
}{
	{"8.4", "8.4.10", true, "当前 LTS，2032 年前都有安全更新，新装选它"},
	{"8.0", "8.0.45", true, "老 LTS，插件兼容性最好，但已于 2026 年 4 月停止更新"},
	{"9.5", "9.5.0", false, "创新版本，下一个创新版发布后即停止维护，不建议用来跑服"},
}

func (mysqlResolver) Versions(ctx context.Context, client *Client, platform Platform) ([]Version, error) {
	if _, err := mysqlFileName("0.0.0", platform); err != nil {
		return nil, err
	}

	out := make([]Version, 0, len(mysqlSeries))
	for _, entry := range mysqlSeries {
		out = append(out, Version{
			Version: entry.Latest,
			Series:  entry.Series,
			LTS:     entry.LTS,
			Note:    entry.Note,
		})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].LTS && !out[b].LTS })
	return out, nil
}

func (mysqlResolver) Resolve(ctx context.Context, client *Client, version string, platform Platform) (Release, error) {
	name, err := mysqlFileName(version, platform)
	if err != nil {
		return Release{}, err
	}
	series := seriesOf(version)
	link := fmt.Sprintf("%s/mysql-%s/%s", mysqlBase, series, name)

	// The CDN is also the only place that can say whether a version exists, so
	// this HEAD doubles as the check that the typed-in version is real.
	size, err := client.size(ctx, link)
	if err != nil {
		return Release{}, err
	}
	release := Release{
		Version:  version,
		Series:   series,
		FileName: name,
		URL:      link,
		Size:     size,
	}
	// Oracle publishes an MD5 sidecar and nothing better. MD5 catches a
	// truncated or corrupted transfer, which is what it is here for; against a
	// forged file it is worth nothing, and TLS to cdn.mysql.com is what the
	// install actually rests on. Its absence is not a reason to refuse.
	if sum, err := client.getText(ctx, link+".md5"); err == nil {
		if fields := strings.Fields(sum); len(fields) > 0 {
			release.Checksum, release.Algo = strings.ToLower(fields[0]), "md5"
		}
	}
	return release, nil
}

// mysqlFileName is the tarball for this machine.
func mysqlFileName(version string, platform Platform) (string, error) {
	switch {
	case platform.OS == "windows" && platform.Arch == "amd64":
		return fmt.Sprintf("mysql-%s-winx64.zip", version), nil
	case platform.OS == "linux" && platform.Arch == "amd64":
		// The "minimal" build is the server, the client and the character sets
		// with the debug binaries and the test suite left out: 60 MB against
		// 900 MB for the full tarball, and nothing a server needs is missing.
		return fmt.Sprintf("mysql-%s-linux-glibc2.17-x86_64-minimal.tar.xz", version), nil
	case platform.OS == "linux" && platform.Arch == "arm64":
		// No minimal build is published for aarch64, so this is the full
		// tarball — a much larger download, which the page warns about.
		return fmt.Sprintf("mysql-%s-linux-glibc2.28-aarch64.tar.xz", version), nil
	case platform.OS == "darwin":
		// The macOS tarball encodes the macOS release it was built on
		// (mysql-8.0.45-macos15-arm64.tar.gz) and that number moves between
		// MySQL patches, so there is no path this package can construct with
		// any confidence. Homebrew is the sane answer on a Mac anyway.
		return "", fmt.Errorf("%w: macOS 上的 MySQL 请用 Homebrew 安装（brew install mysql），"+
			"官方压缩包的文件名带 macOS 版本号，面板没法可靠地拼出来", ErrUnsupported)
	}
	return "", fmt.Errorf("%w: MySQL 没有 %s/%s 的官方压缩包",
		ErrUnsupported, platform.OS, platform.Arch)
}
