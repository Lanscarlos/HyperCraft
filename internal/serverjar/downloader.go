package serverjar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

var (
	// ErrBusy is returned when a download is already running for an instance.
	ErrBusy = errors.New("a download is already running for this instance")
	// ErrExists is returned when the target file is already in the directory
	// and the caller did not ask to replace it.
	ErrExists = errors.New("file already exists")
	// ErrCancelled is recorded on a job the operator stopped.
	ErrCancelled = errors.New("download cancelled")
	// ErrChecksum is recorded when the bytes on disk are not what upstream
	// published. The partial file is removed rather than left to be launched.
	ErrChecksum = errors.New("checksum mismatch")
)

// maxUnknownSize caps a download whose size upstream did not declare. Every
// real core is far below this; the limit exists so a redirect to something
// enormous cannot fill the operator's disk.
const maxUnknownSize = 1 << 30 // 1 GiB

// JobState is where a download has got to.
type JobState string

const (
	JobDownloading JobState = "downloading"
	JobDone        JobState = "done"
	JobFailed      JobState = "failed"
	JobCancelled   JobState = "cancelled"
)

// Job is a snapshot of one instance's most recent core download.
//
// It survives the download: after the transfer ends the finished job stays
// readable, so an operator who closed the tab still sees how it went.
type Job struct {
	InstanceID  string     `json:"instanceId"`
	Project     string     `json:"project"`
	ProjectName string     `json:"projectName"`
	Version     string     `json:"version"`
	Build       int        `json:"build"`
	Channel     string     `json:"channel"`
	FileName    string     `json:"fileName"`
	Total       int64      `json:"total"`
	Downloaded  int64      `json:"downloaded"`
	State       JobState   `json:"state"`
	Error       string     `json:"error,omitempty"`
	SetAsJar    bool       `json:"setAsJar"`
	StartedAt   time.Time  `json:"startedAt"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
}

// Sink is where a download lands. The API layer implements it over
// serverfiles.Browser, which confines every write to the instance directory.
type Sink interface {
	Exists(name string) (bool, error)
	// Create opens name for writing, failing if it already exists.
	Create(name string) (io.WriteCloser, error)
	Remove(name string) error
	Rename(from, to string) error
}

// Request describes one download.
type Request struct {
	Project   string
	Version   string
	Overwrite bool
	SetAsJar  bool
	// OnDone runs once the jar is in place and before the job is marked done.
	// It is how the API layer points the instance's launch config at the file
	// it just fetched — which has to happen in the daemon, since the operator
	// may well have closed the browser by then.
	OnDone func(fileName string) error
}

// Downloader runs core downloads on behalf of instances, one at a time each.
//
// Like the server processes themselves, a download belongs to the daemon and
// not to the request that started it: closing the tab, logging out or losing
// the network does not interrupt a jar that is already coming down.
type Downloader struct {
	client *Client
	log    *slog.Logger

	mu   sync.Mutex
	jobs map[string]*record
}

type record struct {
	job    Job
	cancel context.CancelFunc
	done   chan struct{}
}

func NewDownloader(client *Client, logger *slog.Logger) *Downloader {
	return &Downloader{client: client, log: logger, jobs: make(map[string]*record)}
}

// Client exposes the API client for the metadata handlers.
func (d *Downloader) Client() *Client { return d.client }

// Start resolves the newest build and begins fetching it in the background.
//
// It returns once the download is under way; everything that can be reported
// as a bad request — unknown project, unknown version, file already there — is
// checked before that, so the operator gets a real error rather than a job
// that fails a second later.
func (d *Downloader) Start(instanceID string, req Request, sink Sink) (Job, error) {
	project, ok := LookupProject(req.Project)
	if !ok {
		return Job{}, fmt.Errorf("%w: %s", ErrUnknownProject, req.Project)
	}

	ctx, cancel := context.WithCancel(context.Background())
	rec := &record{
		job: Job{
			InstanceID:  instanceID,
			Project:     project.ID,
			ProjectName: project.Name,
			Version:     req.Version,
			State:       JobDownloading,
			SetAsJar:    req.SetAsJar,
			StartedAt:   time.Now(),
		},
		cancel: cancel,
		done:   make(chan struct{}),
	}

	// Claim the instance before touching the network, so two clicks in quick
	// succession cannot both start writing the same file.
	d.mu.Lock()
	if existing, ok := d.jobs[instanceID]; ok && existing.job.State == JobDownloading {
		d.mu.Unlock()
		cancel()
		return Job{}, ErrBusy
	}
	d.jobs[instanceID] = rec
	d.mu.Unlock()

	build, err := d.resolve(ctx, req)
	if err == nil {
		err = d.checkTarget(sink, build.FileName, req.Overwrite)
	}
	if err != nil {
		cancel()
		d.finish(rec, JobFailed, err)
		return Job{}, err
	}

	d.mu.Lock()
	rec.job.Build = build.Build
	rec.job.Channel = build.Channel
	rec.job.FileName = build.FileName
	rec.job.Total = build.Size
	snapshot := rec.job
	d.mu.Unlock()

	d.log.Info("core download started",
		"instance", instanceID, "project", project.ID, "version", req.Version,
		"build", build.Build, "file", build.FileName, "size", build.Size)

	go func() {
		defer cancel()
		defer close(rec.done)
		d.run(ctx, rec, build, req, sink)
	}()
	return snapshot, nil
}

// resolve looks up the build to fetch. It uses the job's context rather than
// the request's, so a cancel lands even while metadata is still in flight.
func (d *Downloader) resolve(ctx context.Context, req Request) (Build, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return d.client.LatestBuild(lookupCtx, req.Project, req.Version)
}

func (d *Downloader) checkTarget(sink Sink, name string, overwrite bool) error {
	exists, err := sink.Exists(name)
	if err != nil {
		return err
	}
	if exists && !overwrite {
		return fmt.Errorf("%w: %s", ErrExists, name)
	}
	return nil
}

// run streams the artifact to a .part file, checks it, and only then moves it
// into place. A failed or cancelled download never leaves something that looks
// like a working jar behind.
func (d *Downloader) run(ctx context.Context, rec *record, build Build, req Request, sink Sink) {
	instanceID := rec.job.InstanceID

	temp := build.FileName + ".hypercraft-part"
	// A previous attempt may have died with the panel and left its part file.
	_ = sink.Remove(temp)

	err := d.transfer(ctx, rec, temp, build, sink)
	if err == nil {
		err = d.place(sink, temp, build.FileName, req.Overwrite)
	}
	if err == nil && req.OnDone != nil {
		if err = req.OnDone(build.FileName); err != nil {
			err = fmt.Errorf("下载完成，但设置启动 jar 失败: %w", err)
		}
	}

	switch {
	case err == nil:
		d.finish(rec, JobDone, nil)
		d.log.Info("core download finished", "instance", instanceID, "file", build.FileName)
	case ctx.Err() != nil:
		_ = sink.Remove(temp)
		d.finish(rec, JobCancelled, ErrCancelled)
		d.log.Info("core download cancelled", "instance", instanceID, "file", build.FileName)
	default:
		_ = sink.Remove(temp)
		d.finish(rec, JobFailed, err)
		d.log.Warn("core download failed", "instance", instanceID, "file", build.FileName, "err", err)
	}
}

func (d *Downloader) transfer(ctx context.Context, rec *record, temp string, build Build, sink Sink) error {
	body, err := d.client.Fetch(ctx, build)
	if err != nil {
		return err
	}
	defer body.Close()

	file, err := sink.Create(temp)
	if err != nil {
		return err
	}

	limit := build.Size
	if limit <= 0 {
		limit = maxUnknownSize
	}
	digest := sha256.New()
	progress := &progressWriter{
		to: io.MultiWriter(file, digest),
		report: func(n int64) {
			d.mu.Lock()
			rec.job.Downloaded = n
			d.mu.Unlock()
		},
	}

	// One byte past the limit, so an exactly-sized body still succeeds while an
	// oversized one is caught instead of silently truncated.
	written, copyErr := io.Copy(progress, io.LimitReader(body, limit+1))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > limit {
		return fmt.Errorf("%w: download exceeds the declared %d bytes", ErrUpstream, limit)
	}
	if build.Size > 0 && written != build.Size {
		return fmt.Errorf("%w: got %d bytes, expected %d", ErrUpstream, written, build.Size)
	}
	if build.SHA256 != "" {
		if sum := hex.EncodeToString(digest.Sum(nil)); sum != build.SHA256 {
			return fmt.Errorf("%w: got %s, expected %s", ErrChecksum, sum, build.SHA256)
		}
	}
	return nil
}

// place moves the verified download onto its final name.
func (d *Downloader) place(sink Sink, temp, final string, overwrite bool) error {
	exists, err := sink.Exists(final)
	if err != nil {
		return err
	}
	if exists {
		if !overwrite {
			return fmt.Errorf("%w: %s", ErrExists, final)
		}
		// Rename refuses to clobber, so the old jar goes first. It is only
		// removed here — after the replacement is downloaded and verified —
		// so a failed download never costs the operator a working jar.
		if err := sink.Remove(final); err != nil {
			return err
		}
	}
	return sink.Rename(temp, final)
}

// finish stamps the outcome on the record the goroutine owns, rather than on
// whatever the instance's current job happens to be: the instance may have been
// forgotten, or a new download started, while this one was unwinding.
func (d *Downloader) finish(rec *record, state JobState, err error) {
	now := time.Now()

	d.mu.Lock()
	defer d.mu.Unlock()
	rec.job.State = state
	rec.job.FinishedAt = &now
	if err != nil {
		rec.job.Error = err.Error()
	}
}

// Status returns the instance's current or most recent job.
func (d *Downloader) Status(instanceID string) (Job, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rec, ok := d.jobs[instanceID]
	if !ok {
		return Job{}, false
	}
	return rec.job, true
}

// Cancel stops an in-flight download. Cancelling a finished one is a no-op.
func (d *Downloader) Cancel(instanceID string) error {
	d.mu.Lock()
	rec, ok := d.jobs[instanceID]
	if !ok || rec.job.State != JobDownloading {
		d.mu.Unlock()
		return fmt.Errorf("%w: no download is running", ErrCancelled)
	}
	cancel := rec.cancel
	d.mu.Unlock()

	cancel()
	return nil
}

// Forget drops an instance's job, cancelling it first if it is still running.
// Called when the instance itself is deleted.
func (d *Downloader) Forget(instanceID string) {
	d.mu.Lock()
	rec, ok := d.jobs[instanceID]
	delete(d.jobs, instanceID)
	d.mu.Unlock()

	if ok && rec.cancel != nil {
		rec.cancel()
	}
}

// Close cancels every running download and waits briefly for them to unwind,
// so panel shutdown does not leave a writer racing against the process exit.
func (d *Downloader) Close() {
	d.mu.Lock()
	pending := make([]*record, 0, len(d.jobs))
	for _, rec := range d.jobs {
		if rec.job.State == JobDownloading {
			rec.cancel()
			pending = append(pending, rec)
		}
	}
	d.mu.Unlock()

	deadline := time.After(5 * time.Second)
	for _, rec := range pending {
		select {
		case <-rec.done:
		case <-deadline:
			return
		}
	}
}

// progressWriter reports the running total as bytes go past.
type progressWriter struct {
	to      io.Writer
	report  func(int64)
	written int64
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n, err := w.to.Write(p)
	w.written += int64(n)
	// io.Copy hands over 32 KiB at a time, so this is a few hundred calls for
	// a 60 MB jar — cheap enough to report every one and keep the bar smooth.
	w.report(w.written)
	return n, err
}
