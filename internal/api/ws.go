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
// TTY says which protocol this connection speaks, and is fixed for its
// lifetime. When it is set, Lines is empty and the scrollback arrives instead
// as a binary frame immediately after this one — raw bytes cannot be spliced
// into JSON without breaking the multi-byte characters and escape sequences
// that are the whole point of a terminal.
//
// Lines deliberately has no omitempty. An instance with nothing buffered yet
// must still send `"lines": []`, or the client has to guard every access.
type historyMessage struct {
	Type  string              `json:"type"`
	TTY   bool                `json:"tty"`
	Lines []instance.Line     `json:"lines"`
	State *instance.StateInfo `json:"state"`
}

// inbound is a message received from the browser in a text frame. Keystrokes
// for a terminal console ride in binary frames instead.
type inbound struct {
	Type    string `json:"type"`
	Command string `json:"command,omitempty"`
	Cols    uint16 `json:"cols,omitempty"`
	Rows    uint16 `json:"rows,omitempty"`
}

// handleConsoleSocket streams one instance's console to a browser and relays
// what is typed back to the server.
//
// The socket is purely an observer of the instance: dropping it, or dropping
// every socket, does not touch the running process. That holds for a terminal
// console too — unlike the host shell, where the socket *is* the session.
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

	att := inst.Attach()
	defer inst.Unsubscribe(att.Events)

	// Size the terminal before anything is written to it, so the server's first
	// lines are already wrapped for the window that will show them. The client
	// re-reports on every resize; this is only the opening value.
	if att.TTY {
		// A client that names no size gets no viewport, rather than one that
		// clamps up from zero and shrinks the terminal for everybody else.
		if cols, rows := uint16Param(r, "cols", 0), uint16Param(r, "rows", 0); cols > 0 && rows > 0 {
			inst.SetViewport(att.Events, cols, rows)
		}
	}

	// notices carries messages produced by the read pump, so that every write
	// to the connection still happens on this one goroutine (gorilla requires
	// a single concurrent writer).
	notices := make(chan outbound, 8)
	readerDone := make(chan struct{})
	go s.consoleReadPump(conn, inst, att, notices, readerDone)

	writeJSON := func(msg any) error {
		if err := conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
			return err
		}
		return conn.WriteJSON(msg)
	}
	writeBinary := func(data []byte) error {
		if err := conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
			return err
		}
		return conn.WriteMessage(websocket.BinaryMessage, data)
	}
	send := func(msg outbound) error { return writeJSON(msg) }

	lines := att.Lines
	if lines == nil {
		lines = []instance.Line{}
	}
	if err := writeJSON(historyMessage{
		Type: "history", TTY: att.TTY, Lines: lines, State: &att.State,
	}); err != nil {
		return
	}
	if len(att.Terminal) > 0 {
		if err := writeBinary(att.Terminal); err != nil {
			return
		}
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

		case ev, open := <-att.Events:
			if !open {
				// The broker dropped us for falling behind. Tell the client to
				// reconnect: a line console refills the gap from /logs?since=,
				// a terminal one replays the scrollback.
				_ = send(outbound{Type: "resync", Message: "console stream fell behind; reconnecting"})
				return
			}
			if ev.Type == instance.EventOutput {
				if err := writeBinary(ev.Data); err != nil {
					return
				}
				continue
			}
			// The protocol this connection announced is the one it gets, even
			// if the instance's config is switched underneath it: a terminal
			// client handed a line event would draw text the byte stream is
			// also about to deliver. The change lands on the next reconnect.
			if att.TTY && ev.Type == instance.EventLine {
				continue
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
	att instance.Attachment,
	notices chan<- outbound,
	done chan<- struct{},
) {
	defer close(done)

	// A terminal console carries keystrokes, and a paste is one message; a line
	// console only ever carries a single command.
	if att.TTY {
		conn.SetReadLimit(terminalReadLimit)
	} else {
		conn.SetReadLimit(maxCommandBytes)
	}
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				s.log.Debug("console socket closed", "err", err)
			}
			return
		}
		// Any traffic proves the client is alive, not just pongs.
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))

		if kind == websocket.BinaryMessage {
			// Keystrokes for the server's own console. Silently ignored when
			// the instance is not on a terminal: there is nothing there to type
			// into, and a client in the wrong mode should reconnect, not have
			// its keys turned into commands.
			if att.TTY {
				if err := inst.SendInput(data); err != nil && !errors.Is(err, instance.ErrNotRunning) {
					s.log.Debug("console input rejected", "err", err)
				}
			}
			continue
		}

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
		case "resize":
			if att.TTY && msg.Cols > 0 && msg.Rows > 0 {
				inst.SetViewport(att.Events, msg.Cols, msg.Rows)
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
