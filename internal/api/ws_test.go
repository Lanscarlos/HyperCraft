package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lanscarlos/hypercraft/internal/instance"
)

// dialConsole opens the console websocket, carrying the session cookie the
// test client picked up at login.
func (e *testEnv) dialConsole(instanceID string) *websocket.Conn {
	e.t.Helper()

	conn, resp, err := e.tryDialConsole(instanceID)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		e.t.Fatalf("dial console: %v (http %d)", err, status)
	}
	return conn
}

// tryDialConsole is dialConsole without the fatal, for the tests that expect
// the handler to refuse.
func (e *testEnv) tryDialConsole(instanceID string) (*websocket.Conn, *http.Response, error) {
	e.t.Helper()

	url := "ws" + strings.TrimPrefix(e.server.URL, "http")
	header := http.Header{}
	parsed, err := http.NewRequest(http.MethodGet, e.server.URL, nil)
	if err != nil {
		e.t.Fatalf("build request: %v", err)
	}
	for _, cookie := range e.client.Jar.Cookies(parsed.URL) {
		header.Add("Cookie", cookie.Name+"="+cookie.Value)
	}

	conn, resp, err := websocket.DefaultDialer.Dial(
		url+"/api/instances/"+instanceID+"/console", header)
	if conn != nil {
		e.t.Cleanup(func() { conn.Close() })
	}
	return conn, resp, err
}

func (e *testEnv) createInstance(name string) instance.Status {
	e.t.Helper()
	resp := e.do(http.MethodPost, "/api/instances", instanceRequest{Name: name})
	var created instance.Status
	decodeBody(e.t, resp, &created)
	return created
}

// A brand new instance has no scrollback. The opening frame must still carry
// an empty array — a missing key crashes any client that maps over it.
func TestConsoleHistoryAlwaysCarriesALinesArray(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("fresh")

	conn := env.dialConsole(created.ID)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read history frame: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decode history frame: %v", err)
	}
	if _, ok := raw["lines"]; !ok {
		t.Fatalf("history frame omitted \"lines\": %s", data)
	}

	var frame struct {
		Type  string             `json:"type"`
		Lines []instance.Line    `json:"lines"`
		State instance.StateInfo `json:"state"`
	}
	if err := json.Unmarshal(data, &frame); err != nil {
		t.Fatalf("decode typed frame: %v", err)
	}
	if frame.Type != "history" {
		t.Errorf("first frame was %q, want \"history\"", frame.Type)
	}
	if frame.Lines == nil {
		t.Error("lines decoded as nil rather than an empty array")
	}
	if frame.State.State != instance.StateStopped {
		t.Errorf("state = %q, want %q", frame.State.State, instance.StateStopped)
	}
}

// Which protocol a console speaks is settled by the opening frame and fixed
// for the connection: a client that guessed would render escape sequences as
// text, or text as escape sequences.
func TestConsoleAnnouncesItsProtocol(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	for _, tc := range []struct {
		name string
		tty  *bool
		want bool
	}{
		{name: "default", tty: nil, want: instance.TTYSupported()},
		{name: "explicitly off", tty: boolPtr(false), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := env.do(http.MethodPost, "/api/instances",
				instanceRequest{Name: "proto-" + tc.name, TTY: tc.tty})
			var created instance.Status
			decodeBody(t, resp, &created)

			conn := env.dialConsole(created.ID)
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

			_, data, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("read history frame: %v", err)
			}
			var frame historyMessage
			if err := json.Unmarshal(data, &frame); err != nil {
				t.Fatalf("decode history frame: %v", err)
			}
			if frame.TTY != tc.want {
				t.Errorf("history frame said tty=%v, want %v", frame.TTY, tc.want)
			}
			// The config has to come back too, or the settings page cannot show
			// the operator what they chose.
			if got := created.TTY; got == nil || *got != (tc.tty == nil || *tc.tty) {
				t.Errorf("created instance reported tty=%v", got)
			}
		})
	}
}

func boolPtr(v bool) *bool { return &v }

// The whole terminal path, end to end: a real pseudo-terminal, real escape
// sequences, and the binary frames that carry them to a browser. The unit tests
// cover each half; this is the one that would catch them being wired together
// wrongly — a JSON encode slipped into the output path, say, which would look
// fine until the first multi-byte character.
func TestTerminalConsoleStreamsBinaryFrames(t *testing.T) {
	if !instance.TTYSupported() {
		t.Skip("no pseudo-terminal on this platform")
	}

	env := newTestEnv(t)
	env.login()

	dir := t.TempDir()
	script := filepath.Join(dir, "server.sh")
	// Colour and a CJK line: between them they exercise the two things a JSON
	// text frame would ruin.
	body := "#!/bin/sh\n" +
		`printf '\033[32m[12:00:01] [Server thread/INFO]: Done (0.1s)! 你好\033[m\n'` + "\n" +
		"while IFS= read -r line; do :; done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake server: %v", err)
	}

	resp := env.do(http.MethodPost, "/api/instances", instanceRequest{
		Name:      "terminal",
		Directory: dir,
		Command:   []string{"/bin/sh", script},
	})
	var created instance.Status
	decodeBody(t, resp, &created)

	resp = env.do(http.MethodPost, "/api/instances/"+created.ID+"/start", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("start: http %d", resp.StatusCode)
	}
	t.Cleanup(func() { _ = env.mgr.List()[0].Kill() })

	conn := env.dialConsole(created.ID)
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	var seen strings.Builder
	for !strings.Contains(seen.String(), "你好") {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read frame: %v (so far: %q)", err, seen.String())
		}
		if kind == websocket.BinaryMessage {
			seen.Write(data)
			continue
		}
		var frame historyMessage
		if err := json.Unmarshal(data, &frame); err == nil && frame.Type == "history" && !frame.TTY {
			t.Fatal("a console with a terminal announced the line protocol")
		}
	}

	if !strings.Contains(seen.String(), "\x1b[32m") {
		t.Errorf("colour codes did not survive the trip: %q", seen.String())
	}
	// The line discipline turns every \n into \r\n on the way out, which is how
	// a terminal emulator knows to return the cursor as well as advance it.
	if !strings.Contains(seen.String(), "\r\n") {
		t.Errorf("terminal line endings missing: %q", seen.String())
	}
}

// Commands sent to a stopped server must come back as an error on the socket,
// not silently vanish or tear the connection down.
func TestConsoleReportsCommandsToAStoppedServer(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("stopped")

	conn := env.dialConsole(created.ID)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read history frame: %v", err)
	}

	if err := conn.WriteJSON(inbound{Type: "command", Command: "say hi"}); err != nil {
		t.Fatalf("write command: %v", err)
	}

	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	var reply outbound
	if err := json.Unmarshal(data, &reply); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if reply.Type != "error" || reply.Message == "" {
		t.Errorf("expected an error message, got %+v", reply)
	}
}

func TestConsoleRequiresASession(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("guarded")

	// A dialer with no cookie jar stands in for an unauthenticated visitor.
	url := "ws" + strings.TrimPrefix(env.server.URL, "http")
	_, resp, err := websocket.DefaultDialer.Dial(
		url+"/api/instances/"+created.ID+"/console", nil)
	if err == nil {
		t.Fatal("expected the console dial to be rejected")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected HTTP 401, got %v", resp)
	}
}

// A console socket is cheap to open and not cheap to hold: it subscribes to the
// instance's log fan-out and keeps two goroutines alive. A client reconnecting
// without closing what it had must not be able to pile them up on a server
// process that has to keep running.
func TestConsoleSocketsAreCappedPerInstance(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("crowded")

	for i := range maxConsoleSockets {
		conn := env.dialConsole(created.ID)
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatalf("socket %d never opened: %v", i, err)
		}
	}

	_, resp, err := env.tryDialConsole(created.ID)
	if err == nil {
		t.Fatal("expected the socket past the cap to be refused")
	}
	if resp == nil || resp.StatusCode != http.StatusConflict {
		t.Errorf("expected HTTP 409, got %v", resp)
	}
}

// The cap counts what is held, so closing a socket has to hand its slot back —
// otherwise a day of normal reloading would lock the operator out of their own
// console.
func TestClosingAConsoleSocketReturnsItsSlot(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("recycled")

	for range maxConsoleSockets {
		conn := env.dialConsole(created.ID)
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatalf("socket never opened: %v", err)
		}
		conn.Close()
		// The handler releases as it unwinds, which happens on its own
		// goroutine after the peer's close lands.
		waitFor(t, func() bool { return env.api.consoleSockets.active() == 0 })
	}
}

// Two instances are two separate budgets: filling one console must not cost the
// operator the ability to watch another server.
func TestConsoleCapIsPerInstance(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	busy := env.createInstance("busy")
	quiet := env.createInstance("quiet")

	for range maxConsoleSockets {
		conn := env.dialConsole(busy.ID)
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatalf("socket never opened: %v", err)
		}
	}

	conn := env.dialConsole(quiet.ID)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("the second instance's console was refused: %v", err)
	}
}
