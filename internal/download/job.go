package download

import (
	"context"
	"io"
	"time"
)

// Kind is which shelf a download belongs to. It is what the panel-wide list is
// filtered by, and — because each shelf has its own capability — what decides
// whether a given account may see the job at all.
type Kind string

const (
	KindCore     Kind = "core"
	KindJava     Kind = "java"
	KindDatabase Kind = "database"
	KindPlugin   Kind = "plugin"
)

// State is where a download has got to.
//
// The union of what the five separate implementations used to have between
// them: extracting was only ever Java's and the database's, queued was only
// ever the plugin queue's. A shelf that never reaches a state simply never
// reports it.
type State string

const (
	// StateQueued is waiting for one of the concurrency slots.
	StateQueued      State = "queued"
	StateDownloading State = "downloading"
	// StateExtracting is the install half: the bytes are on disk and the
	// caller's Install hook is unpacking them. Reported separately because a
	// 200 MB JDK spends real time here and a bar that sat at 100% would read
	// as a hang.
	StateExtracting State = "extracting"
	StateDone       State = "done"
	StateFailed     State = "failed"
	StateCancelled  State = "cancelled"
)

// Active reports whether a job is still going to do something.
func (s State) Active() bool {
	return s == StateQueued || s == StateDownloading || s == StateExtracting
}

// Job is a snapshot of one download, whatever shelf it belongs to.
//
// It survives the transfer: the finished job stays readable so an operator who
// closed the tab still sees how it went. Before this existed each shelf kept
// one slot, and a failure at 3am was overwritten by the next download.
type Job struct {
	// ID names this job for cancellation. Assigned by the panel and unique for
	// as long as the process lives — the queue is deliberately not persisted,
	// because a download that was interrupted by a panel restart is one that
	// has to be started again rather than resumed.
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`
	// Title and Subtitle are what the panel shows. Built by the caller, which
	// is the only side that knows a build number from a major version.
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	FileName string `json:"fileName"`
	// Route is where the bytes actually came from, which with an automatic
	// order in play is not something the operator's setting can tell them.
	Route      string `json:"route,omitempty"`
	Total      int64  `json:"total"`
	Downloaded int64  `json:"downloaded"`
	State      State  `json:"state"`
	Error      string `json:"error,omitempty"`
	// Ref is what the finished download produced, by the id its own shelf knows
	// it as — a core, a runtime, an install, a plugin. It is how the UI offers
	// "go and look at it" without the panel having to guess.
	Ref      string    `json:"ref,omitempty"`
	QueuedAt time.Time `json:"queuedAt"`
	// StartedAt is when the job left the queue, so it is absent on one that
	// never has. Kept separate from QueuedAt rather than folded into it: "sat
	// in the queue for four minutes" and "took four minutes to download" are
	// different complaints with different causes.
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// Attempt is one place the bytes might come from.
//
// The queue does not build URLs. Route selection lives with the caller because
// it is not uniformly mechanical: a private GitHub asset has exactly one route
// and must never see a proxy, and that rule has no business in a package that
// does not know what a token is. What the queue owns is walking the list,
// recording which entry answered, and verifying what came back.
type Attempt struct {
	Route string
	Open  func(ctx context.Context) (io.ReadCloser, error)
}

// Progress is the handle an Install hook reports through, so the extract half
// of a job keeps the same bar moving as the download half.
type Progress struct {
	q     *Queue
	entry *entry
}
