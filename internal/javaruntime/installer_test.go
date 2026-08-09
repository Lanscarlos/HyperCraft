package javaruntime

import (
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

	job, err := installer.Start(21, ImageJRE)
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

func TestInstallRejectsCorruptArchive(t *testing.T) {
	fake := newFakeAdoptium(t, buildTarGz(t, jdkEntries()))
	fake.corrupt = true
	installer, root := newTestInstaller(t, fake)

	if _, err := installer.Start(21, ImageJRE); err != nil {
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

	if _, err := installer.Start(21, ImageJRE); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if _, err := installer.Start(17, ImageJRE); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Start: got %v, want ErrBusy", err)
	}

	close(fake.gate)
	if done := awaitInstall(t, installer); done.State != JobDone {
		t.Fatalf("install failed: %s / %s", done.State, done.Error)
	}

	// Same version again: already on disk, so there is nothing to do.
	if _, err := installer.Start(21, ImageJRE); !errors.Is(err, ErrExists) {
		t.Fatalf("repeat install: got %v, want ErrExists", err)
	}
}

func TestCancelInstall(t *testing.T) {
	fake := newFakeAdoptium(t, buildTarGz(t, jdkEntries()))
	fake.gate = make(chan struct{})
	defer close(fake.gate)

	installer, root := newTestInstaller(t, fake)
	if _, err := installer.Start(21, ImageJRE); err != nil {
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

	if _, err := installer.Start(99, ImageJRE); !errors.Is(err, ErrUnknownRelease) {
		t.Fatalf("got %v, want ErrUnknownRelease", err)
	}
	job, ok := installer.Status()
	if !ok || job.State != JobFailed {
		t.Errorf("the failed attempt should be visible as a job: %+v", job)
	}
}
