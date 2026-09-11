package javaruntime

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// DefaultBaseURL is the Adoptium API. Temurin is the reference OpenJDK build:
// no account, no click-through licence, and every asset comes with a checksum.
const DefaultBaseURL = "https://api.adoptium.net/v3"

// adoptiumProvider serves Eclipse Temurin.
type adoptiumProvider struct {
	baseURL string
	http    *httpClient
	cache   majorCache
}

func newAdoptiumProvider(baseURL string, client *httpClient) *adoptiumProvider {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &adoptiumProvider{baseURL: strings.TrimSuffix(baseURL, "/"), http: client}
}

// Majors lists the feature releases Adoptium currently ships, newest first,
// with the long-term-support ones flagged.
func (p *adoptiumProvider) Majors(ctx context.Context) ([]Major, error) {
	if cached, ok := p.cache.get(); ok {
		return cached, nil
	}

	var payload struct {
		AvailableReleases    []int `json:"available_releases"`
		AvailableLTSReleases []int `json:"available_lts_releases"`
	}
	if err := p.http.getJSON(ctx, p.baseURL+"/info/available_releases", &payload); err != nil {
		return nil, err
	}
	if len(payload.AvailableReleases) == 0 {
		return nil, fmt.Errorf("%w: no releases listed", ErrUpstream)
	}

	lts := make(map[int]bool, len(payload.AvailableLTSReleases))
	for _, major := range payload.AvailableLTSReleases {
		lts[major] = true
	}
	majors := make([]Major, 0, len(payload.AvailableReleases))
	for _, major := range payload.AvailableReleases {
		majors = append(majors, Major{Major: major, LTS: lts[major]})
	}
	sort.Slice(majors, func(a, b int) bool { return majors[a].Major > majors[b].Major })

	p.cache.put(majors)
	return majors, nil
}

// LatestRelease resolves the newest build of a major version for a platform.
func (p *adoptiumProvider) LatestRelease(ctx context.Context, major int, imageType string, platform Platform) (Release, error) {
	query := url.Values{
		"architecture": {platform.Arch},
		"image_type":   {imageType},
		"os":           {platform.OS},
		"vendor":       {"eclipse"},
	}
	endpoint := "/assets/latest/" + strconv.Itoa(major) + "/hotspot?" + query.Encode()

	var payload []struct {
		Binary struct {
			ImageType string `json:"image_type"`
			OS        string `json:"os"`
			Arch      string `json:"architecture"`
			Package   struct {
				Name     string `json:"name"`
				Link     string `json:"link"`
				Size     int64  `json:"size"`
				Checksum string `json:"checksum"`
			} `json:"package"`
		} `json:"binary"`
		ReleaseName string `json:"release_name"`
		Version     struct {
			Major          int    `json:"major"`
			OpenJDKVersion string `json:"openjdk_version"`
		} `json:"version"`
	}
	if err := p.http.getJSON(ctx, p.baseURL+endpoint, &payload); err != nil {
		return Release{}, err
	}
	if len(payload) == 0 {
		return Release{}, fmt.Errorf("%w: Adoptium 没有 Java %d 的 %s（%s/%s）",
			ErrUnknownRelease, major, imageType, platform.OS, platform.Arch)
	}

	entry := payload[0]
	name, err := safeFileName(entry.Binary.Package.Name)
	if err != nil {
		return Release{}, err
	}
	if err := p.http.checkDownloadURL(entry.Binary.Package.Link); err != nil {
		return Release{}, err
	}
	if !isSupportedArchive(name) {
		return Release{}, fmt.Errorf("%w: 不认识的包格式 %q", ErrUpstream, name)
	}

	version := entry.Version.OpenJDKVersion
	if version == "" {
		version = entry.ReleaseName
	}
	return Release{
		Distribution: DistTemurin,
		Major:        major,
		Version:      version,
		Name:         entry.ReleaseName,
		ImageType:    imageType,
		OS:           entry.Binary.OS,
		Arch:         entry.Binary.Arch,
		FileName:     name,
		URL:          entry.Binary.Package.Link,
		SHA256:       strings.ToLower(entry.Binary.Package.Checksum),
		Size:         entry.Binary.Package.Size,
	}, nil
}
