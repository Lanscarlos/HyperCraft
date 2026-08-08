package instance

import (
	"bufio"
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
	"time"
)

var (
	// ErrNotRunning is returned when an operation needs a live process.
	ErrNotRunning = errors.New("instance is not running")
	// ErrAlreadyRunning is returned when starting something already up.
	ErrAlreadyRunning = errors.New("instance is already running")
	// ErrBusy is returned while a stop is still in flight.
	ErrBusy = errors.New("instance is busy")
)

// scrollback is how many console lines are retained per instance. This is what
// a client sees when it opens the console for an already-running server.
const scrollback = 2000

// readyPattern matches vanilla/Spigot/Paper's "server is up" line. Until we see
// it the instance stays in "starting", which is what the UI greys the console
// input on.
var readyPattern = regexp.MustCompile(`Done \([0-9.]+s\)!`)

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
}

// runningProc holds the handles that only exist while a process is alive.
type runningProc struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	done  chan struct{} // closed once the process has been reaped
}

func New(cfg Config, logger *slog.Logger) (*Instance, error) {
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Instance{
		log:    logger.With("instance", cfg.ID, "name", cfg.Name),
		cfg:    cfg,
		state:  StateStopped,
		ring:   newRing(scrollback),
		broker: newBroker(),
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
	return Status{Config: i.cfg, StateInfo: i.stateInfoLocked(), LastSeq: i.ring.lastSeq()}
}

func (i *Instance) stateInfoLocked() StateInfo {
	info := StateInfo{Rev: i.stateRev, State: i.state, ExitCode: i.exitCode, Message: i.stateMsg}
	if i.run != nil && i.run.cmd.Process != nil {
		info.PID = i.run.cmd.Process.Pid
	}
	if !i.startedAt.IsZero() && i.state.Running() {
		started := i.startedAt
		info.StartedAt = &started
	}
	return info
}

// Subscribe returns a channel of console events plus the sequence number the
// caller should replay from. Always take the snapshot before subscribing so no
// line can slip through between the two.
func (i *Instance) Subscribe() (chan Event, []Line, StateInfo) {
	// Subscribe first, then snapshot: a line published in between shows up in
	// both, and the client de-dupes on Seq. The reverse order would lose it.
	ch := i.broker.subscribe()
	history := i.ring.since(0)

	i.mu.RLock()
	info := i.stateInfoLocked()
	i.mu.RUnlock()
	return ch, history, info
}

func (i *Instance) Unsubscribe(ch chan Event) { i.broker.unsubscribe(ch) }

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
	bin, args, err := cfg.commandLine()
	if err != nil {
		i.mu.Unlock()
		return err
	}
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

	cmd := exec.Command(bin, args...)
	cmd.Dir = cfg.Directory
	configureProcAttr(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		i.mu.Unlock()
		return fmt.Errorf("open stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		i.mu.Unlock()
		return fmt.Errorf("open stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		i.mu.Unlock()
		return fmt.Errorf("open stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		i.mu.Unlock()
		i.emitSystem(fmt.Sprintf("failed to start: %v", err))
		return fmt.Errorf("start process: %w", err)
	}

	run := &runningProc{cmd: cmd, stdin: stdin, done: make(chan struct{})}
	i.run = run
	i.manualStop = false
	i.exitCode = nil
	i.startedAt = time.Now()
	i.setStateLocked(StateStarting, "")
	i.mu.Unlock()

	i.emitSystem(fmt.Sprintf("starting: %s %s (pid %d)", bin, strings.Join(args, " "), cmd.Process.Pid))

	var pumps sync.WaitGroup
	pumps.Add(2)
	go i.pump(&pumps, stdout, StreamStdout)
	go i.pump(&pumps, stderr, StreamStderr)
	go i.reap(run, &pumps)

	return nil
}

// pump reads one output stream line by line into the ring buffer.
func (i *Instance) pump(wg *sync.WaitGroup, r io.Reader, stream StreamKind) {
	defer wg.Done()

	scanner := bufio.NewScanner(r)
	// Crash reports and mod loaders can emit very long single lines; the
	// default 64KiB limit would truncate them into a scan error.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		text := strings.TrimRight(scanner.Text(), "\r")
		i.publishLine(stream, text)

		if stream == StreamStdout && readyPattern.MatchString(text) {
			i.markReady()
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
		i.log.Debug("console stream ended", "stream", stream, "err", err)
	}
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
	// Drain both pipes before Wait, otherwise Wait closes them from under the
	// scanners and we lose the server's final words — usually the crash cause.
	pumps.Wait()
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
	if _, err := io.WriteString(run.stdin, stopCmd+"\n"); err != nil {
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
	// though only one client typed it.
	i.publishLine(StreamSystem, "> "+cmd)
	if _, err := io.WriteString(run.stdin, cmd+"\n"); err != nil {
		return fmt.Errorf("write to server stdin: %w", err)
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

func (i *Instance) publishLine(stream StreamKind, text string) {
	line := i.ring.append(stream, text)
	i.broker.publish(Event{Type: EventLine, Line: &line})
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
