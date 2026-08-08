package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lanscarlos/hypercraft/internal/instance"
)

const (
	// pongWait is how long we tolerate silence from a client before assuming
	// the connection is dead (a closed laptop lid, a dropped VPN).
	pongWait = 60 * time.Second
	// pingPeriod must be comfortably below pongWait so a ping always lands
	// before the deadline it is meant to reset.
	pingPeriod = 25 * time.Second
	// writeWait bounds a single frame write, so one stuck client cannot pin
	// the goroutine forever.
	writeWait = 10 * time.Second
	// maxCommandBytes caps an inbound console command.
	maxCommandBytes = 8 * 1024
)

// outbound is a message sent to the browser.
type outbound struct {
	Type    string              `json:"type"`
	Line    *instance.Line      `json:"line,omitempty"`
	State   *instance.StateInfo `json:"state,omitempty"`
	Message string              `json:"message,omitempty"`
}

// historyMessage is the opening frame: the scrollback plus the current state.
//
// Lines deliberately has no omitempty. An instance with nothing buffered yet
// must still send `"lines": []`, or the client has to guard every access.
type historyMessage struct {
	Type  string              `json:"type"`
	Lines []instance.Line     `json:"lines"`
	State *instance.StateInfo `json:"state"`
}

// inbound is a message received from the browser.
type inbound struct {
	Type    string `json:"type"`
	Command string `json:"command,omitempty"`
}

// handleConsoleSocket streams one instance's console to a browser and relays
// typed commands back to the server's stdin.
//
// The socket is purely an observer of the instance: dropping it, or dropping
// every socket, does not touch the running process.
func (s *Server) handleConsoleSocket(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote an error response.
		s.log.Debug("websocket upgrade failed", "err", err)
		return
	}
	defer conn.Close()

	events, history, state := inst.Subscribe()
	defer inst.Unsubscribe(events)

	// notices carries messages produced by the read pump, so that every write
	// to the connection still happens on this one goroutine (gorilla requires
	// a single concurrent writer).
	notices := make(chan outbound, 8)
	readerDone := make(chan struct{})
	go s.consoleReadPump(conn, inst, notices, readerDone)

	writeJSON := func(msg any) error {
		if err := conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
			return err
		}
		return conn.WriteJSON(msg)
	}
	send := func(msg outbound) error { return writeJSON(msg) }

	if history == nil {
		history = []instance.Line{}
	}
	if err := writeJSON(historyMessage{Type: "history", Lines: history, State: &state}); err != nil {
		return
	}

	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-readerDone:
			return

		case <-r.Context().Done():
			return

		case msg := <-notices:
			if err := send(msg); err != nil {
				return
			}

		case ev, open := <-events:
			if !open {
				// The broker dropped us for falling behind. Tell the client to
				// reconnect and refill the gap from /logs?since=.
				_ = send(outbound{Type: "resync", Message: "console stream fell behind; reconnecting"})
				return
			}
			out := outbound{Type: string(ev.Type), Line: ev.Line, State: ev.State}
			if err := send(out); err != nil {
				return
			}

		case <-ticker.C:
			if err := conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				return
			}
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// consoleReadPump handles client -> server traffic and closes readerDone when
// the connection goes away, which is what unblocks the write loop.
func (s *Server) consoleReadPump(
	conn *websocket.Conn,
	inst *instance.Instance,
	notices chan<- outbound,
	done chan<- struct{},
) {
	defer close(done)

	conn.SetReadLimit(maxCommandBytes)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				s.log.Debug("console socket closed", "err", err)
			}
			return
		}
		// Any traffic proves the client is alive, not just pongs.
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))

		var msg inbound
		if err := json.Unmarshal(data, &msg); err != nil {
			notify(notices, outbound{Type: "error", Message: "malformed message"})
			continue
		}

		switch msg.Type {
		case "command":
			if err := inst.SendCommand(msg.Command); err != nil {
				text := err.Error()
				if errors.Is(err, instance.ErrNotRunning) {
					text = "服务器未在运行，无法发送命令"
				}
				notify(notices, outbound{Type: "error", Message: text})
			}
		case "ping":
			notify(notices, outbound{Type: "pong"})
		default:
			notify(notices, outbound{Type: "error", Message: "unknown message type: " + msg.Type})
		}
	}
}

// notify posts to the write loop without blocking; a client that has generated
// more notices than it can drain does not need every one of them.
func notify(ch chan<- outbound, msg outbound) {
	select {
	case ch <- msg:
	default:
	}
}
