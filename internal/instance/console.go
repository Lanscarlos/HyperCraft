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
	// EventOutput carries raw terminal bytes and is what a TTY-backed instance
	// sends instead of EventLine. The two never mix on one connection: which
	// of them a console speaks is fixed for the life of the socket.
	EventOutput EventType = "output"
)

// Event is one message in the console stream.
type Event struct {
	Type  EventType  `json:"type"`
	Line  *Line      `json:"line,omitempty"`
	State *StateInfo `json:"state,omitempty"`
	// Data is terminal output. It never travels as JSON — splicing raw bytes
	// into a JSON string would break the multi-byte characters and escape
	// sequences it exists to carry — so the transport sends it as a binary
	// frame instead.
	Data []byte `json:"-"`
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

// terminalScrollbackBytes is how much raw terminal output is retained for a
// TTY-backed instance. It is what a browser opening the console sees before
// anything new arrives, so it has to be enough to hold a startup log — but it
// is also held per instance for as long as the panel runs, which is why it is
// counted in bytes rather than in redraws.
const terminalScrollbackBytes = 256 * 1024

// byteRing is the scrollback of a terminal-backed console: raw bytes, escape
// sequences and all, replayed verbatim into a fresh xterm.
//
// Chunks are dropped whole from the front, so a replay can begin in the middle
// of an escape sequence. A terminal emulator discards the fragment and resyncs
// on the next one, which costs at most a few characters at the very top of the
// scrollback — the alternative, parsing the stream to find a safe cut point,
// means emulating a terminal server-side to hand one to the client that
// already is one.
type byteRing struct {
	mu     sync.Mutex
	chunks [][]byte
	size   int
	limit  int
}

func newByteRing(limit int) *byteRing {
	if limit <= 0 {
		limit = terminalScrollbackBytes
	}
	return &byteRing{limit: limit}
}

// append takes ownership of chunk, which must not be written to afterwards.
// Callers hand over a freshly decoded buffer, so there is nothing to copy.
func (r *byteRing) append(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	r.chunks = append(r.chunks, chunk)
	r.size += len(chunk)
	for r.size > r.limit && len(r.chunks) > 1 {
		r.size -= len(r.chunks[0])
		r.chunks[0] = nil
		r.chunks = r.chunks[1:]
	}
}

// snapshot returns the retained output as one buffer, oldest first.
func (r *byteRing) snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]byte, 0, r.size)
	for _, chunk := range r.chunks {
		out = append(out, chunk...)
	}
	return out
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

// attach registers a subscriber, running snapshot (if given) under the same
// lock publishing takes. That is what lets a caller capture the scrollback and
// join the stream as one atomic step, with no window for output to fall into
// both or neither.
func (b *broker) attach(snapshot func()) chan Event {
	ch := make(chan Event, subscriberBuffer)
	b.mu.Lock()
	defer b.mu.Unlock()
	if snapshot != nil {
		snapshot()
	}
	b.subs[ch] = struct{}{}
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
	b.publishFunc(func() Event { return ev })
}

// publishFunc builds the event under the broker's lock and fans it out. The
// builder is where a caller records into its own scrollback, so that appending
// and publishing cannot be observed separately by an attaching subscriber.
func (b *broker) publishFunc(build func() Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ev := build()
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
