package instance

import (
	"sync"
	"time"
)

// StreamKind marks where a console line came from.
type StreamKind string

const (
	StreamStdout StreamKind = "stdout"
	StreamStderr StreamKind = "stderr"
	// StreamSystem is used for notices produced by the panel itself
	// (start/stop announcements, crash reports) so they are visually
	// distinguishable from the server's own output.
	StreamSystem StreamKind = "system"
)

// Line is a single console line. Seq is monotonic per instance and lets a
// reconnecting client ask for "everything after N" instead of the whole buffer.
type Line struct {
	Seq    uint64     `json:"seq"`
	Time   time.Time  `json:"time"`
	Stream StreamKind `json:"stream"`
	Text   string     `json:"text"`
}

// EventType discriminates the payloads pushed over the console websocket.
type EventType string

const (
	EventLine  EventType = "line"
	EventState EventType = "state"
)

// Event is one message in the console stream.
type Event struct {
	Type  EventType  `json:"type"`
	Line  *Line      `json:"line,omitempty"`
	State *StateInfo `json:"state,omitempty"`
}

// ring is a fixed-capacity circular buffer of console lines. It is the
// scrollback a client sees when it opens the console for an already running
// server, which is what makes closing the browser harmless.
type ring struct {
	mu    sync.Mutex
	buf   []Line
	next  int
	count int
	seq   uint64
}

func newRing(capacity int) *ring {
	if capacity <= 0 {
		capacity = 1
	}
	return &ring{buf: make([]Line, capacity)}
}

func (r *ring) append(stream StreamKind, text string) Line {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.seq++
	line := Line{Seq: r.seq, Time: time.Now(), Stream: stream, Text: text}
	r.buf[r.next] = line
	r.next = (r.next + 1) % len(r.buf)
	if r.count < len(r.buf) {
		r.count++
	}
	return line
}

// since returns buffered lines with Seq > after, oldest first. A client that
// reconnects passes the last Seq it rendered and gets only the gap.
func (r *ring) since(after uint64) []Line {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]Line, 0, r.count)
	start := (r.next - r.count + len(r.buf)) % len(r.buf)
	for i := 0; i < r.count; i++ {
		line := r.buf[(start+i)%len(r.buf)]
		if line.Seq > after {
			out = append(out, line)
		}
	}
	return out
}

// lastSeq reports the newest sequence number handed out so far.
func (r *ring) lastSeq() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seq
}

// broker fans console events out to every connected client.
//
// Subscribers are dropped rather than blocked when they fall behind: a stalled
// websocket must never be able to back-pressure the pipe we are reading the
// server's stdout from. A dropped client sees its channel close, reconnects,
// and replays the gap from the ring via since().
type broker struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

func newBroker() *broker {
	return &broker{subs: make(map[chan Event]struct{})}
}

const subscriberBuffer = 512

func (b *broker) subscribe() chan Event {
	ch := make(chan Event, subscriberBuffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *broker) unsubscribe(ch chan Event) {
	b.mu.Lock()
	if _, ok := b.subs[ch]; ok {
		delete(b.subs, ch)
		close(ch)
	}
	b.mu.Unlock()
}

func (b *broker) publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
			// Slow consumer: cut it loose and let it resync on reconnect.
			delete(b.subs, ch)
			close(ch)
		}
	}
}

func (b *broker) closeAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		delete(b.subs, ch)
		close(ch)
	}
}
