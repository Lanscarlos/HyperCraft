package plugin

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
	ErrBusy = errors.New("a plugin download is already running")
	// ErrCancelled is recorded on a job the operator stopped.
	ErrCancelled = errors.New("download cancelled")
)

// downloadTimeout bounds one transfer. Plugin jars are small — a few megabytes
// — but a slow mirror on a bad line is exactly who needs the headroom.
const downloadTimeout = 30 * time.Minute

// maxUnknownSize caps a download whose size the release did not declare.
const maxUnknownSize = 512 << 20

// JobState is where a download has got to.
type JobState string

const (
	JobDownloading JobState = "downloading"
	JobDone        JobState = "done"
	JobFailed      JobState = "failed"
	JobCancelled   JobState = "cancelled"
)

// Job is a snapshot of the panel's most recent plugin download.
//
// Like a core download, it survives the transfer: the finished job stays
// readable so an operator who closed the tab still sees how it went.
type Job struct {
	PluginID   string `json:"pluginId"`
	PluginName string `json:"pluginName"`
	Tag        string `json:"tag"`
	Version    string `json:"version"`
	FileName   string `json:"fileName"`
	// Mirror is where the bytes actually came from, which with the automatic
	// order in play is not something the operator's setting can tell them.
	Mirror     string     `json:"mirror,omitempty"`
	Total      int64      `json:"total"`
	Downloaded int64      `json:"downloaded"`
	State      JobState   `json:"state"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// Downloader fetches plugin releases into the panel-wide library.
//
// One at a time, panel-wide, for the same reason core downloads are: the
// library is shared and two transfers racing over it buys nothing. The job
// belongs to the daemon rather than to the request that started it, so closing
// the tab does not interrupt a jar that is already coming down.
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

// Client exposes the release client for the metadata handlers.
func (d *Downloader) Client() *Client { return d.client }

// Library exposes the directory downloaded plugins land in.
func (d *Downloader) Library() *Library { return d.library }

// Releases lists what a tracked plugin could be installed at.
func (d *Downloader) Releases(ctx context.Context, id string) ([]Release, error) {
	item, err := d.library.Get(id)
	if err != nil {
		return nil, err
	}
	return d.client.Releases(ctx, item.Source)
}

// syncVisibility asks GitHub whether a repository is private and records the
// answer, returning the plugin as it stands afterwards.
//
// This runs before checks and downloads so the panel does not depend on the
// operator having ticked the right box. Getting it wrong is not a cosmetic
// mistake: a private repository fetched as if it were public asks the download
// host for a jar it will never serve, which fails with a 404 that reads like
// the release is gone, and it hands the plugin's name to the download mirror on
// the way. Both are avoided by asking the one party that knows.
//
// It only asks when a token is configured — an anonymous panel gets the same
// 404 for a private repository here as everywhere else, so the call could only
// spend quota to learn nothing. A failure is not an error: the stored flag is
// still the best answer available, and a visibility probe must never be the
// reason an update check or a download does not happen.
func (d *Downloader) syncVisibility(ctx context.Context, item Plugin) Plugin {
	if item.Source.Kind != SourceGitHub || !d.client.Authenticated() {
		return item
	}
	private, err := d.client.Visibility(ctx, item.Source.Repo)
	if err != nil {
		d.log.Debug("could not read repository visibility", "plugin", item.ID, "err", err)
		return item
	}
	changed, err := d.library.SetPrivate(item.ID, private)
	if err != nil {
		d.log.Warn("could not record repository visibility", "plugin", item.ID, "err", err)
		return item
	}
	if changed {
		d.log.Info("repository visibility corrected", "plugin", item.ID, "private", private)
	}
	item.Source.Private = private
	return item
}

// Check refreshes one plugin's newest release and records the result.
//
// The error is returned as well as stored: the operator who clicked "check"
// should see why it failed, and the next page load should still say the check
// was tried and did not work.
func (d *Downloader) Check(ctx context.Context, id string) (Plugin, error) {
	item, err := d.library.Get(id)
	if err != nil {
		return Plugin{}, err
	}
	item = d.syncVisibility(ctx, item)

	latest, checkErr := d.client.Latest(ctx, item.Source)
	var found *Release
	if checkErr == nil {
		release := latest
		found = &release
	}
	if err := d.library.RecordCheck(id, found, checkErr); err != nil {
		return Plugin{}, err
	}
	updated, err := d.library.Get(id)
	if err != nil {
		return Plugin{}, err
	}
	return updated, checkErr
}

// CheckAll refreshes every tracked plugin, one at a time.
//
// Sequential on purpose: the anonymous GitHub API allows 60 calls an hour and
// answers a burst with a rate limit that then blocks the next check too. A
// plugin whose check fails does not stop the ones after it — a repository that
// was renamed should not hide updates for everything else.
func (d *Downloader) CheckAll(ctx context.Context) []Plugin {
	items := d.library.List()
	out := make([]Plugin, 0, len(items))
	for _, item := range items {
		if ctx.Err() != nil {
			break
		}
		checked, err := d.Check(ctx, item.ID)
		if err != nil {
			d.log.Warn("plugin update check failed", "plugin", item.ID, "err", err)
			// Check stores the failure, so re-read rather than dropping the row.
			if stored, getErr := d.library.Get(item.ID); getErr == nil {
				out = append(out, stored)
			}
			continue
		}
		out = append(out, checked)
	}
	return out
}

// Start downloads one release of a tracked plugin into the library.
//
// It returns once the transfer is under way; everything reportable as a bad
// request — unknown plugin, unknown tag, release with no jar — is resolved
// first, so the operator gets a real error rather than a job that fails a
// second later.
func (d *Downloader) Start(pluginID, tag string) (Job, error) {
	item, err := d.library.Get(pluginID)
	if err != nil {
		return Job{}, err
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
		PluginID:   item.ID,
		PluginName: item.Name,
		Tag:        tag,
		Version:    VersionOf(tag),
		State:      JobDownloading,
		StartedAt:  time.Now(),
	}
	d.cancel = cancel
	d.done = make(chan struct{})
	job, done := d.job, d.done
	d.mu.Unlock()

	// Checked again here rather than trusted from the last check: this is the
	// one moment where being wrong about it fails the operation, and a
	// repository that was made private after it was added would otherwise keep
	// failing until someone thought to press "check updates".
	item = d.syncVisibility(ctx, item)

	release, err := d.resolve(ctx, item, tag)
	if err != nil {
		cancel()
		close(done)
		d.finish(job, JobFailed, err)
		return Job{}, err
	}

	d.mu.Lock()
	job.Tag = release.Tag
	job.Version = release.Version
	job.FileName = release.Asset.Name
	job.Total = release.Asset.Size
	snapshot := *job
	d.mu.Unlock()

	d.log.Info("plugin download started",
		"plugin", item.ID, "tag", release.Tag, "file", release.Asset.Name, "size", release.Asset.Size)

	go func() {
		defer cancel()
		defer close(done)
		d.run(ctx, job, item, release)
	}()
	return snapshot, nil
}

// resolve finds the release to fetch. An empty tag means "whatever is newest",
// which is what the update button asks for.
func (d *Downloader) resolve(ctx context.Context, item Plugin, tag string) (Release, error) {
	releases, err := d.client.Releases(ctx, item.Source)
	if err != nil {
		return Release{}, err
	}
	if tag == "" {
		return releases[0], nil
	}
	for _, release := range releases {
		if release.Tag == tag {
			return release, nil
		}
	}
	return Release{}, fmt.Errorf("%w: %s publishes no release tagged %s", ErrNotFound, item.Source.Repo, tag)
}

// run streams the jar to a .part file and only then moves it into place, so a
// failed or cancelled download never leaves something that looks like an
// installable plugin in the library.
func (d *Downloader) run(ctx context.Context, job *Job, item Plugin, release Release) {
	slug, err := versionSlug(release.Tag)
	if err != nil {
		d.finish(job, JobFailed, err)
		return
	}
	dir := filepath.Join(d.library.Root(), item.ID, slug)
	temp := filepath.Join(dir, release.Asset.Name+partSuffix)
	final := filepath.Join(dir, release.Asset.Name)

	var digest string
	if err = os.MkdirAll(dir, 0o755); err == nil {
		// A previous attempt may have died with the panel and left its part file.
		_ = os.Remove(temp)
		digest, err = d.transfer(ctx, job, temp, item.Source, release.Asset)
	}
	if err == nil {
		// Re-downloading a version the operator already has is the repair path
		// for a corrupt jar, so the old file is replaced rather than refused —
		// and only here, with the replacement complete and verified on disk.
		_ = os.Remove(final)
		err = os.Rename(temp, final)
	}
	if err == nil {
		err = d.library.record(item.ID, Version{
			Tag:         release.Tag,
			Version:     release.Version,
			FileName:    release.Asset.Name,
			Size:        release.Asset.Size,
			SHA256:      digest,
			Prerelease:  release.Prerelease,
			Notes:       release.Notes,
			PublishedAt: release.PublishedAt,
			AddedAt:     time.Now(),
		})
		if err != nil {
			// The jar itself is fine, only its metadata is missing; say so
			// rather than implying the download has to be repeated.
			err = fmt.Errorf("下载完成，但记录插件版本失败: %w", err)
		}
	}

	switch {
	case err == nil:
		d.finish(job, JobDone, nil)
		d.log.Info("plugin download finished", "plugin", item.ID, "file", release.Asset.Name)
	case ctx.Err() != nil:
		_ = os.Remove(temp)
		d.finish(job, JobCancelled, ErrCancelled)
		d.log.Info("plugin download cancelled", "plugin", item.ID, "file", release.Asset.Name)
	default:
		_ = os.Remove(temp)
		d.finish(job, JobFailed, err)
		d.log.Warn("plugin download failed", "plugin", item.ID, "file", release.Asset.Name, "err", err)
	}
}

// transfer streams one asset to disk and returns its SHA-256.
func (d *Downloader) transfer(ctx context.Context, job *Job, temp string, src Source, asset Asset) (string, error) {
	body, mirror, err := d.client.Fetch(ctx, src, asset)
	if err != nil {
		return "", err
	}
	defer body.Close()

	d.mu.Lock()
	job.Mirror = mirror
	d.mu.Unlock()

	file, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}

	limit := asset.Size
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
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if written > limit {
		return "", fmt.Errorf("%w: download exceeds the declared %d bytes", ErrUpstream, limit)
	}
	if asset.Size > 0 && written != asset.Size {
		return "", fmt.Errorf("%w: got %d bytes, expected %d", ErrUpstream, written, asset.Size)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
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

// Close cancels a running download and waits briefly for it to unwind, so panel
// shutdown does not leave a writer racing against the process exit.
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
	w.report(w.written)
	return n, err
}
