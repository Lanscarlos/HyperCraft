package javaruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

// The extraction itself is tested in internal/unpack, which owns it. What is
// left here is the fixture the installer tests serve over a fake Adoptium: a
// tarball shaped like a real Temurin build, so the install path — resolve,
// download, check, unpack, flatten, rename into place — is exercised end to end.

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
