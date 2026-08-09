package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os/user"
	"runtime"
	"strconv"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lanscarlos/hypercraft/internal/hostterm"
)

// terminalStatus is what the settings page renders its switch from.
type terminalStatus struct {
	// Enabled is the operator's choice; Supported is whether this build could
	// honour it. The UI needs both to explain a switch it will not let you flip.
	Enabled   bool `json:"enabled"`
	Supported bool `json:"supported"`
	// Shell, User and Cwd describe what turning this on actually hands out, so
	// the warning beside the switch can be specific instead of generic.
	Shell string `json:"shell"`
	User  string `json:"user"`
	Cwd   string `json:"cwd"`
	// Reason explains an unavailable terminal (wrong platform, feature not
	// wired up), and is empty when it is merely switched off.
	Reason string `json:"reason,omitempty"`
	// Live is how many shells are open right now.
	Live int `json:"live"`
}

// Frame sizes for the terminal socket. Keystrokes are tiny; a paste into the
// shell is the only thing that gets near the read limit.
const (
	terminalReadLimit = 64 * 1024
	terminalChunk     = 16 * 1024
	// terminalBacklog is how many output chunks may be in flight to a slow
	// browser. Once it fills, the reader stops draining the pseudo-terminal,
	// which back-pressures the shell — the same thing a real terminal does,
	// and the reason `cat hugefile` scrolls instead of eating memory.
	terminalBacklog = 64
)

// terminalControl is the JSON sent in a text frame. Output and keystrokes ride
// in binary frames instead: raw bytes cannot be spliced into JSON without
// breaking multi-byte characters that straddle a read boundary, and xterm.js
// decodes UTF-8 across chunks for us if we simply hand it the bytes.
type terminalControl struct {
	Type    string `json:"type"`
	Cols    uint16 `json:"cols,omitempty"`
	Rows    uint16 `json:"rows,omitempty"`
	Message string `json:"message,omitempty"`
	Code    int    `json:"code,omitempty"`
}

// terminalOn reports the operator's switch, which is separate from whether the
// platform could run a shell at all.
func (s *Server) terminalOn() bool {
	s.panelMu.RLock()
	defer s.panelMu.RUnlock()
	return s.panel.Terminal.Enabled
}

func (s *Server) terminalStatus() terminalStatus {
	status := terminalStatus{
		Enabled:   s.terminalOn(),
		Supported: s.terminal != nil && hostterm.Supported(),
		User:      currentUsername(),
	}
	switch {
	case s.terminal == nil:
		status.Reason = "本面板未启用终端功能"
	case !hostterm.Supported():
		status.Reason = "本机终端需要伪终端支持，" + runtime.GOOS + " 上暂不可用"
	default:
		status.Shell = s.terminal.Shell()
		status.Cwd = s.terminal.Dir()
		status.Live = s.terminal.Live()
	}
	return status
}

func (s *Server) handleTerminalStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.terminalStatus())
}

type terminalToggleRequest struct {
	Enabled bool `json:"enabled"`
}

// handleTerminalToggle turns the shell on or off and persists the choice.
//
// The switch lives here rather than in a config file the operator has to edit
// and restart around, but it is still a deliberate act: enabling it is logged,
// and disabling it hangs up on every shell that is already open, so revoking
// the feature actually revokes it.
func (s *Server) handleTerminalToggle(w http.ResponseWriter, r *http.Request) {
	var req terminalToggleRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if req.Enabled && (s.terminal == nil || !hostterm.Supported()) {
		writeError(w, http.StatusServiceUnavailable, s.terminalStatus().Reason)
		return
	}

	s.panelMu.Lock()
	panel := s.panel
	panel.Terminal.Enabled = req.Enabled
	s.panel = panel
	s.panelMu.Unlock()

	if err := s.store.SavePanel(panel); err != nil {
		s.log.Error("could not persist the terminal switch", "err", err)
		writeError(w, http.StatusInternalServerError, "保存失败")
		return
	}

	// Turning it off has to reach the shells already running, or the switch
	// only stops people who had not connected yet.
	var hungUp int
	if !req.Enabled && s.terminal != nil {
		hungUp = s.terminal.CloseAll()
	}

	sess, _ := sessionFrom(r.Context())
	s.log.Warn("host terminal switched",
		"enabled", req.Enabled, "by", sess.Username, "sessionsClosed", hungUp)
	writeJSON(w, http.StatusOK, s.terminalStatus())
}

// handleTerminalSocket attaches a browser to a shell on this machine.
//
// One socket is one shell, and closing the socket kills it. That is the
// opposite of the instance console, which survives every browser in the world
// going away — and deliberately so: a Minecraft server is the panel's to keep
// running, whereas a shell nobody is watching is just a way to leave `rm -rf`
// half-finished with no way to see it.
func (s *Server) handleTerminalSocket(w http.ResponseWriter, r *http.Request) {
	if s.terminal == nil || !hostterm.Supported() {
		writeError(w, http.StatusServiceUnavailable, s.terminalStatus().Reason)
		return
	}
	if !s.terminalOn() {
		writeError(w, http.StatusForbidden, "本机终端未启用")
		return
	}

	cols := uint16Param(r, "cols", 80)
	rows := uint16Param(r, "rows", 24)

	// Started before the upgrade so a failure (no pty, session cap) comes back
	// as a plain HTTP error the fetch layer already knows how to show, rather
	// than as a websocket that opens and immediately closes.
	sess, err := s.terminal.Start(cols, rows)
	if err != nil {
		switch {
		case errors.Is(err, hostterm.ErrTooManySessions):
			writeError(w, http.StatusConflict, "同时打开的终端太多了，先关掉一个再试")
		case errors.Is(err, hostterm.ErrUnsupported):
			writeError(w, http.StatusServiceUnavailable, hostterm.ErrUnsupported.Error())
		default:
			s.log.Error("could not start a terminal session", "err", err)
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Debug("terminal websocket upgrade failed", "err", err)
		_ = sess.Close()
		return
	}
	defer conn.Close()
	defer sess.Close()

	who, _ := sessionFrom(r.Context())
	s.log.Info("terminal attached", "user", who.Username, "pid", sess.PID(), "remote", r.RemoteAddr)
	defer s.log.Info("terminal detached", "user", who.Username, "pid", sess.PID())

	// Every write to the socket happens on this goroutine; gorilla allows only
	// one concurrent writer. The two pumps below only ever hand it work.
	quit := make(chan struct{})
	defer close(quit)

	output := make(chan []byte, terminalBacklog)
	go terminalOutputPump(sess, output, quit)

	clientDone := make(chan struct{})
	go s.terminalInputPump(conn, sess, clientDone)

	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	writeControl := func(msg terminalControl) error {
		if err := conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
			return err
		}
		return conn.WriteJSON(msg)
	}

	for {
		select {
		case <-clientDone:
			return

		case <-r.Context().Done():
			return

		case chunk, open := <-output:
			if !open {
				// Drained: the shell is gone and everything it printed is out.
				_ = writeControl(terminalControl{
					Type:    "exit",
					Code:    sess.ExitCode(),
					Message: "会话已结束",
				})
				return
			}
			if err := conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				return
			}
			if err := conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
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

// terminalOutputPump copies the shell's output towards the browser, closing
// output once the pseudo-terminal has drained.
func terminalOutputPump(sess *hostterm.Session, output chan<- []byte, quit <-chan struct{}) {
	defer close(output)

	for {
		// A fresh buffer per read: it is handed to another goroutine, so it
		// cannot be reused until that one is done with it.
		buf := make([]byte, terminalChunk)
		n, err := sess.Read(buf)
		if n > 0 {
			select {
			case output <- buf[:n]:
			case <-quit:
				return
			}
		}
		if err != nil {
			// Read fails with EIO on Linux when the shell exits and the slave
			// side is gone — the normal end of a session, not a fault.
			return
		}
	}
}

// terminalInputPump carries keystrokes and window sizes the other way, and
// closes done when the browser goes away.
func (s *Server) terminalInputPump(conn *websocket.Conn, sess *hostterm.Session, done chan<- struct{}) {
	defer close(done)

	conn.SetReadLimit(terminalReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				s.log.Debug("terminal socket closed", "err", err)
			}
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))

		if kind == websocket.BinaryMessage {
			if _, err := sess.Write(data); err != nil {
				return
			}
			continue
		}

		var msg terminalControl
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.Type == "resize" {
			if err := sess.Resize(msg.Cols, msg.Rows); err != nil {
				s.log.Debug("terminal resize failed", "err", err)
			}
		}
	}
}

// uint16Param reads a bounded integer from the query string, falling back to a
// default for anything missing or unparseable.
func uint16Param(r *http.Request, name string, fallback uint16) uint16 {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback
	}
	n, err := strconv.ParseUint(raw, 10, 16)
	if err != nil {
		return fallback
	}
	return uint16(n)
}

// currentUsername is whoever the panel runs as, which is exactly whose shell
// the terminal hands out. Best effort: an unresolvable uid is still worth
// showing as a number.
func currentUsername() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	if u.Username != "" {
		return u.Username
	}
	return u.Uid
}
