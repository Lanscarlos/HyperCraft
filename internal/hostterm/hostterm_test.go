//go:build !windows

package hostterm

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testService returns a service pinned to /bin/sh, so the tests do not depend
// on whatever login shell the machine running them happens to have.
func testService(t *testing.T) *Service {
	t.Helper()
	if !Supported() {
		t.Skip("no pseudo-terminal support on this platform")
	}
	return New(Options{Shell: "/bin/sh", Dir: t.TempDir()})
}

// readUntil drains the session until it has seen want, so a test never depends
// on where the shell decides to break its output into reads.
func readUntil(t *testing.T, sess *Session, want string, timeout time.Duration) string {
	t.Helper()

	type result struct {
		text string
		err  error
	}
	found := make(chan result, 1)
	go func() {
		var seen strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := sess.Read(buf)
			seen.Write(buf[:n])
			if strings.Contains(seen.String(), want) {
				found <- result{seen.String(), nil}
				return
			}
			if err != nil {
				found <- result{seen.String(), err}
				return
			}
		}
	}()

	select {
	case got := <-found:
		if got.err != nil && !strings.Contains(got.text, want) {
			t.Fatalf("session ended before %q appeared: %v\noutput so far:\n%s", want, got.err, got.text)
		}
		return got.text
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for %q", want)
		return ""
	}
}

func TestSessionRunsCommandsInAShell(t *testing.T) {
	svc := testService(t)

	sess, err := svc.Start(80, 24)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sess.Close()

	// The pseudo-terminal echoes what is typed, so the marker has to be
	// something only the shell's *answer* can contain: the arithmetic is
	// echoed unevaluated, the result is not.
	if _, err := sess.Write([]byte("echo HYPER-$((6*7))\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	readUntil(t, sess, "HYPER-42", 10*time.Second)
}

func TestSessionStartsInTheConfiguredDirectory(t *testing.T) {
	if !Supported() {
		t.Skip("no pseudo-terminal support on this platform")
	}
	dir := t.TempDir()
	svc := New(Options{Shell: "/bin/sh", Dir: dir})

	sess, err := svc.Start(80, 24)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sess.Close()

	// Matched on the last path element rather than the whole path: macOS hands
	// out /var symlinks for temporary directories and pwd reports the resolved
	// form, but the leaf is unique to this t.TempDir() either way.
	leaf := filepath.Base(dir)
	if _, err := sess.Write([]byte("pwd\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	readUntil(t, sess, leaf+"\r\n", 10*time.Second)
}

func TestExitEndsTheSession(t *testing.T) {
	svc := testService(t)

	sess, err := svc.Start(80, 24)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sess.Close()

	if _, err := sess.Write([]byte("exit 3\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("shell did not exit after `exit`")
	}

	if code := sess.ExitCode(); code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
	// Reading drains whatever is left and then reports the end of the session,
	// which is how the websocket handler learns to close.
	if _, err := drain(sess); err == nil {
		t.Error("expected reads to fail once the shell is gone")
	}
}

func drain(sess *Session) (string, error) {
	var seen strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := sess.Read(buf)
		seen.Write(buf[:n])
		if err != nil {
			return seen.String(), err
		}
	}
}

func TestCloseHangsUpAndFreesTheSlot(t *testing.T) {
	svc := testService(t)

	sess, err := svc.Start(80, 24)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if svc.Live() != 1 {
		t.Fatalf("live sessions = %d, want 1", svc.Live())
	}

	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if svc.Live() != 0 {
		t.Errorf("live sessions = %d after Close, want 0", svc.Live())
	}

	done := make(chan struct{})
	go func() { _ = sess.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(killGrace + 5*time.Second):
		t.Fatal("shell survived the hangup")
	}

	// Closing twice is what happens when the handler's defer runs after an
	// error path already cleaned up; it must not double-release the slot.
	_ = sess.Close()
	if svc.Live() != 0 {
		t.Errorf("live sessions = %d after a second Close, want 0", svc.Live())
	}
}

func TestSessionCapIsEnforced(t *testing.T) {
	if !Supported() {
		t.Skip("no pseudo-terminal support on this platform")
	}
	svc := New(Options{Shell: "/bin/sh", Dir: t.TempDir(), MaxSessions: 2})

	for i := range 2 {
		sess, err := svc.Start(80, 24)
		if err != nil {
			t.Fatalf("Start %d: %v", i, err)
		}
		defer sess.Close()
	}

	if _, err := svc.Start(80, 24); !errors.Is(err, ErrTooManySessions) {
		t.Errorf("third Start error = %v, want ErrTooManySessions", err)
	}
}

func TestResizeIsAcceptedAndClamped(t *testing.T) {
	svc := testService(t)

	sess, err := svc.Start(80, 24)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sess.Close()

	// 0 columns would be rejected by the kernel; the clamp is what keeps a
	// browser that reports a hidden (zero-sized) terminal from erroring out.
	if err := sess.Resize(0, 0); err != nil {
		t.Fatalf("Resize to zero: %v", err)
	}
	if err := sess.Resize(120, 40); err != nil {
		t.Fatalf("Resize: %v", err)
	}

	if _, err := sess.Write([]byte("echo COLS-$(tput cols 2>/dev/null || echo ?)\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	readUntil(t, sess, "COLS-", 10*time.Second)
}

func TestClampKeepsWindowSizesUsable(t *testing.T) {
	if got := ClampCols(0); got != MinCols {
		t.Errorf("ClampCols(0) = %d, want %d", got, MinCols)
	}
	if got := ClampCols(60000); got != MaxCols {
		t.Errorf("ClampCols(60000) = %d, want %d", got, MaxCols)
	}
	if got := ClampRows(24); got != 24 {
		t.Errorf("ClampRows(24) = %d, want 24", got)
	}
}

func TestResolveShellPrefersTheOverride(t *testing.T) {
	if got := resolveShell("/usr/bin/fish"); got != "/usr/bin/fish" {
		t.Errorf("resolveShell = %q, want the override", got)
	}
	// An unset $SHELL must still produce something runnable rather than "".
	t.Setenv("SHELL", "")
	if got := resolveShell(""); got == "" {
		t.Error("resolveShell fell back to an empty program")
	}
}

func TestResolveDirFallsBackWhenTheDirectoryIsMissing(t *testing.T) {
	missing := t.TempDir() + "/not-here"
	got := resolveDir(missing)
	if got == missing {
		t.Errorf("resolveDir kept a nonexistent directory: %q", got)
	}
	if got == "" {
		t.Error("resolveDir returned an empty directory")
	}
}

func TestShellEnvDescribesTheTerminal(t *testing.T) {
	t.Setenv("TERM", "dumb")
	env := shellEnv()

	var terms int
	for _, kv := range env {
		if strings.HasPrefix(kv, "TERM=") {
			terms++
			if kv != "TERM=xterm-256color" {
				t.Errorf("TERM = %q, want xterm-256color", kv)
			}
		}
	}
	if terms != 1 {
		t.Errorf("found %d TERM entries, want exactly 1", terms)
	}
}

func TestCloseAllHangsUpEveryShell(t *testing.T) {
	if !Supported() {
		t.Skip("no pseudo-terminal support on this platform")
	}
	svc := New(Options{Shell: "/bin/sh", Dir: t.TempDir()})

	var open []*Session
	for i := range 3 {
		sess, err := svc.Start(80, 24)
		if err != nil {
			t.Fatalf("Start %d: %v", i, err)
		}
		open = append(open, sess)
	}

	if closed := svc.CloseAll(); closed != 3 {
		t.Errorf("CloseAll closed %d sessions, want 3", closed)
	}
	if svc.Live() != 0 {
		t.Errorf("live sessions = %d after CloseAll, want 0", svc.Live())
	}

	for i, sess := range open {
		done := make(chan struct{})
		go func() { _ = sess.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(killGrace + 5*time.Second):
			t.Fatalf("session %d survived CloseAll", i)
		}
	}
}
