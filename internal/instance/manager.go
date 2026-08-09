package instance

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrNotFound is returned for an unknown instance ID.
var ErrNotFound = errors.New("instance not found")

// Persister stores the instance list so it survives a panel restart.
type Persister interface {
	SaveInstances([]Config) error
}

// Manager owns every instance in the panel. It is the single place that knows
// which servers exist, and it outlives any HTTP request or websocket.
type Manager struct {
	log         *slog.Logger
	store       Persister
	serversRoot string

	mu   sync.RWMutex
	byID map[string]*Instance
}

func NewManager(store Persister, serversRoot string, logger *slog.Logger) *Manager {
	return &Manager{
		log:         logger,
		store:       store,
		serversRoot: serversRoot,
		byID:        make(map[string]*Instance),
	}
}

// Load registers previously persisted instances. Invalid entries are skipped
// with a warning rather than taking the whole panel down.
func (m *Manager) Load(configs []Config) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, cfg := range configs {
		inst, err := New(cfg, m.log)
		if err != nil {
			m.log.Warn("skipping unusable instance config", "id", cfg.ID, "err", err)
			continue
		}
		m.byID[cfg.ID] = inst
	}
}

// List returns every instance, ordered by name for a stable UI.
func (m *Manager) List() []*Instance {
	m.mu.RLock()
	out := make([]*Instance, 0, len(m.byID))
	for _, inst := range m.byID {
		out = append(out, inst)
	}
	m.mu.RUnlock()

	sort.Slice(out, func(a, b int) bool {
		return strings.ToLower(out[a].Config().Name) < strings.ToLower(out[b].Config().Name)
	})
	return out
}

func (m *Manager) Get(id string) (*Instance, error) {
	m.mu.RLock()
	inst, ok := m.byID[id]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	return inst, nil
}

// Create registers a new instance. A blank directory is filled in as
// <serversRoot>/<slug>, which is the path most users want.
func (m *Manager) Create(cfg Config) (*Instance, error) {
	cfg.ID = newID()
	cfg.CreatedAt = time.Now()
	cfg.Name = strings.TrimSpace(cfg.Name)

	if strings.TrimSpace(cfg.Directory) == "" {
		dir, err := m.defaultDirectory(cfg.Name)
		if err != nil {
			return nil, err
		}
		cfg.Directory = dir
	} else {
		abs, err := filepath.Abs(cfg.Directory)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
		}
		cfg.Directory = abs
		// Two instances on one directory means two servers writing the same
		// chunks the moment both are started, which corrupts the world rather
		// than merely confusing the panel. Only reachable when a directory is
		// given by hand — importing an existing server, mostly — since the
		// generated ones already skip what is taken.
		if name, taken := m.directoryOwner(abs); taken {
			return nil, fmt.Errorf("%w: 实例「%s」已经在用这个目录了", ErrInvalidConfig, name)
		}
	}

	inst, err := New(cfg, m.log)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.Directory, 0o755); err != nil {
		return nil, fmt.Errorf("create instance directory: %w", err)
	}

	m.mu.Lock()
	m.byID[cfg.ID] = inst
	m.mu.Unlock()

	if err := m.persist(); err != nil {
		return nil, err
	}
	m.log.Info("instance created", "id", cfg.ID, "name", cfg.Name, "dir", cfg.Directory)
	return inst, nil
}

// Update applies new settings to an existing instance and persists them.
func (m *Manager) Update(id string, cfg Config) (*Instance, error) {
	inst, err := m.Get(id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Directory) != "" {
		abs, absErr := filepath.Abs(cfg.Directory)
		if absErr != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, absErr)
		}
		cfg.Directory = abs
	} else {
		cfg.Directory = inst.Config().Directory
	}
	if err := inst.UpdateConfig(cfg); err != nil {
		return nil, err
	}
	if err := m.persist(); err != nil {
		return nil, err
	}
	return inst, nil
}

// Delete removes an instance from the panel. It refuses while the server is
// running: stopping someone's world out from under them should be deliberate.
// deleteFiles additionally removes the instance directory from disk.
func (m *Manager) Delete(id string, deleteFiles bool) error {
	inst, err := m.Get(id)
	if err != nil {
		return err
	}
	if inst.State().Running() {
		return fmt.Errorf("%w: stop the server before deleting it", ErrInvalidConfig)
	}

	m.mu.Lock()
	delete(m.byID, id)
	m.mu.Unlock()
	inst.Close()

	if err := m.persist(); err != nil {
		return err
	}
	if deleteFiles {
		dir := inst.Config().Directory
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove instance directory: %w", err)
		}
		m.log.Info("instance directory removed", "id", id, "dir", dir)
	}
	m.log.Info("instance deleted", "id", id)
	return nil
}

// RunningIDs lists the instances that are currently up. It is taken just
// before a self-update restart so the same servers can be brought back.
func (m *Manager) RunningIDs() []string {
	var ids []string
	for _, inst := range m.List() {
		if inst.State().Running() {
			ids = append(ids, inst.ID())
		}
	}
	return ids
}

// Stopping is one progress report from StopAll: how far the shutdown has got,
// and the names of the servers still saving. The panel shows it during an
// update, where stopping the servers is half the wait.
type Stopping struct {
	Total   int
	Stopped int
	// Pending holds the names of the servers still on their way down, in the
	// same order they are listed in the UI.
	Pending []string
}

// StopAll gracefully stops every running instance and waits for them, leaving
// the instances usable afterwards. It returns the ids it stopped, in list
// order, so the caller can bring exactly those back.
//
// Unlike Shutdown this runs while the panel keeps going — it is what an update
// uses to empty the machine before replacing the binary — so nothing here
// closes an instance: a stopped instance still has its console, its scrollback
// and a working start button, which is what makes an aborted update
// recoverable. report, when given, is called once at the start and then after
// each server goes down.
func (m *Manager) StopAll(timeout time.Duration, report func(Stopping)) []string {
	running := make([]*Instance, 0)
	for _, inst := range m.List() {
		if inst.State().Running() {
			running = append(running, inst)
		}
	}

	total := len(running)
	var (
		mu      sync.Mutex
		stopped int
		pending = make([]string, 0, total)
	)
	for _, inst := range running {
		pending = append(pending, inst.Config().Name)
	}
	publish := func() {
		if report == nil {
			return
		}
		names := make([]string, len(pending))
		copy(names, pending)
		report(Stopping{Total: total, Stopped: stopped, Pending: names})
	}
	publish()

	ids := make([]string, 0, total)
	var wg sync.WaitGroup
	for _, inst := range running {
		ids = append(ids, inst.ID())
		wg.Add(1)
		go func(inst *Instance) {
			defer wg.Done()
			name := inst.Config().Name
			m.log.Info("stopping instance for an update", "name", name)
			if err := inst.Stop(); err != nil && !errors.Is(err, ErrNotRunning) {
				m.log.Warn("graceful stop failed, killing", "name", name, "err", err)
				_ = inst.Kill()
			}
			inst.Wait(timeout)

			mu.Lock()
			defer mu.Unlock()
			stopped++
			for n, other := range pending {
				if other == name {
					pending = append(pending[:n], pending[n+1:]...)
					break
				}
			}
			publish()
		}(inst)
	}
	wg.Wait()
	return ids
}

// StartEach starts the named instances, one after the other, logging whatever
// fails rather than giving up on the rest. It is how servers come back after an
// update that was abandoned partway.
func (m *Manager) StartEach(ids []string) {
	for _, id := range ids {
		inst, err := m.Get(id)
		if err != nil {
			m.log.Warn("cannot restart an instance that no longer exists", "id", id)
			continue
		}
		if inst.State().Running() {
			continue
		}
		if err := inst.Start(); err != nil {
			m.log.Warn("restart after an abandoned update failed", "name", inst.Config().Name, "err", err)
		}
	}
}

// StartAutoStart launches every instance flagged AutoStart, plus any listed in
// resume — servers that were running when the panel restarted itself to
// install an update, whether or not they auto-start on a normal boot.
//
// Called once at panel boot so a machine reboot, or an update, brings the
// servers back by itself.
func (m *Manager) StartAutoStart(resume []string) {
	wanted := make(map[string]bool, len(resume))
	for _, id := range resume {
		wanted[id] = true
	}
	for _, inst := range m.List() {
		cfg := inst.Config()
		if !cfg.AutoStart && !wanted[cfg.ID] {
			continue
		}
		if err := inst.Start(); err != nil {
			m.log.Warn("auto-start failed", "instance", cfg.Name, "err", err)
		}
	}
}

// Shutdown gracefully stops every running instance and waits for them.
//
// The panel is the parent of these processes, so it must not exit while they
// are alive — that would leave servers running with a dead stdin, unreachable
// from the console. Systemd should be given a matching TimeoutStopSec.
func (m *Manager) Shutdown(timeout time.Duration) {
	instances := m.List()

	var wg sync.WaitGroup
	for _, inst := range instances {
		if !inst.State().Running() {
			inst.Close()
			continue
		}
		wg.Add(1)
		go func(inst *Instance) {
			defer wg.Done()
			m.log.Info("stopping instance for shutdown", "name", inst.Config().Name)
			if err := inst.Stop(); err != nil && !errors.Is(err, ErrNotRunning) {
				m.log.Warn("graceful stop failed, killing", "err", err)
				_ = inst.Kill()
			}
			inst.Wait(timeout)
			inst.Close()
		}(inst)
	}
	wg.Wait()
}

// Configs returns the persistable form of every instance.
func (m *Manager) Configs() []Config {
	instances := m.List()
	out := make([]Config, 0, len(instances))
	for _, inst := range instances {
		out = append(out, inst.Config())
	}
	return out
}

func (m *Manager) persist() error {
	if err := m.store.SaveInstances(m.Configs()); err != nil {
		return fmt.Errorf("save instances: %w", err)
	}
	return nil
}

// defaultDirectory picks <serversRoot>/<slug>, adding a numeric suffix if that
// path is already taken by another instance or by files on disk.
func (m *Manager) defaultDirectory(name string) (string, error) {
	base := slugify(name)
	if base == "" {
		base = "server"
	}
	for n := 0; n < 100; n++ {
		candidate := filepath.Join(m.serversRoot, base)
		if n > 0 {
			candidate = filepath.Join(m.serversRoot, fmt.Sprintf("%s-%d", base, n))
		}
		if m.directoryTaken(candidate) {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			continue
		}
		return filepath.Abs(candidate)
	}
	return "", fmt.Errorf("%w: could not find a free directory for %q", ErrInvalidConfig, name)
}

func (m *Manager) directoryTaken(dir string) bool {
	_, taken := m.directoryOwner(dir)
	return taken
}

// directoryOwner names the instance already using dir, if any.
func (m *Manager) directoryOwner(dir string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, inst := range m.byID {
		cfg := inst.Config()
		if cfg.Directory == dir {
			return cfg.Name, true
		}
	}
	return "", false
}

// unsafeInPath matches characters that are illegal or awkward in a path on
// Linux or Windows. Everything else — including CJK — is kept, so an instance
// named 生存服 gets a directory called 生存服 rather than "server-3".
// Whitespace controls (\t\n\v\f\r) are left in so Fields below can collapse
// them into a single separator instead of gluing words together.
var unsafeInPath = regexp.MustCompile(`[\x00-\x08\x0e-\x1f/\\:*?"<>|]+`)

func slugify(s string) string {
	s = unsafeInPath.ReplaceAllString(strings.TrimSpace(s), "")
	s = strings.Join(strings.Fields(s), "-") // collapse whitespace runs
	s = strings.Trim(s, "-. ")

	// "." and ".." would resolve to the servers root or its parent.
	if s == "" || s == "." || s == ".." {
		return ""
	}
	return s
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is unrecoverable; a time-based ID would silently
		// risk collisions, so surface it loudly instead.
		panic(fmt.Sprintf("hypercraft: crypto/rand unavailable: %v", err))
	}
	return hex.EncodeToString(b[:])
}
