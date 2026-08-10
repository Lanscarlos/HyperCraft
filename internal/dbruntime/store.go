package dbruntime

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Install is one engine build unpacked on disk.
type Install struct {
	ID      string `json:"id"`
	Engine  string `json:"engine"`
	Version string `json:"version"`
	Path    string `json:"path"`
	// ServerPath is the daemon binary — mysqld, postgres, mongod. Empty means
	// the directory is there but has nothing runnable in it.
	ServerPath  string    `json:"serverPath"`
	Size        int64     `json:"size"`
	InstalledAt time.Time `json:"installedAt"`
	// Problem is what the server binary said when the panel asked it its
	// version, and Hint is what to do about it.
	//
	// This is not a formality. Every one of these tarballs links against system
	// libraries it does not ship — MySQL against libaio, MongoDB against
	// libcurl on some distributions — and a missing one is a dynamic-linker
	// error at exec time, long after the install reported success. Finding out
	// on the page beats finding out from a service that will not start.
	Problem string `json:"problem,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

// Store owns the engine directory, normally <data>/db/engines.
//
// Like the Java store it lists whatever it finds rather than only what the
// panel downloaded, so an operator who unpacks their own build into it gets it
// in the picker for free. The directory name is the record — <engine>-<version>
// — because none of the three ships a machine-readable "what am I" file the way
// every OpenJDK does.
type Store struct {
	root string

	// The health probe forks the server binary, and the overview endpoint is
	// polled once a second while an install runs. A missing system library is
	// not something that changes minute to minute.
	mu      sync.Mutex
	probes  map[string]probeResult
	probeAt time.Duration
}

type probeResult struct {
	problem string
	hint    string
	expires time.Time
}

func NewStore(root string) *Store {
	return &Store{root: root, probes: map[string]probeResult{}, probeAt: 5 * time.Minute}
}

// Root is the directory engines are installed into.
func (s *Store) Root() string { return s.root }

// List returns every installed engine, newest version of each engine first.
func (s *Store) List() ([]Install, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return []Install{}, nil
		}
		return nil, err
	}

	installs := make([]Install, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		install, err := s.inspect(entry.Name())
		if err != nil {
			continue
		}
		installs = append(installs, install)
	}

	sort.Slice(installs, func(a, b int) bool {
		if installs[a].Engine != installs[b].Engine {
			return installs[a].Engine < installs[b].Engine
		}
		return compareVersions(installs[a].Version, installs[b].Version) > 0
	})
	return installs, nil
}

// Get returns one install by id.
func (s *Store) Get(id string) (Install, error) {
	if err := validID(id); err != nil {
		return Install{}, err
	}
	return s.inspect(id)
}

// Remove deletes an install from disk.
func (s *Store) Remove(id string) error {
	if err := validID(id); err != nil {
		return err
	}
	dir := filepath.Join(s.root, id)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}

	s.mu.Lock()
	delete(s.probes, id)
	s.mu.Unlock()
	return nil
}

func (s *Store) inspect(id string) (Install, error) {
	dir := filepath.Join(s.root, id)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return Install{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	engine, version, ok := splitInstallID(id)
	if !ok {
		return Install{}, fmt.Errorf("%w: %s 不像一个引擎目录名", ErrNotFound, id)
	}

	install := Install{
		ID:          id,
		Engine:      engine,
		Version:     version,
		Path:        dir,
		ServerPath:  findBinary(dir, serverBinary(engine)),
		Size:        directorySize(dir),
		InstalledAt: info.ModTime(),
	}
	if install.ServerPath == "" {
		return Install{}, fmt.Errorf("%w: %s 里没有 %s", ErrNotFound, id, serverBinary(engine))
	}
	install.Problem, install.Hint = s.probe(id, install.ServerPath)
	return install, nil
}

// probe runs the server binary to see whether it can start at all, cached.
func (s *Store) probe(id, binary string) (string, string) {
	s.mu.Lock()
	if cached, ok := s.probes[id]; ok && time.Now().Before(cached.expires) {
		s.mu.Unlock()
		return cached.problem, cached.hint
	}
	s.mu.Unlock()

	problem, hint := checkRunnable(binary)

	s.mu.Lock()
	s.probes[id] = probeResult{problem: problem, hint: hint, expires: time.Now().Add(s.probeAt)}
	s.mu.Unlock()
	return problem, hint
}

// checkRunnable asks a server binary its version. Nothing cares what it
// answers; the question is whether it can be loaded at all.
func checkRunnable(binary string) (string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	output, err := exec.CommandContext(ctx, binary, "--version").CombinedOutput()
	if err == nil {
		return "", ""
	}
	message := strings.TrimSpace(string(output))
	if message == "" {
		message = err.Error()
	}
	// One line is enough; some of these print a paragraph.
	if line, _, found := strings.Cut(message, "\n"); found {
		message = line
	}
	return message, runHint(message)
}

// runHint turns a dynamic-linker error into the command that fixes it. These
// are the failures an operator can actually act on, and every one of them is
// otherwise a search-engine trip.
func runHint(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "libaio"):
		// The t64 rename is worth spelling out. Ubuntu 24.04 and Debian 13
		// renamed the package *and* the library file to libaio.so.1t64 for the
		// 64-bit time_t transition, so installing the package leaves MySQL —
		// which is linked against the old name — failing with exactly the same
		// message it did before. Every operator on a current distribution hits
		// this, and the symlink is the whole fix.
		return "缺 libaio。Debian/Ubuntu：apt install libaio1；" +
			"如果提示没有这个包（24.04 及以上），装 libaio1t64，再补一个软链接：" +
			"ln -s /usr/lib/x86_64-linux-gnu/libaio.so.1t64 /usr/lib/x86_64-linux-gnu/libaio.so.1 && ldconfig。" +
			"CentOS/RHEL：yum install libaio。装完刷新这一页即可。"
	case strings.Contains(lower, "libnuma"):
		return "缺 libnuma。Debian/Ubuntu：apt install libnuma1；CentOS/RHEL：yum install numactl-libs。"
	case strings.Contains(lower, "libncurses"), strings.Contains(lower, "libtinfo"):
		return "缺 ncurses。Debian/Ubuntu：apt install libncurses6 libtinfo6；CentOS/RHEL：yum install ncurses-libs。"
	case strings.Contains(lower, "libssl"), strings.Contains(lower, "libcrypto"):
		return "缺 OpenSSL 运行库。Debian/Ubuntu：apt install libssl3（老系统是 libssl1.1）；CentOS/RHEL：yum install openssl-libs。"
	case strings.Contains(lower, "libcurl"):
		return "缺 libcurl。Debian/Ubuntu：apt install libcurl4；CentOS/RHEL：yum install libcurl。"
	case strings.Contains(lower, "glibc_"), strings.Contains(lower, "glibc "):
		return "系统的 glibc 比这个构建要求的旧。换一个更低版本的数据库，或者把系统升级到更新的发行版。"
	case strings.Contains(lower, "no such file or directory"):
		// The loader reports a missing interpreter this way, which on a musl
		// system is what a glibc binary always looks like.
		return "二进制起不来，多半是系统缺共享库或者用的是 musl（Alpine）。" +
			"Alpine 上只有 PostgreSQL 有对应构建。"
	}
	return ""
}

// serverBinary is the daemon each engine is started with.
func serverBinary(engine string) string {
	switch engine {
	case EngineMySQL:
		return "mysqld" + exeSuffix()
	case EnginePostgreSQL:
		return "postgres" + exeSuffix()
	case EngineMongoDB:
		return "mongod" + exeSuffix()
	}
	return ""
}

// findBinary locates an executable inside an install. Everything the panel
// downloads puts them in bin/ once the wrapper directory is stripped; the
// second candidate covers a MySQL zip unpacked by hand without stripping it.
func findBinary(dir, name string) string {
	if name == "" {
		return ""
	}
	for _, candidate := range []string{
		filepath.Join(dir, "bin", name),
		filepath.Join(dir, name),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// installID names the directory a release is installed into, e.g. mysql-8.0.45.
func installID(engine, version string) string {
	return engine + "-" + version
}

// splitInstallID reads one back. The engine id is matched against the known set
// rather than split on the first dash, so a directory called something else
// entirely is ignored instead of being listed as an engine named "postgres".
func splitInstallID(id string) (string, string, bool) {
	for _, engine := range engines {
		prefix := engine.ID + "-"
		if version, found := strings.CutPrefix(id, prefix); found && version != "" {
			return engine.ID, version, true
		}
	}
	return "", "", false
}

func directorySize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil //nolint:nilerr // a partially readable install still reports a useful size
		}
		if info, err := entry.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func validID(id string) error {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`+"\x00") {
		return fmt.Errorf("%w: %q", ErrInvalidID, id)
	}
	return nil
}
