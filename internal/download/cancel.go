package download

import (
	"errors"
	"fmt"
	"time"
)

// ErrNotFound rejects an id the queue does not hold.
var ErrNotFound = errors.New("没有这个下载任务")

// Cancel stops one download by id. Cancelling a finished one is an error
// rather than a no-op: the button that sends it is only drawn on a live job,
// so a request for a finished one means the page is looking at something the
// panel no longer agrees with.
func (q *Queue) Cancel(id string) error {
	q.mu.Lock()
	var found *entry
	for _, e := range q.jobs {
		if e.pub.ID == id {
			found = e
			break
		}
	}
	if found == nil {
		q.mu.Unlock()
		return fmt.Errorf("%w: 没有编号为 %s 的下载", ErrNotFound, id)
	}
	if !found.pub.State.Active() {
		q.mu.Unlock()
		return fmt.Errorf("%w: 这个下载已经结束了", ErrCancelled)
	}
	// A queued job has no worker to interrupt, so it is finished here and now.
	// Leaving it for dispatch to notice would mean a cancelled download that
	// still runs the moment a slot opens.
	if found.pub.State == StateQueued {
		now := time.Now()
		found.pub.State = StateCancelled
		found.pub.Error = ErrCancelled.Error()
		found.pub.FinishedAt = &now
		q.mu.Unlock()
		return nil
	}
	cancel := found.cancel
	q.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	return nil
}

// CancelAll stops everything still queued or running for the given kinds, or
// for every kind when given none, and reports how many. The queue page's
// one-click way out of a bulk action that turned out to be the wrong bulk
// action.
func (q *Queue) CancelAll(kinds ...Kind) int {
	want := map[Kind]bool{}
	for _, kind := range kinds {
		want[kind] = true
	}
	q.mu.Lock()
	var ids []string
	for _, e := range q.jobs {
		if !e.pub.State.Active() {
			continue
		}
		if len(want) > 0 && !want[e.pub.Kind] {
			continue
		}
		ids = append(ids, e.pub.ID)
	}
	q.mu.Unlock()

	stopped := 0
	for _, id := range ids {
		if q.Cancel(id) == nil {
			stopped++
		}
	}
	return stopped
}

// ClearFinished forgets the history and reports how many rows went. What is
// still queued or running stays — this clears a record, it does not stop work.
func (q *Queue) ClearFinished() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	kept := make([]*entry, 0, len(q.jobs))
	for _, e := range q.jobs {
		if e.pub.State.Active() {
			kept = append(kept, e)
		}
	}
	dropped := len(q.jobs) - len(kept)
	q.jobs = kept
	return dropped
}
