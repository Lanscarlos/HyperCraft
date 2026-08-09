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
	PhaseIdle        Phase = "idle"
	PhaseChecking    Phase = "checking"
	PhaseDownloading Phase = "downloading"
	PhaseInstalling  Phase = "installing"
	PhaseRestarting  Phase = "restarting"
)

// ErrBusy means an update is already running.
var ErrBusy = errors.New("an update is already in progress")

// Hooks lets the panel take part in an update without this package knowing
// anything about instances or HTTP.
type Hooks struct {
	// BeforeInstall runs once the new binary is downloaded and verified, just
	// before it is moved into place. The panel uses it to record which servers
	// are running so they can be brought back afterwards. Returning an error
	// aborts the update with nothing replaced.
	BeforeInstall func() error

	// InstallAborted undoes BeforeInstall when the install fails after it ran.
	// Without it the recorded server list would outlive the failed update and
	// start those servers on some later, unrelated restart.
	InstallAborted func()

	// TriggerRestart asks the panel to shut down — stopping managed servers
	// gracefully — and then exec the newly installed binary, whose path it is
	// given. It must not block, and it must use that path rather than asking
	// the OS for the running executable; see Staged.Target.
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
}

func NewService(repo, currentVersion, mirror string, hooks Hooks, log *slog.Logger) *Service {
	up := New(repo, currentVersion)
	up.SetMirror(mirror)
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
		CurrentVersion: s.up.CurrentVersion(),
		Phase:          s.phase,
		Progress:       s.progress,
		CheckError:     s.checkErr,
		Error:          s.lastErr,
		Mirror:         s.up.Mirror(),
		Eligible:       true,
	}
	if !s.checkedAt.IsZero() {
		t := s.checkedAt
		st.CheckedAt = &t
	}
	if !IsReleaseVersion(s.up.CurrentVersion()) {
		st.Eligible = false
		st.IneligibleWhy = "当前运行的不是正式发布版本（版本号为 " + s.up.CurrentVersion() + "），面板内更新已停用，请手动替换二进制"
	}
	if s.latest == nil {
		return st
	}

	st.LatestVersion = s.latest.Version
	st.ReleaseURL = s.latest.URL
	st.ReleaseNotes = s.latest.Notes
	if !s.latest.PublishedAt.IsZero() {
		t := s.latest.PublishedAt
		st.PublishedAt = &t
	}
	if st.Eligible && !s.latest.HasAssetForPlatform() {
		st.Eligible = false
		st.IneligibleWhy = fmt.Sprintf("最新版本没有提供 %s/%s 的构建，请手动更新", runtime.GOOS, runtime.GOARCH)
	}
	st.UpdateAvailable = st.Eligible && s.up.IsNewerThanCurrent(s.latest)
	return st
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
	s.mu.Unlock()

	if err := s.apply(ctx, rel); err != nil {
		s.mu.Lock()
		s.phase = PhaseIdle
		s.progress = 0
		s.lastErr = err.Error()
		s.mu.Unlock()
		s.log.Error("update failed", "version", rel.Version, "err", err)
		return err
	}
	return nil
}

func (s *Service) apply(ctx context.Context, rel *Release) error {
	s.log.Info("update starting", "from", s.up.CurrentVersion(), "to", rel.Version)

	staged, err := s.up.Prepare(ctx, rel, func(done, total int64) {
		if total <= 0 {
			return
		}
		pct := int(done * 100 / total)
		s.mu.Lock()
		s.progress = pct
		s.mu.Unlock()
	})
	if err != nil {
		return err
	}
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

	if s.hooks.BeforeInstall != nil {
		if err := s.hooks.BeforeInstall(); err != nil {
			staged.Discard()
			return fmt.Errorf("prepare for restart: %w", err)
		}
	}
	if err := staged.Commit(); err != nil {
		staged.Discard()
		if s.hooks.InstallAborted != nil {
			s.hooks.InstallAborted()
		}
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
