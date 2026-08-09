package javaruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
)

// fakeAdoptium serves the API plus the archive it advertises.
type fakeAdoptium struct {
	*httptest.Server
	archive []byte
	// gate, when non-nil, holds the archive back until it is closed.
	gate chan struct{}
	// corrupt makes the CDN hand back something else.
	corrupt bool
}

func newFakeAdoptium(t *testing.T, archive []byte) *fakeAdoptium {
	t.Helper()
	fake := &fakeAdoptium{archive: archive}

	mux := http.NewServeMux()
	mux.HandleFunc("/info/available_releases", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(majorsPayload))
	})
	mux.HandleFunc("/assets/latest/{major}/hotspot", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("major") == "99" {
			w.Write([]byte(`[]`))
			return
		}
		sum := sha256.Sum256(fake.archive)
		w.Write([]byte(releasePayload(fake.URL+"/jre.tar.gz", hex.EncodeToString(sum[:]), int64(len(fake.archive)))))
	})
	mux.HandleFunc("/jre.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		if fake.gate != nil {
			select {
			case <-fake.gate:
			case <-r.Context().Done():
				return
			}
		}
		if fake.corrupt {
			w.Write(make([]byte, len(fake.archive)))
			return
		}
		w.Write(fake.archive)
	})

	fake.Server = httptest.NewServer(mux)
	t.Cleanup(fake.Close)
	return fake
}

func newTestInstaller(t *testing.T, fake *fakeAdoptium) (*Installer, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "java")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewInstaller(NewClient(fake.URL, "test"), NewStore(root), logger), root
}

func awaitInstall(t *testing.T, installer *Installer) Job {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		job, ok := installer.Status()
		if ok && job.State != JobDownloading && job.State != JobExtracting {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("install did not finish in time")
	return Job{}
}

func TestInstallUnpacksAndRegistersRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture archive contains symlinks")
	}
	fake := newFakeAdoptium(t, buildTarGz(t, jdkEntries()))
	installer, root := newTestInstaller(t, fake)

	job, err := installer.Start(21, ImageJRE, SourceOfficial)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if job.Version != "21.0.1+12" || job.RuntimeID != "temurin-21.0.1-12-jre" {
		t.Fatalf("unexpected job: %+v", job)
	}

	done := awaitInstall(t, installer)
	if done.State != JobDone {
		t.Fatalf("install failed: %s / %s", done.State, done.Error)
	}

	// The wrapper directory is dropped, so the path is predictable.
	javaPath := filepath.Join(root, "temurin-21.0.1-12-jre", "bin", "java")
	if _, err := os.Stat(javaPath); err != nil {
		t.Fatalf("bin/java is not where it should be: %v", err)
	}

	runtimes, err := NewStore(root).List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runtimes) != 1 {
		t.Fatalf("expected one runtime, got %+v", runtimes)
	}
	got := runtimes[0]
	if got.Major != 21 || got.Version != "21.0.1" || got.ImageType != "jre" {
		t.Errorf("release file was not read: %+v", got)
	}
	if got.Vendor != "Eclipse Adoptium" || got.JavaPath != javaPath {
		t.Errorf("unexpected runtime: %+v", got)
	}
	if got.Size == 0 {
		t.Errorf("size should be measured")
	}

	// Nothing temporary should survive a successful install.
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if entry.Name() != "temurin-21.0.1-12-jre" {
			t.Errorf("leftover in the runtimes directory: %s", entry.Name())
		}
	}
}

// TestExtractIntoClosesStagingHandle is the regression test for an install that
// failed on Windows with "The process cannot access the file because it is
// being used by another process": the os.Root handle on the staging directory
// stayed open across the rename into place, and Windows will not move a
// directory anything still holds.
//
// Linux renames it regardless, so the failure itself cannot be reproduced here
// — what is pinned instead is the invariant it came from, that extraction hands
// staging back with none of our handles left on it. The rename is the caller's
// next statement, so nothing may outlive this function.
func TestExtractIntoClosesStagingHandle(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("open handles are only enumerable through /proc/self/fd")
	}
	staging := filepath.Join(t.TempDir(), ".installing-temurin-21.0.1-12-jre")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("create staging: %v", err)
	}
	release := Release{Version: "21.0.1+12", ImageType: ImageJRE, FileName: "jre.tar.gz"}

	err := extractInto(context.Background(), staging, release, openArchive(t, buildTarGz(t, jdkEntriesForThisOS())))
	if err != nil {
		t.Fatalf("extractInto: %v", err)
	}
	if held := handlesUnder(t, staging); len(held) != 0 {
		t.Errorf("staging is still held open by %v, which is a sharing violation on Windows", held)
	}
}

// TestUnpackMovesRuntimeIntoPlace checks the other half: after extraction the
// staged tree is renamed to its final ID, wrapper directory dropped.
func TestUnpackMovesRuntimeIntoPlace(t *testing.T) {
	root := t.TempDir()
	staging := filepath.Join(root, ".installing-temurin-21.0.1-12-jre")
	release := Release{Version: "21.0.1+12", ImageType: ImageJRE, FileName: "jre.tar.gz"}

	installer := NewInstaller(nil, NewStore(root), slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := installer.unpack(context.Background(), staging, release, openArchive(t, buildTarGz(t, jdkEntriesForThisOS())))
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "temurin-21.0.1-12-jre", "bin", javaBinary())); err != nil {
		t.Errorf("the staged runtime was not moved into place: %v", err)
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Errorf("the staging directory outlived the install")
	}
}

// TestInstallDiscardsStaleStagingDirectory covers the retry after a failure
// like the one above: whatever the previous attempt left behind must not end up
// mixed into the runtime this one installs.
func TestInstallDiscardsStaleStagingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture archive contains symlinks")
	}
	fake := newFakeAdoptium(t, buildTarGz(t, jdkEntries()))
	installer, root := newTestInstaller(t, fake)

	stale := filepath.Join(root, ".installing-temurin-21.0.1-12-jre")
	if err := os.MkdirAll(filepath.Join(stale, "bin"), 0o755); err != nil {
		t.Fatalf("stage leftovers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stale, "bin", "leftover"), []byte("junk"), 0o644); err != nil {
		t.Fatalf("stage leftovers: %v", err)
	}

	if _, err := installer.Start(21, ImageJRE, SourceOfficial); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if done := awaitInstall(t, installer); done.State != JobDone {
		t.Fatalf("install failed: %s / %s", done.State, done.Error)
	}
	if _, err := os.Stat(filepath.Join(root, "temurin-21.0.1-12-jre", "bin", "leftover")); err == nil {
		t.Errorf("the previous attempt's files were installed as part of the runtime")
	}
}

// handlesUnder lists the paths inside dir this process still has open.
func handlesUnder(t *testing.T, dir string) []string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve %s: %v", dir, err)
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("no /proc/self/fd: %v", err)
	}

	var held []string
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err != nil {
			continue // Closed while we were reading the directory.
		}
		if target == resolved || strings.HasPrefix(target, resolved+string(os.PathSeparator)) {
			held = append(held, target)
		}
	}
	return held
}

// jdkEntriesForThisOS is the fixture archive with the launcher name this
// platform looks for, and without the symlink Windows cannot create unprivileged.
func jdkEntriesForThisOS() []tarEntry {
	entries := jdkEntries()
	entries = entries[:len(entries)-1]
	for i := range entries {
		if entries[i].name == "jdk-21.0.1+12-jre/bin/java" {
			entries[i].name = "jdk-21.0.1+12-jre/bin/" + javaBinary()
		}
	}
	return entries
}

// openArchive writes archive bytes to disk and hands back an open file, the
// form unpack takes its input in.
func openArchive(t *testing.T, data []byte) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "jre.tar.gz")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	t.Cleanup(func() { file.Close() })
	return file
}

func TestInstallRejectsCorruptArchive(t *testing.T) {
	fake := newFakeAdoptium(t, buildTarGz(t, jdkEntries()))
	fake.corrupt = true
	installer, root := newTestInstaller(t, fake)

	if _, err := installer.Start(21, ImageJRE, SourceOfficial); err != nil {
		t.Fatalf("Start: %v", err)
	}
	done := awaitInstall(t, installer)
	if done.State != JobFailed {
		t.Fatalf("expected a failure, got %s", done.State)
	}
	if !strings.Contains(done.Error, "checksum") {
		t.Errorf("expected a checksum error, got %q", done.Error)
	}

	// A bad archive must leave nothing behind — least of all something that
	// looks like an installed runtime.
	if entries, err := os.ReadDir(root); err == nil && len(entries) != 0 {
		t.Errorf("leftovers after a failed install: %+v", entries)
	}
}

func TestInstallRefusesDuplicateAndConcurrent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture archive contains symlinks")
	}
	fake := newFakeAdoptium(t, buildTarGz(t, jdkEntries()))
	fake.gate = make(chan struct{})
	installer, _ := newTestInstaller(t, fake)

	if _, err := installer.Start(21, ImageJRE, SourceOfficial); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if _, err := installer.Start(17, ImageJRE, SourceOfficial); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Start: got %v, want ErrBusy", err)
	}

	close(fake.gate)
	if done := awaitInstall(t, installer); done.State != JobDone {
		t.Fatalf("install failed: %s / %s", done.State, done.Error)
	}

	// Same version again: already on disk, so there is nothing to do.
	if _, err := installer.Start(21, ImageJRE, SourceOfficial); !errors.Is(err, ErrExists) {
		t.Fatalf("repeat install: got %v, want ErrExists", err)
	}
}

func TestCancelInstall(t *testing.T) {
	fake := newFakeAdoptium(t, buildTarGz(t, jdkEntries()))
	fake.gate = make(chan struct{})
	defer close(fake.gate)

	installer, root := newTestInstaller(t, fake)
	if _, err := installer.Start(21, ImageJRE, SourceOfficial); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := installer.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	done := awaitInstall(t, installer)
	if done.State != JobCancelled {
		t.Fatalf("state %s, error %q", done.State, done.Error)
	}
	if entries, err := os.ReadDir(root); err == nil && len(entries) != 0 {
		t.Errorf("cancelled install left files behind: %+v", entries)
	}
	if err := installer.Cancel(); err == nil {
		t.Errorf("cancelling a finished install should fail")
	}
}

func TestInstallUnknownMajor(t *testing.T) {
	fake := newFakeAdoptium(t, buildTarGz(t, jdkEntries()))
	installer, _ := newTestInstaller(t, fake)

	if _, err := installer.Start(99, ImageJRE, SourceOfficial); !errors.Is(err, ErrUnknownRelease) {
		t.Fatalf("got %v, want ErrUnknownRelease", err)
	}
	job, ok := installer.Status()
	if !ok || job.State != JobFailed {
		t.Errorf("the failed attempt should be visible as a job: %+v", job)
	}
}
