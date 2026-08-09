// Package hostterm runs an interactive shell on the machine the panel is
// installed on and hands it to the caller as a byte stream, so the web UI can
// drive it the way a terminal emulator drives a local shell.
//
// This is deliberately the *host's* shell rather than an SSH client: the panel
// already runs on the machine you would have SSH'd into, so borrowing its
// process tree is both simpler and safer than storing SSH credentials — there
// is no key material to leak, and the shell can never have more privilege than
// the panel process already had.
//
// The flip side is that a shell here is a privilege escalation from "manage
// Minecraft servers" to "own this account", which is why nothing in this
// package runs unless the operator explicitly enables it; see config.Terminal.
package hostterm

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrUnsupported is returned by Start on platforms with no pseudo-terminal.
var ErrUnsupported = errors.New("本机终端在当前系统上不可用")

// ErrTooManySessions is returned when the concurrent session cap is reached. A
// browser that reconnects in a loop would otherwise fork a shell per attempt.
var ErrTooManySessions = errors.New("终端会话数量已达上限")

// DefaultMaxSessions caps concurrently open shells. Terminals are opened by
// hand, one or two at a time; a number this small is only ever hit by a bug or
// by something abusive.
const DefaultMaxSessions = 4

// Window size bounds. xterm reports whatever the browser window allows, so the
// clamp is only there to keep a hostile client from handing the kernel a
// nonsense ioctl.
const (
	MinCols, MaxCols = 2, 1000
	MinRows, MaxRows = 2, 500
)

// killGrace is how long a shell (and anything it spawned) gets to notice the
// hangup before the process group is killed outright.
const killGrace = 3 * time.Second

// Options configures a Service.
type Options struct {
	// Shell overrides the program to run. Empty resolves the login shell.
	Shell string
	// Dir is where a new shell starts. Empty, or missing on disk, falls back
	// to the home directory.
	Dir string
	// MaxSessions caps concurrent shells; zero means DefaultMaxSessions.
	MaxSessions int
	Logger      *slog.Logger
}

// Service starts shells. It holds no state beyond the session count, so one
// instance serves every browser tab.
type Service struct {
	shell string
	dir   string
	max   int
	log   *slog.Logger

	// sessions is every shell currently open, so the cap can be enforced and
	// so revoking the feature can hang up on the ones already running.
	mu       sync.Mutex
	sessions map[*Session]struct{}
}

func New(opts Options) *Service {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	max := opts.MaxSessions
	if max <= 0 {
		max = DefaultMaxSessions
	}
	return &Service{
		shell:    resolveShell(opts.Shell),
		dir:      resolveDir(opts.Dir),
		max:      max,
		log:      log,
		sessions: make(map[*Session]struct{}),
	}
}

// Supported reports whether this build can allocate a pseudo-terminal at all.
// It is false on Windows, where the panel otherwise works fine — so the UI
// asks before offering the feature rather than failing at connect time.
func Supported() bool { return ptySupported }

// Shell is the program a new session will run.
func (s *Service) Shell() string { return s.shell }

// Dir is the working directory a new session starts in.
func (s *Service) Dir() string { return s.dir }

// Session is one running shell plus the pseudo-terminal it is attached to.
//
// It is bound to the connection that opened it: unlike a Minecraft server,
// which the panel owns and keeps alive across browser reloads, a shell you
// opened in a tab dies with that tab. Anything meant to outlive the tab
// belongs in a systemd unit or a tmux session started from inside it.
type Session struct {
	cmd *exec.Cmd
	pty *os.File
	log *slog.Logger

	release   func()
	reaped    chan struct{}
	exitErr   atomic.Pointer[error]
	closeOnce sync.Once
}

// Start launches a shell on a pseudo-terminal sized to the caller's window.
//
// The lock is held across the fork, which serialises the handful of terminals
// a panel ever opens at once. That is the cheap way to make the cap exact: a
// slot reserved and then handed back on failure would be two more states to
// get wrong for no gain at this scale.
func (s *Service) Start(cols, rows uint16) (*Session, error) {
	if !ptySupported {
		return nil, ErrUnsupported
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sessions) >= s.max {
		return nil, ErrTooManySessions
	}

	cmd := exec.Command(s.shell)
	cmd.Dir = s.dir
	cmd.Env = shellEnv()

	f, err := startPTY(cmd, ClampCols(cols), ClampRows(rows))
	if err != nil {
		if errors.Is(err, ErrUnsupported) {
			return nil, ErrUnsupported
		}
		return nil, fmt.Errorf("启动 %s 失败: %w", s.shell, err)
	}

	sess := &Session{
		cmd:    cmd,
		pty:    f,
		log:    s.log,
		reaped: make(chan struct{}),
	}
	sess.release = func() { s.forget(sess) }
	s.sessions[sess] = struct{}{}
	// Reaped here rather than by the caller so a session that ends on its own
	// (the operator typed `exit`) never leaves a zombie behind, whether or not
	// anyone gets around to calling Wait.
	go func() {
		err := cmd.Wait()
		sess.exitErr.Store(&err)
		close(sess.reaped)
	}()

	s.log.Info("terminal session started", "shell", s.shell, "pid", cmd.Process.Pid, "dir", s.dir)
	return sess, nil
}

func (s *Service) forget(sess *Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sess)
}

// Live is the number of shells currently open.
func (s *Service) Live() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

// CloseAll hangs up on every open shell.
//
// This is what turning the feature off has to do to mean anything: a switch
// that only stopped *new* terminals would leave whoever was already connected
// with a live shell, which is precisely the access being revoked.
func (s *Service) CloseAll() int {
	s.mu.Lock()
	open := make([]*Session, 0, len(s.sessions))
	for sess := range s.sessions {
		open = append(open, sess)
	}
	s.mu.Unlock()

	// Closed outside the lock: Close calls back into forget.
	for _, sess := range open {
		_ = sess.Close()
	}
	return len(open)
}

// Read returns terminal output. It fails once the shell has exited and the
// pseudo-terminal has drained, which is how a caller learns the session ended.
func (s *Session) Read(p []byte) (int, error) { return s.pty.Read(p) }

// Write feeds keystrokes to the shell.
func (s *Session) Write(p []byte) (int, error) { return s.pty.Write(p) }

// Resize tells the shell the browser window changed, so full-screen programs
// (top, vim, nano) redraw at the right size and line wrapping stays honest.
func (s *Session) Resize(cols, rows uint16) error {
	return resizePTY(s.pty, ClampCols(cols), ClampRows(rows))
}

// PID is the shell's process id.
func (s *Session) PID() int {
	if s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}

// Wait blocks until the shell exits and reports how it went.
func (s *Session) Wait() error {
	<-s.reaped
	if err := s.exitErr.Load(); err != nil {
		return *err
	}
	return nil
}

// ExitCode is the shell's exit status, or -1 while it is still running.
func (s *Session) ExitCode() int {
	select {
	case <-s.reaped:
	default:
		return -1
	}
	if state := s.cmd.ProcessState; state != nil {
		return state.ExitCode()
	}
	return -1
}

// Close hangs up on the shell, the same way closing an SSH connection does.
//
// Both halves of a real hangup are used, because between them they also reach
// what the shell started: the session leader is signalled directly, which is
// what makes an interactive shell forward SIGHUP to its jobs, and the master
// side of the pseudo-terminal is closed, which is what makes the kernel signal
// whatever holds the tty's foreground process group. Leaving a half-finished
// `apt` running with no way to see it would be worse than ending it.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		signalGroup(s.cmd.Process, hangup)
		_ = s.pty.Close()
		s.release()

		go func() {
			select {
			case <-s.reaped:
			case <-time.After(killGrace):
				// Only reached by something that ignored the hangup. The
				// process has not been reaped yet, so its pid — and with it
				// the process group id — cannot have been reused.
				s.log.Warn("terminal session ignored the hangup, killing it", "pid", s.PID())
				signalGroup(s.cmd.Process, kill)
			}
		}()
	})
	return nil
}

// ClampCols and ClampRows keep a window size inside what the kernel will accept.
func ClampCols(cols uint16) uint16 { return clamp(cols, MinCols, MaxCols) }
func ClampRows(rows uint16) uint16 { return clamp(rows, MinRows, MaxRows) }

func clamp(v, lo, hi uint16) uint16 {
	switch {
	case v < lo:
		return lo
	case v > hi:
		return hi
	default:
		return v
	}
}

// resolveShell picks the program a session runs: the operator's override, then
// the account's login shell, then the shells almost certainly present.
func resolveShell(override string) string {
	if override != "" {
		return override
	}
	if shell := os.Getenv("SHELL"); shell != "" {
		if path, err := exec.LookPath(shell); err == nil {
			return path
		}
	}
	for _, candidate := range defaultShells {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	return fallbackShell
}

// resolveDir picks where a session starts. The caller's suggestion (the panel's
// data directory) wins, because that is where the servers, worlds and logs an
// operator opens a terminal for actually live.
func resolveDir(dir string) string {
	if dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	return string(filepath.Separator)
}

// shellEnv is the panel's own environment plus the two variables a program has
// no way to guess from a socket.
func shellEnv() []string {
	env := make([]string, 0, len(os.Environ())+2)
	var hasLang bool
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "TERM="):
			continue // replaced below
		case strings.HasPrefix(kv, "LANG="), strings.HasPrefix(kv, "LC_ALL="):
			hasLang = true
		}
		env = append(env, kv)
	}
	// xterm.js speaks the full 256-colour xterm dialect, and nothing has told
	// the shell so — a daemon started by systemd has no TERM at all, which
	// leaves less/vim/top refusing to draw.
	env = append(env, "TERM=xterm-256color")
	if !hasLang {
		// Same problem one layer down: with no locale, a shell under systemd
		// treats UTF-8 filenames as bytes and mangles every non-ASCII world
		// name. C.UTF-8 is the safe answer — where it is missing the C library
		// falls back to C, which is what we would have had anyway.
		env = append(env, "LANG=C.UTF-8")
	}
	return env
}

// Interface assertions: a Session is the read/write half of a terminal.
var (
	_ io.ReadWriteCloser = (*Session)(nil)
)
