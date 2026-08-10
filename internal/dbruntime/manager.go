package dbruntime

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Persister writes the service registry. The store package implements it, the
// same way it does for instances.
type Persister interface {
	SaveDatabases([]Service) error
}

// startTimeout is how long a service gets to open its port. Generous because
// the first start of a MySQL service applies the bootstrap SQL, and InnoDB
// creating its redo logs on a slow disk is not fast.
const startTimeout = 90 * time.Second

// stopTimeout is how long a clean shutdown gets before the process is killed.
// A database flushing a large buffer pool is the one thing here worth waiting
// out: killing it means crash recovery on the next start.
const stopTimeout = 60 * time.Second

// logLines is how much of an engine's output is kept in memory. Enough to hold
// the reason a start failed, which is all the panel shows.
const logLines = 200

// settleTime is how long a server has to stay up after opening its port before
// the panel calls it started. See the call site for why an open port is not
// enough on its own.
const settleTime = 2 * time.Second

// Manager owns the database services: the registry, and the processes.
//
// The processes belong to the daemon, not to a request or a browser tab —
// exactly like the Minecraft servers in internal/instance. Closing the page
// must not take a database down underneath the server that is using it.
type Manager struct {
	root  string
	store *Store
	save  Persister
	log   *slog.Logger

	mu       sync.Mutex
	services map[string]*service
	order    []string
}

// service is one entry: its config plus whatever is running for it.
type service struct {
	config Service
	state  State
	since  time.Time
	failed string
	cmd    *exec.Cmd
	// done closes when the process has been reaped, so Stop can wait for it.
	done chan struct{}
	logs *ring
}

// NewManager builds the manager from the persisted registry.
func NewManager(root string, store *Store, save Persister, configs []Service, logger *slog.Logger) (*Manager, error) {
	// 0755, not 0750, and it matters. When the panel runs as root the engine
	// runs as somebody else (see resolveRunAs), and that account has to be able
	// to walk down to its own service directory and to the engine binaries it
	// executes. Only the leaves are private: each service directory is 0750 and
	// owned by the account that runs it, which is where the data and — for
	// MySQL — the bootstrap SQL holding the password actually live.
	if err := os.MkdirAll(filepath.Join(root, "services"), 0o755); err != nil {
		return nil, err
	}
	mgr := &Manager{
		root:     root,
		store:    store,
		save:     save,
		log:      logger,
		services: make(map[string]*service, len(configs)),
	}
	for _, config := range configs {
		mgr.services[config.ID] = &service{
			config: config,
			state:  StateStopped,
			since:  time.Now(),
			logs:   newRing(logLines),
		}
		mgr.order = append(mgr.order, config.ID)
	}
	return mgr, nil
}

// ServicesRoot is where service directories live.
func (m *Manager) ServicesRoot() string { return filepath.Join(m.root, "services") }

// List returns every service with its current state.
func (m *Manager) List() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Status, 0, len(m.services))
	for _, id := range m.order {
		if entry, ok := m.services[id]; ok {
			out = append(out, m.statusLocked(entry))
		}
	}
	return out
}

// Get returns one service.
func (m *Manager) Get(id string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.services[id]
	if !ok {
		return Status{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return m.statusLocked(entry), nil
}

func (m *Manager) statusLocked(entry *service) Status {
	status := Status{
		Service: entry.config,
		State:   entry.state,
		Since:   entry.since,
		Error:   entry.failed,
	}
	if entry.cmd != nil && entry.cmd.Process != nil && entry.state.Live() {
		status.PID = entry.cmd.Process.Pid
	}
	if _, err := m.store.Get(entry.config.InstallID); err != nil {
		status.Missing = true
	}
	return status
}

// Logs returns the engine's recent output.
func (m *Manager) Logs(id string) ([]string, error) {
	m.mu.Lock()
	entry, ok := m.services[id]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return entry.logs.lines(), nil
}

// ------------------------------------------------------------------ create

// CreateOptions is what the API hands over for a new service.
type CreateOptions struct {
	Name      string
	InstallID string
	Database  string
	User      string
	Password  string
	Port      int
	Bind      string
	RunAs     string
	AutoStart bool
}

// Create sets up a new database: a directory, an initialised data directory,
// and a registry entry. It does not start it — that is a separate, visible
// action, and a service whose port turns out to be taken should fail on a
// button press rather than inside creation.
func (m *Manager) Create(ctx context.Context, opts CreateOptions) (Status, error) {
	install, err := m.store.Get(opts.InstallID)
	if err != nil {
		return Status{}, err
	}
	engine, err := EngineByID(install.Engine)
	if err != nil {
		return Status{}, err
	}
	if install.Problem != "" {
		return Status{}, fmt.Errorf("%w: 这个 %s 在本机跑不起来（%s），先解决了再建服务",
			ErrInvalidConfig, engine.Name, install.Problem)
	}

	runAs, err := resolveRunAs(install.Engine, opts.RunAs)
	if err != nil {
		return Status{}, err
	}

	m.mu.Lock()
	taken := make(map[int]bool, len(m.services))
	for _, entry := range m.services {
		taken[entry.config.Port] = true
	}
	id := m.freeIDLocked(install.Engine)
	m.mu.Unlock()

	config := Service{
		ID:        id,
		Name:      strings.TrimSpace(opts.Name),
		Engine:    install.Engine,
		Version:   install.Version,
		InstallID: install.ID,
		Dir:       filepath.Join(m.ServicesRoot(), id),
		Port:      opts.Port,
		Bind:      opts.Bind,
		Database:  opts.Database,
		User:      opts.User,
		Password:  opts.Password,
		RunAs:     runAs,
		AutoStart: opts.AutoStart,
		CreatedAt: time.Now(),
	}
	if config.Name == "" {
		config.Name = engine.Name + " " + install.Version
	}
	if config.Port == 0 {
		config.Port = defaultPort(engine, taken)
	}
	if config.Password == "" && engine.Password {
		if config.Password, err = generatePassword(); err != nil {
			return Status{}, err
		}
	}
	if err := config.validate(); err != nil {
		return Status{}, err
	}

	if _, err := os.Stat(config.Dir); err == nil {
		return Status{}, fmt.Errorf("%w: 目录 %s 已经存在", ErrInvalidConfig, config.Dir)
	}
	if err := os.MkdirAll(config.Dir, 0o750); err != nil {
		return Status{}, err
	}
	if err := initialize(ctx, install, config); err != nil {
		// A half-initialised data directory is worse than none: the engine
		// would refuse to initialise over it and refuse to start on it.
		_ = os.RemoveAll(config.Dir)
		removeAllIfEmpty(m.ServicesRoot())
		return Status{}, err
	}

	m.mu.Lock()
	entry := &service{config: config, state: StateStopped, since: time.Now(), logs: newRing(logLines)}
	m.services[id] = entry
	m.order = append(m.order, id)
	status := m.statusLocked(entry)
	m.mu.Unlock()

	m.persist()
	m.log.Info("database service created",
		"service", id, "engine", config.Engine, "version", config.Version, "port", config.Port)
	return status, nil
}

// freeIDLocked names a new service after its engine, numbering from two.
func (m *Manager) freeIDLocked(engine string) string {
	for n := 1; ; n++ {
		id := engine
		if n > 1 {
			id = fmt.Sprintf("%s-%d", engine, n)
		}
		_, taken := m.services[id]
		if !taken {
			if _, err := os.Stat(filepath.Join(m.ServicesRoot(), id)); os.IsNotExist(err) {
				return id
			}
		}
	}
}

// generatePassword makes one the operator never has to think about. Base64 of
// 18 random bytes: 24 characters, no quotes or backslashes, which is what
// validPassword and the generated SQL both need.
func generatePassword() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	password := base64.RawURLEncoding.EncodeToString(buf)
	return password, nil
}

// ------------------------------------------------------------- start / stop

// Start brings a service up and waits for it to accept connections.
func (m *Manager) Start(id string) error {
	m.mu.Lock()
	entry, ok := m.services[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if entry.state.Live() {
		m.mu.Unlock()
		return ErrAlreadyRunning
	}
	config := entry.config
	entry.state, entry.since, entry.failed = StateStarting, time.Now(), ""
	m.mu.Unlock()

	install, err := m.store.Get(config.InstallID)
	if err != nil {
		m.fail(entry, fmt.Sprintf("这个服务用的引擎 %s 已经不在了，重新安装一次即可", config.InstallID))
		return err
	}

	cmd, err := startCommand(install, config)
	if err != nil {
		m.fail(entry, err.Error())
		return err
	}
	if err := m.attachOutput(entry, cmd, config); err != nil {
		m.fail(entry, err.Error())
		return err
	}
	if err := cmd.Start(); err != nil {
		m.fail(entry, err.Error())
		return fmt.Errorf("启动 %s 失败：%w", config.Name, err)
	}

	done := make(chan struct{})
	m.mu.Lock()
	entry.cmd, entry.done = cmd, done
	m.mu.Unlock()

	go m.reap(entry, cmd, done)

	alive := func() bool {
		select {
		case <-done:
			return false
		default:
			return true
		}
	}
	if err := waitForPort(config.dialAddress(), startTimeout, alive); err != nil {
		// The engine's own last words say far more than "it did not start".
		detail := strings.Join(tail(entry.logs.lines(), 4), " / ")
		_ = terminateTree(cmd)
		m.fail(entry, strings.TrimSpace(err.Error()+"。"+detail))
		return fmt.Errorf("%s 没能启动：%s", config.Name, detail)
	}

	// An open port is not the same as a working server.
	//
	// mysqld binds its listener *before* it reads --init-file, so there is a
	// second or so in which a server that is about to abort over a bad init
	// file answers on the port and looks healthy. Whatever the engine, a
	// process that exits immediately after opening its port has not started,
	// and reporting it as running means the operator goes looking at their
	// plugin config instead of at the reason in the log right here.
	if err := settle(settleTime, alive); err != nil {
		detail := strings.Join(tail(entry.logs.lines(), 4), " / ")
		m.fail(entry, strings.TrimSpace("端口开了之后进程又退出了。"+detail))
		return fmt.Errorf("%s 没能启动：%s", config.Name, detail)
	}

	m.mu.Lock()
	if entry.state == StateStarting {
		entry.state, entry.since = StateRunning, time.Now()
	}
	m.mu.Unlock()

	m.log.Info("database started", "service", id, "engine", config.Engine, "port", config.Port)
	return nil
}

// attachOutput wires the engine's stdout and stderr into the in-memory tail and
// the service's log file. Both streams: MySQL and PostgreSQL write everything
// worth reading to stderr, MongoDB writes it to stdout.
func (m *Manager) attachOutput(entry *service, cmd *exec.Cmd, config Service) error {
	file, err := os.OpenFile(config.LogFile(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	if err := chownTree(config.LogFile(), config.RunAs); err != nil {
		file.Close()
		return err
	}

	pipe, err := cmd.StdoutPipe()
	if err != nil {
		file.Close()
		return err
	}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		file.Close()
		return err
	}

	var pending sync.WaitGroup
	pending.Add(2)
	for _, stream := range []io.Reader{pipe, errPipe} {
		go func(r io.Reader) {
			defer pending.Done()
			scanner := bufio.NewScanner(r)
			scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
			for scanner.Scan() {
				line := scanner.Text()
				entry.logs.add(line)
				_, _ = file.WriteString(line + "\n")
			}
		}(stream)
	}
	go func() {
		pending.Wait()
		file.Close()
	}()
	return nil
}

// reap waits for the process and records why it went away.
func (m *Manager) reap(entry *service, cmd *exec.Cmd, done chan struct{}) {
	err := cmd.Wait()
	close(done)

	m.mu.Lock()
	defer m.mu.Unlock()
	if entry.cmd != cmd {
		// Superseded by a later start; that one owns the state now.
		return
	}
	entry.cmd = nil
	switch {
	case entry.state == StateStopping:
		entry.state, entry.since, entry.failed = StateStopped, time.Now(), ""
	case err != nil:
		entry.state, entry.since = StateFailed, time.Now()
		if entry.failed == "" {
			entry.failed = strings.Join(tail(entry.logs.lines(), 4), " / ")
			if entry.failed == "" {
				entry.failed = err.Error()
			}
		}
		m.log.Warn("database exited unexpectedly",
			"service", entry.config.ID, "err", err, "detail", entry.failed)
	default:
		// A clean exit nobody asked for still means the database is gone.
		entry.state, entry.since = StateStopped, time.Now()
		m.log.Info("database exited", "service", entry.config.ID)
	}
}

func (m *Manager) fail(entry *service, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry.state, entry.since, entry.failed = StateFailed, time.Now(), reason
}

// Stop shuts a service down, cleanly if the engine offers a way and by signal
// otherwise, and kills it if it will not go.
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	entry, ok := m.services[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if !entry.state.Live() || entry.cmd == nil {
		m.mu.Unlock()
		return ErrNotRunning
	}
	config, cmd, done := entry.config, entry.cmd, entry.done
	entry.state, entry.since = StateStopping, time.Now()
	m.mu.Unlock()

	if install, err := m.store.Get(config.InstallID); err == nil {
		if shutdown := stopCommand(install, config); shutdown != nil {
			shutCtx, cancel := context.WithTimeout(context.Background(), stopTimeout)
			if output, err := runWithContext(shutCtx, shutdown); err != nil {
				// Not fatal: the signal below is the fallback, and on Unix it
				// works on all three engines.
				m.log.Info("clean shutdown command failed, falling back to a signal",
					"service", id, "err", err, "output", lastLines(string(output), 2))
			}
			cancel()
		}
	}

	select {
	case <-done:
		m.log.Info("database stopped", "service", id)
		return nil
	case <-time.After(5 * time.Second):
	}

	_ = terminateTree(cmd)
	select {
	case <-done:
		m.log.Info("database stopped", "service", id)
		return nil
	case <-time.After(stopTimeout):
	}

	m.log.Warn("database did not stop in time, killing it", "service", id)
	_ = killTree(cmd)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
	}
	return nil
}

func runWithContext(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
	// exec.Cmd built elsewhere has no context on it; wiring one in after the
	// fact means starting it and killing it here.
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err := <-waited:
		return nil, err
	case <-ctx.Done():
		_ = killTree(cmd)
		return nil, ctx.Err()
	}
}

// ------------------------------------------------------------ update/delete

// UpdateOptions is the subset of a service that can be changed after creation.
// The data directory's own settings — the database name, the account — are not
// among them: changing those means SQL against a running server, which is
// exactly the administration this package stays out of.
type UpdateOptions struct {
	Name      *string
	Port      *int
	Bind      *string
	AutoStart *bool
}

// Update changes a stopped service's settings.
func (m *Manager) Update(id string, opts UpdateOptions) (Status, error) {
	m.mu.Lock()
	entry, ok := m.services[id]
	if !ok {
		m.mu.Unlock()
		return Status{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if entry.state.Live() && (opts.Port != nil || opts.Bind != nil) {
		m.mu.Unlock()
		return Status{}, fmt.Errorf("%w: 改端口或监听地址要先停掉数据库", ErrAlreadyRunning)
	}

	next := entry.config
	if opts.Name != nil {
		next.Name = strings.TrimSpace(*opts.Name)
	}
	if opts.Port != nil {
		next.Port = *opts.Port
	}
	if opts.Bind != nil {
		next.Bind = *opts.Bind
	}
	if opts.AutoStart != nil {
		next.AutoStart = *opts.AutoStart
	}
	if err := next.validate(); err != nil {
		m.mu.Unlock()
		return Status{}, err
	}
	// pg_hba.conf was written to match the bind address at creation time, so a
	// service that becomes reachable from the network has to have it opened.
	reopen := next.Engine == EnginePostgreSQL && !isLoopback(next.Bind) && isLoopback(entry.config.Bind)
	entry.config = next
	status := m.statusLocked(entry)
	m.mu.Unlock()

	if reopen {
		if err := allowPostgresHosts(next); err != nil {
			return Status{}, err
		}
	}
	m.persist()
	return status, nil
}

// Delete removes a service. keepData leaves the directory alone, which is the
// safe answer whenever the operator is not sure.
func (m *Manager) Delete(id string, keepData bool) error {
	m.mu.Lock()
	entry, ok := m.services[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if entry.state.Live() {
		m.mu.Unlock()
		return fmt.Errorf("%w: 先停掉再删除", ErrAlreadyRunning)
	}
	dir := entry.config.Dir
	delete(m.services, id)
	for i, existing := range m.order {
		if existing == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	m.mu.Unlock()

	if !keepData {
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	m.persist()
	m.log.Info("database service deleted", "service", id, "keptData", keepData, "dir", dir)
	return nil
}

// UsersOf names the services running on an engine install, so deleting one is
// an informed decision.
func (m *Manager) UsersOf(installID string) []Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []Status
	for _, id := range m.order {
		if entry, ok := m.services[id]; ok && entry.config.InstallID == installID {
			out = append(out, m.statusLocked(entry))
		}
	}
	return out
}

// ---------------------------------------------------------------- lifecycle

// StartAuto brings up the services marked to start with the panel. Failures are
// logged rather than returned: one database that will not start must not stop
// the panel from booting, and the page shows why.
func (m *Manager) StartAuto() {
	m.mu.Lock()
	var ids []string
	for _, id := range m.order {
		if entry, ok := m.services[id]; ok && entry.config.AutoStart {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()

	for _, id := range ids {
		if err := m.Start(id); err != nil {
			m.log.Warn("could not auto-start a database", "service", id, "err", err)
		}
	}
}

// Close stops every running service. A database killed by the panel exiting
// would recover on the next start, but recovery on a large InnoDB buffer pool
// takes minutes an operator restarting the panel does not expect to wait.
func (m *Manager) Close() {
	m.mu.Lock()
	var ids []string
	for id, entry := range m.services {
		if entry.state.Live() {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()

	for _, id := range ids {
		if err := m.Stop(id); err != nil {
			m.log.Warn("could not stop a database on shutdown", "service", id, "err", err)
		}
	}
}

// Snapshot returns the registry for persisting.
func (m *Manager) Snapshot() []Service {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Service, 0, len(m.services))
	for _, id := range m.order {
		if entry, ok := m.services[id]; ok {
			out = append(out, entry.config)
		}
	}
	sortServices(out)
	return out
}

func (m *Manager) persist() {
	if m.save == nil {
		return
	}
	if err := m.save.SaveDatabases(m.Snapshot()); err != nil {
		m.log.Error("could not persist the database registry", "err", err)
	}
}

// -------------------------------------------------------------------- ring

// ring keeps the last n lines of an engine's output.
type ring struct {
	mu    sync.Mutex
	buf   []string
	limit int
}

func newRing(limit int) *ring { return &ring{limit: limit} }

func (r *ring) add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, line)
	if len(r.buf) > r.limit {
		r.buf = r.buf[len(r.buf)-r.limit:]
	}
}

func (r *ring) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.buf))
	copy(out, r.buf)
	return out
}

func tail(lines []string, n int) []string {
	if len(lines) > n {
		return lines[len(lines)-n:]
	}
	return lines
}
