package serverjar

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
	"strconv"
	"strings"
	"testing"
	"time"
)

// upstream is a fake Fill API plus CDN.
type upstream struct {
	*httptest.Server
	body []byte
	// gate, when non-nil, blocks the artifact handler until it is closed.
	gate chan struct{}
	// corrupt makes the CDN serve something other than what it advertised.
	corrupt bool
}

func newUpstream(t *testing.T, body []byte) *upstream {
	t.Helper()
	up := &upstream{body: body}
	mux := http.NewServeMux()
	mux.HandleFunc("/projects/paper/versions/1.21.11/builds/latest", func(w http.ResponseWriter, r *http.Request) {
		sum := sha256.Sum256(up.body)
		w.Write([]byte(buildPayload(up.URL+"/artifact.jar", hex.EncodeToString(sum[:]), int64(len(up.body)))))
	})
	mux.HandleFunc("/artifact.jar", func(w http.ResponseWriter, r *http.Request) {
		if up.gate != nil {
			select {
			case <-up.gate:
			case <-r.Context().Done():
				return
			}
		}
		payload := up.body
		if up.corrupt {
			payload = []byte(strings.Repeat("x", len(up.body)))
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.Write(payload)
	})
	up.Server = httptest.NewServer(mux)
	t.Cleanup(up.Close)
	return up
}

// newTestDownloader returns a downloader writing into a fresh library.
func newTestDownloader(t *testing.T, up *upstream) (*Downloader, *Library) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	library := NewLibrary(t.TempDir())
	return NewDownloader(NewClient(up.URL, "test"), library, logger), library
}

// awaitJob polls until the download leaves the downloading state.
func awaitJob(t *testing.T, d *Downloader) Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		job, ok := d.Status()
		if ok && job.State != JobDownloading {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("download did not finish in time")
	return Job{}
}

// read returns a file's contents from the library directory.
func read(t *testing.T, library *Library, name string) ([]byte, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(library.Root(), name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false
		}
		t.Fatalf("read %s: %v", name, err)
	}
	return data, true
}

func write(t *testing.T, library *Library, name, content string) {
	t.Helper()
	if err := os.MkdirAll(library.Root(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(library.Root(), name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestDownloadWritesVerifiedJar(t *testing.T) {
	body := []byte(strings.Repeat("paper", 5000))
	up := newUpstream(t, body)
	d, library := newTestDownloader(t, up)

	job, err := d.Start(Request{Project: "paper", Version: "1.21.11"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if job.FileName != "paper-1.21.11-132.jar" || job.Total != int64(len(body)) || job.Build != 132 {
		t.Fatalf("unexpected job snapshot: %+v", job)
	}

	done := awaitJob(t, d)
	if done.State != JobDone {
		t.Fatalf("state %s, error %q", done.State, done.Error)
	}
	if done.Downloaded != int64(len(body)) {
		t.Errorf("downloaded %d, want %d", done.Downloaded, len(body))
	}
	if done.CoreID != "paper-1.21.11-132.jar" {
		t.Errorf("job names core %q", done.CoreID)
	}
	if got, ok := read(t, library, "paper-1.21.11-132.jar"); !ok || string(got) != string(body) {
		t.Errorf("jar contents differ (present=%v)", ok)
	}
	if _, ok := read(t, library, "paper-1.21.11-132.jar"+partSuffix); ok {
		t.Errorf("part file was left behind")
	}

	// The build details have to survive in the index, or the library page can
	// only show a file name.
	cores, err := library.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(cores) != 1 {
		t.Fatalf("library holds %d cores, want 1", len(cores))
	}
	if cores[0].Version != "1.21.11" || cores[0].Build != 132 || cores[0].Project != "paper" {
		t.Errorf("metadata was not recorded: %+v", cores[0])
	}
	if cores[0].Imported {
		t.Errorf("a downloaded core must not be marked as imported")
	}
}

// A jar that does not match its published checksum must never end up where the
// launcher would pick it up.
func TestDownloadRejectsBadChecksum(t *testing.T) {
	up := newUpstream(t, []byte(strings.Repeat("paper", 100)))
	up.corrupt = true
	d, library := newTestDownloader(t, up)

	if _, err := d.Start(Request{Project: "paper", Version: "1.21.11"}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := awaitJob(t, d)
	if done.State != JobFailed || !strings.Contains(done.Error, "checksum") {
		t.Fatalf("expected a checksum failure, got %s / %q", done.State, done.Error)
	}
	if _, ok := read(t, library, "paper-1.21.11-132.jar"); ok {
		t.Errorf("corrupt download was placed anyway")
	}
	if _, ok := read(t, library, "paper-1.21.11-132.jar"+partSuffix); ok {
		t.Errorf("part file was left behind")
	}
}

func TestDownloadRefusesToClobber(t *testing.T) {
	up := newUpstream(t, []byte("jar"))
	d, library := newTestDownloader(t, up)
	write(t, library, "paper-1.21.11-132.jar", "the jar already in the library")

	_, err := d.Start(Request{Project: "paper", Version: "1.21.11"})
	if !errors.Is(err, ErrExists) {
		t.Fatalf("got %v, want ErrExists", err)
	}
	if got, _ := read(t, library, "paper-1.21.11-132.jar"); string(got) != "the jar already in the library" {
		t.Errorf("existing jar was touched: %q", got)
	}
}

func TestDownloadOverwritesWhenAsked(t *testing.T) {
	body := []byte("a newer build")
	up := newUpstream(t, body)
	d, library := newTestDownloader(t, up)
	write(t, library, "paper-1.21.11-132.jar", "older")

	if _, err := d.Start(Request{Project: "paper", Version: "1.21.11", Overwrite: true}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if done := awaitJob(t, d); done.State != JobDone {
		t.Fatalf("state %s, error %q", done.State, done.Error)
	}
	if got, _ := read(t, library, "paper-1.21.11-132.jar"); string(got) != string(body) {
		t.Errorf("jar was not replaced: %q", got)
	}
}

func TestSecondDownloadIsRejectedWhileOneRuns(t *testing.T) {
	up := newUpstream(t, []byte("slow"))
	up.gate = make(chan struct{})
	d, _ := newTestDownloader(t, up)

	if _, err := d.Start(Request{Project: "paper", Version: "1.21.11"}); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if _, err := d.Start(Request{Project: "paper", Version: "1.21.11"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Start: got %v, want ErrBusy", err)
	}

	close(up.gate)
	if done := awaitJob(t, d); done.State != JobDone {
		t.Fatalf("state %s, error %q", done.State, done.Error)
	}
}

func TestCancelStopsDownload(t *testing.T) {
	up := newUpstream(t, []byte("slow"))
	up.gate = make(chan struct{})
	defer close(up.gate)

	d, library := newTestDownloader(t, up)
	if _, err := d.Start(Request{Project: "paper", Version: "1.21.11"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := d.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	done := awaitJob(t, d)
	if done.State != JobCancelled {
		t.Fatalf("state %s, error %q", done.State, done.Error)
	}
	if _, ok := read(t, library, "paper-1.21.11-132.jar"+partSuffix); ok {
		t.Errorf("cancelled download left a part file")
	}
	// A finished job is not cancellable, and saying so beats a silent success.
	if err := d.Cancel(); err == nil {
		t.Errorf("cancelling a finished job should fail")
	}
}

func TestStartRejectsUnknownProject(t *testing.T) {
	up := newUpstream(t, []byte("jar"))
	d, _ := newTestDownloader(t, up)

	if _, err := d.Start(Request{Project: "forge", Version: "1.21.11"}); !errors.Is(err, ErrUnknownProject) {
		t.Fatalf("got %v, want ErrUnknownProject", err)
	}
	if _, ok := d.Status(); ok {
		t.Errorf("a rejected project should not leave a job behind")
	}
}
