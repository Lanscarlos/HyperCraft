//go:build !windows

package instance

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
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

func newTestInstance(t *testing.T, body string) *Instance {
	t.Helper()

	dir := t.TempDir()
	inst, err := New(Config{
		ID:        "test",
		Name:      "test",
		Directory: dir,
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

	// stderr must be captured too, or crash causes would be invisible.
	var sawStderr bool
	for _, line := range inst.LinesSince(0) {
		if line.Stream == StreamStderr && strings.Contains(line.Text, "eula.txt") {
			sawStderr = true
		}
	}
	if !sawStderr {
		t.Errorf("stderr was not captured:\n%s", consoleDump(inst))
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

	events, history, _ := inst.Subscribe()
	if len(history) == 0 {
		t.Error("a client connecting to a running server should get scrollback")
	}
	inst.Unsubscribe(events)

	if got := inst.State(); got != StateRunning {
		t.Fatalf("server stopped when its console disconnected: state %q", got)
	}

	// It must still accept commands and record output with nobody watching.
	if err := inst.SendCommand("list"); err != nil {
		t.Fatalf("SendCommand after disconnect: %v", err)
	}
	waitForLine(t, inst, "ran list")

	// A late-joining client sees everything it missed.
	_, history2, state := inst.Subscribe()
	if state.State != StateRunning {
		t.Errorf("reconnecting client saw state %q", state.State)
	}
	if len(history2) <= len(history) {
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
