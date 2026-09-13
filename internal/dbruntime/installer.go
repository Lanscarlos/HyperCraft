package dbruntime

import (
	"cmp"
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/lanscarlos/hypercraft/internal/unpack"

	"github.com/lanscarlos/hypercraft/internal/download"
)

// JobState is where an install has got to.
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
// one of its jobs back into the vocabulary the database endpoints speak.
type Job struct {
	Engine     string     `json:"engine"`
	Version    string     `json:"version"`
	FileName   string     `json:"fileName"`
	Total      int64      `json:"total"`
	Downloaded int64      `json:"downloaded"`
	State      string     `json:"state"`
	Error      string     `json:"error,omitempty"`
	InstallID  string     `json:"installId,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// Meta keys Start writes onto a kernel job so jobOf can rebuild a Job from it.
const (
	metaEngine    = "engine"
	metaVersion   = "version"
	metaInstallID = "installId"
)

// jobOf projects a kernel job back into this package's shape.
func jobOf(j download.Job) Job {
	started := j.QueuedAt
	if j.StartedAt != nil {
		started = *j.StartedAt
	}
	state := string(j.State)
	if j.State == download.StateQueued {
		// New: installs used to hold a single slot. A client that has only ever
		// known five states reads queued as downloading; the panel-wide list
		// shows the real one.
		state = JobDownloading
	}
	return Job{
		Engine:     j.Meta[metaEngine],
		Version:    j.Meta[metaVersion],
		FileName:   j.FileName,
		Total:      j.Total,
		Downloaded: j.Downloaded,
		State:      state,
		Error:      j.Error,
		// Known from the moment the build resolves, which is before the job
		// exists — so the page can name what is being installed rather than
		// only what was.
		InstallID:  cmp.Or(j.Ref, j.Meta[metaInstallID]),
		StartedAt:  started,
		FinishedAt: j.FinishedAt,
	}
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
// The queue it used to own moved to internal/download, shared with every other
// shelf, and with it the single slot went. What stays here is what only this
// package knows: how three vendors describe a build, and what unpacking one
// safely means.
type Installer struct {
	client *Client
	store  *Store
	queue  *download.Queue
	log    *slog.Logger
}

func NewInstaller(client *Client, store *Store, queue *download.Queue, logger *slog.Logger) *Installer {
	return &Installer{client: client, store: store, queue: queue, log: logger}
}

// Client exposes the metadata client for the version handlers.
func (i *Installer) Client() *Client { return i.client }

// Store exposes the engines directory.
func (i *Installer) Store() *Store { return i.store }

// Start resolves a build and queues it.
//
// Resolved on the request, not on the worker: an unknown version and an
// already-installed engine are answers a person is waiting for with a dialog
// open. See serverjar.Downloader.Start for why the plugin shelf differs.
func (i *Installer) Start(engine, version string) (Job, error) {
	if _, err := EngineByID(engine); err != nil {
		return Job{}, err
	}
	platform, err := CurrentPlatform()
	if err != nil {
		return Job{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	release, err := i.client.Resolve(ctx, engine, version, platform)
	if err != nil {
		return Job{}, err
	}
	if err := i.checkNotInstalled(release); err != nil {
		return Job{}, err
	}
	id := installID(release.Engine, release.Version)

	job, err := i.queue.Submit(download.Request{
		Kind:     download.KindDatabase,
		Title:    engineName(engine) + " " + release.Version,
		FileName: release.FileName,
		Total:    release.Size,
		// Whatever this vendor publishes, named by its algorithm: sha256 from
		// MongoDB, sha1 from Maven for PostgreSQL, md5 from Oracle for MySQL.
		// Nothing here gets to choose, and refusing the weak ones would not make
		// those downloads safer — it would make them unchecked.
		Digest:    download.Digest{Algo: release.Algo, Value: release.Checksum},
		DedupeKey: id,
		// Beside the engines, so the archive and the staging directory it
		// unpacks into are on one filesystem.
		TempDir: i.store.Root(),
		Meta: map[string]string{
			metaEngine:    engine,
			metaVersion:   release.Version,
			metaInstallID: id,
		},
		Attempts: func(_ context.Context, _ *download.Progress) ([]download.Attempt, error) {
			i.log.Info("database install started",
				"engine", engine, "version", release.Version,
				"file", release.FileName, "size", release.Size)
			// One route. The three vendors' CDNs have nothing in common with
			// each other, with GitHub or with PaperMC, and no mirror of any of
			// them has been verified — so there is nothing to offer but the
			// origin. The route table has room for one if that ever changes.
			return []download.Attempt{{
				Route: "direct",
				Open:  i.client.Opener(release.URL),
			}}, nil
		},
		Install: func(ctx context.Context, temp, _ string, pub *download.Progress) (string, error) {
			pub.Extracting()
			if err := i.install(ctx, release, temp); err != nil {
				return "", err
			}
			i.log.Info("database install finished", "install", id)
			return id, nil
		},
	})
	if err != nil {
		return Job{}, err
	}
	return jobOf(job), nil
}

// install unpacks a verified archive into a staging directory that is only
// renamed into place once a runnable server is in it — so a half-unpacked
// engine never shows up in the picker.
// engineName is what the panel calls an engine, or its id when this build does
// not know it — a title is not worth failing an install over.
func engineName(id string) string {
	if engine, err := EngineByID(id); err == nil {
		return engine.Name
	}
	return id
}

func (i *Installer) install(ctx context.Context, release Release, temp string) error {
	id := installID(release.Engine, release.Version)
	staging := filepath.Join(i.store.Root(), ".installing-"+id)

	// A staging directory left by an earlier attempt has to go before this one
	// starts: unpacking on top of it would mix the previous run's files into
	// the new install.
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
		if j.Kind == download.KindDatabase {
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

// Cancel stops every install still going.
func (i *Installer) Cancel() error {
	if i.queue.CancelAll(download.KindDatabase) == 0 {
		return fmt.Errorf("%w: no install is running", ErrCancelled)
	}
	return nil
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
