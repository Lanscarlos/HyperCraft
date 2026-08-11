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
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	// ErrBusy is returned when the queue is full.
	ErrBusy = errors.New("下载队列已经排满了，等几个下完再来")
	// ErrCancelled is recorded on a job the operator stopped.
	ErrCancelled = errors.New("download cancelled")
)

// downloadTimeout bounds one transfer. Plugin jars are small — a few megabytes
// — but a slow mirror on a bad line is exactly who needs the headroom.
const downloadTimeout = 30 * time.Minute

// maxUnknownSize caps a download whose size the release did not declare.
const maxUnknownSize = 512 << 20

// maxConcurrent is how many jars come down at once.
//
// Three rather than "as many as were asked for", and the limit is upstream's
// rather than the disk's: every job opens with a release lookup against the
// GitHub API, where an anonymous panel gets 60 calls an hour and a burst is
// answered with a rate limit that then blocks the next *check* too. Twenty
// parallel downloads would spend an hour's budget in one click and leave the
// operator unable to ask what the newest version is. Mirrors throttle on much
// the same terms.
const maxConcurrent = 3

// maxQueued bounds jobs waiting for a slot. A backstop against a bulk action
// that fans out further than anybody intended, not a limit anyone should meet.
const maxQueued = 100

// maxHistory bounds the finished jobs kept around to be read.
//
// Finished jobs are the whole reason this is a list rather than a counter: a
// download that failed at 3am is only useful if it is still there in the
// morning, and before the queue existed the *next* download overwrote it.
const maxHistory = 30

// JobState is where a download has got to.
type JobState string

const (
	// JobQueued is waiting for one of the concurrency slots.
	JobQueued      JobState = "queued"
	JobDownloading JobState = "downloading"
	JobDone        JobState = "done"
	JobFailed      JobState = "failed"
	JobCancelled   JobState = "cancelled"
)

// Active reports whether a job is still going to do something.
func (s JobState) Active() bool { return s == JobQueued || s == JobDownloading }

// Job is a snapshot of one plugin download.
//
// Like a core download, it survives the transfer: the finished job stays
// readable so an operator who closed the tab still sees how it went.
type Job struct {
	// ID names this job for cancellation. Assigned by the panel and unique for
	// as long as the process lives — the queue is deliberately not persisted,
	// because a download that was interrupted by a panel restart is one that
	// has to be started again rather than resumed.
	ID         string `json:"id"`
	PluginID   string `json:"pluginId"`
	PluginName string `json:"pluginName"`
	Tag        string `json:"tag"`
	Version    string `json:"version"`
	FileName   string `json:"fileName"`
	// Mirror is where the bytes actually came from, which with the automatic
	// order in play is not something the operator's setting can tell them.
	Mirror     string    `json:"mirror,omitempty"`
	Total      int64     `json:"total"`
	Downloaded int64     `json:"downloaded"`
	State      JobState  `json:"state"`
	Error      string    `json:"error,omitempty"`
	QueuedAt   time.Time `json:"queuedAt"`
	// StartedAt is when the job left the queue, so it is absent on one that
	// never has. Kept separate from QueuedAt rather than folded into it: "sat
	// in the queue for four minutes" and "took four minutes to download" are
	// different complaints with different causes.
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// job is one queue entry: what the operator asked for, and where it has got to.
//
// The request is held beside the Job rather than inside it because the two
// drift apart on purpose. An empty tag means "whatever is newest", and once
// the release resolves the Job says v5.5.71 — but a second request for
// "newest" is still the same request, and matching it against the resolved tag
// is how the panel ends up downloading the same jar twice.
type job struct {
	pub *Job
	// want* are the request as it arrived: empty tag is "newest", empty asset
	// is "the release's primary jar".
	wantTag   string
	wantAsset string
	cancel    context.CancelFunc
}

// Downloader fetches plugin releases into the panel-wide library.
//
// A queue with a small number of workers, panel-wide. It used to be one slot:
// a second download while the first was running was refused outright, which
// made "update these five plugins" into five clicks spread over as long as the
// downloads took, and left a failed job visible only until the next one
// replaced it.
//
// The queue belongs to the daemon rather than to the request that started it,
// so closing the tab does not interrupt a jar that is already coming down, and
// a job that was still waiting for a slot does not lose its place.
type Downloader struct {
	client  *Client
	library *Library
	log     *slog.Logger

	mu     sync.Mutex
	jobs   []*job // oldest first, which is the order they run in
	active int
	seq    int
	closed bool

	wg sync.WaitGroup
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
// It only asks when this source has a token to ask with — an anonymous panel
// gets the same 404 for a private repository here as everywhere else, so the
// call could only spend quota to learn nothing. A failure is not an error: the
// stored flag is still the best answer available, and a visibility probe must
// never be the reason an update check or a download does not happen.
func (d *Downloader) syncVisibility(ctx context.Context, item Plugin) Plugin {
	if item.Source.Kind != SourceGitHub || !d.client.HasTokenFor(item.Source) {
		return item
	}
	private, err := d.client.Visibility(ctx, item.Source)
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
	// An uploaded jar has no upstream. Not an error and not a failed check —
	// there is simply nothing to ask, and recording "check failed" against it
	// would put a warning on a plugin that is working exactly as intended.
	if item.Source.Kind == SourceLocal {
		return item, nil
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

// Start queues one jar of one release of a tracked plugin for the library.
//
// `asset` names which jar, by file name, and empty means the release's primary
// — which is the right answer for the great majority of releases, because they
// publish one. A release that ships a build per platform is the reason this is
// a parameter at all: the paper jar and the velocity jar are the same version,
// and a panel that could only ever fetch the first of them would be a panel
// that cannot put this plugin on a proxy.
//
// Asking twice for the same jar returns the job already doing it rather than a
// second one. That is not politeness: two workers writing the same .part file
// is a corrupt download, and the single-slot design used to prevent it by
// accident, by refusing the second click outright.
//
// What it does *not* do any more is resolve the release first. That check used
// to happen here so an unknown tag came back as a bad request rather than as a
// job that failed a second later — but it needs the network, and a queued job
// may be minutes away from its turn. So the only thing answered synchronously
// is whether the plugin is tracked at all; anything upstream has to say lands
// on the job, where the queue page shows it.
func (d *Downloader) Start(pluginID, tag, asset string) (Job, error) {
	item, err := d.library.Get(pluginID)
	if err != nil {
		return Job{}, err
	}
	tag, asset = strings.TrimSpace(tag), strings.TrimSpace(asset)

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return Job{}, ErrBusy
	}
	if existing := d.duplicate(pluginID, tag, asset); existing != nil {
		return *existing.pub, nil
	}
	queued := 0
	for _, entry := range d.jobs {
		if entry.pub.State == JobQueued {
			queued++
		}
	}
	if queued >= maxQueued {
		return Job{}, ErrBusy
	}

	d.seq++
	entry := &job{
		pub: &Job{
			ID:         strconv.Itoa(d.seq),
			PluginID:   item.ID,
			PluginName: item.Name,
			Tag:        tag,
			Version:    VersionOf(tag),
			FileName:   asset,
			State:      JobQueued,
			QueuedAt:   time.Now(),
		},
		wantTag:   tag,
		wantAsset: asset,
	}
	d.jobs = append(d.jobs, entry)
	d.prune()
	d.dispatch()
	return *entry.pub, nil
}

// duplicate finds an unfinished job for exactly this request. Called with the
// lock held.
func (d *Downloader) duplicate(pluginID, tag, asset string) *job {
	for _, entry := range d.jobs {
		if !entry.pub.State.Active() || entry.pub.PluginID != pluginID {
			continue
		}
		if entry.wantTag == tag && strings.EqualFold(entry.wantAsset, asset) {
			return entry
		}
	}
	return nil
}

// prune drops the oldest finished jobs once there are more than the history
// holds. Only finished ones: a queue longer than the history is still a queue,
// and forgetting a job that has not run yet would lose the download. Called
// with the lock held.
func (d *Downloader) prune() {
	finished := 0
	for _, entry := range d.jobs {
		if !entry.pub.State.Active() {
			finished++
		}
	}
	if finished <= maxHistory {
		return
	}
	drop := finished - maxHistory
	kept := make([]*job, 0, len(d.jobs)-drop)
	for _, entry := range d.jobs {
		if drop > 0 && !entry.pub.State.Active() {
			drop--
			continue
		}
		kept = append(kept, entry)
	}
	d.jobs = kept
}

// dispatch starts queued jobs while there are slots. Called with the lock held.
func (d *Downloader) dispatch() {
	if d.closed {
		return
	}
	for _, entry := range d.jobs {
		if d.active >= maxConcurrent {
			return
		}
		if entry.pub.State != JobQueued {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		now := time.Now()
		entry.cancel = cancel
		entry.pub.State = JobDownloading
		entry.pub.StartedAt = &now
		d.active++
		d.wg.Add(1)
		go d.work(ctx, entry)
	}
}

// work runs one job end to end and then hands its slot to whatever is next.
func (d *Downloader) work(ctx context.Context, entry *job) {
	defer func() {
		d.wg.Done()
		d.mu.Lock()
		d.active--
		if entry.cancel != nil {
			entry.cancel()
			entry.cancel = nil
		}
		d.dispatch()
		d.mu.Unlock()
	}()

	// Re-read rather than closing over what Start saw: a job that waited in the
	// queue may have been sitting there while the operator edited the source or
	// swapped the token it reads with.
	item, err := d.library.Get(entry.pub.PluginID)
	if err != nil {
		d.finish(entry.pub, JobFailed, err)
		return
	}

	// Checked here rather than trusted from the last check: this is the one
	// moment where being wrong about it fails the operation, and a repository
	// that was made private after it was added would otherwise keep failing
	// until someone thought to press "check updates".
	item = d.syncVisibility(ctx, item)

	release, err := d.resolve(ctx, item, entry.wantTag)
	var want Asset
	if err == nil {
		want, err = pickNamed(release, entry.wantAsset)
	}
	if err != nil {
		if ctx.Err() != nil {
			d.finish(entry.pub, JobCancelled, ErrCancelled)
			return
		}
		d.finish(entry.pub, JobFailed, err)
		return
	}

	d.mu.Lock()
	entry.pub.Tag = release.Tag
	entry.pub.Version = release.Version
	entry.pub.FileName = want.Name
	entry.pub.Total = want.Size
	d.mu.Unlock()

	d.log.Info("plugin download started",
		"plugin", item.ID, "tag", release.Tag, "file", want.Name, "size", want.Size)

	d.run(ctx, entry.pub, item, release, want)
}

// firstOf is the first list that says anything. Used where an asset's own
// claim outranks its release's, and the release's is the fallback rather than
// nothing.
func firstOf(preferred, fallback []string) []string {
	if len(preferred) > 0 {
		return preferred
	}
	return fallback
}

// pickNamed finds the jar a download asked for. An empty name is the release's
// primary; a name that is not on the release is an error rather than a silent
// fallback, because the fallback would be the wrong platform's jar.
func pickNamed(release Release, name string) (Asset, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return release.Asset, nil
	}
	for _, asset := range release.Assets {
		if strings.EqualFold(asset.Name, name) {
			return asset, nil
		}
	}
	return Asset{}, fmt.Errorf("%w: %s 里没有名为 %s 的文件", ErrNotFound, release.Tag, name)
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
func (d *Downloader) run(ctx context.Context, pub *Job, item Plugin, release Release, want Asset) {
	slug, err := versionSlug(release.Tag)
	if err != nil {
		d.finish(pub, JobFailed, err)
		return
	}
	dir := filepath.Join(d.library.Root(), item.ID, slug)
	temp := filepath.Join(dir, want.Name+partSuffix)
	final := filepath.Join(dir, want.Name)

	var digest string
	if err = os.MkdirAll(dir, 0o755); err == nil {
		// A previous attempt may have died with the panel and left its part file.
		_ = os.Remove(temp)
		digest, err = d.transfer(ctx, pub, temp, item.Source, want)
	}
	if err == nil {
		// Re-downloading a version the operator already has is the repair path
		// for a corrupt jar, so the old file is replaced rather than refused —
		// and only here, with the replacement complete and verified on disk.
		_ = os.Remove(final)
		err = os.Rename(temp, final)
	}
	if err == nil {
		// The jar is asked what it is, now that it is whole and on disk. This
		// is the identity everything downstream depends on: the upgrade sweep
		// deletes by declared plugin name, and it cannot do that for a jar the
		// panel never opened. A descriptor that will not parse is not an error
		// — the file is still a perfectly good download — it just leaves those
		// fields empty and the panel says so rather than guessing.
		//
		// What the *jar* supports, not what the release does: on a release
		// that ships one build per platform those are different claims, and
		// the one an install has to be judged against is this file's.
		artifact := Artifact{
			SHA256:       digest,
			FileName:     want.Name,
			Size:         want.Size,
			Platform:     want.Platform,
			GameVersions: firstOf(want.GameVersions, release.GameVersions),
			Loaders:      firstOf(want.Loaders, release.Loaders),
			AddedAt:      time.Now(),
		}
		if info, size, readErr := readJar(final); readErr == nil {
			artifact.Size = size
			artifact.applyJarInfo(info)
			if info.Platform == "" && want.Platform != "" {
				artifact.Platform = want.Platform
			}
		}

		err = d.library.record(item.ID, Version{
			Tag:          release.Tag,
			Version:      release.Version,
			Artifacts:    []Artifact{artifact},
			Prerelease:   release.Prerelease,
			Notes:        release.Notes,
			PublishedAt:  release.PublishedAt,
			AddedAt:      time.Now(),
			GameVersions: release.GameVersions,
			Loaders:      release.Loaders,
		})
		if err != nil {
			// The jar itself is fine, only its metadata is missing; say so
			// rather than implying the download has to be repeated.
			err = fmt.Errorf("下载完成，但记录插件版本失败: %w", err)
		}
	}

	switch {
	case err == nil:
		d.finish(pub, JobDone, nil)
		d.log.Info("plugin download finished", "plugin", item.ID, "file", want.Name)
	case ctx.Err() != nil:
		_ = os.Remove(temp)
		d.finish(pub, JobCancelled, ErrCancelled)
		d.log.Info("plugin download cancelled", "plugin", item.ID, "file", want.Name)
	default:
		_ = os.Remove(temp)
		d.finish(pub, JobFailed, err)
		d.log.Warn("plugin download failed", "plugin", item.ID, "file", want.Name, "err", err)
	}
}

// transfer streams one asset to disk and returns its SHA-256.
func (d *Downloader) transfer(ctx context.Context, pub *Job, temp string, src Source, asset Asset) (string, error) {
	body, mirror, err := d.client.Fetch(ctx, src, asset)
	if err != nil {
		return "", err
	}
	defer body.Close()

	d.mu.Lock()
	pub.Mirror = mirror
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
			pub.Downloaded = n
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

func (d *Downloader) finish(pub *Job, state JobState, err error) {
	now := time.Now()

	d.mu.Lock()
	defer d.mu.Unlock()
	// A job cancelled while it was still queued is already finished; a worker
	// that started before the cancellation landed must not resurrect it.
	if !pub.State.Active() {
		return
	}
	pub.State = state
	pub.FinishedAt = &now
	if err != nil {
		pub.Error = err.Error()
	}
	// Pruned here as well as on insert, because a job only becomes history when
	// it ends: pruning only on insert leaves the queue one row over the cap
	// between the last download finishing and the next one starting.
	d.prune()
}

// Jobs returns the queue and the history, newest first.
func (d *Downloader) Jobs() []Job {
	d.mu.Lock()
	defer d.mu.Unlock()

	out := make([]Job, 0, len(d.jobs))
	for i := len(d.jobs) - 1; i >= 0; i-- {
		out = append(out, *d.jobs[i].pub)
	}
	return out
}

// Status returns the most recent job, for the single-job field older clients
// read. The queue is what the panel itself shows.
func (d *Downloader) Status() (Job, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.jobs) == 0 {
		return Job{}, false
	}
	return *d.jobs[len(d.jobs)-1].pub, true
}

// Cancel stops one download by id. Cancelling a finished one is an error
// rather than a no-op: the button that sends it is only drawn on a live job,
// so a request for a finished one means the page is looking at something the
// panel no longer agrees with.
func (d *Downloader) Cancel(id string) error {
	d.mu.Lock()
	var found *job
	for _, entry := range d.jobs {
		if entry.pub.ID == id {
			found = entry
			break
		}
	}
	if found == nil {
		d.mu.Unlock()
		return fmt.Errorf("%w: 没有编号为 %s 的下载", ErrNotFound, id)
	}
	if !found.pub.State.Active() {
		d.mu.Unlock()
		return fmt.Errorf("%w: 这个下载已经结束了", ErrCancelled)
	}
	// A queued job has no worker to interrupt, so it is finished here and now.
	// Leaving it for dispatch to notice would mean a cancelled download that
	// still runs the moment a slot opens.
	if found.pub.State == JobQueued {
		now := time.Now()
		found.pub.State = JobCancelled
		found.pub.Error = ErrCancelled.Error()
		found.pub.FinishedAt = &now
		d.mu.Unlock()
		return nil
	}
	cancel := found.cancel
	d.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	return nil
}

// CancelAll stops everything still queued or running, and reports how many.
// The queue page's one-click way out of a bulk action that turned out to be
// the wrong bulk action.
func (d *Downloader) CancelAll() int {
	d.mu.Lock()
	now := time.Now()
	cancels := make([]context.CancelFunc, 0, d.active)
	stopped := 0
	for _, entry := range d.jobs {
		switch entry.pub.State {
		case JobQueued:
			entry.pub.State = JobCancelled
			entry.pub.Error = ErrCancelled.Error()
			entry.pub.FinishedAt = &now
			stopped++
		case JobDownloading:
			if entry.cancel != nil {
				cancels = append(cancels, entry.cancel)
			}
			stopped++
		}
	}
	d.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
	return stopped
}

// ClearFinished forgets the history and reports how many rows went. What is
// still queued or running stays — this clears a record, it does not stop work.
func (d *Downloader) ClearFinished() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	kept := make([]*job, 0, len(d.jobs))
	for _, entry := range d.jobs {
		if entry.pub.State.Active() {
			kept = append(kept, entry)
		}
	}
	dropped := len(d.jobs) - len(kept)
	d.jobs = kept
	return dropped
}

// Close cancels everything in flight and waits briefly for the workers to
// unwind, so panel shutdown does not leave a writer racing against the process
// exit.
func (d *Downloader) Close() {
	d.mu.Lock()
	d.closed = true
	now := time.Now()
	cancels := make([]context.CancelFunc, 0, d.active)
	for _, entry := range d.jobs {
		switch entry.pub.State {
		case JobQueued:
			// Nothing will pick these up again, and leaving them as "queued"
			// would be the panel claiming work it is not going to do.
			entry.pub.State = JobCancelled
			entry.pub.Error = ErrCancelled.Error()
			entry.pub.FinishedAt = &now
		case JobDownloading:
			if entry.cancel != nil {
				cancels = append(cancels, entry.cancel)
			}
		}
	}
	running := d.active > 0
	d.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
	if !running {
		return
	}

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
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
