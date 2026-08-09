package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"
)

// CheckInterval is how often the panel asks GitHub about new releases. The API
// allows 60 unauthenticated requests an hour per address; this is far below it
// and still notices a release the same day.
const CheckInterval = 6 * time.Hour

// Phase is what the updater is currently doing.
type Phase string

const (
	PhaseIdle     Phase = "idle"
	PhaseChecking Phase = "checking"
	// PhaseDownloading is the first half of an update: the release is coming
	// down and the servers are being stopped, at the same time.
	PhaseDownloading Phase = "downloading"
	// PhaseStopping is that same half with the download already finished — all
	// that is left is waiting for the last world to save.
	PhaseStopping   Phase = "stopping"
	PhaseInstalling Phase = "installing"
	PhaseRestarting Phase = "restarting"
)

// ErrBusy means an update is already running.
var ErrBusy = errors.New("an update is already in progress")

// Shutdown is how far the "stop every server" half of an update has got. Zero
// total means there was nothing running to stop.
type Shutdown struct {
	Total   int `json:"total"`
	Stopped int `json:"stopped"`
	// Pending names the servers still saving, so the UI can say which one the
	// update is waiting on rather than only how many.
	Pending []string `json:"pending,omitempty"`
}

// Hooks lets the panel take part in an update without this package knowing
// anything about instances or HTTP.
type Hooks struct {
	// StopServers records which servers are running — so they can be brought
	// back on the other side — and then stops them all gracefully, returning
	// once the last one is down. It runs *beside* the download rather than
	// after it: the two waits are independent, and a world that takes a minute
	// to save can spend that minute overlapping the transfer instead of after
	// it. Returning an error abandons the update with nothing replaced.
	//
	// report, when the panel calls it, updates Status.Shutdown.
	StopServers func(ctx context.Context, report func(Shutdown)) error

	// ServersAborted undoes StopServers for an update that failed after it ran:
	// the panel is not restarting after all, so servers stopped for it should
	// come back rather than stay down, and the recorded resume list has to go
	// with them — otherwise it would outlive this update and start those
	// servers on some later, unrelated restart.
	ServersAborted func()

	// TriggerRestart asks the panel to shut down and then exec the newly
	// installed binary, whose path it is given. By the time it runs the servers
	// are already down. It must not block, and it must use that path rather
	// than asking the OS for the running executable; see Staged.Target.
	TriggerRestart func(binary string)
}

// Status is a snapshot of what the updater knows, safe to serialise to the UI.
type Status struct {
	CurrentVersion  string     `json:"currentVersion"`
	LatestVersion   string     `json:"latestVersion,omitempty"`
	UpdateAvailable bool       `json:"updateAvailable"`
	ReleaseURL      string     `json:"releaseUrl,omitempty"`
	ReleaseNotes    string     `json:"releaseNotes,omitempty"`
	PublishedAt     *time.Time `json:"publishedAt,omitempty"`
	CheckedAt       *time.Time `json:"checkedAt,omitempty"`
	CheckError      string     `json:"checkError,omitempty"`
	Phase           Phase      `json:"phase"`
	Progress        int        `json:"progress"`
	Eligible        bool       `json:"eligible"`
	IneligibleWhy   string     `json:"ineligibleWhy,omitempty"`
	Error           string     `json:"error,omitempty"`
	// Mirror is the configured download proxy, "" when downloading straight
	// from GitHub.
	Mirror string `json:"mirror"`
	// Channel is the release channel this panel follows.
	Channel Channel `json:"channel"`
	// CurrentIsSnapshot marks a panel running a snapshot or an rc rather than
	// a release, so the UI can label it. A dev build is not one of these: it
	// has no version at all, which Eligible already reports.
	CurrentIsSnapshot bool `json:"currentIsSnapshot"`
	// LatestIsPrerelease marks the offered version as a snapshot or rc.
	LatestIsPrerelease bool `json:"latestIsPrerelease"`
	// Downgrade means installing the offered version moves backwards; see
	// Updater.Offer for the one case that happens in.
	Downgrade bool `json:"downgrade"`
	// Shutdown tracks the servers being stopped for this update. Nil outside an
	// update, and while one is running it is what the progress bar's second
	// half is made of.
	Shutdown *Shutdown `json:"shutdown,omitempty"`
}

// Service keeps the last check result and runs updates one at a time.
type Service struct {
	up    *Updater
	log   *slog.Logger
	hooks Hooks

	mu        sync.RWMutex
	latest    *Release
	checkedAt time.Time
	checkErr  string
	phase     Phase
	progress  int
	lastErr   string
	shutdown  *Shutdown
}

func NewService(repo, currentVersion, mirror string, channel Channel, hooks Hooks, log *slog.Logger) *Service {
	up := New(repo, currentVersion)
	up.SetMirror(mirror)
	up.SetChannel(channel)
	return &Service{
		up:    up,
		log:   log,
		hooks: hooks,
		phase: PhaseIdle,
	}
}

// SetMirror changes the download proxy. Refused mid-update so a run cannot
// switch source between fetching the checksums and fetching the archive.
func (s *Service) SetMirror(mirror string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != PhaseIdle {
		return ErrBusy
	}
	s.up.SetMirror(mirror)
	return nil
}

// SetChannel changes which releases the panel is offered, discarding the cached
// check: it describes the other channel, and leaving it in place would show a
// snapshot as "available" to a panel that just asked to stop seeing them. The
// caller is expected to run a Check straight after.
func (s *Service) SetChannel(c Channel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != PhaseIdle {
		return ErrBusy
	}
	if s.up.Channel() == ParseChannel(string(c)) {
		return nil
	}
	s.up.SetChannel(c)
	s.latest = nil
	s.checkedAt = time.Time{}
	s.checkErr = ""
	return nil
}

// Run checks on startup and then on a timer until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	if !IsReleaseVersion(s.up.CurrentVersion()) {
		// A dev build has no release to compare against; skip the polling
		// entirely rather than making pointless requests.
		return
	}
	// A short delay keeps the panel's own start-up — which may be racing a
	// machine boot and its network — off the critical path.
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if _, err := s.Check(ctx); err != nil {
				s.log.Debug("update check failed", "err", err)
			}
			timer.Reset(CheckInterval)
		}
	}
}

// Check queries GitHub and caches the result.
func (s *Service) Check(ctx context.Context) (Status, error) {
	s.mu.Lock()
	if s.phase != PhaseIdle && s.phase != PhaseChecking {
		defer s.mu.Unlock()
		return s.statusLocked(), ErrBusy
	}
	s.phase = PhaseChecking
	s.mu.Unlock()

	rel, err := s.up.Check(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase = PhaseIdle
	s.checkedAt = time.Now()
	if err != nil {
		s.checkErr = err.Error()
		return s.statusLocked(), err
	}
	s.checkErr = ""
	s.latest = rel
	return s.statusLocked(), nil
}

// Status returns the cached view without touching the network.
func (s *Service) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.statusLocked()
}

func (s *Service) statusLocked() Status {
	st := Status{
		CurrentVersion:    s.up.CurrentVersion(),
		Phase:             s.phase,
		Progress:          s.progress,
		CheckError:        s.checkErr,
		Error:             s.lastErr,
		Mirror:            s.up.Mirror(),
		Channel:           s.up.Channel(),
		CurrentIsSnapshot: IsReleaseVersion(s.up.CurrentVersion()) && !IsStableVersion(s.up.CurrentVersion()),
		Eligible:          true,
	}
	if !s.checkedAt.IsZero() {
		t := s.checkedAt
		st.CheckedAt = &t
	}
	if s.shutdown != nil {
		snapshot := *s.shutdown
		st.Shutdown = &snapshot
	}
	// Snapshots carry a real version and stay updatable; this is about builds
	// with no version at all, which a local `go build` produces.
	if !IsReleaseVersion(s.up.CurrentVersion()) {
		st.Eligible = false
		st.IneligibleWhy = "当前运行的不是从 release 或快照装出来的版本（版本号为 " + s.up.CurrentVersion() + "），面板内更新已停用，请手动替换二进制"
	}
	if s.latest == nil {
		return st
	}

	st.LatestVersion = s.latest.Version
	st.ReleaseURL = s.latest.URL
	st.ReleaseNotes = s.latest.Notes
	st.LatestIsPrerelease = s.latest.Prerelease
	if !s.latest.PublishedAt.IsZero() {
		t := s.latest.PublishedAt
		st.PublishedAt = &t
	}
	if st.Eligible && !s.latest.HasAssetForPlatform() {
		st.Eligible = false
		st.IneligibleWhy = fmt.Sprintf("最新版本没有提供 %s/%s 的构建，请手动更新", runtime.GOOS, runtime.GOARCH)
	}
	if st.Eligible {
		st.UpdateAvailable, st.Downgrade = s.up.Offer(s.latest)
	}
	return st
}

// Applying reports whether an update is actually running, as opposed to the
// panel merely asking GitHub what is out there. The API uses it to refuse
// starting a server that this update is in the middle of emptying the machine
// of; a check is no reason to refuse anything.
func (s *Service) Applying() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.phase != PhaseIdle && s.phase != PhaseChecking
}

// Apply downloads and installs the cached latest release, then asks the panel
// to restart into it. It returns once the new binary is in place; the restart
// itself happens asynchronously so the caller can still answer the HTTP request
// that triggered it.
func (s *Service) Apply(ctx context.Context) error {
	s.mu.Lock()
	if s.phase != PhaseIdle {
		s.mu.Unlock()
		return ErrBusy
	}
	st := s.statusLocked()
	if !st.Eligible {
		s.mu.Unlock()
		return errors.New(st.IneligibleWhy)
	}
	if !st.UpdateAvailable {
		s.mu.Unlock()
		return ErrUpToDate
	}
	rel := s.latest
	s.phase = PhaseDownloading
	s.progress = 0
	s.lastErr = ""
	s.shutdown = nil
	s.mu.Unlock()

	if err := s.apply(ctx, rel); err != nil {
		s.mu.Lock()
		s.phase = PhaseIdle
		s.progress = 0
		s.shutdown = nil
		s.lastErr = err.Error()
		s.mu.Unlock()
		s.log.Error("update failed", "version", rel.Version, "err", err)
		return err
	}
	return nil
}

// apply runs an update in two steps.
//
// Step one is the download and the shutdown, at the same time: neither needs
// the other, and the shutdown is the slow half — a big world can take a minute
// to save, which is a minute the transfer may as well spend running. Step two
// starts only once both are finished, because replacing the binary under live
// server processes is exactly what the old order was arranged to avoid: the
// panel is the parent of those processes, and the exec at the end of the
// restart inherits none of them.
//
// If either half fails, nothing is replaced and the servers this update stopped
// are started again — a panel still on the old version with its servers down is
// an outage, not a failed update.
func (s *Service) apply(ctx context.Context, rel *Release) error {
	s.log.Info("update starting", "from", s.up.CurrentVersion(), "to", rel.Version)

	type download struct {
		staged *Staged
		err    error
	}
	downloaded := make(chan download, 1)
	go func() {
		staged, err := s.up.Prepare(ctx, rel, func(done, total int64) {
			if total <= 0 {
				return
			}
			pct := int(done * 100 / total)
			s.mu.Lock()
			s.progress = pct
			s.mu.Unlock()
		})
		if err == nil {
			// The servers may still be saving. Say so rather than leaving the
			// bar sitting at "下载中 100%" for the rest of the wait.
			s.mu.Lock()
			s.progress = 100
			if s.phase == PhaseDownloading {
				s.phase = PhaseStopping
			}
			s.mu.Unlock()
		}
		downloaded <- download{staged, err}
	}()

	stopErr := s.stopServers(ctx)
	got := <-downloaded

	switch {
	case got.err != nil:
		// The archive never arrived, so there is nothing to install. Whatever
		// the shutdown managed has to be undone either way.
		s.resumeServers()
		return got.err
	case stopErr != nil:
		got.staged.Discard()
		s.resumeServers()
		return fmt.Errorf("stop the servers: %w", stopErr)
	}
	staged := got.staged

	s.log.Info("release downloaded and verified", "from", staged.ArchiveURL())
	if staged.ChecksumFromMirror() {
		s.log.Warn("GitHub was unreachable for the checksums, so the mirror supplied them; "+
			"this update's integrity rests on the mirror rather than on GitHub",
			"mirror", s.up.Mirror())
	}

	s.mu.Lock()
	s.phase = PhaseInstalling
	s.progress = 100
	s.mu.Unlock()

	if err := staged.Commit(); err != nil {
		staged.Discard()
		s.resumeServers()
		return err
	}
	s.log.Info("new binary installed", "version", rel.Version)

	s.mu.Lock()
	s.phase = PhaseRestarting
	s.mu.Unlock()

	if s.hooks.TriggerRestart != nil {
		s.hooks.TriggerRestart(staged.Target())
	}
	return nil
}

// stopServers runs the shutdown half of step one, publishing its progress into
// the status the UI polls.
func (s *Service) stopServers(ctx context.Context) error {
	if s.hooks.StopServers == nil {
		return nil
	}
	return s.hooks.StopServers(ctx, func(progress Shutdown) {
		s.mu.Lock()
		snapshot := progress
		s.shutdown = &snapshot
		s.mu.Unlock()
	})
}

// resumeServers brings back what stopServers stopped, for an update that is not
// going to happen after all.
func (s *Service) resumeServers() {
	if s.hooks.ServersAborted != nil {
		s.hooks.ServersAborted()
	}
}
