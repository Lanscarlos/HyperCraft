package dbruntime

import (
	"archive/tar"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ulikunitz/xz"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeTarball builds something shaped like a MySQL minimal tarball: one wrapper
// directory with bin/mysqld inside it, compressed the way Oracle compresses it.
func fakeTarball(t *testing.T, wrapper, binary, body string) []byte {
	t.Helper()

	var plain bytes.Buffer
	writer := tar.NewWriter(&plain)
	entries := []struct {
		name string
		mode int64
		data string
	}{
		{wrapper + "/bin/" + binary, 0o755, body},
		{wrapper + "/share/charsets/Index.xml", 0o644, "<charsets/>"},
	}
	for _, entry := range entries {
		header := &tar.Header{
			Name: entry.name, Mode: entry.mode,
			Typeflag: tar.TypeReg, Size: int64(len(entry.data)),
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatalf("write header: %v", err)
		}
		if _, err := writer.Write([]byte(entry.data)); err != nil {
			t.Fatalf("write body: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}

	var compressed bytes.Buffer
	xzWriter, err := xz.NewWriter(&compressed)
	if err != nil {
		t.Fatalf("new xz writer: %v", err)
	}
	if _, err := xzWriter.Write(plain.Bytes()); err != nil {
		t.Fatalf("write xz: %v", err)
	}
	if err := xzWriter.Close(); err != nil {
		t.Fatalf("close xz: %v", err)
	}
	return compressed.Bytes()
}

// fakeMySQLCDN serves the two files Oracle's CDN serves: the tarball and its
// MD5 sidecar.
func fakeMySQLCDN(t *testing.T, archive []byte, corruptChecksum bool) *httptest.Server {
	t.Helper()

	sum := md5.Sum(archive) //nolint:gosec // matching what upstream publishes
	digest := hex.EncodeToString(sum[:])
	if corruptChecksum {
		digest = strings.Repeat("0", len(digest))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".md5"):
			fmt.Fprintf(w, "%s  %s\n", digest, filepath.Base(strings.TrimSuffix(r.URL.Path, ".md5")))
		case strings.Contains(r.URL.Path, "mysql-8.0.45"):
			w.Header().Set("Content-Length", fmt.Sprint(len(archive)))
			if r.Method == http.MethodHead {
				return
			}
			_, _ = w.Write(archive)
		default:
			// The CDN answers 403, not 404, for a version it never published.
			w.WriteHeader(http.StatusForbidden)
		}
	})
	return httptest.NewServer(mux)
}

func newTestInstaller(t *testing.T, base string) (*Installer, *Store) {
	t.Helper()

	client := NewClient("HyperCraft-test")
	client.AllowInsecure()
	store := NewStore(filepath.Join(t.TempDir(), "engines"))
	return NewInstaller(client, store, testLogger()), store
}

// The whole install path, end to end: resolve over HTTP, verify the checksum,
// unpack xz, drop the wrapper directory, and land somewhere the store lists.
func TestInstallMySQL(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture is a Unix tarball")
	}
	archive := fakeTarball(t, "mysql-8.0.45-linux-glibc2.17-x86_64-minimal", "mysqld",
		"#!/bin/sh\necho 'mysqld  Ver 8.0.45'\n")
	server := fakeMySQLCDN(t, archive, false)
	defer server.Close()

	previous := mysqlBase
	mysqlBase = server.URL
	defer func() { mysqlBase = previous }()

	installer, store := newTestInstaller(t, server.URL)
	if _, err := installer.Start(EngineMySQL, "8.0.45"); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForJob(t, installer)

	job, _ := installer.Status()
	if job.State != JobDone {
		t.Fatalf("install did not finish: %s %s", job.State, job.Error)
	}

	installs, err := store.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(installs) != 1 {
		t.Fatalf("got %d installs, want 1", len(installs))
	}
	install := installs[0]
	if install.ID != "mysql-8.0.45" || install.Engine != EngineMySQL || install.Version != "8.0.45" {
		t.Errorf("unexpected install: %+v", install)
	}
	// The wrapper directory has to be gone, or nothing downstream finds the
	// server binary at the path it expects.
	if want := filepath.Join(install.Path, "bin", "mysqld"); install.ServerPath != want {
		t.Errorf("server path is %q, want %q", install.ServerPath, want)
	}
	if _, err := os.Stat(filepath.Join(install.Path, "share", "charsets", "Index.xml")); err != nil {
		t.Errorf("the rest of the tarball did not survive: %v", err)
	}
	// The fixture is a runnable shell script, so the health probe should be
	// happy with it — that is the check that catches a missing libaio.
	if install.Problem != "" {
		t.Errorf("a runnable binary was reported as broken: %s / %s", install.Problem, install.Hint)
	}
}

// A file that does not match what upstream published must not be unpacked at
// all, let alone left half-installed.
func TestInstallRefusesABadChecksum(t *testing.T) {
	archive := fakeTarball(t, "mysql-8.0.45-linux-glibc2.17-x86_64-minimal", "mysqld", "x")
	server := fakeMySQLCDN(t, archive, true)
	defer server.Close()

	previous := mysqlBase
	mysqlBase = server.URL
	defer func() { mysqlBase = previous }()

	installer, store := newTestInstaller(t, server.URL)
	if _, err := installer.Start(EngineMySQL, "8.0.45"); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForJob(t, installer)

	job, _ := installer.Status()
	if job.State != JobFailed || !strings.Contains(job.Error, "校验不上") {
		t.Fatalf("a corrupt download was accepted: %s %s", job.State, job.Error)
	}
	if installs, _ := store.List(); len(installs) != 0 {
		t.Errorf("a rejected download still landed on disk: %+v", installs)
	}
	// Nothing may be left behind either — not the staging directory, not the
	// partial download.
	entries, _ := os.ReadDir(store.Root())
	for _, entry := range entries {
		t.Errorf("leftover in the engines directory: %s", entry.Name())
	}
}

func TestInstallRejectsAnUnknownVersion(t *testing.T) {
	server := fakeMySQLCDN(t, []byte("x"), false)
	defer server.Close()

	previous := mysqlBase
	mysqlBase = server.URL
	defer func() { mysqlBase = previous }()

	installer, _ := newTestInstaller(t, server.URL)
	if _, err := installer.Start(EngineMySQL, "1.2.3"); err == nil {
		t.Fatal("an unpublished version was accepted")
	}
}

func TestInstallRejectsAnUnknownEngine(t *testing.T) {
	installer, _ := newTestInstaller(t, "")
	if _, err := installer.Start("mariadb", "11.4.0"); err == nil {
		t.Fatal("an unsupported engine was accepted")
	}
}

// MongoDB's manifest is the one metadata source that names files and checksums
// directly, so the resolver reading it is worth pinning down.
func TestMongoResolveReadsTheManifest(t *testing.T) {
	payload := mongoPayload{Versions: []mongoVersion{{
		Version:    "8.0.28",
		LTS:        true,
		Production: true,
		Downloads: []mongoAsset{
			mongoAssetWith("enterprise", "ubuntu2204", "x86_64",
				"https://example.invalid/enterprise.tgz", "aa"),
			mongoAssetWith("targeted", "ubuntu2204", "x86_64",
				"https://example.invalid/mongodb-linux-x86_64-ubuntu2204-8.0.28.tgz", "BB"),
		},
	}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()

	previous := mongoManifest
	mongoManifest = server.URL
	defer func() { mongoManifest = previous }()

	client := NewClient("HyperCraft-test")
	client.AllowInsecure()
	platform := Platform{OS: "linux", Arch: "amd64", Distro: "ubuntu", DistroVersion: "22.04"}

	versions, err := client.Versions(t.Context(), EngineMongoDB, platform)
	if err != nil {
		t.Fatalf("versions: %v", err)
	}
	if len(versions) != 1 || versions[0].Version != "8.0.28" || !versions[0].LTS {
		t.Fatalf("unexpected versions: %+v", versions)
	}

	release, err := client.Resolve(t.Context(), EngineMongoDB, "8.0.28", platform)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if release.FileName != "mongodb-linux-x86_64-ubuntu2204-8.0.28.tgz" {
		t.Errorf("unexpected file name %q", release.FileName)
	}
	if release.Checksum != "bb" || release.Algo != "sha256" {
		t.Errorf("checksum was not carried through: %q/%q", release.Checksum, release.Algo)
	}

	// A version the manifest does not carry has to fail here rather than after
	// a few hundred megabytes.
	if _, err := client.Resolve(t.Context(), EngineMongoDB, "6.0.1", platform); err == nil {
		t.Error("an unlisted version resolved")
	}
}

func mongoAssetWith(edition, target, arch, url, sha string) mongoAsset {
	asset := mongoAsset{Edition: edition, Target: target, Arch: arch}
	asset.Archive.URL = url
	asset.Archive.SHA256 = sha
	return asset
}

// Downloads are HTTPS or nothing: for MySQL and PostgreSQL, whose checksums are
// too weak to catch a substitution, TLS to the publisher is the whole guarantee.
func TestPlainHTTPIsRefused(t *testing.T) {
	client := NewClient("HyperCraft-test")
	if err := client.checkURL("http://example.invalid/mysql.tar.xz"); err == nil {
		t.Error("a plain HTTP download was allowed")
	}
	if err := client.checkURL("file:///etc/passwd"); err == nil {
		t.Error("a file:// download was allowed")
	}
	if err := client.checkURL("https://example.invalid/mysql.tar.xz"); err != nil {
		t.Errorf("HTTPS should be allowed: %v", err)
	}
}

func waitForJob(t *testing.T, installer *Installer) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		job, ok := installer.Status()
		if ok && job.State != JobDownloading && job.State != JobExtracting {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the install never finished")
}
