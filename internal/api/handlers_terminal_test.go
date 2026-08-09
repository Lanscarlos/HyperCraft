package api

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lanscarlos/hypercraft/internal/hostterm"
)

// withTerminal wires a real shell service into a test panel. The default test
// env leaves it out, which is also how a panel built without the feature looks.
func withTerminal(t *testing.T) func(*Options) {
	t.Helper()
	return func(o *Options) {
		o.Terminal = hostterm.New(hostterm.Options{Shell: "/bin/sh", Dir: t.TempDir()})
	}
}

func (e *testEnv) terminalStatus() terminalStatus {
	e.t.Helper()
	resp := e.do(http.MethodGet, "/api/terminal", nil)
	var status terminalStatus
	decodeBody(e.t, resp, &status)
	return status
}

// dialTerminal opens the shell websocket with the logged-in session cookie.
func (e *testEnv) dialTerminal() (*websocket.Conn, *http.Response, error) {
	e.t.Helper()

	req, err := http.NewRequest(http.MethodGet, e.server.URL, nil)
	if err != nil {
		e.t.Fatalf("build request: %v", err)
	}
	header := http.Header{}
	for _, cookie := range e.client.Jar.Cookies(req.URL) {
		header.Add("Cookie", cookie.Name+"="+cookie.Value)
	}

	url := "ws" + strings.TrimPrefix(e.server.URL, "http") + "/api/terminal/session?cols=100&rows=30"
	conn, resp, err := websocket.DefaultDialer.Dial(url, header)
	if conn != nil {
		e.t.Cleanup(func() { conn.Close() })
	}
	return conn, resp, err
}

func TestTerminalIsOffUntilItIsTurnedOn(t *testing.T) {
	env := newTestEnv(t, withTerminal(t))
	env.login()

	status := env.terminalStatus()
	if status.Enabled {
		t.Error("a fresh panel must not hand out a shell")
	}
	if !status.Supported {
		t.Skip("no pseudo-terminal support on this platform")
	}
	if status.Shell == "" {
		t.Error("status should describe the shell it would run")
	}

	// The socket has to refuse too, not just the UI: the switch is the whole
	// security boundary, and a client can dial the URL directly.
	_, resp, err := env.dialTerminal()
	if err == nil {
		t.Fatal("expected the terminal socket to be refused while it is off")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %v", resp)
	}
}

func TestTerminalCannotBeEnabledWithoutSupport(t *testing.T) {
	// No terminal service at all, which is what an unsupported platform and a
	// panel built without the feature both look like from here.
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPut, "/api/terminal", terminalToggleRequest{Enabled: true})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", resp.StatusCode)
	}
	if env.terminalStatus().Enabled {
		t.Error("the switch must not stick when the feature cannot run")
	}
}

func TestTerminalSwitchIsPersisted(t *testing.T) {
	env := newTestEnv(t, withTerminal(t))
	env.login()
	if !hostterm.Supported() {
		t.Skip("no pseudo-terminal support on this platform")
	}

	resp := env.do(http.MethodPut, "/api/terminal", terminalToggleRequest{Enabled: true})
	var status terminalStatus
	decodeBody(t, resp, &status)
	if !status.Enabled {
		t.Fatal("PUT did not report the terminal as enabled")
	}

	// Written through to panel.json, so a restart does not silently forget a
	// switch this consequential in either direction.
	stored, err := env.store.LoadPanel()
	if err != nil {
		t.Fatalf("LoadPanel: %v", err)
	}
	if !stored.Terminal.Enabled {
		t.Error("the switch was not saved to panel.json")
	}

	resp = env.do(http.MethodPut, "/api/terminal", terminalToggleRequest{Enabled: false})
	resp.Body.Close()
	stored, err = env.store.LoadPanel()
	if err != nil {
		t.Fatalf("LoadPanel: %v", err)
	}
	if stored.Terminal.Enabled {
		t.Error("turning the terminal off was not saved")
	}
}

func TestTerminalRequiresASession(t *testing.T) {
	env := newTestEnv(t, withTerminal(t))

	resp := env.do(http.MethodGet, "/api/terminal", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without a session, got %d", resp.StatusCode)
	}
}

func TestTerminalSocketRunsAShell(t *testing.T) {
	if !hostterm.Supported() {
		t.Skip("no pseudo-terminal support on this platform")
	}
	env := newTestEnv(t, withTerminal(t))
	env.login()
	env.do(http.MethodPut, "/api/terminal", terminalToggleRequest{Enabled: true}).Body.Close()

	conn, _, err := env.dialTerminal()
	if err != nil {
		t.Fatalf("dial terminal: %v", err)
	}

	// Keystrokes go up as binary frames, so a multi-byte character split
	// across reads can never be mangled by a JSON round trip.
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("echo HYPER-$((6*7))\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	var seen strings.Builder
	for !strings.Contains(seen.String(), "HYPER-42") {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v (output so far: %q)", err, seen.String())
		}
		if kind == websocket.BinaryMessage {
			seen.Write(data)
		}
	}
}

func TestTerminalSocketReportsTheShellExiting(t *testing.T) {
	if !hostterm.Supported() {
		t.Skip("no pseudo-terminal support on this platform")
	}
	env := newTestEnv(t, withTerminal(t))
	env.login()
	env.do(http.MethodPut, "/api/terminal", terminalToggleRequest{Enabled: true}).Body.Close()

	conn, _, err := env.dialTerminal()
	if err != nil {
		t.Fatalf("dial terminal: %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("exit 0\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The exit notice is a text frame; output stays binary, which is how the
	// client tells the two apart without a wrapper on every chunk.
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("socket closed before the exit notice: %v", err)
		}
		if kind != websocket.TextMessage {
			continue
		}
		var msg terminalControl
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("decode control frame: %v", err)
		}
		if msg.Type != "exit" {
			t.Fatalf("control frame type = %q, want exit", msg.Type)
		}
		return
	}
}

func TestTerminalSocketIsDeniedWithoutASession(t *testing.T) {
	env := newTestEnv(t, withTerminal(t))
	// Enable it through a logged-in client, then throw the cookie away: the
	// switch being on must not make the socket public.
	env.login()
	if hostterm.Supported() {
		env.do(http.MethodPut, "/api/terminal", terminalToggleRequest{Enabled: true}).Body.Close()
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	env.client.Jar = jar

	_, resp, err := env.dialTerminal()
	if err == nil {
		t.Fatal("expected the terminal socket to be refused without a session")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %v", resp)
	}
}
