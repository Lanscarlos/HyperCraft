package dbruntime

import (
	"context"
	"encoding/xml"
	"fmt"
	"sort"
	"strings"
)

// PostgreSQL binaries come from Maven Central.
//
// That needs explaining, because it is the one engine here the panel does not
// download from the project itself. postgresql.org ships source for Linux and
// nothing else; EnterpriseDB's "binaries" bundles, which used to fill the gap,
// are Windows and macOS only now. There is no official portable Linux build to
// download, so a panel that will not touch the system package manager has to
// get its server from someone who repackages one.
//
// io.zonky.test.postgres is that someone: the binaries behind embedded-postgres,
// built for glibc and musl on both architectures, published per PostgreSQL
// patch release, and used by a very large number of Java test suites. Maven
// Central artifacts are immutable once published and carry a checksum beside
// them, which is the property that matters — see Installer.download.
const (
	mavenBase  = "https://repo1.maven.org/maven2/io/zonky/test/postgres"
	pgArtifact = "embedded-postgres-binaries"
	// pgMajors caps how many product lines the page offers. PostgreSQL supports
	// five at a time; a sixth is on Maven long after upstream stopped shipping
	// fixes for it.
	pgMajors = 5
)

type postgresResolver struct{}

func (postgresResolver) Versions(ctx context.Context, client *Client, platform Platform) ([]Version, error) {
	artifact, err := pgArtifactID(platform)
	if err != nil {
		return nil, err
	}

	// maven-metadata.xml nests the list two deep: <versioning><versions><version>.
	var payload struct {
		Versions []string `xml:"versioning>versions>version"`
	}
	link := fmt.Sprintf("%s/%s/maven-metadata.xml", mavenBase, artifact)
	body, err := client.getText(ctx, link)
	if err != nil {
		return nil, err
	}
	if err := xml.Unmarshal([]byte(body), &payload); err != nil {
		return nil, fmt.Errorf("%w: Maven 元数据解析失败：%v", ErrUpstream, err)
	}

	// One entry per major: an operator picks "PostgreSQL 17", never "17.6
	// rather than 17.5". The newest patch of each line is the only one worth
	// installing anyway.
	newest := map[string]string{}
	for _, raw := range payload.Versions {
		version := pgVersionOf(raw)
		if version == "" {
			continue
		}
		major := strings.Split(version, ".")[0]
		if current, ok := newest[major]; !ok || compareVersions(version, current) > 0 {
			newest[major] = version
		}
	}
	if len(newest) == 0 {
		return nil, fmt.Errorf("%w: Maven 上没有 %s 的 PostgreSQL 构建", ErrUnsupported, artifact)
	}

	out := make([]Version, 0, len(newest))
	for major, version := range newest {
		out = append(out, Version{
			Version: version,
			Series:  major,
			// Every PostgreSQL major gets five years of fixes, so the
			// distinction the other two engines draw between a supported line
			// and a short-lived one does not exist here.
			LTS:  true,
			Note: "社区版，上游维护五年",
		})
	}
	sort.Slice(out, func(a, b int) bool { return compareVersions(out[a].Version, out[b].Version) > 0 })
	if len(out) > pgMajors {
		out = out[:pgMajors]
	}
	return out, nil
}

func (postgresResolver) Resolve(ctx context.Context, client *Client, version string, platform Platform) (Release, error) {
	artifact, err := pgArtifactID(platform)
	if err != nil {
		return Release{}, err
	}

	// Maven wants three components; PostgreSQL versions have two. The third is
	// zonky's own packaging revision and has been 0 for every release so far.
	mavenVersion := version
	if strings.Count(version, ".") == 1 {
		mavenVersion = version + ".0"
	}
	name := fmt.Sprintf("%s-%s-%s.jar", pgArtifact, pgPlatformID(platform), mavenVersion)
	link := fmt.Sprintf("%s/%s/%s/%s", mavenBase, artifact, mavenVersion, name)

	size, err := client.size(ctx, link)
	if err != nil {
		return Release{}, err
	}
	release := Release{
		Version:  version,
		Series:   strings.Split(version, ".")[0],
		FileName: name,
		URL:      link,
		Size:     size,
		// The jar is a container: the server itself is one .txz inside it.
		Inner: ".txz",
	}
	// Maven publishes SHA-1 beside every artifact and nothing stronger. Weak
	// against a forged file, fine against a truncated one, and the artifact is
	// immutable and served over TLS either way — so a missing checksum file is
	// not worth failing the install over.
	if sum, err := client.getText(ctx, link+".sha1"); err == nil {
		release.Checksum, release.Algo = strings.ToLower(strings.Fields(sum)[0]), "sha1"
	}
	return release, nil
}

// pgArtifactID is the Maven artifact for this machine.
func pgArtifactID(platform Platform) (string, error) {
	id := pgPlatformID(platform)
	if id == "" {
		return "", fmt.Errorf("%w: PostgreSQL 没有 %s/%s 的便携构建",
			ErrUnsupported, platform.OS, platform.Arch)
	}
	return pgArtifact + "-" + id, nil
}

// pgPlatformID is zonky's platform suffix, e.g. linux-amd64-alpine.
func pgPlatformID(platform Platform) string {
	arch := "amd64"
	if platform.Arch == "arm64" {
		arch = "arm64v8"
	}
	switch platform.OS {
	case "linux":
		if platform.Musl {
			// The one engine of the three with a musl build. Alpine hosts are
			// common enough in the container world to be worth the branch.
			return "linux-" + arch + "-alpine"
		}
		return "linux-" + arch
	case "darwin":
		return "darwin-" + arch
	case "windows":
		// No arm64 Windows build is published; amd64 runs under emulation.
		return "windows-amd64"
	}
	return ""
}

// pgVersionOf turns a Maven version into a PostgreSQL one: 17.6.0 -> 17.6.
// Anything that is not three numeric components is a packaging experiment we
// have no use for.
func pgVersionOf(raw string) string {
	parts := strings.Split(strings.TrimSpace(raw), ".")
	if len(parts) != 3 {
		return ""
	}
	for _, part := range parts {
		if part == "" {
			return ""
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return ""
			}
		}
	}
	return parts[0] + "." + parts[1]
}
