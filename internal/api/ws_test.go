package api

import (
	"encoding/json"
	"net/http"
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
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		e.t.Fatalf("dial console: %v (http %d)", err, status)
	}
	e.t.Cleanup(func() { conn.Close() })
	return conn
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
