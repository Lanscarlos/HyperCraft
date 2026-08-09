//go:build !windows

package instance

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeServer writes a shell script that behaves like a Minecraft server:
// it announces readiness the way vanilla does, echoes console commands, and
// shuts down on "stop". Using it keeps these tests free of a JVM.
func fakeServer(t *testing.T, dir, body string) []string {
	t.Helper()

	script := filepath.Join(dir, "fake-server.sh")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake server: %v", err)
	}
	return []string{"/bin/sh", script}
}

const wellBehavedServer = `#!/bin/sh
echo "[12:00:00] [Server thread/INFO]: Starting minecraft server version 1.21"
echo "[12:00:01] [Server thread/INFO]: Done (1.234s)! For help, type \"help\""
while IFS= read -r line; do
  case "$line" in
    stop) echo "[12:00:09] [Server thread/INFO]: Stopping server"; exit 0 ;;
    *)    echo "[12:00:05] [Server thread/INFO]: ran $line" ;;
  esac
done
`

// newTestInstance builds an instance with the shipped defaults, which means it
// runs on a pseudo-terminal.
func newTestInstance(t *testing.T, body string) *Instance {
	t.Helper()
	return newInstanceWith(t, body, nil)
}

// newPipeInstance builds one with the terminal turned off.
func newPipeInstance(t *testing.T, body string) *Instance {
	t.Helper()
	off := false
	return newInstanceWith(t, body, &off)
}

func newInstanceWith(t *testing.T, body string, tty *bool) *Instance {
	t.Helper()

	dir := t.TempDir()
	inst, err := New(Config{
		ID:        "test",
		Name:      "test",
		Directory: dir,
		TTY:       tty,
		Command:   fakeServer(t, dir, body),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		_ = inst.Kill()
		inst.Close()
	})
	return inst
}

// terminalScrollback is what a console attaching right now would replay.
func terminalScrollback(inst *Instance) []byte {
	att := inst.Attach()
	defer inst.Unsubscribe(att.Events)
	return att.Terminal
}

func waitForTerminal(t *testing.T, inst *Instance, substr string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(string(terminalScrollback(inst)), substr) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for terminal output containing %q\nterminal:\n%s\nconsole:\n%s",
		substr, terminalScrollback(inst), consoleDump(inst))
}

func waitForState(t *testing.T, inst *Instance, want State) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got := inst.State(); got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for state %q, still %q\nconsole:\n%s",
		want, inst.State(), consoleDump(inst))
}

func waitForLine(t *testing.T, inst *Instance, substr string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range inst.LinesSince(0) {
			if strings.Contains(line.Text, substr) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for a console line containing %q\nconsole:\n%s",
		substr, consoleDump(inst))
}

func consoleDump(inst *Instance) string {
	var b strings.Builder
	for _, line := range inst.LinesSince(0) {
		b.WriteString("  " + string(line.Stream) + " | " + line.Text + "\n")
	}
	return b.String()
}

func TestLifecycle(t *testing.T) {
	inst := newTestInstance(t, wellBehavedServer)

	if err := inst.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForState(t, inst, StateRunning)

	if err := inst.SendCommand("say hello"); err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	waitForLine(t, inst, "ran say hello")

	if err := inst.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitForState(t, inst, StateStopped)

	status := inst.Status()
	if status.ExitCode == nil || *status.ExitCode != 0 {
		t.Errorf("expected a clean exit code, got %v", status.ExitCode)
	}
	if status.PID != 0 {
		t.Errorf("PID should be cleared once stopped, got %d", status.PID)
	}
}

// The readiness line is what flips "starting" to "running"; without it the
// console input stays disabled even though the server is up.
func TestStaysStartingUntilReadyLine(t *testing.T) {
	inst := newTestInstance(t, `#!/bin/sh
echo "[12:00:00] [Server thread/INFO]: Loading libraries, please wait..."
sleep 0.4
echo "[12:00:01] [Server thread/INFO]: Done (0.4s)! For help, type \"help\""
while IFS= read -r line; do :; done
`)

	if err := inst.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForLine(t, inst, "Loading libraries")
	if got := inst.State(); got != StateStarting {
		t.Errorf("expected %q before the ready line, got %q", StateStarting, got)
	}
	waitForState(t, inst, StateRunning)
}

// The panel asks servers to colour their output, which wraps the readiness
// line in escape codes. Missing it would leave the console input greyed out on
// a server that is up and running.
func TestReadyLineIsFoundThroughColourCodes(t *testing.T) {
	inst := newTestInstance(t, `#!/bin/sh
printf '\033[32m[12:00:01 INFO]: Done (1.234s)! For help, type "help"\033[m\n'
while IFS= read -r line; do :; done
`)

	if err := inst.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForState(t, inst, StateRunning)
}

func TestUnexpectedExitIsReportedAsCrashed(t *testing.T) {
	inst := newTestInstance(t, `#!/bin/sh
echo "[12:00:00] [main/ERROR]: Failed to load eula.txt" >&2
exit 1
`)

	if err := inst.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForState(t, inst, StateCrashed)

	status := inst.Status()
	if status.ExitCode == nil || *status.ExitCode != 1 {
		t.Errorf("expected exit code 1, got %v", status.ExitCode)
	}

	// The crash cause must be captured, or a failed start is a blank console.
	// Which *stream* it is labelled as depends on the transport: a terminal
	// has only one, and that is the trade this panel makes for it.
	var sawCause bool
	for _, line := range inst.LinesSince(0) {
		if strings.Contains(line.Text, "eula.txt") {
			sawCause = true
		}
	}
	if !sawCause {
		t.Errorf("stderr was not captured:\n%s", consoleDump(inst))
	}
}

// The pipe console is the one place stdout and stderr stay apart, which is the
// reason it is still offered after the terminal became the default.
func TestPipeConsoleKeepsStderrSeparate(t *testing.T) {
	inst := newPipeInstance(t, `#!/bin/sh
echo "[12:00:00] [main/ERROR]: Failed to load eula.txt" >&2
exit 1
`)

	if err := inst.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForState(t, inst, StateCrashed)

	var sawStderr bool
	for _, line := range inst.LinesSince(0) {
		if line.Stream == StreamStderr && strings.Contains(line.Text, "eula.txt") {
			sawStderr = true
		}
	}
	if !sawStderr {
		t.Errorf("stderr was not captured as stderr:\n%s", consoleDump(inst))
	}
}

// Kill is the escape hatch for a server that has stopped reading stdin.
func TestKillTerminatesAnUnresponsiveServer(t *testing.T) {
	inst := newTestInstance(t, `#!/bin/sh
echo "[12:00:01] [Server thread/INFO]: Done (0.1s)! For help, type \"help\""
while true; do sleep 1; done
`)

	if err := inst.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForState(t, inst, StateRunning)

	if err := inst.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	waitForState(t, inst, StateStopped)
}

// This is the property the whole panel is built around: the process belongs to
// the daemon, not to a viewer. Every console subscriber can go away and the
// server keeps running with its output still being recorded.
func TestServerSurvivesEveryConsoleDisconnect(t *testing.T) {
	inst := newTestInstance(t, wellBehavedServer)

	if err := inst.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForState(t, inst, StateRunning)

	att := inst.Attach()
	if len(att.Lines) == 0 && len(att.Terminal) == 0 {
		t.Error("a client connecting to a running server should get scrollback")
	}
	history := inst.LinesSince(0)
	if len(history) == 0 {
		t.Error("a running server's output should be recorded")
	}
	inst.Unsubscribe(att.Events)

	if got := inst.State(); got != StateRunning {
		t.Fatalf("server stopped when its console disconnected: state %q", got)
	}

	// It must still accept commands and record output with nobody watching.
	if err := inst.SendCommand("list"); err != nil {
		t.Fatalf("SendCommand after disconnect: %v", err)
	}
	waitForLine(t, inst, "ran list")

	// A late-joining client sees everything it missed.
	late := inst.Attach()
	defer inst.Unsubscribe(late.Events)
	if late.State.State != StateRunning {
		t.Errorf("reconnecting client saw state %q", late.State.State)
	}
	if history2 := inst.LinesSince(0); len(history2) <= len(history) {
		t.Errorf("scrollback did not grow while disconnected: %d -> %d", len(history), len(history2))
	}
}

// A server that writes the host code page instead of UTF-8 — the everyday
// case on a Chinese Windows box — must still reach the browser as text, and
// must understand a command typed in the same language.
func TestLegacyEncodedConsoleRoundTrips(t *testing.T) {
	dir := t.TempDir()
	inst, err := New(Config{
		ID:        "gbk",
		Name:      "gbk",
		Directory: dir,
		Encoding:  "gbk",
		// \304\343\272\303 is "你好" in GBK, written as the octal escapes
		// POSIX printf understands so no shell reinterprets the bytes.
		Command: fakeServer(t, dir, `#!/bin/sh
printf '[12:00:01] [Server thread/INFO]: Done (0.1s)! \304\343\272\303\n'
while IFS= read -r line; do
  printf 'ran %s\n' "$line"
done
`),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = inst.Kill(); inst.Close() })

	if err := inst.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForState(t, inst, StateRunning)
	waitForLine(t, inst, "你好")

	// The command goes out as GBK and comes back through the same decoder, so
	// seeing it echoed proves both directions.
	if err := inst.SendCommand("say 世界"); err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	waitForLine(t, inst, "ran say 世界")
}

func TestStartRejectsASecondProcess(t *testing.T) {
	inst := newTestInstance(t, wellBehavedServer)

	if err := inst.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForState(t, inst, StateRunning)

	if err := inst.Start(); err != ErrAlreadyRunning {
		t.Errorf("expected ErrAlreadyRunning, got %v", err)
	}
}

func TestSendCommandRequiresARunningServer(t *testing.T) {
	inst := newTestInstance(t, wellBehavedServer)

	if err := inst.SendCommand("say hi"); err != ErrNotRunning {
		t.Errorf("expected ErrNotRunning, got %v", err)
	}
}

func TestStopEscalatesWhenTheStopCommandIsIgnored(t *testing.T) {
	dir := t.TempDir()
	inst, err := New(Config{
		ID:             "stubborn",
		Name:           "stubborn",
		Directory:      dir,
		StopTimeoutSec: 1,
		Command: fakeServer(t, dir, `#!/bin/sh
echo "[12:00:01] [Server thread/INFO]: Done (0.1s)! For help, type \"help\""
while true; do sleep 1; done
`),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = inst.Kill(); inst.Close() })

	if err := inst.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForState(t, inst, StateRunning)

	// The fake server never acts on "stop", so only the SIGTERM escalation
	// after StopTimeoutSec can bring it down.
	if err := inst.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitForState(t, inst, StateStopped)
}

// ---------------------------------------------------------------- terminal

// The point of the whole terminal mode: the server can see a tty, which is what
// JLine and TerminalConsoleAppender look for before offering the server's own
// completion and colouring their output.
func TestTerminalConsoleGivesTheServerATTY(t *testing.T) {
	inst := newTestInstance(t, `#!/bin/sh
if [ -t 1 ]; then echo "stdout is a terminal"; else echo "stdout is a pipe"; fi
echo "[12:00:01] [Server thread/INFO]: Done (0.1s)! For help, type \"help\""
while IFS= read -r line; do
  case "$line" in
    stop) exit 0 ;;
    *)    echo "ran $line" ;;
  esac
done
`)

	if err := inst.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForState(t, inst, StateRunning)
	waitForLine(t, inst, "stdout is a terminal")

	if !inst.Status().TTYActive {
		t.Error("the running process should report a live terminal")
	}
	if !inst.UsesTTY() {
		t.Error("the console should be speaking the terminal protocol")
	}
}

// A pipe console cannot show output that has not ended in a newline yet, which
// is why every progress bar a pregenerator or a mod loader draws used to look
// like the server had hung. On a terminal it arrives as it is drawn.
func TestTerminalShowsOutputThatHasNoNewlineYet(t *testing.T) {
	inst := newTestInstance(t, `#!/bin/sh
printf 'Pregenerating chunks: 42%%\r'
while true; do sleep 1; done
`)

	if err := inst.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForTerminal(t, inst, "Pregenerating chunks: 42%")

	// The same bytes cannot have reached the line view: there is no line yet.
	for _, line := range inst.LinesSince(0) {
		if strings.Contains(line.Text, "Pregenerating") {
			t.Errorf("a line was recorded before its newline arrived: %q", line.Text)
		}
	}
}

// Once the redraws do end in a newline, the log view keeps the finished state
// rather than every percentage that was overwritten on the way there.
func TestCarriageReturnRedrawsCollapseInTheLineView(t *testing.T) {
	inst := newTestInstance(t, `#!/bin/sh
printf 'Progress: 10%%\rProgress: 60%%\rProgress: 100%%\n'
echo "[12:00:01] [Server thread/INFO]: Done (0.1s)! For help, type \"help\""
while true; do sleep 1; done
`)

	if err := inst.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForState(t, inst, StateRunning)
	waitForLine(t, inst, "Progress: 100%")

	for _, line := range inst.LinesSince(0) {
		if strings.Contains(line.Text, "Progress:") && !strings.Contains(line.Text, "100%") {
			t.Errorf("an overwritten redraw was kept as a line: %q", line.Text)
		}
	}
}

// A browser attaching to a server that has been up for hours has to be able to
// rebuild the screen, the same way reconnecting to tmux does.
func TestTerminalScrollbackReplaysToALateConsole(t *testing.T) {
	inst := newTestInstance(t, wellBehavedServer)

	if err := inst.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForState(t, inst, StateRunning)
	waitForTerminal(t, inst, "Done (1.234s)!")

	att := inst.Attach()
	defer inst.Unsubscribe(att.Events)
	if !att.TTY {
		t.Fatal("attachment should be in terminal mode")
	}
	if len(att.Lines) != 0 {
		t.Errorf("a terminal attachment should not carry lines, got %d", len(att.Lines))
	}
	if !strings.Contains(string(att.Terminal), "Starting minecraft server") {
		t.Errorf("scrollback missing the startup output:\n%s", att.Terminal)
	}
}

// Keystrokes go to the server's own line editor, which is what makes its tab
// completion and history work in the browser at all.
func TestSendInputReachesTheServer(t *testing.T) {
	inst := newTestInstance(t, wellBehavedServer)

	if err := inst.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForState(t, inst, StateRunning)

	if err := inst.SendInput([]byte("list\r")); err != nil {
		t.Fatalf("SendInput: %v", err)
	}
	waitForLine(t, inst, "ran list")
}

// Raw input has nowhere to go on a pipe console, and must not be quietly
// reinterpreted as a command.
func TestSendInputIsRejectedWithoutATerminal(t *testing.T) {
	inst := newPipeInstance(t, wellBehavedServer)

	if err := inst.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForState(t, inst, StateRunning)

	if err := inst.SendInput([]byte("list\r")); !errors.Is(err, ErrNotRunning) {
		t.Errorf("expected ErrNotRunning for input without a terminal, got %v", err)
	}
}

// Output must reach an attached console exactly once: it is either in the
// snapshot it replays or in the stream it then follows, never both. A terminal
// has no sequence numbers for a client to de-duplicate on, so this is the
// server's problem to get right.
func TestTerminalAttachDoesNotDuplicateOutput(t *testing.T) {
	inst := newTestInstance(t, `#!/bin/sh
i=0
while [ $i -lt 200 ]; do echo "line $i"; i=$((i+1)); done
while true; do sleep 1; done
`)

	if err := inst.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Attach while the output is still being produced, then collect both what
	// was replayed and what arrived afterwards.
	att := inst.Attach()
	defer inst.Unsubscribe(att.Events)

	// A terminal's line discipline translates every \n on the way out, so the
	// bytes on the wire end lines with \r\n whatever the server wrote.
	const eol = "\r\n"

	seen := append([]byte(nil), att.Terminal...)
	deadline := time.After(5 * time.Second)
	for !strings.Contains(string(seen), "line 199"+eol) {
		select {
		case ev, open := <-att.Events:
			if !open {
				t.Fatal("console was dropped for lagging")
			}
			if ev.Type == EventOutput {
				seen = append(seen, ev.Data...)
			}
		case <-deadline:
			t.Fatalf("timed out; got:\n%s", seen)
		}
	}

	for i := 0; i < 200; i++ {
		if n := strings.Count(string(seen), "line "+strconv.Itoa(i)+eol); n != 1 {
			t.Fatalf("line %d appeared %d times, want exactly 1", i, n)
		}
	}
}
