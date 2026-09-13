package plugin

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lanscarlos/hypercraft/internal/download"
)

var (
	// ErrBusy is returned when the queue is full.
	ErrBusy = download.ErrBusy
	// ErrCancelled is recorded on a job the operator stopped.
	ErrCancelled = download.ErrCancelled
)

// Job is one plugin download, in the shape the panel API has always published.
//
// The queue itself is internal/download's now — this is the projection of one
// of its jobs back into the vocabulary the plugin endpoints speak. The fields
// that are not on a kernel job (which plugin, which tag, which asset) ride
// there in Job.Meta, put on by Start, so this package keeps no side table to
// fall out of step with the kernel's history.
type Job struct {
	ID         string `json:"id"`
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
	State      string     `json:"state"`
	Error      string     `json:"error,omitempty"`
	QueuedAt   time.Time  `json:"queuedAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// The states a plugin job reports, which are the kernel's under this package's
// long-standing names. Kept as a vocabulary rather than dropped: they are what
// the panel API publishes and what its clients switch on.
const (
	JobQueued      = string(download.StateQueued)
	JobDownloading = string(download.StateDownloading)
	JobDone        = string(download.StateDone)
	JobFailed      = string(download.StateFailed)
	JobCancelled   = string(download.StateCancelled)
)

// Meta keys Start writes onto a kernel job so jobOf can rebuild a Job from it.
const (
	metaPluginID   = "pluginId"
	metaPluginName = "pluginName"
	metaTag        = "tag"
	metaVersion    = "version"
)

// jobOf projects a kernel job back into this package's shape.
//
// StateExtracting has no counterpart here and never appears: a plugin jar is
// recorded, not unpacked, and that takes microseconds. Should it ever show up,
// reporting it as downloading is the honest answer for a client that has only
// ever known four states.
func jobOf(j download.Job) Job {
	state := string(j.State)
	if j.State == download.StateExtracting {
		state = string(download.StateDownloading)
	}
	return Job{
		ID:         j.ID,
		PluginID:   j.Meta[metaPluginID],
		PluginName: j.Meta[metaPluginName],
		Tag:        j.Meta[metaTag],
		Version:    j.Meta[metaVersion],
		FileName:   j.FileName,
		Mirror:     j.Route,
		Total:      j.Total,
		Downloaded: j.Downloaded,
		State:      state,
		Error:      j.Error,
		QueuedAt:   j.QueuedAt,
		StartedAt:  j.StartedAt,
		FinishedAt: j.FinishedAt,
	}
}

// Downloader fetches plugin releases into the panel-wide library.
//
// The queue it used to own moved to internal/download, shared with every other
// shelf. What stays here is what only this package knows: which repository a
// plugin comes from, whether that repository is private and which token reads
// it, which jar of a multi-platform release was asked for, and what recording a
// finished download in the library means.
type Downloader struct {
	client  *Client
	library *Library
	queue   *download.Queue
	log     *slog.Logger
}

func NewDownloader(client *Client, library *Library, queue *download.Queue, logger *slog.Logger) *Downloader {
	return &Downloader{client: client, library: library, queue: queue, log: logger}
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
// What it does *not* do is resolve the release first. That check used to happen
// here so an unknown tag came back as a bad request rather than as a job that
// failed a second later — but it needs the network, and a queued job may be
// minutes away from its turn. So the only thing answered synchronously is
// whether the plugin is tracked at all; anything upstream has to say lands on
// the job, where the queue page shows it.
func (d *Downloader) Start(pluginID, tag, asset string) (Job, error) {
	item, err := d.library.Get(pluginID)
	if err != nil {
		return Job{}, err
	}
	tag, asset = strings.TrimSpace(tag), strings.TrimSpace(asset)

	// The request as it arrived, not as it resolves. An empty tag means
	// "whatever is newest", and once the release resolves the job says v5.5.71
	// — but a second request for "newest" is still the same request, and
	// matching it against the resolved tag is how the panel ends up downloading
	// the same jar twice.
	key := pluginID + "\x00" + tag + "\x00" + strings.ToLower(asset)

	// Resolved on the worker, not here: a job that waited in the queue may have
	// been sitting there while the operator edited the source or swapped the
	// token it reads with.
	var pinned struct {
		item    Plugin
		release Release
		want    Asset
	}

	job, err := d.queue.Submit(download.Request{
		Kind:      download.KindPlugin,
		Title:     item.Name,
		FileName:  asset,
		DedupeKey: key,
		TempDir:   d.library.Root(),
		Meta: map[string]string{
			metaPluginID:   item.ID,
			metaPluginName: item.Name,
			metaTag:        tag,
			metaVersion:    VersionOf(tag),
		},
		Attempts: func(ctx context.Context, pub *download.Progress) ([]download.Attempt, error) {
			resolved, err := d.library.Get(pluginID)
			if err != nil {
				return nil, err
			}
			// Checked here rather than trusted from the last check: this is the
			// one moment where being wrong about it fails the operation, and a
			// repository that was made private after it was added would
			// otherwise keep failing until someone thought to press "check
			// updates".
			resolved = d.syncVisibility(ctx, resolved)

			release, err := d.resolve(ctx, resolved, tag)
			if err != nil {
				return nil, err
			}
			want, err := pickNamed(release, asset)
			if err != nil {
				return nil, err
			}
			pinned.item, pinned.release, pinned.want = resolved, release, want

			// Until now the row said only which plugin was asked for: "最新" has
			// no file name and no size, and a request pinned to a tag still does
			// not know which jar of it. This is the moment the panel learns.
			pub.Describe(download.Description{
				FileName: want.Name,
				Total:    want.Size,
				Subtitle: release.Version,
				Meta: map[string]string{
					metaTag:     release.Tag,
					metaVersion: release.Version,
				},
			})

			d.log.Info("plugin download started",
				"plugin", resolved.ID, "tag", release.Tag, "file", want.Name, "size", want.Size)
			return d.client.Attempts(resolved.Source, want)
		},
		Install: func(ctx context.Context, temp, sum string, _ *download.Progress) (string, error) {
			if err := d.record(pinned.item, pinned.release, pinned.want, temp, sum); err != nil {
				return "", err
			}
			d.log.Info("plugin download finished", "plugin", pinned.item.ID, "file", pinned.want.Name)
			return pinned.item.ID, nil
		},
	})
	if err != nil {
		return Job{}, err
	}
	return jobOf(job), nil
}

// Jobs returns the queue and the history, newest first. Plugin jobs only: the
// panel-wide list is somewhere else.
func (d *Downloader) Jobs() []Job {
	all := d.queue.Jobs()
	out := make([]Job, 0, len(all))
	for _, j := range all {
		if j.Kind == download.KindPlugin {
			out = append(out, jobOf(j))
		}
	}
	return out
}

// Status returns the most recent job, for the single-job field older clients
// read. The queue is what the panel itself shows.
func (d *Downloader) Status() (Job, bool) {
	jobs := d.Jobs()
	if len(jobs) == 0 {
		return Job{}, false
	}
	return jobs[0], true
}

// Cancel stops one download by id.
func (d *Downloader) Cancel(id string) error { return d.queue.Cancel(id) }

// CancelAll stops every plugin download still queued or running, and reports
// how many. Other shelves' downloads are not this button's business.
func (d *Downloader) CancelAll() int { return d.queue.CancelAll(download.KindPlugin) }

// ClearFinished forgets the history and reports how many rows went.
//
// Panel-wide rather than plugin-only, which is what the kernel offers and what
// the button will mean once the download page lands. Until then it clears a
// little more than the plugin page shows.
func (d *Downloader) ClearFinished() int { return d.queue.ClearFinished() }

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

// record moves a finished jar into place and writes the version into the
// library. All of this used to be the back half of run().
//
// The .part file the kernel hands over is renamed rather than copied, which is
// why Start points TempDir at the library root: the two are on one filesystem.
func (d *Downloader) record(item Plugin, release Release, want Asset, temp, digest string) error {
	slug, err := versionSlug(release.Tag)
	if err != nil {
		return err
	}
	dir := filepath.Join(d.library.Root(), item.ID, slug)
	final := filepath.Join(dir, want.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Re-downloading a version the operator already has is the repair path for
	// a corrupt jar, so the old file is replaced rather than refused — and only
	// here, with the replacement complete and verified on disk.
	_ = os.Remove(final)
	if err := os.Rename(temp, final); err != nil {
		return err
	}

	// The jar is asked what it is, now that it is whole and on disk. This is the
	// identity everything downstream depends on: the upgrade sweep deletes by
	// declared plugin name, and it cannot do that for a jar the panel never
	// opened. A descriptor that will not parse is not an error — the file is
	// still a perfectly good download — it just leaves those fields empty and the
	// panel says so rather than guessing.
	//
	// What the *jar* supports, not what the release does: on a release that ships
	// one build per platform those are different claims, and the one an install
	// has to be judged against is this file's.
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

	if err := d.library.record(item.ID, Version{
		Tag:          release.Tag,
		Version:      release.Version,
		Artifacts:    []Artifact{artifact},
		Prerelease:   release.Prerelease,
		Notes:        release.Notes,
		PublishedAt:  release.PublishedAt,
		AddedAt:      time.Now(),
		GameVersions: release.GameVersions,
		Loaders:      release.Loaders,
	}); err != nil {
		// The jar itself is fine, only its metadata is missing; say so rather
		// than implying the download has to be repeated.
		return fmt.Errorf("下载完成，但记录插件版本失败: %w", err)
	}
	return nil
}
