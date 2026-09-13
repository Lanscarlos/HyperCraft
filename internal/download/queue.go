package download

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

var (
	// ErrBusy is returned when the queue is full.
	ErrBusy = errors.New("下载队列已经排满了，等几个下完再来")
	// ErrCancelled is recorded on a job the operator stopped.
	ErrCancelled = errors.New("download cancelled")
)

// maxQueued bounds jobs waiting for a slot. A backstop against a bulk action
// that fans out further than anybody intended, not a limit anyone should meet.
const maxQueued = 100

// maxHistory bounds the finished jobs kept around to be read.
//
// Finished jobs are the whole reason this is a list rather than a counter: a
// download that failed at 3am is only useful if it is still there in the
// morning, and before the queue existed the *next* download overwrote it.
const maxHistory = 30

// defaultLimits is how many of each kind come down at once.
//
// Per kind rather than one shared pool, and the numbers are upstream's rather
// than the disk's. Three plugin jars is what the GitHub API tolerates: every
// plugin job opens with a release lookup, an anonymous panel gets 60 calls an
// hour, and a burst is answered with a rate limit that then blocks the next
// *check* too. A JDK and a server core have no such lookup and no relation to
// that budget, so queueing them behind three jars would be a limit invented
// here rather than imposed from outside.
var defaultLimits = map[Kind]int{
	KindPlugin:   3,
	KindCore:     1,
	KindJava:     1,
	KindDatabase: 1,
}

// Request is what a caller submits.
type Request struct {
	Kind            Kind
	Title, Subtitle string
	FileName        string
	// Total is the declared size, for the bar. Zero means unknown.
	Total int64
	// SHA256 is the digest the *upstream metadata* published, which is what
	// makes a mirror safe to use: whichever route serves the bytes, they are
	// checked against what the origin said they would be. Empty where upstream
	// publishes none (GitHub release assets), and then there is no content
	// check at all — see transfer.
	SHA256 string
	// SHA512 is the same thing in the other algorithm, and exists because
	// Modrinth publishes sha512 (and sha1, deliberately never read: it is the
	// weakest of the three and would be the one an attacker picks if the panel
	// accepted it) and no sha256. A source publishes one or the other, never
	// both, and either is enough to check a download against — see transfer.
	SHA512 string
	// DedupeKey collapses a repeat request onto the job already doing it. Two
	// workers writing the same part file is a corrupt download.
	DedupeKey string
	// TempDir is where the part file is written before Install is handed it.
	// Empty means os.TempDir(). Callers that want the bytes to land on the same
	// filesystem as their final home set it, so the move at the end is a rename
	// rather than a copy of 200 MB.
	TempDir string
	// Attempts is where to try, most preferred first. Called on the worker
	// rather than at submit time, because a queued job may be minutes from its
	// turn and the operator may have changed the route in between.
	Attempts func(ctx context.Context) ([]Attempt, error)
	// Install is what to do with the finished bytes: unpack, record, register.
	// It runs on the worker goroutine with the job in StateExtracting, and
	// returns the id its shelf knows the result by.
	Install func(ctx context.Context, temp string, pub *Progress) (ref string, err error)
}

// entry is one queue slot: the public snapshot plus what it takes to run it.
type entry struct {
	pub    *Job
	req    Request
	cancel context.CancelFunc
}

// Queue runs downloads for every shelf in the panel.
//
// It belongs to the daemon rather than to the request that started it, so
// closing the tab does not interrupt something already coming down, and a job
// that was still waiting for a slot does not lose its place.
type Queue struct {
	log *slog.Logger

	mu     sync.Mutex
	jobs   []*entry // oldest first, which is the order they run in
	active map[Kind]int
	limits map[Kind]int
	seq    int
	closed bool

	wg sync.WaitGroup
}

func NewQueue(logger *slog.Logger) *Queue {
	limits := make(map[Kind]int, len(defaultLimits))
	for kind, n := range defaultLimits {
		limits[kind] = n
	}
	return &Queue{log: logger, active: map[Kind]int{}, limits: limits}
}

// SetLimit overrides one kind's concurrency. Tests use it; production takes
// the defaults.
func (q *Queue) SetLimit(kind Kind, n int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.limits[kind] = n
}

func (q *Queue) Submit(r Request) (Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return Job{}, ErrBusy
	}
	if existing := q.duplicate(r.Kind, r.DedupeKey); existing != nil {
		return *existing.pub, nil
	}
	queued := 0
	for _, e := range q.jobs {
		if e.pub.State == StateQueued {
			queued++
		}
	}
	if queued >= maxQueued {
		return Job{}, ErrBusy
	}

	q.seq++
	e := &entry{
		pub: &Job{
			ID:       strconv.Itoa(q.seq),
			Kind:     r.Kind,
			Title:    r.Title,
			Subtitle: r.Subtitle,
			FileName: r.FileName,
			Total:    r.Total,
			State:    StateQueued,
			QueuedAt: time.Now(),
		},
		req: r,
	}
	q.jobs = append(q.jobs, e)
	q.prune()
	q.dispatch()
	return *e.pub, nil
}

// duplicate finds an unfinished job for exactly this request. Called with the
// lock held. An empty key never matches: a caller that does not name its
// request is asking for a second one.
func (q *Queue) duplicate(kind Kind, key string) *entry {
	if key == "" {
		return nil
	}
	for _, e := range q.jobs {
		if e.pub.State.Active() && e.pub.Kind == kind && e.req.DedupeKey == key {
			return e
		}
	}
	return nil
}

// prune drops the oldest finished jobs once there are more than the history
// holds. Only finished ones: a queue longer than the history is still a queue,
// and forgetting a job that has not run yet would lose the download. Called
// with the lock held.
func (q *Queue) prune() {
	finished := 0
	for _, e := range q.jobs {
		if !e.pub.State.Active() {
			finished++
		}
	}
	if finished <= maxHistory {
		return
	}
	drop := finished - maxHistory
	kept := make([]*entry, 0, len(q.jobs)-drop)
	for _, e := range q.jobs {
		if drop > 0 && !e.pub.State.Active() {
			drop--
			continue
		}
		kept = append(kept, e)
	}
	q.jobs = kept
}

// dispatch starts queued jobs while their kind has a slot. Called with the
// lock held.
func (q *Queue) dispatch() {
	if q.closed {
		return
	}
	for _, e := range q.jobs {
		if e.pub.State != StateQueued {
			continue
		}
		kind := e.pub.Kind
		if q.active[kind] >= q.limitOf(kind) {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		now := time.Now()
		e.cancel = cancel
		e.pub.State = StateDownloading
		e.pub.StartedAt = &now
		q.active[kind]++
		q.wg.Add(1)
		go q.work(ctx, e)
	}
}

func (q *Queue) limitOf(kind Kind) int {
	if n, ok := q.limits[kind]; ok && n > 0 {
		return n
	}
	return 1
}

// Jobs returns the queue and the history, newest first.
func (q *Queue) Jobs() []Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Job, 0, len(q.jobs))
	for i := len(q.jobs) - 1; i >= 0; i-- {
		out = append(out, *q.jobs[i].pub)
	}
	return out
}

// Close stops the queue: jobs still waiting for a slot are cancelled without
// ever running, jobs already in flight are cancelled, and Close waits for
// their workers to actually exit — so a caller that follows Close with, say,
// removing the directory a temp file lives in does not race the worker that
// is still writing to it.
func (q *Queue) Close() {
	q.mu.Lock()
	q.closed = true
	now := time.Now()
	var cancels []context.CancelFunc
	for _, e := range q.jobs {
		switch e.pub.State {
		case StateQueued:
			// Nothing will pick these up again, and leaving them as "queued"
			// would be the panel claiming work it is not going to do.
			e.pub.State = StateCancelled
			e.pub.Error = ErrCancelled.Error()
			e.pub.FinishedAt = &now
		case StateDownloading, StateExtracting:
			if e.cancel != nil {
				cancels = append(cancels, e.cancel)
			}
		}
	}
	running := len(cancels) > 0
	q.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
	if !running {
		return
	}

	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

// finish records how a job ended. The only place State leaves Active.
func (q *Queue) finish(e *entry, state State, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	// A job cancelled while it was still queued is already finished; a worker
	// that started before the cancellation landed must not resurrect it.
	if !e.pub.State.Active() {
		return
	}
	now := time.Now()
	e.pub.State = state
	e.pub.FinishedAt = &now
	if err != nil {
		e.pub.Error = err.Error()
	}
	// Pruned here as well as on insert, because a job only becomes history when
	// it ends: pruning only on insert leaves the queue one row over the cap
	// between the last download finishing and the next one starting.
	q.prune()
}

// work runs one job end to end and then hands its slot to whatever is next.
func (q *Queue) work(ctx context.Context, e *entry) {
	defer func() {
		q.wg.Done()
		q.mu.Lock()
		q.active[e.pub.Kind]--
		if e.cancel != nil {
			e.cancel()
			e.cancel = nil
		}
		q.dispatch()
		q.mu.Unlock()
	}()

	dir := e.req.TempDir
	if dir == "" {
		dir = os.TempDir()
	}
	// The job ID is unique for the life of this process, but the queue is not
	// persisted (see Job.ID) and IDs restart at 1 — a stray file left behind
	// by a process that died mid-download could otherwise collide with a
	// fresh job that happens to draw the same ID.
	temp := filepath.Join(dir, e.pub.ID+".part")
	_ = os.Remove(temp)

	if err := transfer(ctx, q, e, e.req, temp); err != nil {
		os.Remove(temp)
		if ctx.Err() != nil {
			q.finish(e, StateCancelled, ErrCancelled)
			return
		}
		q.finish(e, StateFailed, err)
		return
	}

	q.mu.Lock()
	e.pub.State = StateExtracting
	q.mu.Unlock()

	var ref string
	var err error
	if e.req.Install != nil {
		ref, err = e.req.Install(ctx, temp, &Progress{q: q, entry: e})
	}
	// Success path decides where the bytes end up (Install's own os.Rename
	// moves them out from under this path), so the Remove that follows either
	// way finds nothing there and its error is ignored.
	os.Remove(temp)
	if err != nil {
		if ctx.Err() != nil {
			q.finish(e, StateCancelled, ErrCancelled)
			return
		}
		q.finish(e, StateFailed, err)
		return
	}

	q.mu.Lock()
	e.pub.Ref = ref
	q.mu.Unlock()
	q.finish(e, StateDone, nil)
}
