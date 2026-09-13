package download

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// held serves bytes but blocks inside Open until release is closed, which is
// the only way to observe a queue: everything else finishes too fast to
// overlap.
type held struct {
	inFlight atomic.Int32
	peak     atomic.Int32
	release  chan struct{}
}

func newHeld() *held { return &held{release: make(chan struct{})} }

func (h *held) attempt(body string) Attempt {
	return Attempt{Route: "test", Open: func(ctx context.Context) (io.ReadCloser, error) {
		now := h.inFlight.Add(1)
		for {
			peak := h.peak.Load()
			if now <= peak || h.peak.CompareAndSwap(peak, now) {
				break
			}
		}
		defer h.inFlight.Add(-1)
		select {
		case <-h.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return io.NopCloser(strings.NewReader(body)), nil
	}}
}

func req(q *Queue, h *held, kind Kind, title string) Request {
	return Request{
		Kind:      kind,
		Title:     title,
		FileName:  title + ".bin",
		DedupeKey: title,
		Attempts:  func(context.Context) ([]Attempt, error) { return []Attempt{h.attempt("ok!")}, nil },
		Install:   func(context.Context, string, string, *Progress) (string, error) { return title + "-ref", nil },
	}
}

func TestDownloadsOfOneKindRunSideBySideUpToItsLimit(t *testing.T) {
	h := newHeld()
	q := NewQueue(slog.New(slog.DiscardHandler))
	t.Cleanup(q.Close)
	q.SetLimit(KindPlugin, 3)

	for i := 0; i < 5; i++ {
		if _, err := q.Submit(req(q, h, KindPlugin, "p"+string(rune('a'+i)))); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	waitFor(t, func() bool { return h.inFlight.Load() == 3 })
	close(h.release)
	waitFor(t, func() bool { return activeCount(q) == 0 })

	if peak := h.peak.Load(); peak != 3 {
		t.Fatalf("peak concurrency = %d, want 3", peak)
	}
}

// The limit is per Kind because the reason for it is upstream's rate limit,
// not the panel's disk: three plugin jars must not keep a JDK waiting.
func TestOneKindsQueueDoesNotBlockAnother(t *testing.T) {
	h := newHeld()
	q := NewQueue(slog.New(slog.DiscardHandler))
	t.Cleanup(q.Close)
	q.SetLimit(KindPlugin, 1)
	q.SetLimit(KindJava, 1)

	if _, err := q.Submit(req(q, h, KindPlugin, "jar")); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Submit(req(q, h, KindJava, "jdk")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return h.inFlight.Load() == 2 })
	close(h.release)
}

func TestAskingTwiceForTheSameThingReusesTheJob(t *testing.T) {
	h := newHeld()
	q := NewQueue(slog.New(slog.DiscardHandler))
	t.Cleanup(q.Close)
	q.SetLimit(KindPlugin, 1)

	first, err := q.Submit(req(q, h, KindPlugin, "same"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := q.Submit(req(q, h, KindPlugin, "same"))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("second submit made a new job %s, want %s", second.ID, first.ID)
	}
	close(h.release)
}

// TestFinishDoesNotResurrectAFinishedJob pins the guard at the top of finish:
// a job cancelled while still queued is already finished, and a worker that
// started before the cancellation landed must not overwrite that outcome.
// Task 2's Cancel(id) is exactly the caller this protects against racing
// work's own call to finish.
func TestFinishDoesNotResurrectAFinishedJob(t *testing.T) {
	h := newHeld()
	q := NewQueue(slog.New(slog.DiscardHandler))
	t.Cleanup(q.Close)
	q.SetLimit(KindPlugin, 1)

	if _, err := q.Submit(req(q, h, KindPlugin, "solo")); err != nil {
		t.Fatal(err)
	}

	q.mu.Lock()
	e := q.jobs[0]
	q.mu.Unlock()

	first := errors.New("first")
	q.finish(e, StateFailed, first)
	q.finish(e, StateDone, nil) // must be a no-op: the job already finished

	q.mu.Lock()
	state, errText := e.pub.State, e.pub.Error
	q.mu.Unlock()

	if state != StateFailed || errText != first.Error() {
		t.Fatalf("finish resurrected a finished job: state=%s err=%q, want %s/%q",
			state, errText, StateFailed, first.Error())
	}

	close(h.release) // let the worker unblock so Close does not wait out its timeout
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within 3s")
}

func activeCount(q *Queue) int {
	n := 0
	for _, job := range q.Jobs() {
		if job.State.Active() {
			n++
		}
	}
	return n
}

func stateOf(q *Queue, id string) State {
	for _, job := range q.Jobs() {
		if job.ID == id {
			return job.State
		}
	}
	return ""
}

// CancelAll's kind filter is new: the single-kind plugin.Downloader this was
// extracted from had no such concept, so nothing in the original suite could
// have caught a reversed or ignored filter.
func TestCancelAllOnlyStopsTheGivenKinds(t *testing.T) {
	h := newHeld()
	q := NewQueue(slog.New(slog.DiscardHandler))
	t.Cleanup(q.Close)
	q.SetLimit(KindPlugin, 3)
	q.SetLimit(KindJava, 3)

	plugin, _ := q.Submit(req(q, h, KindPlugin, "jar"))
	java, _ := q.Submit(req(q, h, KindJava, "jdk"))
	waitFor(t, func() bool { return h.inFlight.Load() == 2 })

	if n := q.CancelAll(KindPlugin); n != 1 {
		t.Fatalf("CancelAll(KindPlugin) stopped %d, want 1", n)
	}
	waitFor(t, func() bool { return stateOf(q, plugin.ID) == StateCancelled })
	if got := stateOf(q, java.ID); got != StateDownloading {
		t.Fatalf("java job state = %q, want downloading (filter must not touch other kinds)", got)
	}
	close(h.release)
}

// The no-kinds form is "cancel everything", and it has to actually reach
// every kind rather than, say, only the zero value of Kind.
func TestCancelAllWithNoKindsStopsEveryKind(t *testing.T) {
	h := newHeld()
	q := NewQueue(slog.New(slog.DiscardHandler))
	t.Cleanup(q.Close)
	q.SetLimit(KindPlugin, 3)
	q.SetLimit(KindJava, 3)

	plugin, _ := q.Submit(req(q, h, KindPlugin, "jar2"))
	java, _ := q.Submit(req(q, h, KindJava, "jdk2"))
	waitFor(t, func() bool { return h.inFlight.Load() == 2 })

	if n := q.CancelAll(); n != 2 {
		t.Fatalf("CancelAll() stopped %d, want 2", n)
	}
	waitFor(t, func() bool { return stateOf(q, plugin.ID) == StateCancelled })
	waitFor(t, func() bool { return stateOf(q, java.ID) == StateCancelled })
	close(h.release)
}

func TestCancelUnknownIDReturnsErrNotFound(t *testing.T) {
	q := NewQueue(slog.New(slog.DiscardHandler))
	t.Cleanup(q.Close)

	if err := q.Cancel("no-such-id"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Cancel(unknown) = %v, want ErrNotFound", err)
	}
}

// Cancelling a job that already finished must be rejected, and — this is the
// case finish()'s re-entrancy guard exists for — must not rewrite the
// recorded outcome of a job that is done.
func TestCancelOnAFinishedJobErrorsAndLeavesItAlone(t *testing.T) {
	done := newHeld()
	close(done.release)
	q := NewQueue(slog.New(slog.DiscardHandler))
	t.Cleanup(q.Close)
	q.SetLimit(KindPlugin, 1)

	job, err := q.Submit(req(q, done, KindPlugin, "already-done"))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return stateOf(q, job.ID) == StateDone })

	if err := q.Cancel(job.ID); err == nil {
		t.Fatal("Cancel on a finished job returned nil, want an error")
	}
	if got := stateOf(q, job.ID); got != StateDone {
		t.Fatalf("Cancel on a finished job changed its state to %q, want done", got)
	}
}

// A job still in StateQueued has no worker to interrupt: Cancel must finish
// it inline, and — the point of this test — it must never reach
// StateDownloading afterwards even once its kind's slot frees up.
func TestCancelOnAQueuedJobFinishesItWithoutEverDispatching(t *testing.T) {
	occupied := newHeld()
	q := NewQueue(slog.New(slog.DiscardHandler))
	t.Cleanup(q.Close)
	q.SetLimit(KindPlugin, 1)

	first, _ := q.Submit(req(q, occupied, KindPlugin, "first"))
	waitFor(t, func() bool { return stateOf(q, first.ID) == StateDownloading })

	waiting, _ := q.Submit(req(q, occupied, KindPlugin, "waiting"))
	if got := stateOf(q, waiting.ID); got != StateQueued {
		t.Fatalf("second job state = %q, want queued (limit is 1 and the first is still running)", got)
	}

	if err := q.Cancel(waiting.ID); err != nil {
		t.Fatalf("cancel queued job: %v", err)
	}
	if got := stateOf(q, waiting.ID); got != StateCancelled {
		t.Fatalf("queued job state after Cancel = %q, want cancelled", got)
	}

	// Free the slot the first job holds and give the queue a moment to
	// dispatch whatever it thinks is next — it must not be the cancelled job.
	close(occupied.release)
	waitFor(t, func() bool { return activeCount(q) == 0 })
	if got := stateOf(q, waiting.ID); got != StateCancelled {
		t.Fatalf("cancelled job was dispatched after all: state = %q", got)
	}
}

func TestCancelStopsOneJobAndLeavesTheRest(t *testing.T) {
	h := newHeld()
	q := NewQueue(slog.New(slog.DiscardHandler))
	t.Cleanup(q.Close)
	q.SetLimit(KindPlugin, 3)

	one, _ := q.Submit(req(q, h, KindPlugin, "one"))
	two, _ := q.Submit(req(q, h, KindPlugin, "two"))
	waitFor(t, func() bool { return h.inFlight.Load() == 2 })

	if err := q.Cancel(one.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	waitFor(t, func() bool { return stateOf(q, one.ID) == StateCancelled })
	if got := stateOf(q, two.ID); got != StateDownloading {
		t.Fatalf("sibling state = %q, want downloading", got)
	}
	close(h.release)
}

// A failure has to outlive the download that follows it: before the queue
// existed the next job overwrote the only record of what went wrong.
func TestAFailedJobSurvivesTheNextDownload(t *testing.T) {
	q := NewQueue(slog.New(slog.DiscardHandler))
	t.Cleanup(q.Close)
	q.SetLimit(KindCore, 1)

	bad := Request{
		Kind: KindCore, Title: "bad", DedupeKey: "bad",
		Attempts: func(context.Context) ([]Attempt, error) { return nil, errors.New("boom") },
	}
	if _, err := q.Submit(bad); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return activeCount(q) == 0 })

	h := newHeld()
	close(h.release)
	if _, err := q.Submit(req(q, h, KindCore, "good")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return activeCount(q) == 0 })

	var failed int
	for _, job := range q.Jobs() {
		if job.State == StateFailed && job.Error != "" {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("failed jobs in history = %d, want 1", failed)
	}
}

func TestClearFinishedKeepsWhatIsStillRunning(t *testing.T) {
	h := newHeld()
	q := NewQueue(slog.New(slog.DiscardHandler))
	t.Cleanup(q.Close)
	q.SetLimit(KindPlugin, 2)

	done := newHeld()
	close(done.release)
	if _, err := q.Submit(req(q, done, KindPlugin, "over")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return activeCount(q) == 0 })

	live, _ := q.Submit(req(q, h, KindPlugin, "live"))
	waitFor(t, func() bool { return stateOf(q, live.ID) == StateDownloading })

	if n := q.ClearFinished(); n != 1 {
		t.Fatalf("cleared %d, want 1", n)
	}
	if got := stateOf(q, live.ID); got != StateDownloading {
		t.Fatalf("running job state = %q, want downloading", got)
	}
	close(h.release)
}
