package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
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
)

// fakeRelease serves a GitHub-shaped release whose archive really does contain
// the given binary bytes, so the whole download → checksum → unpack path runs
// against real archives rather than mocks.
type fakeRelease struct {
	t          *testing.T
	version    string
	binary     []byte
	corruptSum bool // publish a checksum that does not match the archive
	omitSums   bool
	server     *httptest.Server
}

func newFakeRelease(t *testing.T, version string, binary []byte) *fakeRelease {
	t.Helper()
	f := &fakeRelease{t: t, version: version, binary: binary}

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		assets := []map[string]string{
			{"name": AssetName(f.version), "browser_download_url": f.server.URL + "/dl/archive"},
		}
		if !f.omitSums {
			assets = append(assets, map[string]string{
				"name": "SHA256SUMS.txt", "browser_download_url": f.server.URL + "/dl/sums",
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name":     "v" + f.version,
			"name":         "v" + f.version,
			"body":         "release notes for " + f.version,
			"html_url":     "https://example.invalid/releases/v" + f.version,
			"published_at": "2026-08-08T00:00:00Z",
			"assets":       assets,
		})
	})
	mux.HandleFunc("/dl/archive", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(f.archive())
	})
	mux.HandleFunc("/dl/sums", func(w http.ResponseWriter, r *http.Request) {
		sum := sha256.Sum256(f.archive())
		hexSum := hex.EncodeToString(sum[:])
		if f.corruptSum {
			hexSum = strings.Repeat("0", 64)
		}
		fmt.Fprintf(w, "%s  %s\n", hexSum, AssetName(f.version))
	})

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

// archive builds the release archive in the same shape the release workflow
// produces: a top-level directory holding the binary and the docs.
func (f *fakeRelease) archive() []byte {
	dir := fmt.Sprintf("hypercraft-%s-%s-%s", f.version, runtime.GOOS, runtime.GOARCH)
	name := "hypercraft"
	if runtime.GOOS == "windows" {
		name = "hypercraft.exe"
	}

	var buf bytes.Buffer
	if runtime.GOOS == "windows" {
		zw := zip.NewWriter(&buf)
		for _, entry := range []struct{ path, body string }{
			{dir + "/README.md", "readme"},
			{dir + "/" + name, string(f.binary)},
		} {
			w, err := zw.Create(entry.path)
			if err != nil {
				f.t.Fatal(err)
			}
			if _, err := w.Write([]byte(entry.body)); err != nil {
				f.t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			f.t.Fatal(err)
		}
		return buf.Bytes()
	}

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, entry := range []struct {
		path string
		body []byte
	}{
		{dir + "/README.md", []byte("readme")},
		{dir + "/" + name, f.binary},
	} {
		hdr := &tar.Header{Name: entry.path, Mode: 0o755, Size: int64(len(entry.body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			f.t.Fatal(err)
		}
		if _, err := tw.Write(entry.body); err != nil {
			f.t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		f.t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		f.t.Fatal(err)
	}
	return buf.Bytes()
}

// updaterFor points an Updater at the fake release and at a throwaway
// "executable", so nothing in the test can touch the real test binary.
func (f *fakeRelease) updaterFor(t *testing.T, current string) (*Updater, string) {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "hypercraft")
	if err := os.WriteFile(exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	u := New("owner/repo", current)
	u.apiBase = f.server.URL
	u.exePath = exe
	return u, exe
}

func TestCheckReportsNewerRelease(t *testing.T) {
	f := newFakeRelease(t, "1.2.0", []byte("new binary"))
	u, _ := f.updaterFor(t, "v1.0.0")

	rel, err := u.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if rel.Version != "1.2.0" {
		t.Errorf("Version = %q, want 1.2.0", rel.Version)
	}
	if !u.IsNewerThanCurrent(rel) {
		t.Error("IsNewerThanCurrent = false, want true")
	}
	if !rel.HasAssetForPlatform() {
		t.Errorf("HasAssetForPlatform = false; release has no %s", AssetName(rel.Version))
	}
}

func TestCheckDoesNotOfferOlderOrEqualRelease(t *testing.T) {
	f := newFakeRelease(t, "1.2.0", []byte("new binary"))

	for _, current := range []string{"v1.2.0", "v1.3.0"} {
		u, _ := f.updaterFor(t, current)
		rel, err := u.Check(context.Background())
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if u.IsNewerThanCurrent(rel) {
			t.Errorf("running %s: offered %s as an update", current, rel.Version)
		}
	}
}

func TestCheckNeverOffersAnUpdateToADevBuild(t *testing.T) {
	f := newFakeRelease(t, "1.2.0", []byte("new binary"))
	u, _ := f.updaterFor(t, "dev")

	rel, err := u.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if u.IsNewerThanCurrent(rel) {
		t.Error("a dev build was offered an update; it would overwrite a local build")
	}
}

func TestPrepareAndCommitReplaceTheBinary(t *testing.T) {
	want := []byte("the new binary contents")
	f := newFakeRelease(t, "1.2.0", want)
	u, exe := f.updaterFor(t, "v1.0.0")

	rel, err := u.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	var lastDone int64
	staged, err := u.Prepare(context.Background(), rel, func(done, total int64) { lastDone = done })
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if lastDone == 0 {
		t.Error("progress callback was never called with any bytes")
	}

	// Nothing may change until Commit: a failed download must leave a running
	// panel completely untouched.
	if got, _ := os.ReadFile(exe); string(got) != "old binary" {
		t.Fatalf("Prepare modified the executable: %q", got)
	}
	if filepath.Dir(staged.Path()) != filepath.Dir(exe) {
		t.Errorf("staged at %s, want it beside %s so the rename is atomic", staged.Path(), exe)
	}

	if err := staged.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("binary after update = %q, want %q", got, want)
	}
	// The previous binary is kept for a manual rollback.
	old, err := os.ReadFile(exe + ".old")
	if err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if string(old) != "old binary" {
		t.Errorf("backup = %q, want the previous binary", old)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(exe)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("installed binary is not executable: %v", info.Mode())
		}
	}
}

func TestPrepareRejectsChecksumMismatch(t *testing.T) {
	f := newFakeRelease(t, "1.2.0", []byte("new binary"))
	f.corruptSum = true
	u, exe := f.updaterFor(t, "v1.0.0")

	rel, err := u.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if _, err := u.Prepare(context.Background(), rel, nil); err == nil {
		t.Fatal("Prepare accepted an archive whose checksum did not match")
	} else if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error = %v, want a checksum mismatch", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "old binary" {
		t.Errorf("a rejected download still modified the executable: %q", got)
	}
	// The staging file must not be left behind in the install directory.
	entries, err := os.ReadDir(filepath.Dir(exe))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".hypercraft-update-") {
			t.Errorf("staging file %s was left behind", e.Name())
		}
	}
}

func TestPrepareRefusesReleaseWithoutChecksums(t *testing.T) {
	f := newFakeRelease(t, "1.2.0", []byte("new binary"))
	f.omitSums = true
	u, _ := f.updaterFor(t, "v1.0.0")

	rel, err := u.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if _, err := u.Prepare(context.Background(), rel, nil); err == nil {
		t.Fatal("Prepare installed a binary from a release with no checksums")
	}
}

func TestPrepareReportsMissingPlatformBuild(t *testing.T) {
	f := newFakeRelease(t, "1.2.0", []byte("new binary"))
	u, _ := f.updaterFor(t, "v1.0.0")

	rel, err := u.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	// Drop this platform's archive, leaving the checksums in place.
	delete(rel.assets, AssetName(rel.Version))

	if rel.HasAssetForPlatform() {
		t.Fatal("HasAssetForPlatform = true after removing the asset")
	}
	if _, err := u.Prepare(context.Background(), rel, nil); err == nil {
		t.Fatal("Prepare succeeded without an archive for this platform")
	}
}

func TestServiceRestartsTheInstalledBinaryNotTheBackup(t *testing.T) {
	// Commit renames the running binary to <exe>.old, after which
	// os.Executable() reports that backup path — /proc/self/exe follows the
	// inode, not the name. A restart that re-derives the path from the OS
	// therefore execs the binary the update just replaced, and the panel comes
	// back on the old version having reported success.
	want := []byte("the new binary contents")
	f := newFakeRelease(t, "1.2.0", want)

	var restartedWith string
	var beforeInstallRan bool
	svc := NewService("owner/repo", "v1.0.0", "", Hooks{
		BeforeInstall:  func() error { beforeInstallRan = true; return nil },
		TriggerRestart: func(binary string) { restartedWith = binary },
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	exe := filepath.Join(t.TempDir(), "hypercraft")
	if err := os.WriteFile(exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc.up.apiBase = f.server.URL
	svc.up.exePath = exe

	if _, err := svc.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if err := svc.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if !beforeInstallRan {
		t.Error("BeforeInstall never ran, so no servers would be recorded for resume")
	}
	if restartedWith != exe {
		t.Errorf("restart target = %q, want %q", restartedWith, exe)
	}
	got, err := os.ReadFile(restartedWith)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("restart target holds %q, want the new binary %q", got, want)
	}
	if phase := svc.Status().Phase; phase != PhaseRestarting {
		t.Errorf("Phase = %q, want restarting", phase)
	}
}

func TestServiceAbortsCleanlyWhenPreparationFails(t *testing.T) {
	f := newFakeRelease(t, "1.2.0", []byte("new binary"))
	f.corruptSum = true

	var restarted, aborted bool
	svc := NewService("owner/repo", "v1.0.0", "", Hooks{
		BeforeInstall:  func() error { return nil },
		InstallAborted: func() { aborted = true },
		TriggerRestart: func(string) { restarted = true },
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	exe := filepath.Join(t.TempDir(), "hypercraft")
	if err := os.WriteFile(exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc.up.apiBase = f.server.URL
	svc.up.exePath = exe

	if _, err := svc.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if err := svc.Apply(context.Background()); err == nil {
		t.Fatal("Apply succeeded with a corrupt checksum")
	}
	if restarted {
		t.Error("the panel was restarted after a failed download")
	}
	if aborted {
		t.Error("InstallAborted ran even though the install was never reached")
	}
	// The panel must be left usable, on the old binary, ready to retry.
	if got, _ := os.ReadFile(exe); string(got) != "old binary" {
		t.Errorf("executable = %q, want it untouched", got)
	}
	status := svc.Status()
	if status.Phase != PhaseIdle {
		t.Errorf("Phase = %q, want idle so the operator can retry", status.Phase)
	}
	if status.Error == "" {
		t.Error("no error surfaced to the UI after a failed update")
	}
}
