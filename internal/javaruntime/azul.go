package javaruntime

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// defaultAzulBaseURL is Azul's metadata API. Zulu is a TCK-certified OpenJDK
// build with no account and no click-through licence, and every package comes
// with a SHA-256 the panel checks before unpacking anything.
const defaultAzulBaseURL = "https://api.azul.com/metadata/v1/zulu"

// azulProvider serves Azul Zulu.
type azulProvider struct {
	baseURL string
	http    *httpClient
	cache   majorCache
}

func newAzulProvider(baseURL string, client *httpClient) *azulProvider {
	if baseURL == "" {
		baseURL = defaultAzulBaseURL
	}
	return &azulProvider{baseURL: strings.TrimSuffix(baseURL, "/"), http: client}
}

// azulPackage is one entry of the packages listing. Only the fields the panel
// uses are named; sha256_hash and size arrive because the query asks for them
// with include_fields, which is what keeps an install down to one request.
type azulPackage struct {
	Name        string `json:"name"`
	JavaVersion []int  `json:"java_version"`
	DownloadURL string `json:"download_url"`
	SHA256      string `json:"sha256_hash"`
	Size        int64  `json:"size"`
	SupportTerm string `json:"support_term"`
}

// version renders 21.0.12.1 out of the array the API reports.
func (p azulPackage) version() string {
	parts := make([]string, 0, len(p.JavaVersion))
	for _, n := range p.JavaVersion {
		parts = append(parts, strconv.Itoa(n))
	}
	return strings.Join(parts, ".")
}

// baseQuery is everything both calls have to say to get a plain, current,
// generally-available Zulu and nothing else.
//
// crac_supported and javafx_bundled are not optional: left off, the API really
// does return CRaC builds and JavaFX-bundled ones mixed in with the plain
// packages, and the panel would install whichever came back first.
func baseQuery() url.Values {
	return url.Values{
		"availability_type": {"ca"},
		"release_status":    {"ga"},
		"javafx_bundled":    {"false"},
		"crac_supported":    {"false"},
	}
}

// archiveType is what an OS ships as. Asking for the wrong one is an empty
// result rather than an error, which would read as "Zulu has no build for you".
func archiveType(osName string) string {
	if osName == "windows" {
		return "zip"
	}
	return "tar.gz"
}

// Majors lists the feature releases Azul currently ships, newest first, with
// the long-term-support ones flagged.
func (p *azulProvider) Majors(ctx context.Context) ([]Major, error) {
	if cached, ok := p.cache.get(); ok {
		return cached, nil
	}

	query := baseQuery()
	query.Set("java_package_type", ImageJRE)
	query.Set("latest", "true")
	query.Set("page_size", "200")
	query.Set("include_fields", "support_term")

	var payload []azulPackage
	if err := p.http.getJSON(ctx, p.baseURL+"/packages/?"+query.Encode(), &payload); err != nil {
		return nil, err
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("%w: no releases listed", ErrUpstream)
	}

	// There is no "available releases" endpoint here, only the package
	// listing, and one feature release appears in it many times over — once
	// per build, platform and architecture. Fold it down to the feature number.
	lts := make(map[int]bool)
	for _, entry := range payload {
		if len(entry.JavaVersion) == 0 {
			continue
		}
		major := entry.JavaVersion[0]
		isLTS := strings.EqualFold(entry.SupportTerm, "lts")
		if seen, ok := lts[major]; !ok || (!seen && isLTS) {
			lts[major] = isLTS
		}
	}
	if len(lts) == 0 {
		return nil, fmt.Errorf("%w: no usable releases listed", ErrUpstream)
	}

	majors := make([]Major, 0, len(lts))
	for major, isLTS := range lts {
		majors = append(majors, Major{Major: major, LTS: isLTS})
	}
	sort.Slice(majors, func(a, b int) bool { return majors[a].Major > majors[b].Major })

	p.cache.put(majors)
	return majors, nil
}

// LatestRelease resolves the newest build of a major version for a platform.
func (p *azulProvider) LatestRelease(ctx context.Context, major int, imageType string, platform Platform) (Release, error) {
	query := baseQuery()
	query.Set("java_version", strconv.Itoa(major))
	query.Set("java_package_type", imageType)
	query.Set("os", platform.OS)
	query.Set("arch", platform.Arch)
	query.Set("archive_type", archiveType(platform.OS))
	query.Set("latest", "true")
	query.Set("page_size", "1")
	query.Set("include_fields", "sha256_hash,size")
	if platform.LibC != "" {
		query.Set("lib_c_type", platform.LibC)
	}

	var payload []azulPackage
	if err := p.http.getJSON(ctx, p.baseURL+"/packages/?"+query.Encode(), &payload); err != nil {
		return Release{}, err
	}
	if len(payload) == 0 {
		return Release{}, fmt.Errorf("%w: Azul 没有 Java %d 的 %s（%s/%s%s）",
			ErrUnknownRelease, major, imageType, platform.OS, platform.Arch, libcSuffix(platform.LibC))
	}

	entry := payload[0]
	name, err := safeFileName(entry.Name)
	if err != nil {
		return Release{}, err
	}
	if err := p.http.checkDownloadURL(entry.DownloadURL); err != nil {
		return Release{}, err
	}
	if !isSupportedArchive(name) {
		return Release{}, fmt.Errorf("%w: 不认识的包格式 %q", ErrUpstream, name)
	}
	version := entry.version()
	if version == "" {
		return Release{}, fmt.Errorf("%w: package %q reports no java version", ErrUpstream, name)
	}

	return Release{
		Distribution: DistZulu,
		Major:        major,
		Version:      version,
		Name:         strings.TrimSuffix(strings.TrimSuffix(name, ".tar.gz"), ".zip"),
		ImageType:    imageType,
		// The platform comes from the request, never from the response: Azul
		// reports "x86" with hw_bitness 64 for an x64 build and "arm" for
		// aarch64, so its own fields would put the wrong label on the runtime
		// and on every error message about it.
		OS:       platform.OS,
		Arch:     platform.Arch,
		FileName: name,
		URL:      entry.DownloadURL,
		SHA256:   strings.ToLower(entry.SHA256),
		Size:     entry.Size,
	}, nil
}

// libcSuffix names the C library in an error, but only when it is the unusual
// one — "linux/x64" reads better than "linux/x64, glibc".
func libcSuffix(libc string) string {
	if libc == "" || libc == "glibc" {
		return ""
	}
	return "，" + libc
}
