package unpack

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"
)

// tarEntry is one member of a test archive.
type tarEntry struct {
	name     string
	body     string
	mode     int64
	typeflag byte
	link     string
}

func buildTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	writer := tar.NewWriter(gz)
	for _, entry := range entries {
		flag := entry.typeflag
		if flag == 0 {
			flag = tar.TypeReg
		}
		mode := entry.mode
		if mode == 0 {
			mode = 0o644
		}
		header := &tar.Header{
			Name: entry.name, Mode: mode, Typeflag: flag,
			Linkname: entry.link, Size: int64(len(entry.body)),
		}
		if flag != tar.TypeReg {
			header.Size = 0
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatalf("write header %s: %v", entry.name, err)
		}
		if flag == tar.TypeReg {
			if _, err := writer.Write([]byte(entry.body)); err != nil {
				t.Fatalf("write body %s: %v", entry.name, err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

// jdkEntries is the shape of a real Temurin tarball: one wrapper directory,
// bin/java with the execute bit, a release file, and relative symlinks under
// legal/ (there are 145 of them in a genuine JRE).
func jdkEntries() []tarEntry {
	return []tarEntry{
		{name: "jdk-21.0.1+12-jre/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "jdk-21.0.1+12-jre/release", body: "JAVA_VERSION=\"21.0.1\"\nIMPLEMENTOR=\"Eclipse Adoptium\"\nIMAGE_TYPE=\"JRE\"\n"},
		{name: "jdk-21.0.1+12-jre/bin/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "jdk-21.0.1+12-jre/bin/java", body: "#!/bin/sh\necho java\n", mode: 0o755},
		{name: "jdk-21.0.1+12-jre/legal/java.base/LICENSE", body: "GPLv2+CE\n"},
		{name: "jdk-21.0.1+12-jre/legal/jdk.zipfs/LICENSE", typeflag: tar.TypeSymlink, link: "../java.base/LICENSE"},
	}
}

func extractToTemp(t *testing.T, name string, data []byte) (string, error) {
	t.Helper()

	archivePath := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(archivePath, data, 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer file.Close()

	dest := t.TempDir()
	root, err := os.OpenRoot(dest)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	defer root.Close()

	return dest, Extract(context.Background(), name, file, root, Limits{})
}

func TestExtractTarGzKeepsModesAndLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink extraction needs privileges on Windows")
	}
	dest, err := extractToTemp(t, "jre.tar.gz", buildTarGz(t, jdkEntries()))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	java := filepath.Join(dest, "jdk-21.0.1+12-jre", "bin", "java")
	info, err := os.Stat(java)
	if err != nil {
		t.Fatalf("stat bin/java: %v", err)
	}
	// A runtime whose launcher is not executable looks installed and cannot
	// start, which is the worst of both.
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("bin/java is not executable: %v", info.Mode())
	}

	link := filepath.Join(dest, "jdk-21.0.1+12-jre", "legal", "jdk.zipfs", "LICENSE")
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("symlink was not created: %v", err)
	}
	body, err := os.ReadFile(link)
	if err != nil || !strings.Contains(string(body), "GPLv2") {
		t.Errorf("symlink does not resolve to the licence: %v %q", err, body)
	}
}

// Path traversal is the classic archive bug: an entry named ../../x written
// relative to the extraction directory lands wherever it likes.
func TestExtractRejectsEscapingEntries(t *testing.T) {
	cases := []struct {
		name    string
		entries []tarEntry
	}{
		{"parent traversal", []tarEntry{{name: "../escaped.txt", body: "x"}}},
		{"nested traversal", []tarEntry{{name: "jre/../../escaped.txt", body: "x"}}},
		{"absolute path", []tarEntry{{name: "/etc/cron.d/evil", body: "x"}}},
		{"windows absolute", []tarEntry{{name: `C:\windows\system32\evil`, body: "x"}}},
		{"escaping symlink", []tarEntry{
			{name: "jre/link", typeflag: tar.TypeSymlink, link: "../../../../etc/passwd"},
		}},
		{"absolute symlink", []tarEntry{
			{name: "jre/link", typeflag: tar.TypeSymlink, link: "/etc/passwd"},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dest, err := extractToTemp(t, "evil.tar.gz", buildTarGz(t, tc.entries))
			if err == nil {
				t.Fatalf("extraction was allowed")
			}
			if !errors.Is(err, ErrBadArchive) && !errors.Is(err, fs.ErrInvalid) && !os.IsNotExist(err) {
				t.Errorf("unexpected error type: %v", err)
			}
			if _, statErr := os.Stat(filepath.Join(filepath.Dir(dest), "escaped.txt")); statErr == nil {
				t.Errorf("a file escaped the extraction directory")
			}
		})
	}
}

func TestExtractZip(t *testing.T) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, entry := range []struct {
		name string
		body string
		mode fs.FileMode
	}{
		{"jdk-21/bin/java.exe", "MZ", 0o755},
		{"jdk-21/release", "JAVA_VERSION=\"21.0.1\"\n", 0o644},
	} {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		part, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := part.Write([]byte(entry.body)); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	dest, err := extractToTemp(t, "jre.zip", buf.Bytes())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "jdk-21", "bin", "java.exe")); err != nil {
		t.Errorf("java.exe missing: %v", err)
	}
}

func TestZipRejectsEscapingEntry(t *testing.T) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	part, err := writer.Create("../escaped.txt")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := part.Write([]byte("x")); err != nil {
		t.Fatalf("write: %v", err)
	}
	writer.Close()

	if _, err := extractToTemp(t, "evil.zip", buf.Bytes()); !errors.Is(err, ErrBadArchive) {
		t.Fatalf("got %v, want ErrBadArchive", err)
	}
}

func TestFlattenLiftsTheWrapperDirectory(t *testing.T) {
	dest, err := extractToTemp(t, "jre.tar.gz", buildTarGzWithoutSymlinks(t))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	defer root.Close()

	if err := Flatten(root); err != nil {
		t.Fatalf("flatten: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "bin", "java")); err != nil {
		t.Errorf("bin/java should now be at the top level: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "jdk-21.0.1+12-jre")); err == nil {
		t.Errorf("the wrapper directory should be gone")
	}
}

func buildTarGzWithoutSymlinks(t *testing.T) []byte {
	t.Helper()
	entries := jdkEntries()
	return buildTarGz(t, entries[:len(entries)-1])
}

// MySQL publishes its Linux server tarballs as .tar.xz and in no other format,
// so xz is not an optional convenience: without it the engine cannot be
// installed on the platform most panels run on.
func TestExtractTarXZ(t *testing.T) {
	gzipped := buildTarGz(t, jdkEntries()[:len(jdkEntries())-1])
	gz, err := gzip.NewReader(bytes.NewReader(gzipped))
	if err != nil {
		t.Fatalf("re-read fixture: %v", err)
	}
	plain, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var buf bytes.Buffer
	writer, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatalf("new xz writer: %v", err)
	}
	if _, err := writer.Write(plain); err != nil {
		t.Fatalf("write xz: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close xz: %v", err)
	}

	dest, err := extractToTemp(t, "mysql-8.0.45-linux.tar.xz", buf.Bytes())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "jdk-21.0.1+12-jre", "bin", "java")); err != nil {
		t.Errorf("xz payload was not extracted: %v", err)
	}
}

// The limits are a parameter because a MySQL tarball is an order of magnitude
// larger than a JDK; a caller that sets one has to actually get it enforced.
func TestExtractHonoursLimits(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "big.tar.gz")
	data := buildTarGz(t, []tarEntry{{name: "jre/big", body: strings.Repeat("x", 4096)}})
	if err := os.WriteFile(archivePath, data, 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer file.Close()

	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	defer root.Close()

	err = Extract(context.Background(), "big.tar.gz", file, root, Limits{MaxBytes: 1024})
	if !errors.Is(err, ErrBadArchive) {
		t.Fatalf("got %v, want ErrBadArchive", err)
	}
}

func TestSupported(t *testing.T) {
	for _, name := range []string{
		"OpenJDK21U-jre_x64_linux_hotspot.tar.gz",
		"mongodb-linux-x86_64-ubuntu2204-8.0.28.tgz",
		"mysql-8.0.45-linux-glibc2.17-x86_64-minimal.tar.xz",
		"embedded-postgres-binaries-linux-amd64-17.6.0.jar",
		"mysql-8.0.45-winx64.zip",
	} {
		if !Supported(name) {
			t.Errorf("%s should be supported", name)
		}
	}
	for _, name := range []string{"mysql.deb", "postgres.rpm", "server.tar.bz2", ""} {
		if Supported(name) {
			t.Errorf("%s should not be supported", name)
		}
	}
}
