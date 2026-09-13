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
		Install:   func(context.Context, string, *Progress) (string, error) { return title + "-ref", nil },
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

var _ = errors.Is
