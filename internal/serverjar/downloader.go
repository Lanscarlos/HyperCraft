package serverjar

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/lanscarlos/hypercraft/internal/download"
)

var (
	// ErrBusy is returned when the queue cannot take the request.
	ErrBusy = download.ErrBusy
	// ErrExists is returned when that exact build is already in the library
	// and the caller did not ask to replace it.
	ErrExists = errors.New("this core is already in the library")
	// ErrCancelled is recorded on a job the operator stopped.
	ErrCancelled = download.ErrCancelled
	// ErrChecksum is recorded when the bytes on disk are not what upstream
	// published. The partial file is removed rather than left to be launched.
	ErrChecksum = download.ErrChecksum
)

// routeSet is the set in internal/download core downloads are routed through.
const routeSet = "papermc"

// The states a core job reports, which are the kernel's under this package's
// long-standing names — what the panel API publishes and its clients switch on.
const (
	JobDownloading = string(download.StateDownloading)
	JobDone        = string(download.StateDone)
	JobFailed      = string(download.StateFailed)
	JobCancelled   = string(download.StateCancelled)
)

// Job is a snapshot of one core download, in the shape the panel API has always
// published.
//
// The queue itself is internal/download's now — this is the projection of one of
// its jobs back into the vocabulary the core endpoints speak. The fields that
// are not on a kernel job ride there in Job.Meta, put on by Start.
type Job struct {
	Project     string `json:"project"`
	ProjectName string `json:"projectName"`
	Version     string `json:"version"`
	Build       int    `json:"build"`
	Channel     string `json:"channel"`
	FileName    string `json:"fileName"`
	// Source is the route that actually served the bytes. New with the mirror:
	// with the automatic order in play, "it downloaded" and "it downloaded from
	// the one you would have picked" are different facts.
	Source     string `json:"source,omitempty"`
	Total      int64  `json:"total"`
	Downloaded int64  `json:"downloaded"`
	State      string `json:"state"`
	Error      string `json:"error,omitempty"`
	// CoreID names the library entry a finished download produced.
	CoreID     string     `json:"coreId,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// Meta keys Start writes onto a kernel job so jobOf can rebuild a Job from it.
const (
	metaProject     = "project"
	metaProjectName = "projectName"
	metaVersion     = "version"
	metaBuild       = "build"
	metaChannel     = "channel"
)

// jobOf projects a kernel job back into this package's shape.
func jobOf(j download.Job) Job {
	build, _ := strconv.Atoi(j.Meta[metaBuild])
	started := j.QueuedAt
	if j.StartedAt != nil {
		started = *j.StartedAt
	}
	state := string(j.State)
	switch j.State {
	case download.StateQueued, download.StateExtracting:
		// Neither has ever appeared in this shelf's API. Queued is new — cores
		// used to hold a single slot — and a core is recorded rather than
		// unpacked, so extracting is over before it can be polled. Reporting
		// both as downloading is the honest answer for a client that has only
		// ever known four states; the panel-wide list shows the real one.
		state = JobDownloading
	}
	return Job{
		Project:     j.Meta[metaProject],
		ProjectName: j.Meta[metaProjectName],
		Version:     j.Meta[metaVersion],
		Build:       build,
		Channel:     j.Meta[metaChannel],
		FileName:    j.FileName,
		Source:      j.Route,
		Total:       j.Total,
		Downloaded:  j.Downloaded,
		State:       state,
		Error:       j.Error,
		CoreID:      j.Ref,
		StartedAt:   started,
		FinishedAt:  j.FinishedAt,
	}
}

// Request describes one download.
type Request struct {
	Project string
	Version string
	// Build is which build to fetch, 0 for the newest.
	//
	// The 添加核心 dialog lists a version's builds and lets one be picked, so
	// "newest" stopped being the only answer. It stays the default because
	// that is what every caller before the dialog meant, and what the creation
	// wizard still means.
	Build     int
	Overwrite bool
}

// Downloader fetches server cores into the panel-wide library.
//
// The queue it used to own moved to internal/download, shared with every other
// shelf, and with it the single slot went: asking for a second core while the
// first is coming down now queues rather than answering 409. What stays here is
// what only this package knows — which projects exist, how PaperMC's metadata
// describes a build, and what recording a finished download in the core library
// means.
type Downloader struct {
	client  *Client
	library *Library
	queue   *download.Queue
	log     *slog.Logger

	// source is the route cores are fetched through, by the id of one of the
	// papermc set's routes or a custom prefix. Held here rather than on the
	// client because it is the operator's standing answer about this machine's
	// network, not a property of the API.
	source string
}

func NewDownloader(client *Client, library *Library, queue *download.Queue, logger *slog.Logger) *Downloader {
	return &Downloader{client: client, library: library, queue: queue, log: logger}
}

// Client exposes the API client for the metadata handlers.
func (d *Downloader) Client() *Client { return d.client }

// Library exposes the directory downloaded cores land in.
func (d *Downloader) Library() *Library { return d.library }

// SetSource chooses the route cores are downloaded through. An unknown id is
// refused rather than quietly turned into the default.
func (d *Downloader) SetSource(id string) error {
	resolved, err := download.ResolveRoute(routeSet, id)
	if err != nil {
		return err
	}
	d.source = resolved
	return nil
}

// Source is the configured route, or download.RouteAuto.
func (d *Downloader) Source() string {
	if d.source == "" {
		return download.RouteAuto
	}
	return d.source
}

// Sources lists the routes a core download can be pointed at, automatic first.
func Sources() []download.Route {
	routes := download.RouteSets[routeSet].Routes
	out := make([]download.Route, 0, len(routes)+1)
	out = append(out, download.Route{
		ID:   download.RouteAuto,
		Name: "自动",
		Note: "按上面的顺序挨个试，哪个通用哪个",
	})
	return append(out, routes...)
}

// Start resolves the newest build and queues it.
//
// Everything that can be reported as a bad request — unknown project, unknown
// version, core already in the library — is checked before the job exists, so
// the operator gets a real error rather than a row that fails a second later.
// That costs a metadata call on the request path, and it is worth it: these are
// answers a person is waiting for with a dialog open, and the panel's API says
// them with a 400 and a 409.
//
// This is where cores and plugins deliberately differ. A plugin request resolves
// on the worker (see plugin.Downloader.Start) because three of them can be
// queued behind each other and the tag they asked for is "newest", which means
// something different by the time their turn comes. A core download is one at a
// time and names its version outright.
func (d *Downloader) Start(req Request) (Job, error) {
	project, ok := LookupProject(req.Project)
	if !ok {
		return Job{}, fmt.Errorf("%w: %s", ErrUnknownProject, req.Project)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	build, err := d.resolve(ctx, req)
	if err != nil {
		return Job{}, err
	}
	if err := d.checkTarget(build.FileName, req.Overwrite); err != nil {
		return Job{}, err
	}

	// Read off the version listing, which is cached, so a core downloaded today
	// can still say what Java it needs on a machine that is offline tomorrow.
	// A failure here is not a failure to download: the row shows 未知 instead.
	javaMin := 0
	if versions, err := d.client.Versions(ctx, project.ID); err == nil {
		for _, version := range versions {
			if version.ID == req.Version {
				javaMin = version.JavaMinimum
				break
			}
		}
	}

	// The metadata always comes from the origin — it is a few kilobytes, and it
	// is what makes a mirror safe to offer, because the digest it carries is
	// what the bytes are checked against whichever route serves them.
	up := download.Origin(build.URL)
	up.Parts = map[string]string{
		"project": project.ID,
		"version": req.Version,
		"build":   strconv.Itoa(build.Build),
	}

	job, err := d.queue.Submit(download.Request{
		Kind:      download.KindCore,
		Title:     project.Name + " " + req.Version,
		Subtitle:  fmt.Sprintf("#%d", build.Build),
		FileName:  build.FileName,
		Total:     build.Size,
		Digest:    download.Digest{Algo: "sha256", Value: build.SHA256},
		DedupeKey: project.ID + "\x00" + req.Version + "\x00" + strconv.Itoa(build.Build),
		TempDir:   d.library.Root(),
		Meta: map[string]string{
			metaProject:     project.ID,
			metaProjectName: project.Name,
			metaVersion:     req.Version,
			metaBuild:       strconv.Itoa(build.Build),
			metaChannel:     build.Channel,
		},
		Attempts: func(_ context.Context, _ *download.Progress) ([]download.Attempt, error) {
			d.log.Info("core download started",
				"project", project.ID, "version", req.Version,
				"build", build.Build, "file", build.FileName, "size", build.Size)
			routes := download.RouteOrder(routeSet, d.Source(), up)
			out := make([]download.Attempt, 0, len(routes))
			for _, route := range routes {
				out = append(out, download.Attempt{
					Route: route.ID,
					Open:  d.client.Opener(route.Link(up)),
				})
			}
			return out, nil
		},
		Install: func(_ context.Context, temp, sum string, _ *download.Progress) (string, error) {
			if err := d.record(build, project, req, javaMin, temp, sum); err != nil {
				return "", err
			}
			d.log.Info("core download finished", "file", build.FileName)
			return build.FileName, nil
		},
	})
	if err != nil {
		return Job{}, err
	}
	return jobOf(job), nil
}

// record moves a verified jar onto its final name and writes it into the
// library. All of this used to be the back half of run().
func (d *Downloader) record(build Build, project Project, req Request, javaMin int, temp, digest string) error {
	root := d.library.Root()
	final := filepath.Join(root, build.FileName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if err := place(temp, final, req.Overwrite); err != nil {
		return err
	}
	if err := d.library.record(Core{
		ID:          build.FileName,
		FileName:    build.FileName,
		Project:     project.ID,
		ProjectName: project.Name,
		Kind:        project.Kind,
		Version:     req.Version,
		Build:       build.Build,
		Channel:     build.Channel,
		SHA256:      digest,
		Size:        build.Size,
		AddedAt:     time.Now(),
		JavaMinimum: javaMin,
		Minecraft:   MinecraftOf(project.ID, req.Version),
	}); err != nil {
		// The jar itself is fine, only its metadata is missing; say so rather
		// than implying the download has to be repeated.
		return fmt.Errorf("下载完成，但记录核心信息失败: %w", err)
	}
	return nil
}

// Jobs returns this shelf's downloads, newest first.
func (d *Downloader) Jobs() []Job {
	all := d.queue.Jobs()
	out := make([]Job, 0, len(all))
	for _, j := range all {
		if j.Kind == download.KindCore {
			out = append(out, jobOf(j))
		}
	}
	return out
}

// Status returns the current or most recent job.
func (d *Downloader) Status() (Job, bool) {
	jobs := d.Jobs()
	if len(jobs) == 0 {
		return Job{}, false
	}
	return jobs[0], true
}

// Cancel stops every core download still going. The button that sends it has
// never named one, because there was only ever one to name.
func (d *Downloader) Cancel() error {
	if d.queue.CancelAll(download.KindCore) == 0 {
		return fmt.Errorf("%w: no download is running", ErrCancelled)
	}
	return nil
}

// resolve looks up the build to fetch: the newest one, or the exact build the
// 添加核心 dialog picked. It uses the job's context rather than the request's,
// so a cancel lands even while metadata is still in flight.
//
// A build the operator chose is fetched even once it has been superseded —
// what was on screen when they pressed the button is what they asked for.
func (d *Downloader) resolve(ctx context.Context, req Request) (Build, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if req.Build <= 0 {
		return d.client.LatestBuild(lookupCtx, req.Project, req.Version)
	}
	builds, err := d.client.Builds(lookupCtx, req.Project, req.Version)
	if err != nil {
		return Build{}, err
	}
	for _, build := range builds {
		if build.Build == req.Build {
			return build, nil
		}
	}
	return Build{}, fmt.Errorf("%w: %s %s has no build #%d",
		ErrUnknownVersion, req.Project, req.Version, req.Build)
}

func (d *Downloader) checkTarget(name string, overwrite bool) error {
	if d.library.Has(name) && !overwrite {
		return fmt.Errorf("%w: %s", ErrExists, name)
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
