package api

import (
	"net/http"
	"sync"
	"time"
)

// The panel writes every credential event to its log, but that log goes to
// stdout and from there to journald — which means the one question an operator
// actually asks after wiring up a reverse proxy or an accelerator ("which
// address is reaching the panel, and has anyone else signed in?") can only be
// answered over SSH. This keeps the same events in memory so the settings page
// can show them.
//
// In memory on purpose, like sessions: it costs nothing, it cannot fill a disk,
// and it never becomes a second copy of the credential trail that has to be
// protected. The trade is that a restart — including a self-update — clears it,
// which is why the slog lines stay exactly as they were. This is the convenient
// view, not the system of record.
const (
	eventSignIn          = "signin"
	eventSignInFailed    = "signin-failed"
	eventThrottled       = "throttled"
	eventPaired          = "paired"
	eventPairFailed      = "pair-failed"
	eventUnpaired        = "unpaired"
	eventPasswordChanged = "password-changed"
	// eventTokenRejected is a request that presented a device token the panel
	// does not know. Almost always a client that was unpaired and has not been
	// told; the row exists so that case is visible instead of silent.
	eventTokenRejected = "token-rejected"
)

// authLogSize is how many events are kept. Two hundred rows is more history
// than anyone scrolls and still a fixed few tens of kilobytes.
const authLogSize = 200

// authEvent is one thing that happened to the panel's credentials.
type authEvent struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"`
	// Username is what the caller claimed. On a failure it is unverified by
	// definition, so the UI has to present it as a claim rather than a fact.
	Username string `json:"username,omitempty"`
	// Client is who the panel believes made the request, and Remote is the
	// address the TCP connection came from. They differ only behind a trusted
	// proxy — which is exactly the case this view exists for. Remote carries no
	// port: it changes per connection, and it would stop repeats collapsing.
	Client string `json:"client"`
	Remote string `json:"remote"`
	// Detail names the device for pairing events.
	Detail string `json:"detail,omitempty"`
	// Count folds a run of identical events into one row. Without it a client
	// being throttled would push everything else out of the ring within
	// seconds, which is the moment the history matters most.
	Count int `json:"count"`
}

// authLog is a fixed-size ring of recent credential events.
type authLog struct {
	mu   sync.Mutex
	ring []authEvent
	// next is where the following event goes; n is how many slots are in use.
	next int
	n    int
	// now is swappable so tests do not depend on the wall clock.
	now func() time.Time
}

func newAuthLog() *authLog {
	return &authLog{ring: make([]authEvent, authLogSize), now: time.Now}
}

// record files an event, folding it into the previous one if it is a repeat.
func (l *authLog) record(ev authEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()

	ev.At = l.now()
	ev.Count = 1

	if l.n > 0 {
		last := &l.ring[(l.next-1+len(l.ring))%len(l.ring)]
		if sameAuthEvent(*last, ev) {
			last.Count++
			last.At = ev.At
			return
		}
	}

	l.ring[l.next] = ev
	l.next = (l.next + 1) % len(l.ring)
	if l.n < len(l.ring) {
		l.n++
	}
}

// list returns the events newest first, which is the order they are read in.
func (l *authLog) list() []authEvent {
	l.mu.Lock()
	defer l.mu.Unlock()

	out := make([]authEvent, 0, l.n)
	for i := range l.n {
		out = append(out, l.ring[(l.next-1-i+2*len(l.ring))%len(l.ring)])
	}
	return out
}

// sameAuthEvent reports whether two events are the same thing happening again,
// and so should share a row. Deliberately not comparing At or Count.
func sameAuthEvent(a, b authEvent) bool {
	return a.Kind == b.Kind &&
		a.Username == b.Username &&
		a.Client == b.Client &&
		a.Remote == b.Remote &&
		a.Detail == b.Detail
}

// recordAuth files an event for the request in hand, filling in both addresses.
func (s *Server) recordAuth(r *http.Request, kind, username, detail string) {
	s.authLog.record(authEvent{
		Kind:     kind,
		Username: username,
		Client:   s.clientAddr(r),
		Remote:   peerHost(r),
		Detail:   detail,
	})
}

// peerHost is the address the connection came from, without the ephemeral port.
func peerHost(r *http.Request) string {
	if addr, ok := peerAddr(r); ok {
		return addr.String()
	}
	return r.RemoteAddr
}
