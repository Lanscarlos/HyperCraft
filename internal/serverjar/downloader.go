package serverjar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	// ErrBusy is returned when a download is already running.
	ErrBusy = errors.New("a core download is already running")
	// ErrExists is returned when that exact build is already in the library
	// and the caller did not ask to replace it.
	ErrExists = errors.New("this core is already in the library")
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

// Job is a snapshot of the panel's most recent core download.
//
// It survives the download: after the transfer ends the finished job stays
// readable, so an operator who closed the tab still sees how it went.
type Job struct {
	Project     string   `json:"project"`
	ProjectName string   `json:"projectName"`
	Version     string   `json:"version"`
	Build       int      `json:"build"`
	Channel     string   `json:"channel"`
	FileName    string   `json:"fileName"`
	Total       int64    `json:"total"`
	Downloaded  int64    `json:"downloaded"`
	State       JobState `json:"state"`
	Error       string   `json:"error,omitempty"`
	// CoreID names the library entry a finished download produced.
	CoreID     string     `json:"coreId,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// Request describes one download.
type Request struct {
	Project   string
	Version   string
	Overwrite bool
}

// Downloader fetches server cores into the panel-wide library.
//
// One at a time, panel-wide: the library is shared, and two transfers racing
// over the same directory buys nothing. Like the server processes themselves, a
// download belongs to the daemon and not to the request that started it —
// closing the tab, logging out or losing the network does not interrupt a jar
// that is already coming down.
type Downloader struct {
	client  *Client
	library *Library
	log     *slog.Logger

	mu     sync.Mutex
	job    *Job
	cancel context.CancelFunc
	done   chan struct{}
}

func NewDownloader(client *Client, library *Library, logger *slog.Logger) *Downloader {
	return &Downloader{client: client, library: library, log: logger}
}

// Client exposes the API client for the metadata handlers.
func (d *Downloader) Client() *Client { return d.client }

// Library exposes the directory downloaded cores land in.
func (d *Downloader) Library() *Library { return d.library }

// Start resolves the newest build and begins fetching it in the background.
//
// It returns once the download is under way; everything that can be reported
// as a bad request — unknown project, unknown version, core already in the
// library — is checked before that, so the operator gets a real error rather
// than a job that fails a second later.
func (d *Downloader) Start(req Request) (Job, error) {
	project, ok := LookupProject(req.Project)
	if !ok {
		return Job{}, fmt.Errorf("%w: %s", ErrUnknownProject, req.Project)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Claim the slot before touching the network, so two clicks in quick
	// succession cannot both start writing the same file.
	d.mu.Lock()
	if d.job != nil && d.job.State == JobDownloading {
		d.mu.Unlock()
		cancel()
		return Job{}, ErrBusy
	}
	d.job = &Job{
		Project:     project.ID,
		ProjectName: project.Name,
		Version:     req.Version,
		State:       JobDownloading,
		StartedAt:   time.Now(),
	}
	d.cancel = cancel
	d.done = make(chan struct{})
	job, done := d.job, d.done
	d.mu.Unlock()

	build, err := d.resolve(ctx, req)
	if err == nil {
		err = d.checkTarget(build.FileName, req.Overwrite)
	}
	if err != nil {
		cancel()
		close(done)
		d.finish(job, JobFailed, err)
		return Job{}, err
	}

	d.mu.Lock()
	job.Build = build.Build
	job.Channel = build.Channel
	job.FileName = build.FileName
	job.Total = build.Size
	snapshot := *job
	d.mu.Unlock()

	d.log.Info("core download started",
		"project", project.ID, "version", req.Version,
		"build", build.Build, "file", build.FileName, "size", build.Size)

	go func() {
		defer cancel()
		defer close(done)
		d.run(ctx, job, build, project, req)
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

func (d *Downloader) checkTarget(name string, overwrite bool) error {
	if d.library.Has(name) && !overwrite {
		return fmt.Errorf("%w: %s", ErrExists, name)
	}
	return nil
}

// run streams the artifact to a .part file, checks it, and only then moves it
// into place. A failed or cancelled download never leaves something that looks
// like a working jar in the library.
func (d *Downloader) run(ctx context.Context, job *Job, build Build, project Project, req Request) {
	root := d.library.Root()
	temp := filepath.Join(root, build.FileName+partSuffix)
	final := filepath.Join(root, build.FileName)

	err := os.MkdirAll(root, 0o755)
	if err == nil {
		// A previous attempt may have died with the panel and left its part file.
		_ = os.Remove(temp)
		err = d.transfer(ctx, job, temp, build)
	}
	if err == nil {
		err = place(temp, final, req.Overwrite)
	}
	if err == nil {
		err = d.library.record(Core{
			ID:          build.FileName,
			FileName:    build.FileName,
			Project:     project.ID,
			ProjectName: project.Name,
			Kind:        project.Kind,
			Version:     req.Version,
			Build:       build.Build,
			Channel:     build.Channel,
			SHA256:      build.SHA256,
			Size:        build.Size,
			AddedAt:     time.Now(),
		})
		if err != nil {
			// The jar itself is fine, only its metadata is missing; say so
			// rather than implying the download has to be repeated.
			err = fmt.Errorf("下载完成，但记录核心信息失败: %w", err)
		}
	}

	switch {
	case err == nil:
		d.mu.Lock()
		job.CoreID = build.FileName
		d.mu.Unlock()
		d.finish(job, JobDone, nil)
		d.log.Info("core download finished", "file", build.FileName)
	case ctx.Err() != nil:
		_ = os.Remove(temp)
		d.finish(job, JobCancelled, ErrCancelled)
		d.log.Info("core download cancelled", "file", build.FileName)
	default:
		_ = os.Remove(temp)
		d.finish(job, JobFailed, err)
		d.log.Warn("core download failed", "file", build.FileName, "err", err)
	}
}

func (d *Downloader) transfer(ctx context.Context, job *Job, temp string, build Build) error {
	body, err := d.client.Fetch(ctx, build)
	if err != nil {
		return err
	}
	defer body.Close()

	file, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
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
			job.Downloaded = n
			d.mu.Unlock()
		},
	}

	// One byte past the limit, so an exactly-sized body still succeeds while an
	// oversized one is caught instead of silently truncated.
	written, copyErr := io.Copy(progress, io.LimitReader(body, limit+1))
	// A close error on the last flush is the difference between a whole jar and
	// a truncated one, so it is checked rather than deferred away.
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
func place(temp, final string, overwrite bool) error {
	if _, err := os.Stat(final); err == nil {
		if !overwrite {
			return fmt.Errorf("%w: %s", ErrExists, filepath.Base(final))
		}
		// The old jar is only removed here — after the replacement is
		// downloaded and verified — so a failed download never costs the
		// operator a working core.
		if err := os.Remove(final); err != nil {
			return err
		}
	}
	return os.Rename(temp, final)
}

func (d *Downloader) finish(job *Job, state JobState, err error) {
	now := time.Now()

	d.mu.Lock()
	defer d.mu.Unlock()
	job.State = state
	job.FinishedAt = &now
	if err != nil {
		job.Error = err.Error()
	}
}

// Status returns the current or most recent job.
func (d *Downloader) Status() (Job, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.job == nil {
		return Job{}, false
	}
	return *d.job, true
}

// Cancel stops an in-flight download. Cancelling a finished one is a no-op.
func (d *Downloader) Cancel() error {
	d.mu.Lock()
	if d.job == nil || d.job.State != JobDownloading {
		d.mu.Unlock()
		return fmt.Errorf("%w: no download is running", ErrCancelled)
	}
	cancel := d.cancel
	d.mu.Unlock()

	cancel()
	return nil
}

// Close cancels a running download and waits briefly for it to unwind, so
// panel shutdown does not leave a writer racing against the process exit.
func (d *Downloader) Close() {
	d.mu.Lock()
	running := d.job != nil && d.job.State == JobDownloading
	cancel, done := d.cancel, d.done
	d.mu.Unlock()

	if !running || cancel == nil {
		return
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
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
