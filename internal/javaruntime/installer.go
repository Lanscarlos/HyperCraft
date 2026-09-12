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
	// ErrChecksum is recorded when the archive is not what the distribution
	// published.
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
	// Distribution is who built the runtime being installed.
	Distribution string `json:"distribution"`
	Major        int    `json:"major"`
	ImageType    string `json:"imageType"`
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
// dist names the OpenJDK distribution and source names where the archive comes
// from; empty means the default and SourceAuto. The source has no bearing on
// which build gets installed — that comes from the distribution's metadata API
// either way — only on where the bytes are pulled from.
func (i *Installer) Start(dist string, major int, imageType, source string) (Job, error) {
	platform, err := CurrentPlatform()
	if err != nil {
		return Job{}, err
	}
	dist, err = ResolveDistribution(dist)
	if err != nil {
		return Job{}, err
	}
	source, err = ResolveSource(dist, source)
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
		Distribution: dist,
		Major:        major,
		ImageType:    imageType,
		Source:       source,
		State:        JobDownloading,
		StartedAt:    time.Now(),
	}
	i.cancel = cancel
	i.done = make(chan struct{})
	job, done := i.job, i.done
	i.mu.Unlock()

	release, err := i.resolve(ctx, dist, major, imageType, platform)
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
		"dist", dist, "major", major, "image", imageType, "version", release.Version,
		"file", release.FileName, "size", release.Size, "source", source)

	go func() {
		defer cancel()
		defer close(done)
		i.run(ctx, job, release, source)
	}()
	return snapshot, nil
}

func (i *Installer) resolve(ctx context.Context, dist string, major int, imageType string, platform Platform) (Release, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return i.client.LatestRelease(lookupCtx, dist, major, imageType, platform)
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
// unpacked until the bytes match what the distribution published: an archive
// is a lot of files to have to clean up after deciding not to trust it.
//
// That check is also what makes the mirrors safe to offer — the checksum comes
// from the distribution's metadata API, never from the source serving the file.
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

	// sized marks the case where the declared size is the only check there
	// is, and so has to be exact.
	//
	// With a checksum it is neither the stronger check nor a reliable one:
	// Azul's metadata says Zulu 25.0.4.1's linux/x64 JRE is 61117500 bytes,
	// cdn.azul.com serves 61117509, and those 61117509 bytes hash to exactly
	// the SHA-256 Azul published for the package. Gating on the size turned a
	// perfectly good archive into "exceeds the declared 61117500 bytes" with
	// no way round it, on the one distribution that has no second source to
	// try. So the size drives the progress bar and nothing else, and the cap
	// falls back to the blanket ceiling: the checksum catches a truncated,
	// stale or substituted archive either way, and the ceiling is only there
	// to bound the disk a runaway source can eat.
	sized := release.Size > 0 && release.SHA256 == ""
	limit := int64(maxArchiveBytes)
	if sized {
		limit = release.Size
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
	from := SourceName(release.Distribution, served)
	switch {
	case sized && written > limit:
		cleanup()
		return nil, fmt.Errorf("%w: %s: 下载的内容比声明的 %d 字节还多", ErrUpstream, from, limit)
	case sized && written != release.Size:
		cleanup()
		return nil, fmt.Errorf("%w: %s: 收到 %d 字节，应为 %d", ErrUpstream, from, written, release.Size)
	case written > limit:
		cleanup()
		return nil, fmt.Errorf("%w: %s: 下载超过 %d 字节的上限，已中止", ErrUpstream, from, limit)
	}
	if release.SHA256 != "" {
		if sum := hex.EncodeToString(digest.Sum(nil)); sum != release.SHA256 {
			cleanup()
			// A short body is the one checksum failure with an obvious cause,
			// and "the connection dropped, run it again" is very different
			// advice from "this source is serving the wrong file" — so say
			// which one it was while the byte count is still to hand.
			if release.Size > 0 && written < release.Size {
				return nil, fmt.Errorf("%w: %s: 下载中断，只收到 %d 字节，应为 %d",
					ErrChecksum, from, written, release.Size)
			}
			return nil, fmt.Errorf("%w: %s: 校验和不符，算出 %s，应为 %s", ErrChecksum, from, sum, release.SHA256)
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
	// The distribution leads the name so two vendors' builds of the same
	// version can sit side by side. Runtimes installed before this existed are
	// all "temurin-…" and keep working: the store reads their release file,
	// not their directory name.
	return release.Distribution + "-" + version + "-" + release.ImageType
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
