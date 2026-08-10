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

	"github.com/lanscarlos/hypercraft/internal/unpack"
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
	Major     int    `json:"major"`
	ImageType string `json:"imageType"`
	// Source is the download source in use. It starts out as the one that was
	// asked for and becomes the one that actually answered, so a job that fell
	// back off an out-of-date mirror says so on the page.
	Source     string     `json:"source,omitempty"`
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
//
// source names where the archive comes from; empty means SourceAuto. It has no
// bearing on which build gets installed — that comes from the Adoptium API
// either way — only on where the bytes are pulled from.
func (i *Installer) Start(major int, imageType, source string) (Job, error) {
	platform, err := CurrentPlatform()
	if err != nil {
		return Job{}, err
	}
	source, err = ResolveSource(source)
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
	i.job = &Job{
		Major:     major,
		ImageType: imageType,
		Source:    source,
		State:     JobDownloading,
		StartedAt: time.Now(),
	}
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
		"file", release.FileName, "size", release.Size, "source", source)

	go func() {
		defer cancel()
		defer close(done)
		i.run(ctx, job, release, source)
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
func (i *Installer) run(ctx context.Context, job *Job, release Release, source string) {
	id := installID(release)
	staging := filepath.Join(i.store.Root(), ".installing-"+id)

	// A staging directory left by an earlier attempt has to go before this one
	// starts: unpacking on top of it would leave the previous run's files mixed
	// into the new runtime, and flatten() would not recognise the layout.
	err := os.RemoveAll(staging)
	if err == nil {
		err = i.fetchAndUnpack(ctx, job, staging, release, source)
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

// fetchAndUnpack downloads the archive and unpacks it, deleting the temporary
// download either way.
func (i *Installer) fetchAndUnpack(ctx context.Context, job *Job, staging string, release Release, source string) error {
	archive, err := i.download(ctx, job, release, source)
	if err != nil {
		return err
	}
	defer func() {
		archive.Close()
		_ = os.Remove(archive.Name())
	}()

	i.setState(job, JobExtracting)
	return i.unpack(ctx, staging, release, archive)
}

// download streams the archive to a temp file and verifies it. Nothing is
// unpacked until the bytes match what Adoptium published: an archive is a lot
// of files to have to clean up after deciding not to trust it.
//
// That check is also what makes the mirrors safe to offer — the checksum comes
// from the Adoptium API, never from the source serving the file.
func (i *Installer) download(ctx context.Context, job *Job, release Release, source string) (*os.File, error) {
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

	body, served, err := i.client.Fetch(ctx, release, source)
	if err != nil {
		cleanup()
		return nil, err
	}
	defer body.Close()

	if served != source {
		i.log.Info("java download fell back to another source",
			"asked", source, "using", served, "file", release.FileName)
	}
	i.mu.Lock()
	job.Source = served
	i.mu.Unlock()

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
	// Every failure below names the source that served the bytes. With more
	// than one to choose from, "which mirror handed me this" is the first
	// thing an operator needs to know — a stale or half-synced copy shows up
	// exactly here, and the fix is to install from somewhere else.
	from := SourceName(served)
	switch {
	case written > limit:
		cleanup()
		return nil, fmt.Errorf("%w: %s: download exceeds the declared %d bytes", ErrUpstream, from, limit)
	case release.Size > 0 && written != release.Size:
		cleanup()
		return nil, fmt.Errorf("%w: %s: got %d bytes, expected %d", ErrUpstream, from, written, release.Size)
	}
	if release.SHA256 != "" {
		if sum := hex.EncodeToString(digest.Sum(nil)); sum != release.SHA256 {
			cleanup()
			return nil, fmt.Errorf("%w: %s: got %s, expected %s", ErrChecksum, from, sum, release.SHA256)
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
	if err := extractInto(ctx, staging, release, archive); err != nil {
		return err
	}
	if findJava(staging) == "" {
		return fmt.Errorf("%w: 解压后没找到 bin/%s", unpack.ErrBadArchive, javaBinary())
	}

	final := filepath.Join(i.store.Root(), installID(release))
	if err := renameInstall(ctx, staging, final); err != nil {
		if isLocked(err) {
			// The one failure an operator can actually do something about.
			return fmt.Errorf("%w：解压好的文件被别的程序占着，"+
				"多半是杀毒软件正在扫描，或者有资源管理器窗口开在 data\\java 里；"+
				"关掉之后重新安装即可", err)
		}
		return err
	}
	return nil
}

// extractInto unpacks the archive into staging, and — importantly — closes the
// os.Root handle on staging before it returns.
//
// Windows will not rename a directory anything still has open, and Go opens the
// Root without FILE_SHARE_DELETE, so a handle that outlives the extraction turns
// the move into place into a sharing violation every single time. On Unix the
// rename would have gone through regardless, which is why this only ever showed
// up on Windows.
func extractInto(ctx context.Context, staging string, release Release, archive *os.File) error {
	root, err := os.OpenRoot(staging)
	if err != nil {
		return err
	}
	defer root.Close()

	if err := unpack.Extract(ctx, release.FileName, archive, root, unpack.Limits{}); err != nil {
		return err
	}
	// JDK archives wrap everything in one directory named after the build.
	// Dropping it keeps the installed path predictable: <id>/bin/java.
	return unpack.Flatten(root)
}

// renameInstall moves the staged runtime into place, retrying for a few seconds
// while the directory is locked.
//
// Our own handles are shut by now, but on Windows an on-access virus scanner
// routinely still holds a file it watched us write — 25k of them just landed —
// and one such file makes the whole directory unrenameable until it lets go.
// That is a transient condition and worth waiting out; anything else is
// returned to the operator immediately.
func renameInstall(ctx context.Context, from, to string) error {
	const (
		attempts = 12
		backoff  = 250 * time.Millisecond
	)
	for attempt := range attempts {
		err := os.Rename(from, to)
		if err == nil || !isLocked(err) || attempt == attempts-1 {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
	// Unreachable: the loop always returns on its last attempt.
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
