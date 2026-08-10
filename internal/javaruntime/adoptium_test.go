package javaruntime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

const majorsPayload = `{
  "available_releases": [8, 11, 17, 21, 25],
  "available_lts_releases": [8, 11, 17, 21, 25],
  "most_recent_lts": 25
}`

func releasePayload(url, checksum string, size int64) string {
	return fmt.Sprintf(`[{
      "binary": {"image_type":"jre","os":"linux","architecture":"x64",
        "package": {"name":"OpenJDK21U-jre_x64_linux_hotspot_21.0.1_12.tar.gz",
          "link":%q,"size":%d,"checksum":%q}},
      "release_name": "jdk-21.0.1+12",
      "version": {"major": 21, "openjdk_version": "21.0.1+12"}
    }]`, url, size, checksum)
}

func testPlatform() Platform { return Platform{OS: "linux", Arch: "x64"} }

func TestMajorsFlagsLTSAndCaches(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(majorsPayload))
	}))
	defer upstream.Close()

	client := NewClient(upstream.URL, "test")
	majors, err := client.Majors(context.Background())
	if err != nil {
		t.Fatalf("Majors: %v", err)
	}
	if len(majors) != 5 || majors[0].Major != 25 {
		t.Fatalf("expected newest first, got %+v", majors)
	}
	if !majors[0].LTS {
		t.Errorf("25 should be flagged LTS")
	}

	if _, err := client.Majors(context.Background()); err != nil {
		t.Fatalf("second Majors: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("upstream hit %d times, want 1 (cached)", got)
	}
}

func TestLatestReleaseParsesBinary(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("image_type"); got != "jre" {
			t.Errorf("image_type = %q", got)
		}
		if got := r.URL.Query().Get("vendor"); got != "eclipse" {
			t.Errorf("vendor = %q", got)
		}
		w.Write([]byte(releasePayload("https://cdn.example/jre.tar.gz", "ABC123", 4096)))
	}))
	defer upstream.Close()

	release, err := NewClient(upstream.URL, "test").
		LatestRelease(context.Background(), 21, ImageJRE, testPlatform())
	if err != nil {
		t.Fatalf("LatestRelease: %v", err)
	}
	if release.Version != "21.0.1+12" || release.Size != 4096 {
		t.Errorf("unexpected release: %+v", release)
	}
	if release.SHA256 != "abc123" {
		t.Errorf("checksum should be lowercased, got %q", release.SHA256)
	}
	if id := installID(release); id != "temurin-21.0.1-12-jre" {
		t.Errorf("install id = %q", id)
	}
}

func TestInstallIDDropsTheLTSSuffix(t *testing.T) {
	// What Adoptium actually returns for an LTS line.
	release := Release{Major: 21, Version: "21.0.12+8-LTS", ImageType: ImageJRE}
	if id := installID(release); id != "temurin-21.0.12-8-jre" {
		t.Errorf("install id = %q, want temurin-21.0.12-8-jre", id)
	}
}

func TestLatestReleaseRejectsBadInput(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream should not have been called for %s", r.URL)
	}))
	defer upstream.Close()

	client := NewClient(upstream.URL, "test")
	if _, err := client.LatestRelease(context.Background(), 0, ImageJRE, testPlatform()); !errors.Is(err, ErrUnknownRelease) {
		t.Errorf("major 0: got %v", err)
	}
	if _, err := client.LatestRelease(context.Background(), 21, "everything", testPlatform()); !errors.Is(err, ErrUnknownRelease) {
		t.Errorf("bad image type: got %v", err)
	}
}

func TestLatestReleaseRejectsNonHTTPSDownload(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(releasePayload("file:///etc/passwd", "abc", 1)))
	}))
	defer upstream.Close()

	_, err := NewClient(upstream.URL, "test").
		LatestRelease(context.Background(), 21, ImageJRE, testPlatform())
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("got %v, want ErrUpstream", err)
	}
}

func TestLatestReleaseRejectsUnknownArchiveFormat(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"binary":{"package":{"name":"jre.msi","link":"https://cdn.example/jre.msi","size":1,
		  "checksum":"ab"}},"release_name":"jdk-21","version":{"major":21,"openjdk_version":"21"}}]`))
	}))
	defer upstream.Close()

	_, err := NewClient(upstream.URL, "test").
		LatestRelease(context.Background(), 21, ImageJRE, testPlatform())
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("got %v, want ErrUpstream", err)
	}
}

func TestLatestReleaseWithNoBuilds(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	}))
	defer upstream.Close()

	_, err := NewClient(upstream.URL, "test").
		LatestRelease(context.Background(), 26, ImageJRE, Platform{OS: "linux", Arch: "s390x"})
	if !errors.Is(err, ErrUnknownRelease) {
		t.Fatalf("got %v, want ErrUnknownRelease", err)
	}
}

func TestMajorOf(t *testing.T) {
	cases := map[string]int{
		"21.0.12":     21,
		"1.8.0_502":   8,
		"17":          17,
		"25.0.4+7":    25,
		"":            0,
		"not-a-thing": 0,
	}
	for version, want := range cases {
		if got := majorOf(version); got != want {
			t.Errorf("majorOf(%q) = %d, want %d", version, got, want)
		}
	}
}
