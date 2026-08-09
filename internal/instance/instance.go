package instance

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrNotRunning is returned when an operation needs a live process.
	ErrNotRunning = errors.New("instance is not running")
	// ErrAlreadyRunning is returned when starting something already up.
	ErrAlreadyRunning = errors.New("instance is already running")
	// ErrBusy is returned while a stop is still in flight.
	ErrBusy = errors.New("instance is busy")
	// errPTYUnsupported is returned by startPTY where there is no pseudo-
	// terminal to be had. It never reaches an operator as an error: a start
	// that hits it falls back to pipes and says so in the console.
	errPTYUnsupported = errors.New("pseudo-terminal unavailable on this platform")
)

// scrollback is how many console lines are retained per instance. This is what
// a client sees when it opens the console for an already-running server.
const scrollback = 2000

// ptyChunk is how much terminal output is read at a time. Big enough that a
// redraw usually arrives whole, small enough to reach the browser promptly.
const ptyChunk = 16 * 1024

// Window size bounds and the size a terminal starts at before any browser has
// said how big it is. A server that never gets a viewport still needs a width
// to wrap its log lines at, and 120x32 is roomier than the 80x24 a server would
// otherwise assume.
const (
	minCols, maxCols = 20, 1000
	minRows, maxRows = 5, 500
	initialCols      = 120
	initialRows      = 32
)

// drainGrace bounds how long a terminal is drained after the server exits.
// Normally the read fails the moment the last holder of the slave side is
// gone; a grandchild that inherited the tty and outlived its parent is what
// this is for.
const drainGrace = 3 * time.Second

// readyPattern matches vanilla/Spigot/Paper's "server is up" line. Until we see
// it the instance stays in "starting", which is what the UI greys the console
// input on.
var readyPattern = regexp.MustCompile(`Done \([0-9.]+s\)!`)

// ansiPattern matches the escape sequences a coloured console emits: CSI
// sequences (colour, cursor moves) and OSC strings (window titles).
var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b[@-Z\\\\-_]")

// stripANSI removes the escape sequences from a line so patterns can be
// matched against what a human would read. Now that the panel asks servers to
// colour their output, the readiness line arrives wrapped in them.
func stripANSI(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	return ansiPattern.ReplaceAllString(s, "")
}

// Instance supervises a single Minecraft server process.
//
// The process is owned by the panel daemon, not by any HTTP request: closing
// the browser, or every websocket disconnecting, has no effect on it. Console
// output keeps flowing into the ring buffer regardless of who is watching.
type Instance struct {
	log *slog.Logger

	mu        sync.RWMutex
	cfg       Config
	state     State
	stateMsg  string
	stateRev  uint64
	startedAt time.Time
	exitCode  *int
	run       *runningProc

	// Restart bookkeeping, guarded by mu.
	manualStop      bool
	restartAttempts int
	restartTimer    *time.Timer
	closed          bool

	ring   *ring
	broker *broker
	// bytes is the terminal scrollback, used instead of ring's lines by a
	// TTY-backed console. Both are kept: the line view still backs the logs
	// API, the readiness check and anything else that needs to read the
	// server's output rather than draw it.
	bytes *byteRing
	// viewports is the window size each attached console reports, guarded by
	// mu. The terminal is sized to the smallest of them, so no viewer sees
	// output wrapped for a window wider than their own.
	viewports map[chan Event]viewport
}

// viewport is one attached console's window size, in character cells.
type viewport struct{ cols, rows uint16 }

// runningProc holds the handles that only exist while a process is alive.
type runningProc struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	// pty is the master side of the pseudo-terminal, or nil when this process
	// was started on pipes. It doubles as stdin above.
	pty *os.File
	// codec is pinned at start: changing the instance's encoding must not
	// reinterpret a stream mid-flight.
	codec *codec
	done  chan struct{} // closed once the process has been reaped
	// ready latches once the readiness line has been seen, so the pattern is
	// not matched against every line for the rest of the server's life.
	ready atomic.Bool
}

func New(cfg Config, logger *slog.Logger) (*Instance, error) {
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Instance{
		log:       logger.With("instance", cfg.ID, "name", cfg.Name),
		cfg:       cfg,
		state:     StateStopped,
		ring:      newRing(scrollback),
		broker:    newBroker(),
		bytes:     newByteRing(terminalScrollbackBytes),
		viewports: make(map[chan Event]viewport),
	}, nil
}

// ---------------------------------------------------------------- accessors

func (i *Instance) ID() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.cfg.ID
}

func (i *Instance) Config() Config {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.cfg
}

func (i *Instance) State() State {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.state
}

func (i *Instance) Status() Status {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return Status{
		Config:       i.cfg,
		StateInfo:    i.stateInfoLocked(),
		LastSeq:      i.ring.lastSeq(),
		TTYSupported: ptySupported,
	}
}

// TTYSupported reports whether this build can back a console with a
// pseudo-terminal at all. Where it is false, every instance runs on pipes
// whatever its config says.
func TTYSupported() bool { return ptySupported }

// UsesTTY reports which console protocol this instance speaks: raw terminal
// bytes, or lines.
//
// It follows the configuration and the platform, not whatever the current
// process ended up with. A start that had to fall back to pipes still feeds the
// terminal view — the pipe output is rendered into it — so that a console does
// not change protocol underneath a connected browser.
func (i *Instance) UsesTTY() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.cfg.wantsTTY()
}

func (i *Instance) stateInfoLocked() StateInfo {
	info := StateInfo{Rev: i.stateRev, State: i.state, ExitCode: i.exitCode, Message: i.stateMsg}
	if i.run != nil && i.run.cmd.Process != nil {
		info.PID = i.run.cmd.Process.Pid
		info.TTYActive = i.run.pty != nil
	}
	if !i.startedAt.IsZero() && i.state.Running() {
		started := i.startedAt
		info.StartedAt = &started
	}
	return info
}

// Attachment is everything a newly connected console needs: the live stream,
// and the past to render before it.
type Attachment struct {
	Events chan Event
	// TTY says which of the two below is populated, and which protocol the
	// caller should speak for the life of the connection.
	TTY bool
	// Lines is the scrollback of a pipe console.
	Lines []Line
	// Terminal is the scrollback of a TTY console: raw bytes to replay into a
	// terminal emulator.
	Terminal []byte
	State    StateInfo
}

// Attach subscribes to the console and snapshots the scrollback.
//
// The two happen under the broker's lock, which is also the lock publishing
// takes, so output can be in the snapshot or in the stream but never in both
// and never in neither. Line consoles could get away with less — a duplicated
// line is caught by its sequence number — but terminal bytes carry no such
// identity, and replaying a redraw twice is visible.
func (i *Instance) Attach() Attachment {
	att := Attachment{TTY: i.UsesTTY()}
	att.Events = i.broker.attach(func() {
		if att.TTY {
			att.Terminal = i.bytes.snapshot()
			return
		}
		att.Lines = i.ring.since(0)
	})

	i.mu.RLock()
	att.State = i.stateInfoLocked()
	i.mu.RUnlock()
	return att
}

func (i *Instance) Unsubscribe(ch chan Event) {
	i.broker.unsubscribe(ch)
	i.dropViewport(ch)
}

// SetViewport records how big one attached console's window is and resizes the
// terminal to match the smallest of them.
func (i *Instance) SetViewport(ch chan Event, cols, rows uint16) {
	i.mu.Lock()
	i.viewports[ch] = viewport{cols: clampCols(cols), rows: clampRows(rows)}
	i.applySizeLocked()
	i.mu.Unlock()
}

func (i *Instance) dropViewport(ch chan Event) {
	i.mu.Lock()
	if _, ok := i.viewports[ch]; ok {
		delete(i.viewports, ch)
		i.applySizeLocked()
	}
	i.mu.Unlock()
}

// applySizeLocked pushes the agreed window size onto the pseudo-terminal. The
// caller holds mu.
//
// The size is the minimum over every attached console rather than the last one
// to report: two browsers on one server share a single terminal, and a server
// that wrapped its output for the wider of them would leave the narrower one
// reading ragged text. The last size is kept when nobody is attached, so a
// console that disconnects and comes back does not make the server reflow.
func (i *Instance) applySizeLocked() {
	if i.run == nil || i.run.pty == nil || len(i.viewports) == 0 {
		return
	}
	cols, rows := uint16(maxCols), uint16(maxRows)
	for _, v := range i.viewports {
		cols = min(cols, v.cols)
		rows = min(rows, v.rows)
	}
	if err := resizePTY(i.run.pty, cols, rows); err != nil {
		i.log.Debug("terminal resize failed", "err", err)
	}
}

func clampCols(v uint16) uint16 { return clampSize(v, minCols, maxCols) }
func clampRows(v uint16) uint16 { return clampSize(v, minRows, maxRows) }

func clampSize(v, lo, hi uint16) uint16 {
	switch {
	case v < lo:
		return lo
	case v > hi:
		return hi
	default:
		return v
	}
}

// LinesSince returns buffered console lines newer than the given sequence.
func (i *Instance) LinesSince(after uint64) []Line { return i.ring.since(after) }

// --------------------------------------------------------------- lifecycle

// Start launches the server process. It returns as soon as the process has
// been spawned; readiness is reported asynchronously via state events.
func (i *Instance) Start() error {
	i.mu.Lock()

	if i.closed {
		i.mu.Unlock()
		return errors.New("instance has been removed")
	}
	switch {
	case i.state == StateStopping:
		i.mu.Unlock()
		return ErrBusy
	case i.state.Running():
		i.mu.Unlock()
		return ErrAlreadyRunning
	}
	i.cancelPendingRestartLocked()

	cfg := i.cfg
	if err := os.MkdirAll(cfg.Directory, 0o755); err != nil {
		i.mu.Unlock()
		return fmt.Errorf("create instance directory: %w", err)
	}
	if !cfg.usesCustomCommand() {
		if _, err := os.Stat(filepath.Join(cfg.Directory, cfg.Jar)); err != nil {
			i.mu.Unlock()
			return fmt.Errorf("%w: server jar %q not found in %s", ErrInvalidConfig, cfg.Jar, cfg.Directory)
		}
	}

	size := i.startupSizeLocked()
	spawned, err := spawn(cfg, size, cfg.wantsTTY())

	var fellBack string
	if err != nil && cfg.wantsTTY() {
		// The terminal could not be had — no pty devices in this container, or
		// the allocation is exhausted. A server that starts on pipes is worth
		// far more than one that refuses to start, so retry that way and say
		// what happened; the console keeps its terminal protocol either way.
		i.log.Warn("could not start on a pseudo-terminal, falling back to pipes", "err", err)
		ptyErr := err
		if spawned, err = spawn(cfg, size, false); err == nil {
			fellBack = fmt.Sprintf("no pseudo-terminal available (%v); running on pipes instead", ptyErr)
		}
	}
	if err != nil {
		i.mu.Unlock()
		i.emitSystem(fmt.Sprintf("failed to start: %v", err))
		return fmt.Errorf("start process: %w", err)
	}

	run := spawned.proc
	i.run = run
	i.manualStop = false
	i.exitCode = nil
	i.startedAt = time.Now()
	i.setStateLocked(StateStarting, "")
	i.mu.Unlock()

	if fellBack != "" {
		i.emitSystem(fellBack)
	}
	i.emitSystem(fmt.Sprintf("starting: %s %s (pid %d)",
		spawned.bin, strings.Join(spawned.args, " "), run.cmd.Process.Pid))

	var pumps sync.WaitGroup
	if run.pty != nil {
		pumps.Add(1)
		go i.pumpTerminal(&pumps, run)
	} else {
		pumps.Add(2)
		go i.pump(&pumps, spawned.stdout, StreamStdout, run)
		go i.pump(&pumps, spawned.stderr, StreamStderr, run)
	}
	go i.reap(run, &pumps)

	return nil
}

// startupSizeLocked is the window size a terminal opens at: whatever the
// consoles already watching are showing, so a restart does not make the server
// reflow its output a moment later. The caller holds mu.
func (i *Instance) startupSizeLocked() viewport {
	size := viewport{cols: initialCols, rows: initialRows}
	for _, v := range i.viewports {
		size.cols = min(size.cols, v.cols)
		size.rows = min(size.rows, v.rows)
	}
	return size
}

// spawned is a started process plus the handles the caller needs to drain it.
type spawned struct {
	proc   *runningProc
	bin    string
	args   []string
	stdout io.Reader
	stderr io.Reader
}

// spawn launches the server on a pseudo-terminal or on pipes. The two differ
// in more than plumbing — the JVM is told different things about its console —
// so the choice is made before the command line is built, not after.
func spawn(cfg Config, size viewport, tty bool) (spawned, error) {
	bin, args, err := cfg.commandLine(tty)
	if err != nil {
		return spawned{}, err
	}

	cmd := exec.Command(bin, args...)
	cmd.Dir = cfg.Directory
	cmd.Env = launchEnv()
	if tty {
		cmd.Env = withTerminalEnv(cmd.Env)
	}
	configureProcAttr(cmd, tty)

	cd := newCodec(cfg.Encoding)
	out := spawned{bin: bin, args: args}

	if tty {
		f, err := startPTY(cmd, size.cols, size.rows)
		if err != nil {
			return spawned{}, err
		}
		out.proc = &runningProc{
			cmd: cmd, stdin: f, pty: f, codec: cd, done: make(chan struct{}),
		}
		return out, nil
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return spawned{}, fmt.Errorf("open stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return spawned{}, fmt.Errorf("open stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return spawned{}, fmt.Errorf("open stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return spawned{}, err
	}

	out.proc = &runningProc{cmd: cmd, stdin: stdin, codec: cd, done: make(chan struct{})}
	out.stdout, out.stderr = stdout, stderr
	return out, nil
}

// pump reads one output stream line by line into the ring buffer, converting
// each line to UTF-8 as it goes.
func (i *Instance) pump(wg *sync.WaitGroup, r io.Reader, stream StreamKind, run *runningProc) {
	defer wg.Done()

	scanner := bufio.NewScanner(r)
	// Crash reports and mod loaders can emit very long single lines; the
	// default 64KiB limit would truncate them into a scan error.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		// Decode the raw bytes: scanner.Text() would hand a GBK or Shift_JIS
		// line straight on as if it had been UTF-8 all along.
		text := run.codec.decode(bytes.TrimRight(scanner.Bytes(), "\r"))
		i.publishLine(stream, text)

		if stream == StreamStdout {
			i.checkReady(run, text)
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
		i.log.Debug("console stream ended", "stream", stream, "err", err)
	}
}

// pumpTerminal drains a pseudo-terminal into both console views at once: the
// bytes go to the terminal verbatim, and the lines recovered from them go to
// the ring buffer so the logs API, the readiness check and anything else that
// reads rather than renders keep working.
func (i *Instance) pumpTerminal(wg *sync.WaitGroup, run *runningProc) {
	defer wg.Done()

	dec := newStreamDecoder(run.codec)
	var lines lineSplitter

	for {
		// A fresh buffer per read: the decoded slice can alias it, and it ends
		// up owned by the scrollback.
		buf := make([]byte, ptyChunk)
		n, err := run.pty.Read(buf)
		if n > 0 {
			i.consumeTerminal(run, dec.decode(buf[:n]), &lines)
		}
		if err != nil {
			// EIO on Linux once the server is gone and the slave side has been
			// closed: the normal end of a session, not a fault.
			break
		}
	}

	i.consumeTerminal(run, dec.flush(), &lines)
	if tail, ok := lines.flush(); ok {
		i.recordLine(StreamStdout, tail)
	}
}

// consumeTerminal feeds one decoded chunk to both views.
func (i *Instance) consumeTerminal(run *runningProc, text []byte, lines *lineSplitter) {
	if len(text) == 0 {
		return
	}
	// Lines first: publishBytes hands the buffer to the scrollback, and reading
	// it afterwards would rely on nothing there ever writing to it.
	for _, line := range lines.push(text) {
		i.recordLine(StreamStdout, line)
		i.checkReady(run, line)
	}
	i.publishBytes(text)
}

// checkReady promotes the instance to running on the server's readiness line.
// The pattern is only matched until it hits: on a terminal every line carries
// escape sequences, and stripping them off all of them forever to look for a
// line that has already been seen is pure waste.
func (i *Instance) checkReady(run *runningProc, text string) {
	if run.ready.Load() {
		return
	}
	if !readyPattern.MatchString(stripANSI(text)) {
		return
	}
	run.ready.Store(true)
	i.markReady()
}

func (i *Instance) markReady() {
	i.mu.Lock()
	if i.state != StateStarting {
		i.mu.Unlock()
		return
	}
	i.setStateLocked(StateRunning, "")
	i.mu.Unlock()
}

// reap waits for the process to exit and decides what happens next.
func (i *Instance) reap(run *runningProc, pumps *sync.WaitGroup) {
	// Drain the output before Wait, otherwise Wait closes the pipes from under
	// the scanners and we lose the server's final words — usually the crash
	// cause.
	drained := make(chan struct{})
	go func() {
		pumps.Wait()
		close(drained)
	}()

	if run.pty != nil {
		// A pseudo-terminal only reports end-of-file once nothing holds the
		// slave side open. Usually that is the moment the server exits, but
		// anything it forked and left behind inherited the tty too, and waiting
		// on those forever would strand the instance in "stopping". Hang up
		// instead, which is also what closing the master means.
		select {
		case <-drained:
		case <-time.After(drainGrace):
			i.log.Debug("terminal still held open after exit, hanging up")
			_ = run.pty.Close()
			<-drained
		}
		_ = run.pty.Close()
	} else {
		<-drained
	}

	waitErr := run.cmd.Wait()

	exitCode := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	i.mu.Lock()
	if i.run != run {
		// Superseded by a newer process; nothing to update.
		i.mu.Unlock()
		close(run.done)
		return
	}

	uptime := time.Since(i.startedAt)
	manual := i.manualStop
	i.run = nil
	i.exitCode = &exitCode

	nextState, msg := StateStopped, "stopped by panel"
	switch {
	case manual:
	case exitCode == 0:
		msg = "server exited normally"
	default:
		nextState = StateCrashed
		msg = fmt.Sprintf("server exited unexpectedly (code %d)", exitCode)
	}

	// A process that stayed up a while is a fresh start, not a restart loop.
	if uptime > time.Minute {
		i.restartAttempts = 0
	}
	shouldRestart := !manual && i.cfg.AutoRestart && !i.closed && exitCode != 0
	stateMsg, delay := msg, time.Duration(0)
	if shouldRestart {
		i.restartAttempts++
		if i.restartAttempts > maxRestartAttempts {
			shouldRestart = false
			stateMsg = fmt.Sprintf("%s; auto-restart gave up after %d attempts", msg, maxRestartAttempts)
		} else {
			delay = restartDelay(i.restartAttempts)
		}
	}
	attempt := i.restartAttempts
	i.setStateLocked(nextState, stateMsg)
	i.mu.Unlock()

	close(run.done)
	i.emitSystem(stateMsg)

	if !shouldRestart {
		return
	}

	i.emitSystem(fmt.Sprintf("auto-restart in %s (attempt %d/%d)", delay, attempt, maxRestartAttempts))
	i.mu.Lock()
	i.restartTimer = time.AfterFunc(delay, func() {
		if err := i.Start(); err != nil {
			i.log.Warn("auto-restart failed", "err", err)
			i.emitSystem(fmt.Sprintf("auto-restart failed: %v", err))
		}
	})
	i.mu.Unlock()
}

const maxRestartAttempts = 5

func restartDelay(attempt int) time.Duration {
	d := time.Duration(attempt) * 5 * time.Second
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

func (i *Instance) cancelPendingRestartLocked() {
	if i.restartTimer != nil {
		i.restartTimer.Stop()
		i.restartTimer = nil
	}
}

// Stop asks the server to shut down gracefully and returns immediately. A
// watchdog escalates to SIGTERM and then SIGKILL if the stop command is
// ignored, so a wedged server can never hang the panel.
func (i *Instance) Stop() error {
	i.mu.Lock()
	i.cancelPendingRestartLocked()
	if i.run == nil {
		i.mu.Unlock()
		return ErrNotRunning
	}
	if i.state == StateStopping {
		i.mu.Unlock()
		return ErrBusy
	}
	run := i.run
	stopCmd := i.cfg.StopCommand
	timeout := time.Duration(i.cfg.StopTimeoutSec) * time.Second
	i.manualStop = true
	i.setStateLocked(StateStopping, "graceful stop requested")
	i.mu.Unlock()

	i.emitSystem(fmt.Sprintf("sending %q, waiting up to %s", stopCmd, timeout))
	if _, err := run.stdin.Write(run.codec.encode(stopCmd + "\n")); err != nil {
		// stdin is gone; skip straight to signalling.
		i.log.Warn("stop command could not be written", "err", err)
		i.emitSystem(fmt.Sprintf("could not write stop command (%v), signalling instead", err))
		_ = terminateTree(run.cmd)
	}

	go i.stopWatchdog(run, timeout)
	return nil
}

// stopWatchdog escalates: graceful command -> SIGTERM -> SIGKILL.
func (i *Instance) stopWatchdog(run *runningProc, timeout time.Duration) {
	select {
	case <-run.done:
		return
	case <-time.After(timeout):
	}

	i.emitSystem("stop timed out, sending terminate signal")
	if err := terminateTree(run.cmd); err != nil {
		i.log.Warn("terminate failed", "err", err)
	}

	select {
	case <-run.done:
		return
	case <-time.After(15 * time.Second):
	}

	i.emitSystem("still alive, force killing")
	if err := killTree(run.cmd); err != nil {
		i.log.Warn("kill failed", "err", err)
	}
}

// Kill terminates the process immediately, without a graceful shutdown.
// World data may be lost; the UI labels it accordingly.
func (i *Instance) Kill() error {
	i.mu.Lock()
	i.cancelPendingRestartLocked()
	if i.run == nil {
		i.mu.Unlock()
		return ErrNotRunning
	}
	run := i.run
	i.manualStop = true
	i.setStateLocked(StateStopping, "force kill requested")
	i.mu.Unlock()

	i.emitSystem("force killing process")
	return killTree(run.cmd)
}

// Restart stops the server if needed and starts it again once it is down.
func (i *Instance) Restart() error {
	i.mu.RLock()
	run := i.run
	running := i.state.Running()
	i.mu.RUnlock()

	if !running || run == nil {
		return i.Start()
	}
	if err := i.Stop(); err != nil && !errors.Is(err, ErrBusy) {
		return err
	}

	go func() {
		<-run.done
		// Give the JVM a moment to release the port before rebinding it.
		time.Sleep(2 * time.Second)
		if err := i.Start(); err != nil {
			i.log.Warn("restart failed", "err", err)
			i.emitSystem(fmt.Sprintf("restart failed: %v", err))
		}
	}()
	return nil
}

// SendCommand writes a line to the server's stdin, exactly as typing it into
// the server console would.
func (i *Instance) SendCommand(cmd string) error {
	cmd = strings.TrimRight(cmd, "\r\n")
	if strings.TrimSpace(cmd) == "" {
		return nil
	}

	i.mu.RLock()
	run := i.run
	state := i.state
	i.mu.RUnlock()

	if run == nil || !state.Running() {
		return ErrNotRunning
	}

	// Echo before writing so every connected console shows the command even
	// though only one client typed it. A terminal does its own echoing — the
	// line discipline or the server's own line editor — so echoing there too
	// would show it twice.
	if run.pty == nil {
		i.publishLine(StreamSystem, "> "+cmd)
	}
	if _, err := run.stdin.Write(run.codec.encode(cmd + "\n")); err != nil {
		return fmt.Errorf("write to server stdin: %w", err)
	}
	return nil
}

// SendInput writes raw keystrokes to a terminal-backed server, which is what
// makes the server's own line editor — history, and the tab completion it
// answers from its live command tree — work in the browser.
//
// It is only meaningful on a pseudo-terminal. On pipes there is no line editor
// listening and no echo, so a keystroke has nowhere to go; callers send whole
// commands with SendCommand instead.
func (i *Instance) SendInput(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	i.mu.RLock()
	run := i.run
	state := i.state
	i.mu.RUnlock()

	if run == nil || run.pty == nil || !state.Running() {
		return ErrNotRunning
	}
	if _, err := run.stdin.Write(run.codec.encode(string(data))); err != nil {
		return fmt.Errorf("write to server terminal: %w", err)
	}
	return nil
}

// UpdateConfig replaces the launch/supervision settings. Most changes take
// effect on the next start; the working directory cannot move under a live
// process, so that one is rejected while running.
func (i *Instance) UpdateConfig(next Config) error {
	next.applyDefaults()
	if err := next.validate(); err != nil {
		return err
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	next.ID = i.cfg.ID
	next.CreatedAt = i.cfg.CreatedAt
	if i.state.Running() && next.Directory != i.cfg.Directory {
		return fmt.Errorf("%w: cannot change directory while the server is running", ErrInvalidConfig)
	}
	i.cfg = next
	return nil
}

// Close shuts the instance down for good: no auto-restart, no new
// subscribers. Used when the instance is deleted or the panel exits.
func (i *Instance) Close() {
	i.mu.Lock()
	i.closed = true
	i.cancelPendingRestartLocked()
	i.mu.Unlock()
	i.broker.closeAll()
}

// Wait blocks until the current process (if any) has exited.
func (i *Instance) Wait(timeout time.Duration) {
	i.mu.RLock()
	run := i.run
	i.mu.RUnlock()
	if run == nil {
		return
	}
	select {
	case <-run.done:
	case <-time.After(timeout):
	}
}

// ------------------------------------------------------------------ events

// publishLine records a line and sends it to every attached console, in
// whichever form that console speaks. A terminal console has no line protocol,
// so the line is drawn into its byte stream instead — which is how panel
// notices, and a pipe fallback's output, still reach a browser that attached
// expecting a terminal.
func (i *Instance) publishLine(stream StreamKind, text string) {
	if i.UsesTTY() {
		i.recordLine(stream, text)
		i.publishBytes(renderLine(stream, text))
		return
	}
	line := i.ring.append(stream, text)
	i.broker.publish(Event{Type: EventLine, Line: &line})
}

// recordLine puts a line in the buffer without sending it anywhere. It is what
// the terminal pump uses: those lines have already reached every console as
// bytes, and exist here only so the logs API has something to serve.
func (i *Instance) recordLine(stream StreamKind, text string) {
	i.ring.append(stream, text)
}

// publishBytes appends terminal output to the scrollback and fans it out. Both
// happen under the broker's lock so that an attaching console cannot see a
// chunk in the snapshot and receive it again on the stream.
//
// It takes ownership of chunk.
func (i *Instance) publishBytes(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	i.broker.publishFunc(func() Event {
		i.bytes.append(chunk)
		return Event{Type: EventOutput, Data: chunk}
	})
}

// streamStyle is the SGR prefix a line is drawn in when the panel has to render
// it into a terminal itself. stdout keeps whatever colour the server chose.
var streamStyle = map[StreamKind]string{
	StreamStderr: "\x1b[38;5;203m",
	StreamSystem: "\x1b[38;5;110m",
}

// renderLine turns a line into the bytes a terminal would have shown for it.
func renderLine(stream StreamKind, text string) []byte {
	style := streamStyle[stream]
	if style == "" {
		return []byte(text + "\r\n")
	}
	return []byte(style + text + "\x1b[0m\r\n")
}

// emitSystem records a panel-generated notice in the console.
func (i *Instance) emitSystem(text string) {
	i.publishLine(StreamSystem, "[panel] "+text)
}

// setStateLocked updates the state and notifies subscribers. The caller holds
// mu. Publishing only ever takes the broker's lock and never calls back into
// the instance, so doing it inline is deadlock-free and — unlike deferring it
// to a goroutine — keeps state events ordered.
func (i *Instance) setStateLocked(state State, msg string) {
	if i.state == state && i.stateMsg == msg {
		return
	}
	i.state = state
	i.stateMsg = msg
	i.stateRev++
	info := i.stateInfoLocked()
	i.broker.publish(Event{Type: EventState, State: &info})
}
