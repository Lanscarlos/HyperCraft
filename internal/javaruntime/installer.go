package javaruntime

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lanscarlos/hypercraft/internal/unpack"

	"github.com/lanscarlos/hypercraft/internal/download"
)

var (
	// ErrBusy is returned while another install is already running.
	ErrBusy = download.ErrBusy
	// ErrExists is returned when that runtime is already on disk.
	ErrExists = errors.New("this java version is already installed")
	// ErrCancelled is recorded on an install the operator stopped.
	ErrCancelled = download.ErrCancelled
	// ErrChecksum is recorded when the archive is not what the distribution
	// published.
	ErrChecksum = download.ErrChecksum
)

// The states an install reports, which are the kernel's under this package's
// long-standing names — what the panel API publishes and its clients switch on.
const (
	JobDownloading = string(download.StateDownloading)
	JobExtracting  = string(download.StateExtracting)
	JobDone        = string(download.StateDone)
	JobFailed      = string(download.StateFailed)
	JobCancelled   = string(download.StateCancelled)
)

// Job is a snapshot of one install, in the shape the panel API has always
// published. The queue itself is internal/download's; this is the projection of
// one of its jobs back into the vocabulary the Java endpoints speak.
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
	State      string     `json:"state"`
	Error      string     `json:"error,omitempty"`
	RuntimeID  string     `json:"runtimeId,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// Meta keys Start writes onto a kernel job so jobOf can rebuild a Job from it.
const (
	metaDistribution = "distribution"
	metaMajor        = "major"
	metaImageType    = "imageType"
	metaAskedSource  = "source"
	metaVersion      = "version"
	metaRuntimeID    = "runtimeId"
)

// jobOf projects a kernel job back into this package's shape.
func jobOf(j download.Job) Job {
	major, _ := strconv.Atoi(j.Meta[metaMajor])
	started := j.QueuedAt
	if j.StartedAt != nil {
		started = *j.StartedAt
	}
	state := string(j.State)
	if j.State == download.StateQueued {
		// New: installs used to hold a single slot. A client that has only ever
		// known four states reads queued as downloading; the panel-wide list
		// shows the real one.
		state = JobDownloading
	}
	// Route is empty until a route answers, and until then the honest thing to
	// show is what the operator asked for.
	source := j.Route
	if source == "" {
		source = j.Meta[metaAskedSource]
	}
	return Job{
		Distribution: j.Meta[metaDistribution],
		Major:        major,
		ImageType:    j.Meta[metaImageType],
		Source:       source,
		Version:      j.Meta[metaVersion],
		FileName:     j.FileName,
		Total:        j.Total,
		Downloaded:   j.Downloaded,
		State:        state,
		Error:        j.Error,
		// Known from the moment the build resolves, which is before the job
		// exists — so the page can link to what is being installed rather than
		// only to what was. Ref is the same id once the install finishes.
		RuntimeID:  cmp.Or(j.Ref, j.Meta[metaRuntimeID]),
		StartedAt:  started,
		FinishedAt: j.FinishedAt,
	}
}

// Installer downloads and unpacks Java runtimes.
//
// The queue it used to own moved to internal/download, shared with every other
// shelf, and with it the single slot went: asking for a second JDK while the
// first is coming down now queues rather than answering 409. What stays here is
// what only this package knows — which distributions exist, how their metadata
// describes a build, and what unpacking one safely means.
type Installer struct {
	client *Client
	store  *Store
	// registry is the operator's own Java paths. It rides on the installer
	// rather than beside it so that the API keeps one nil check for "this
	// panel does Java management" instead of one per feature.
	registry *Registry
	queue    *download.Queue
	log      *slog.Logger
}

func NewInstaller(client *Client, store *Store, registry *Registry, queue *download.Queue, logger *slog.Logger) *Installer {
	return &Installer{client: client, store: store, registry: registry, queue: queue, log: logger}
}

// Client exposes the API client for the metadata handlers.
func (i *Installer) Client() *Client { return i.client }

// Store exposes the runtimes directory.
func (i *Installer) Store() *Store { return i.store }

// Registry is the list of Java paths the operator registered by hand.
func (i *Installer) Registry() *Registry { return i.registry }

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

	// Resolved on the request, not on the worker. An unknown major version and
	// an already-installed runtime are answers a person is waiting for with a
	// dialog open, and the panel's API says them with a 400 and a 409 — the same
	// call cores make, and for the same reason. See serverjar.Downloader.Start.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	release, err := i.client.LatestRelease(ctx, dist, major, imageType, platform)
	if err != nil {
		return Job{}, err
	}
	if err := i.checkNotInstalled(release); err != nil {
		return Job{}, err
	}
	id := installID(release)

	job, err := i.queue.Submit(download.Request{
		Kind:     download.KindJava,
		Title:    DistributionName(dist) + " " + release.Version,
		Subtitle: imageType,
		FileName: release.FileName,
		Total:    release.Size,
		// The digest the distribution's metadata API published, never the one
		// the source serving the file claims. This is what makes the mirrors
		// safe to offer at all.
		Digest:    download.Digest{Algo: "sha256", Value: release.SHA256},
		DedupeKey: id,
		// Beside the runtimes, so the finished archive and the staging
		// directory it unpacks into are on one filesystem.
		TempDir: i.store.Root(),
		Meta: map[string]string{
			metaDistribution: dist,
			metaMajor:        strconv.Itoa(major),
			metaImageType:    imageType,
			metaAskedSource:  source,
			metaVersion:      release.Version,
			metaRuntimeID:    id,
		},
		Attempts: func(_ context.Context, _ *download.Progress) ([]download.Attempt, error) {
			i.log.Info("java install started",
				"dist", dist, "major", major, "image", imageType, "version", release.Version,
				"file", release.FileName, "size", release.Size, "source", source)
			return i.client.Attempts(release, source)
		},
		Install: func(ctx context.Context, temp, _ string, pub *download.Progress) (string, error) {
			pub.Extracting()
			if err := i.install(ctx, release, temp); err != nil {
				return "", err
			}
			i.log.Info("java install finished", "runtime", id, "version", release.Version)
			return id, nil
		},
	})
	if err != nil {
		return Job{}, err
	}
	return jobOf(job), nil
}

// install unpacks a verified archive into a staging directory that is only
// renamed into place once a working java is in it — so a half-unpacked runtime
// never shows up in the dropdown.
func (i *Installer) install(ctx context.Context, release Release, temp string) error {
	id := installID(release)
	staging := filepath.Join(i.store.Root(), ".installing-"+id)

	// A staging directory left by an earlier attempt has to go before this one
	// starts: unpacking on top of it would leave the previous run's files mixed
	// into the new runtime, and flatten() would not recognise the layout.
	if err := os.RemoveAll(staging); err != nil {
		return err
	}

	archive, err := os.Open(temp)
	if err != nil {
		return err
	}
	defer archive.Close()

	if err := i.unpack(ctx, staging, release, archive); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	return nil
}

// Jobs returns this shelf's installs, newest first.
func (i *Installer) Jobs() []Job {
	all := i.queue.Jobs()
	out := make([]Job, 0, len(all))
	for _, j := range all {
		if j.Kind == download.KindJava {
			out = append(out, jobOf(j))
		}
	}
	return out
}

// Status returns the current or most recent install job.
func (i *Installer) Status() (Job, bool) {
	jobs := i.Jobs()
	if len(jobs) == 0 {
		return Job{}, false
	}
	return jobs[0], true
}

// Cancel stops every install still going. The button that sends it has never
// named one, because there was only ever one to name.
func (i *Installer) Cancel() error {
	if i.queue.CancelAll(download.KindJava) == 0 {
		return fmt.Errorf("%w: no install is running", ErrCancelled)
	}
	return nil
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
