package javaruntime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// azulPayload is one package as the metadata API returns it, with the fields
// include_fields adds. java_version is an array rather than a string — that is
// the main shape difference from Adoptium.
func azulPayload(link, checksum string, size int64) string {
	return fmt.Sprintf(`[{
	  "package_uuid":"f89c621b-7e5d-4147-b7f7-8dc832ad2413",
	  "name":"zulu21.52.203-ca-jre21.0.12.1-linux_x64.tar.gz",
	  "java_version":[21,0,12,1],
	  "distro_version":[21,52,203,0],
	  "download_url":%q,
	  "sha256_hash":%q,
	  "size":%d,
	  "java_package_type":"jre",
	  "os":"linux",
	  "arch":"x86",
	  "lib_c_type":"glibc",
	  "availability_type":"CA",
	  "latest":true
	}]`, link, checksum, size)
}

func testAzul(t *testing.T, handler http.HandlerFunc) *azulProvider {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	return newAzulProvider(upstream.URL, newHTTPClient("test", true))
}

func TestAzulLatestReleaseParsesAPackage(t *testing.T) {
	azul := testAzul(t, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		// These two keep CRaC builds and JavaFX-bundled builds out. Without
		// them the API really does hand back a crac jre alongside the plain
		// one, and whichever sorted first is what would get installed.
		if got := query.Get("crac_supported"); got != "false" {
			t.Errorf("crac_supported = %q, want false", got)
		}
		if got := query.Get("javafx_bundled"); got != "false" {
			t.Errorf("javafx_bundled = %q, want false", got)
		}
		if got := query.Get("release_status"); got != "ga" {
			t.Errorf("release_status = %q, want ga", got)
		}
		if got := query.Get("java_package_type"); got != "jre" {
			t.Errorf("java_package_type = %q", got)
		}
		if got := query.Get("java_version"); got != "21" {
			t.Errorf("java_version = %q", got)
		}
		if got := query.Get("archive_type"); got != "tar.gz" {
			t.Errorf("archive_type = %q, want tar.gz", got)
		}
		if got := query.Get("lib_c_type"); got != "glibc" {
			t.Errorf("lib_c_type = %q", got)
		}
		fmt.Fprint(w, azulPayload("https://cdn.example/zulu.tar.gz", "AABBCC", 4096))
	})

	release, err := azul.LatestRelease(context.Background(), 21, ImageJRE,
		Platform{OS: "linux", Arch: "x64", LibC: "glibc"})
	if err != nil {
		t.Fatalf("LatestRelease: %v", err)
	}
	if release.Distribution != DistZulu {
		t.Errorf("distribution = %q", release.Distribution)
	}
	if release.Version != "21.0.12.1" {
		t.Errorf("version = %q, want 21.0.12.1", release.Version)
	}
	if release.Major != 21 || release.Size != 4096 {
		t.Errorf("unexpected release: %+v", release)
	}
	if release.SHA256 != "aabbcc" {
		t.Errorf("checksum should be lowercased, got %q", release.SHA256)
	}
	if release.FileName != "zulu21.52.203-ca-jre21.0.12.1-linux_x64.tar.gz" {
		t.Errorf("file name = %q", release.FileName)
	}
	// The response says "x86" for a 64-bit build (and "arm" for aarch64).
	// Trusting it would put the wrong arch on the runtime and on every error
	// message about it.
	if release.Arch != "x64" {
		t.Errorf("arch = %q, want the requested x64 rather than the response's", release.Arch)
	}
}

// A musl machine needs the musl build, which is the whole reason Zulu is worth
// having on Alpine.
func TestAzulAsksForMuslOnAMuslMachine(t *testing.T) {
	azul := testAzul(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("lib_c_type"); got != "musl" {
			t.Errorf("lib_c_type = %q, want musl", got)
		}
		fmt.Fprint(w, azulPayload("https://cdn.example/zulu-musl.tar.gz", "AABBCC", 4096))
	})

	if _, err := azul.LatestRelease(context.Background(), 21, ImageJRE,
		Platform{OS: "linux", Arch: "x64", LibC: "musl"}); err != nil {
		t.Fatalf("LatestRelease: %v", err)
	}
}

// Windows ships as a zip; asking for tar.gz there is an empty result rather
// than an error, which would read as "Zulu has no Java 21 for Windows".
func TestAzulAsksForZipOnWindows(t *testing.T) {
	azul := testAzul(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("archive_type"); got != "zip" {
			t.Errorf("archive_type = %q, want zip", got)
		}
		fmt.Fprint(w, `[{"name":"zulu21.52.203-ca-jre21.0.12.1-win_x64.zip",
		  "java_version":[21,0,12,1],"download_url":"https://cdn.example/zulu.zip",
		  "sha256_hash":"aabbcc","size":4096}]`)
	})

	release, err := azul.LatestRelease(context.Background(), 21, ImageJRE,
		Platform{OS: "windows", Arch: "x64"})
	if err != nil {
		t.Fatalf("LatestRelease: %v", err)
	}
	if release.FileName != "zulu21.52.203-ca-jre21.0.12.1-win_x64.zip" {
		t.Errorf("file name = %q", release.FileName)
	}
}

func TestAzulMajorsFoldsBuildsAndCaches(t *testing.T) {
	var hits int
	azul := testAzul(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		// The listing is per package, so one feature release shows up many
		// times over — once per build and platform.
		fmt.Fprint(w, `[{"java_version":[25,0,1],"support_term":"lts"},
		  {"java_version":[24,0,2],"support_term":"mts"},
		  {"java_version":[21,0,12,1],"support_term":"lts"},
		  {"java_version":[21,0,11],"support_term":"lts"}]`)
	})

	majors, err := azul.Majors(context.Background())
	if err != nil {
		t.Fatalf("Majors: %v", err)
	}
	if len(majors) != 3 || majors[0].Major != 25 || majors[2].Major != 21 {
		t.Fatalf("expected 25/24/21 newest first, got %+v", majors)
	}
	if !majors[0].LTS || majors[1].LTS {
		t.Errorf("LTS flags wrong: %+v", majors)
	}

	if _, err := azul.Majors(context.Background()); err != nil {
		t.Fatalf("second Majors: %v", err)
	}
	if hits != 1 {
		t.Errorf("upstream hit %d times, want 1 (cached)", hits)
	}
}

func TestAzulReportsNothingMatching(t *testing.T) {
	azul := testAzul(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[]`)
	})

	_, err := azul.LatestRelease(context.Background(), 99, ImageJRE,
		Platform{OS: "linux", Arch: "x64", LibC: "glibc"})
	if err == nil {
		t.Fatal("expected an error for a version Azul does not build")
	}
}

// A package whose download link is not HTTPS is refused before anything is
// fetched, the same as on the Adoptium side.
func TestAzulRejectsNonHTTPSDownload(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, azulPayload("file:///etc/passwd", "aabbcc", 1))
	}))
	defer upstream.Close()

	azul := newAzulProvider(upstream.URL, newHTTPClient("test", false))
	if _, err := azul.LatestRelease(context.Background(), 21, ImageJRE,
		Platform{OS: "linux", Arch: "x64", LibC: "glibc"}); err == nil {
		t.Error("expected a file:// download link to be refused")
	}
}
