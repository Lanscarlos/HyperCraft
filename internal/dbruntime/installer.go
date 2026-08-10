package dbruntime

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lanscarlos/hypercraft/internal/unpack"
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
	Engine     string     `json:"engine"`
	Version    string     `json:"version"`
	FileName   string     `json:"fileName"`
	Total      int64      `json:"total"`
	Downloaded int64      `json:"downloaded"`
	State      JobState   `json:"state"`
	Error      string     `json:"error,omitempty"`
	InstallID  string     `json:"installId,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// maxArchiveBytes caps a download whose size upstream did not declare. It is
// larger than the Java installer's because the MySQL tarball for aarch64 —
// the only build Oracle publishes for that architecture — is close to a
// gigabyte on its own.
const maxArchiveBytes = 2 << 30 // 2 GiB

// extractLimits bound what one engine may unpack to. The full MySQL tarball
// expands to about 4 GB, so the shared default would reject it halfway.
var extractLimits = unpack.Limits{MaxBytes: 8 << 30, MaxEntries: 200_000}

// Installer downloads and unpacks database engines.
//
// One at a time, panel-wide, for the same reasons as javaruntime.Installer:
// installs are not per-server, two of them racing over the engines directory
// buys nothing, and the job belongs to the daemon so closing the browser does
// not stop it.
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

// Client exposes the metadata client for the version handlers.
func (i *Installer) Client() *Client { return i.client }

// Store exposes the engines directory.
func (i *Installer) Store() *Store { return i.store }

// Start resolves a build and begins installing it in the background.
func (i *Installer) Start(engine, version string) (Job, error) {
	if _, err := EngineByID(engine); err != nil {
		return Job{}, err
	}
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
	i.job = &Job{
		Engine:    engine,
		Version:   version,
		State:     JobDownloading,
		StartedAt: time.Now(),
	}
	i.cancel = cancel
	i.done = make(chan struct{})
	job, done := i.job, i.done
	i.mu.Unlock()

	release, err := i.resolve(ctx, engine, version, platform)
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
	job.InstallID = installID(engine, release.Version)
	snapshot := *job
	i.mu.Unlock()

	i.log.Info("database install started",
		"engine", engine, "version", release.Version,
		"file", release.FileName, "size", release.Size)

	go func() {
		defer cancel()
		defer close(done)
		i.run(ctx, job, release)
	}()
	return snapshot, nil
}

func (i *Installer) resolve(ctx context.Context, engine, version string, platform Platform) (Release, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return i.client.Resolve(lookupCtx, engine, version, platform)
}

func (i *Installer) checkNotInstalled(release Release) error {
	id := installID(release.Engine, release.Version)
	if _, err := os.Stat(filepath.Join(i.store.Root(), id)); err == nil {
		return fmt.Errorf("%w: %s", ErrExists, id)
	}
	return nil
}

// run downloads the archive, checks it, and unpacks it into a staging
// directory that is only renamed into place once a runnable server is in it —
// so a half-unpacked engine never shows up in the picker.
func (i *Installer) run(ctx context.Context, job *Job, release Release) {
	id := installID(release.Engine, release.Version)
	staging := filepath.Join(i.store.Root(), ".installing-"+id)

	// A staging directory left by an earlier attempt has to go before this one
	// starts: unpacking on top of it would mix the previous run's files into
	// the new install.
	err := os.RemoveAll(staging)
	if err == nil {
		err = i.fetchAndUnpack(ctx, job, staging, release)
	}

	switch {
	case err == nil:
		i.finish(job, JobDone, nil)
		i.log.Info("database install finished", "install", id)
	case ctx.Err() != nil:
		_ = os.RemoveAll(staging)
		i.finish(job, JobCancelled, ErrCancelled)
		i.log.Info("database install cancelled", "install", id)
	default:
		_ = os.RemoveAll(staging)
		i.finish(job, JobFailed, err)
		i.log.Warn("database install failed", "install", id, "err", err)
	}
}

func (i *Installer) fetchAndUnpack(ctx context.Context, job *Job, staging string, release Release) error {
	archive, err := i.download(ctx, job, release)
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

// download streams the archive to a temp file and verifies it.
//
// There are no mirrors here, unlike the Java installer. That is a deliberate
// consequence of what upstream publishes: mirroring Adoptium is safe because
// every asset comes with a SHA-256 from the API, so the mirror cannot serve
// something else. MySQL publishes only an MD5 and the PostgreSQL builds only a
// SHA-1 — neither survives a deliberate substitution — which leaves TLS to the
// publisher as the only real guarantee. A mirror would quietly remove it.
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
	digest := newDigest(release.Algo)
	sink := io.Writer(temp)
	if digest != nil {
		sink = io.MultiWriter(temp, digest)
	}
	progress := &progressWriter{
		to: sink,
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
		return nil, fmt.Errorf("%w: 下载超过了声明的 %d 字节", ErrUpstream, limit)
	case release.Size > 0 && written != release.Size:
		cleanup()
		return nil, fmt.Errorf("%w: 下到 %d 字节，声明的是 %d", ErrUpstream, written, release.Size)
	}
	if digest != nil && release.Checksum != "" {
		if sum := hex.EncodeToString(digest.Sum(nil)); sum != release.Checksum {
			cleanup()
			return nil, fmt.Errorf("%w: %s 校验不上，算出来是 %s，上游写的是 %s",
				ErrChecksum, release.Algo, sum, release.Checksum)
		}
	}
	return temp, nil
}

// newDigest returns the hash upstream published this file's checksum with.
// Nothing here gets to choose the algorithm — sha256 for MongoDB, sha1 for
// Maven, md5 for Oracle — so all three are accepted for what they are worth.
func newDigest(algo string) hash.Hash {
	switch algo {
	case "sha256":
		return sha256.New()
	case "sha1":
		return sha1.New() //nolint:gosec // upstream publishes nothing stronger; see Installer.download
	case "md5":
		return md5.New() //nolint:gosec // ditto
	}
	return nil
}

func (i *Installer) unpack(ctx context.Context, staging string, release Release, archive *os.File) error {
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return err
	}
	if err := extractInto(ctx, staging, release, archive); err != nil {
		return err
	}
	if findBinary(staging, serverBinary(release.Engine)) == "" {
		return fmt.Errorf("%w: 解压后没找到 bin/%s",
			unpack.ErrBadArchive, serverBinary(release.Engine))
	}

	final := filepath.Join(i.store.Root(), installID(release.Engine, release.Version))
	return os.Rename(staging, final)
}

// extractInto unpacks the archive into staging and — importantly — closes the
// os.Root handle before it returns, because Windows will not rename a directory
// anything still has open. javaruntime learned this the hard way.
func extractInto(ctx context.Context, staging string, release Release, archive *os.File) error {
	root, err := os.OpenRoot(staging)
	if err != nil {
		return err
	}
	defer root.Close()

	if err := unpack.Extract(ctx, release.FileName, archive, root, extractLimits); err != nil {
		return err
	}
	if release.Inner != "" {
		if err := extractInner(ctx, staging, release.Inner, root); err != nil {
			return err
		}
	}
	// Every one of these archives wraps the build in a directory named after
	// it. Dropping it keeps the installed path predictable: <id>/bin/mysqld.
	return unpack.Flatten(root)
}

// extractInner unpacks an archive nested inside the download.
//
// Only PostgreSQL needs it: its builds are published as Maven jars with a
// single .txz inside, so the first extraction yields the container rather than
// the server. The nested archive and the jar's metadata are removed afterwards
// so what lands in the engines directory is a plain install like the others.
func extractInner(ctx context.Context, staging, suffix string, root *os.Root) error {
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	var name string
	for _, entry := range entries {
		if !entry.IsDir() && len(entry.Name()) > len(suffix) &&
			filepath.Ext(entry.Name()) == suffix {
			name = entry.Name()
			break
		}
	}
	if name == "" {
		return fmt.Errorf("%w: 压缩包里没有 %s 文件", unpack.ErrBadArchive, suffix)
	}

	inner, err := os.Open(filepath.Join(staging, name))
	if err != nil {
		return err
	}
	defer inner.Close()

	if err := unpack.Extract(ctx, name, inner, root, extractLimits); err != nil {
		return err
	}
	inner.Close()
	if err := root.Remove(name); err != nil {
		return err
	}
	// The jar's manifest is not part of the server and would stop Flatten from
	// recognising the layout.
	return os.RemoveAll(filepath.Join(staging, "META-INF"))
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
		job.InstallID = ""
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
		return fmt.Errorf("%w: 现在没有正在进行的安装", ErrCancelled)
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
