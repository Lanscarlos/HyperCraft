package serverjar

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// memSink is an in-memory stand-in for the instance directory.
type memSink struct {
	mu    sync.Mutex
	files map[string][]byte
}

func newMemSink() *memSink { return &memSink{files: make(map[string][]byte)} }

func (s *memSink) Exists(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.files[name]
	return ok, nil
}

func (s *memSink) Create(name string) (io.WriteCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.files[name]; ok {
		return nil, ErrExists
	}
	s.files[name] = nil
	return &memFile{sink: s, name: name}, nil
}

func (s *memSink) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.files[name]; !ok {
		return errors.New("not found")
	}
	delete(s.files, name)
	return nil
}

func (s *memSink) Rename(from, to string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.files[from]
	if !ok {
		return errors.New("not found")
	}
	if _, taken := s.files[to]; taken {
		return ErrExists
	}
	s.files[to] = data
	delete(s.files, from)
	return nil
}

func (s *memSink) get(name string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.files[name]
	return data, ok
}

type memFile struct {
	sink *memSink
	name string
}

func (f *memFile) Write(p []byte) (int, error) {
	f.sink.mu.Lock()
	defer f.sink.mu.Unlock()
	f.sink.files[f.name] = append(f.sink.files[f.name], p...)
	return len(p), nil
}

func (f *memFile) Close() error { return nil }

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

func newTestDownloader(up *upstream) *Downloader {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewDownloader(NewClient(up.URL, "test"), logger)
}

// awaitJob polls until the instance's job leaves the downloading state.
func awaitJob(t *testing.T, d *Downloader, instanceID string) Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		job, ok := d.Status(instanceID)
		if ok && job.State != JobDownloading {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("download did not finish in time")
	return Job{}
}

func TestDownloadWritesVerifiedJar(t *testing.T) {
	body := []byte(strings.Repeat("paper", 5000))
	up := newUpstream(t, body)
	d := newTestDownloader(up)
	sink := newMemSink()

	var applied string
	job, err := d.Start("inst-1", Request{
		Project: "paper",
		Version: "1.21.11",
		OnDone:  func(name string) error { applied = name; return nil },
	}, sink)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if job.FileName != "paper-1.21.11-132.jar" || job.Total != int64(len(body)) || job.Build != 132 {
		t.Fatalf("unexpected job snapshot: %+v", job)
	}

	done := awaitJob(t, d, "inst-1")
	if done.State != JobDone {
		t.Fatalf("state %s, error %q", done.State, done.Error)
	}
	if done.Downloaded != int64(len(body)) {
		t.Errorf("downloaded %d, want %d", done.Downloaded, len(body))
	}
	if got, ok := sink.get("paper-1.21.11-132.jar"); !ok || string(got) != string(body) {
		t.Errorf("jar contents differ (present=%v)", ok)
	}
	if _, ok := sink.get("paper-1.21.11-132.jar.hypercraft-part"); ok {
		t.Errorf("part file was left behind")
	}
	if applied != "paper-1.21.11-132.jar" {
		t.Errorf("OnDone got %q", applied)
	}
}

// A jar that does not match its published checksum must never end up where the
// launcher would pick it up.
func TestDownloadRejectsBadChecksum(t *testing.T) {
	up := newUpstream(t, []byte(strings.Repeat("paper", 100)))
	up.corrupt = true
	d := newTestDownloader(up)
	sink := newMemSink()

	if _, err := d.Start("inst-1", Request{Project: "paper", Version: "1.21.11"}, sink); err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := awaitJob(t, d, "inst-1")
	if done.State != JobFailed || !strings.Contains(done.Error, "checksum") {
		t.Fatalf("expected a checksum failure, got %s / %q", done.State, done.Error)
	}
	if _, ok := sink.get("paper-1.21.11-132.jar"); ok {
		t.Errorf("corrupt download was placed anyway")
	}
	if _, ok := sink.get("paper-1.21.11-132.jar.hypercraft-part"); ok {
		t.Errorf("part file was left behind")
	}
}

func TestDownloadRefusesToClobber(t *testing.T) {
	up := newUpstream(t, []byte("jar"))
	d := newTestDownloader(up)
	sink := newMemSink()
	sink.files["paper-1.21.11-132.jar"] = []byte("the jar already running this server")

	_, err := d.Start("inst-1", Request{Project: "paper", Version: "1.21.11"}, sink)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("got %v, want ErrExists", err)
	}
	if got, _ := sink.get("paper-1.21.11-132.jar"); string(got) != "the jar already running this server" {
		t.Errorf("existing jar was touched: %q", got)
	}
}

func TestDownloadOverwritesWhenAsked(t *testing.T) {
	body := []byte("a newer build")
	up := newUpstream(t, body)
	d := newTestDownloader(up)
	sink := newMemSink()
	sink.files["paper-1.21.11-132.jar"] = []byte("older")

	if _, err := d.Start("inst-1", Request{Project: "paper", Version: "1.21.11", Overwrite: true}, sink); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if done := awaitJob(t, d, "inst-1"); done.State != JobDone {
		t.Fatalf("state %s, error %q", done.State, done.Error)
	}
	if got, _ := sink.get("paper-1.21.11-132.jar"); string(got) != string(body) {
		t.Errorf("jar was not replaced: %q", got)
	}
}

func TestSecondDownloadIsRejectedWhileOneRuns(t *testing.T) {
	up := newUpstream(t, []byte("slow"))
	up.gate = make(chan struct{})
	d := newTestDownloader(up)
	sink := newMemSink()

	if _, err := d.Start("inst-1", Request{Project: "paper", Version: "1.21.11"}, sink); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if _, err := d.Start("inst-1", Request{Project: "paper", Version: "1.21.11"}, sink); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Start: got %v, want ErrBusy", err)
	}

	close(up.gate)
	if done := awaitJob(t, d, "inst-1"); done.State != JobDone {
		t.Fatalf("state %s, error %q", done.State, done.Error)
	}
}

func TestCancelStopsDownload(t *testing.T) {
	up := newUpstream(t, []byte("slow"))
	up.gate = make(chan struct{})
	defer close(up.gate)

	d := newTestDownloader(up)
	sink := newMemSink()
	if _, err := d.Start("inst-1", Request{Project: "paper", Version: "1.21.11"}, sink); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := d.Cancel("inst-1"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	done := awaitJob(t, d, "inst-1")
	if done.State != JobCancelled {
		t.Fatalf("state %s, error %q", done.State, done.Error)
	}
	if _, ok := sink.get("paper-1.21.11-132.jar.hypercraft-part"); ok {
		t.Errorf("cancelled download left a part file")
	}
	// A finished job is not cancellable, and saying so beats a silent success.
	if err := d.Cancel("inst-1"); err == nil {
		t.Errorf("cancelling a finished job should fail")
	}
}

func TestForgetDropsJob(t *testing.T) {
	up := newUpstream(t, []byte("jar"))
	d := newTestDownloader(up)
	sink := newMemSink()

	if _, err := d.Start("inst-1", Request{Project: "paper", Version: "1.21.11"}, sink); err != nil {
		t.Fatalf("Start: %v", err)
	}
	awaitJob(t, d, "inst-1")

	d.Forget("inst-1")
	if _, ok := d.Status("inst-1"); ok {
		t.Errorf("job survived Forget")
	}
}

func TestStartRejectsUnknownProject(t *testing.T) {
	up := newUpstream(t, []byte("jar"))
	d := newTestDownloader(up)

	if _, err := d.Start("inst-1", Request{Project: "forge", Version: "1.21.11"}, newMemSink()); !errors.Is(err, ErrUnknownProject) {
		t.Fatalf("got %v, want ErrUnknownProject", err)
	}
	if _, ok := d.Status("inst-1"); ok {
		t.Errorf("a rejected project should not leave a job behind")
	}
}
