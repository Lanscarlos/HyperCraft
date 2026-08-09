package javaruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	// ErrBusy is returned while another install is already running.
	ErrBusy = errors.New("a java install is already running")
	// ErrExists is returned when that runtime is already on disk.
	ErrExists = errors.New("this java version is already installed")
	// ErrCancelled is recorded on an install the operator stopped.
	ErrCancelled = errors.New("install cancelled")
	// ErrChecksum is recorded when the archive is not what Adoptium published.
	ErrChecksum = errors.New("checksum mismatch")
)

// JobState is where an install has got to.
type JobState string

const (
	JobDownloading JobState = "downloading"
	JobExtracting  JobState = "extracting"
	JobDone        JobState = "done"
	JobFailed      JobState = "failed"
	JobCancelled   JobState = "cancelled"
)

// Job is a snapshot of the most recent install.
type Job struct {
	Major      int        `json:"major"`
	ImageType  string     `json:"imageType"`
	Version    string     `json:"version"`
	FileName   string     `json:"fileName"`
	Total      int64      `json:"total"`
	Downloaded int64      `json:"downloaded"`
	State      JobState   `json:"state"`
	Error      string     `json:"error,omitempty"`
	RuntimeID  string     `json:"runtimeId,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// Installer downloads and unpacks Java runtimes.
//
// One at a time, panel-wide: installs are not per-instance, and two of them
// racing over the runtimes directory buys nothing. Like every other long job
// here it belongs to the daemon, so closing the browser does not stop it.
type Installer struct {
	client *Client
	store  *Store
	log    *slog.Logger

	mu     sync.Mutex
	job    *Job
	cancel context.CancelFunc
	done   chan struct{}
}

func NewInstaller(client *Client, store *Store, logger *slog.Logger) *Installer {
	return &Installer{client: client, store: store, log: logger}
}

// Client exposes the API client for the metadata handlers.
func (i *Installer) Client() *Client { return i.client }

// Store exposes the runtimes directory.
func (i *Installer) Store() *Store { return i.store }

// Start resolves a build and begins installing it in the background.
func (i *Installer) Start(major int, imageType string) (Job, error) {
	platform, err := CurrentPlatform()
	if err != nil {
		return Job{}, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	i.mu.Lock()
	if i.job != nil && (i.job.State == JobDownloading || i.job.State == JobExtracting) {
		i.mu.Unlock()
		cancel()
		return Job{}, ErrBusy
	}
	i.job = &Job{Major: major, ImageType: imageType, State: JobDownloading, StartedAt: time.Now()}
	i.cancel = cancel
	i.done = make(chan struct{})
	job, done := i.job, i.done
	i.mu.Unlock()

	release, err := i.resolve(ctx, major, imageType, platform)
	if err == nil {
		err = i.checkNotInstalled(release)
	}
	if err != nil {
		cancel()
		close(done)
		i.finish(job, JobFailed, err)
		return Job{}, err
	}

	i.mu.Lock()
	job.Version = release.Version
	job.FileName = release.FileName
	job.Total = release.Size
	job.RuntimeID = installID(release)
	snapshot := *job
	i.mu.Unlock()

	i.log.Info("java install started",
		"major", major, "image", imageType, "version", release.Version,
		"file", release.FileName, "size", release.Size)

	go func() {
		defer cancel()
		defer close(done)
		i.run(ctx, job, release)
	}()
	return snapshot, nil
}

func (i *Installer) resolve(ctx context.Context, major int, imageType string, platform Platform) (Release, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return i.client.LatestRelease(lookupCtx, major, imageType, platform)
}

func (i *Installer) checkNotInstalled(release Release) error {
	id := installID(release)
	if _, err := os.Stat(filepath.Join(i.store.Root(), id)); err == nil {
		return fmt.Errorf("%w: %s", ErrExists, id)
	}
	return nil
}

// run downloads the archive, checks it, and unpacks it into a staging
// directory that is only renamed into place once a working java is in it —
// so a half-unpacked runtime never shows up in the dropdown.
func (i *Installer) run(ctx context.Context, job *Job, release Release) {
	id := installID(release)
	staging := filepath.Join(i.store.Root(), ".installing-"+id)
	_ = os.RemoveAll(staging)

	archive, err := i.download(ctx, job, release)
	if err == nil {
		defer func() {
			archive.Close()
			_ = os.Remove(archive.Name())
		}()
		i.setState(job, JobExtracting)
		err = i.unpack(ctx, staging, release, archive)
	}

	switch {
	case err == nil:
		i.finish(job, JobDone, nil)
		i.log.Info("java install finished", "runtime", id, "version", release.Version)
	case ctx.Err() != nil:
		_ = os.RemoveAll(staging)
		i.finish(job, JobCancelled, ErrCancelled)
		i.log.Info("java install cancelled", "runtime", id)
	default:
		_ = os.RemoveAll(staging)
		i.finish(job, JobFailed, err)
		i.log.Warn("java install failed", "runtime", id, "err", err)
	}
}

// download streams the archive to a temp file and verifies it. Nothing is
// unpacked until the bytes match what Adoptium published: an archive is a lot
// of files to have to clean up after deciding not to trust it.
func (i *Installer) download(ctx context.Context, job *Job, release Release) (*os.File, error) {
	if err := os.MkdirAll(i.store.Root(), 0o755); err != nil {
		return nil, err
	}
	temp, err := os.CreateTemp(i.store.Root(), ".download-*.part")
	if err != nil {
		return nil, err
	}
	cleanup := func() {
		temp.Close()
		_ = os.Remove(temp.Name())
	}

	body, err := i.client.Fetch(ctx, release)
	if err != nil {
		cleanup()
		return nil, err
	}
	defer body.Close()

	limit := release.Size
	if limit <= 0 {
		limit = maxArchiveBytes
	}
	digest := sha256.New()
	progress := &progressWriter{
		to: io.MultiWriter(temp, digest),
		report: func(n int64) {
			i.mu.Lock()
			job.Downloaded = n
			i.mu.Unlock()
		},
	}

	written, err := io.Copy(progress, io.LimitReader(body, limit+1))
	if err != nil {
		cleanup()
		return nil, err
	}
	switch {
	case written > limit:
		cleanup()
		return nil, fmt.Errorf("%w: download exceeds the declared %d bytes", ErrUpstream, limit)
	case release.Size > 0 && written != release.Size:
		cleanup()
		return nil, fmt.Errorf("%w: got %d bytes, expected %d", ErrUpstream, written, release.Size)
	}
	if release.SHA256 != "" {
		if sum := hex.EncodeToString(digest.Sum(nil)); sum != release.SHA256 {
			cleanup()
			return nil, fmt.Errorf("%w: got %s, expected %s", ErrChecksum, sum, release.SHA256)
		}
	}
	return temp, nil
}

// maxArchiveBytes caps a download whose size upstream did not declare.
const maxArchiveBytes = 1 << 30 // 1 GiB

func (i *Installer) unpack(ctx context.Context, staging string, release Release, archive *os.File) error {
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(staging)
	if err != nil {
		return err
	}
	defer root.Close()

	if err := extractArchive(ctx, release.FileName, archive, root); err != nil {
		return err
	}
	// JDK archives wrap everything in one directory named after the build.
	// Dropping it keeps the installed path predictable: <id>/bin/java.
	if err := flatten(root); err != nil {
		return err
	}
	if findJava(staging) == "" {
		return fmt.Errorf("%w: 解压后没找到 bin/%s", ErrBadArchive, javaBinary())
	}

	final := filepath.Join(i.store.Root(), installID(release))
	if err := os.Rename(staging, final); err != nil {
		return err
	}
	return nil
}

// flatten lifts the contents of a lone top-level directory up one level.
func flatten(root *os.Root) error {
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return nil
	}

	wrapper := entries[0].Name()
	inner, err := fs.ReadDir(root.FS(), wrapper)
	if err != nil {
		return err
	}
	for _, entry := range inner {
		if err := root.Rename(wrapper+"/"+entry.Name(), entry.Name()); err != nil {
			return err
		}
	}
	return root.Remove(wrapper)
}

func (i *Installer) setState(job *Job, state JobState) {
	i.mu.Lock()
	defer i.mu.Unlock()
	job.State = state
}

func (i *Installer) finish(job *Job, state JobState, err error) {
	now := time.Now()

	i.mu.Lock()
	defer i.mu.Unlock()
	job.State = state
	job.FinishedAt = &now
	if err != nil {
		job.Error = err.Error()
	}
	if state != JobDone {
		job.RuntimeID = ""
	}
}

// Status returns the current or most recent install job.
func (i *Installer) Status() (Job, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.job == nil {
		return Job{}, false
	}
	return *i.job, true
}

// Cancel stops an install that is still running.
func (i *Installer) Cancel() error {
	i.mu.Lock()
	if i.job == nil || (i.job.State != JobDownloading && i.job.State != JobExtracting) {
		i.mu.Unlock()
		return fmt.Errorf("%w: no install is running", ErrCancelled)
	}
	cancel := i.cancel
	i.mu.Unlock()

	cancel()
	return nil
}

// Close cancels a running install and waits briefly for it to unwind.
func (i *Installer) Close() {
	i.mu.Lock()
	running := i.job != nil && (i.job.State == JobDownloading || i.job.State == JobExtracting)
	cancel, done := i.cancel, i.done
	i.mu.Unlock()

	if !running || cancel == nil {
		return
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

// installID names the directory a release is installed into, e.g.
// temurin-21.0.12-8-jre.
func installID(release Release) string {
	// Adoptium reports 21.0.12+8-LTS; the support status is not part of the
	// version and only makes the directory name harder to read.
	version := release.Version
	if trimmed := strings.TrimSuffix(strings.ToUpper(version), "-LTS"); len(trimmed) != len(version) {
		version = version[:len(trimmed)]
	}
	version = strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '.':
			return r
		default:
			return '-'
		}
	}, version)
	version = strings.Trim(version, "-.")
	if version == "" {
		version = fmt.Sprintf("%d", release.Major)
	}
	return "temurin-" + version + "-" + release.ImageType
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
	w.report(w.written)
	return n, err
}
