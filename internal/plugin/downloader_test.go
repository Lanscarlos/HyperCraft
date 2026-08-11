package plugin

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// downloadStub is a GitHub that publishes one release per plugin and serves the
// jar from the same host, with a hook for holding transfers open — which is the
// only way to observe a queue: everything else finishes too fast to overlap.
type downloadStub struct {
	server *httptest.Server
	// inFlight counts transfers currently sitting inside the handler, and peak
	// is the most that were ever there at once. peak is the assertion: the
	// concurrency limit is a claim about simultaneity, not about totals.
	inFlight atomic.Int32
	peak     atomic.Int32
	// release is closed to let held transfers finish.
	release chan struct{}
	hold    bool
	started chan string
}

func newDownloadStub(t *testing.T, hold bool) *downloadStub {
	t.Helper()
	stub := &downloadStub{
		release: make(chan struct{}),
		hold:    hold,
		started: make(chan string, 64),
	}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases") {
			owner, name, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/repos/"), "/")
			name = strings.TrimSuffix(name, "/releases")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `[{"tag_name":"v1.0.0","name":"One","draft":false,"prerelease":false,
			  "published_at":"2026-01-01T00:00:00Z",
			  "assets":[{"name":"%s-1.0.0.jar","size":4,
			             "browser_download_url":"%s/dl/%s"}]}]`, name, stub.server.URL, owner+"-"+name)
			return
		}

		now := stub.inFlight.Add(1)
		for {
			peak := stub.peak.Load()
			if now <= peak || stub.peak.CompareAndSwap(peak, now) {
				break
			}
		}
		defer stub.inFlight.Add(-1)

		select {
		case stub.started <- r.URL.Path:
		default:
		}
		if stub.hold {
			select {
			case <-stub.release:
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Second):
			}
		}
		_, _ = io.WriteString(w, "jar!")
	}))
	t.Cleanup(func() {
		stub.releaseAll()
		stub.server.Close()
	})
	return stub
}

func (s *downloadStub) releaseAll() {
	select {
	case <-s.release:
	default:
		close(s.release)
	}
}

// downloaderFixture wires a downloader to the stub with `count` tracked
// plugins, all of which have exactly one release to fetch.
func downloaderFixture(t *testing.T, hold bool, count int) (*Downloader, *downloadStub, []Plugin) {
	t.Helper()
	stub := newDownloadStub(t, hold)
	client := NewClient(stub.server.URL, "test")
	library := newLibrary(t)

	items := make([]Plugin, 0, count)
	for i := range count {
		items = append(items, addPlugin(t, library, fmt.Sprintf("Plug%d", i), fmt.Sprintf("owner%d/plug%d", i, i)))
	}
	downloader := NewDownloader(client, library, slog.New(slog.DiscardHandler))
	t.Cleanup(downloader.Close)
	return downloader, stub, items
}

// waitFor polls until the condition holds, so a test never depends on how long
// a goroutine took to get going.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func countState(jobs []Job, state JobState) int {
	n := 0
	for _, job := range jobs {
		if job.State == state {
			n++
		}
	}
	return n
}

func TestDownloadsRunSideBySideUpToTheLimit(t *testing.T) {
	downloader, stub, items := downloaderFixture(t, true, maxConcurrent+2)

	for _, item := range items {
		if _, err := downloader.Start(item.ID, "", ""); err != nil {
			t.Fatalf("Start(%s): %v", item.ID, err)
		}
	}

	// The point of the change: asking for five downloads starts five jobs
	// rather than one and four rejections.
	waitFor(t, "the limit to fill", func() bool {
		return countState(downloader.Jobs(), JobDownloading) == maxConcurrent
	})
	jobs := downloader.Jobs()
	if queued := countState(jobs, JobQueued); queued != 2 {
		t.Fatalf("expected 2 jobs still queued, got %d of %d", queued, len(jobs))
	}

	// And the point of the limit: the extra two waited rather than piling onto
	// an API that answers a burst with an hour-long rate limit.
	if peak := stub.peak.Load(); peak > maxConcurrent {
		t.Errorf("%d transfers ran at once, limit is %d", peak, maxConcurrent)
	}

	stub.releaseAll()
	waitFor(t, "every job to finish", func() bool {
		return countState(downloader.Jobs(), JobDone) == len(items)
	})
}

func TestQueuedDownloadsRunWhenASlotOpens(t *testing.T) {
	downloader, stub, items := downloaderFixture(t, false, maxConcurrent+3)

	for _, item := range items {
		if _, err := downloader.Start(item.ID, "", ""); err != nil {
			t.Fatalf("Start: %v", err)
		}
	}
	waitFor(t, "the queue to drain", func() bool {
		return countState(downloader.Jobs(), JobDone) == len(items)
	})
	if peak := stub.peak.Load(); peak > maxConcurrent {
		t.Errorf("%d transfers ran at once, limit is %d", peak, maxConcurrent)
	}
}

func TestAskingTwiceForTheSameJarReusesTheJob(t *testing.T) {
	downloader, _, items := downloaderFixture(t, true, 1)

	first, err := downloader.Start(items[0].ID, "", "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Two clicks on 更新入库 must not put two writers on one .part file. Before
	// the queue this was prevented by refusing the second outright.
	second, err := downloader.Start(items[0].ID, "", "")
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("a repeat request started a second job: %s and %s", first.ID, second.ID)
	}
	if jobs := downloader.Jobs(); len(jobs) != 1 {
		t.Errorf("expected one job, got %d", len(jobs))
	}
}

func TestARepeatOfNewestIsNotADifferentRequestOnceItResolves(t *testing.T) {
	downloader, stub, items := downloaderFixture(t, true, 1)

	first, err := downloader.Start(items[0].ID, "", "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Wait until the release has resolved and the job says v1.0.0 rather than
	// "newest". Matching a repeat against *that* is how the same jar gets
	// downloaded twice.
	waitFor(t, "the release to resolve", func() bool {
		jobs := downloader.Jobs()
		return len(jobs) == 1 && jobs[0].Tag == "v1.0.0"
	})

	second, err := downloader.Start(items[0].ID, "", "")
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("the resolved tag made a repeat look like a new request")
	}
	stub.releaseAll()
}

func TestCancelStopsOneJobAndLeavesTheRest(t *testing.T) {
	downloader, stub, items := downloaderFixture(t, true, 2)

	first, err := downloader.Start(items[0].ID, "", "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := downloader.Start(items[1].ID, "", ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "both to start", func() bool {
		return countState(downloader.Jobs(), JobDownloading) == 2
	})

	if err := downloader.Cancel(first.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	waitFor(t, "the cancelled job to unwind", func() bool {
		for _, job := range downloader.Jobs() {
			if job.ID == first.ID {
				return job.State == JobCancelled
			}
		}
		return false
	})
	// The other one is untouched: a queue whose cancel button stops everything
	// is a queue nobody dares press it on.
	for _, job := range downloader.Jobs() {
		if job.ID != first.ID && job.State != JobDownloading {
			t.Errorf("the other job went to %s", job.State)
		}
	}
	stub.releaseAll()
}

func TestCancellingAQueuedJobStopsItBeforeItRuns(t *testing.T) {
	downloader, stub, items := downloaderFixture(t, true, maxConcurrent+1)

	var last Job
	for _, item := range items {
		job, err := downloader.Start(item.ID, "", "")
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		last = job
	}
	waitFor(t, "the limit to fill", func() bool {
		return countState(downloader.Jobs(), JobDownloading) == maxConcurrent
	})

	if err := downloader.Cancel(last.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	// A queued job has no worker to interrupt. Leaving it for the dispatcher to
	// notice would mean a cancelled download that still runs the moment a slot
	// opens — which is exactly what releasing the others opens.
	stub.releaseAll()
	waitFor(t, "the running jobs to finish", func() bool {
		return countState(downloader.Jobs(), JobDone) == maxConcurrent
	})

	for _, job := range downloader.Jobs() {
		if job.ID == last.ID && job.State != JobCancelled {
			t.Fatalf("a cancelled queued job ran anyway: %s", job.State)
		}
	}
}

func TestAFailedJobSurvivesTheNextDownload(t *testing.T) {
	downloader, _, items := downloaderFixture(t, false, 1)

	// An unknown tag. This used to be refused synchronously; queued, it lands
	// on the job — which is the whole reason the history has to keep it.
	if _, err := downloader.Start(items[0].ID, "v9.9.9", ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the bad tag to fail", func() bool {
		return countState(downloader.Jobs(), JobFailed) == 1
	})

	if _, err := downloader.Start(items[0].ID, "v1.0.0", ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the good one to finish", func() bool {
		return countState(downloader.Jobs(), JobDone) == 1
	})

	// Before the queue, the second download replaced the first and the failure
	// was gone by the time anybody looked.
	if failed := countState(downloader.Jobs(), JobFailed); failed != 1 {
		t.Errorf("the failed job was lost: %d failures in %d jobs", failed, len(downloader.Jobs()))
	}
}

func TestClearFinishedKeepsWhatIsStillRunning(t *testing.T) {
	downloader, stub, items := downloaderFixture(t, true, 2)

	if _, err := downloader.Start(items[0].ID, "v9.9.9", ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the bad tag to fail", func() bool {
		return countState(downloader.Jobs(), JobFailed) == 1
	})
	if _, err := downloader.Start(items[1].ID, "", ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the good one to start", func() bool {
		return countState(downloader.Jobs(), JobDownloading) == 1
	})

	if dropped := downloader.ClearFinished(); dropped != 1 {
		t.Errorf("cleared %d rows, expected 1", dropped)
	}
	jobs := downloader.Jobs()
	if len(jobs) != 1 || jobs[0].State != JobDownloading {
		t.Errorf("clearing the history stopped work: %+v", jobs)
	}
	stub.releaseAll()
}

func TestHistoryIsBoundedButTheQueueIsNot(t *testing.T) {
	downloader, _, items := downloaderFixture(t, false, 1)

	// Every one of these fails, and fails fast: the point is the count.
	for i := range maxHistory + 5 {
		if _, err := downloader.Start(items[0].ID, fmt.Sprintf("v9.9.%d", i), ""); err != nil {
			t.Fatalf("Start: %v", err)
		}
		waitFor(t, "the job to finish", func() bool {
			jobs := downloader.Jobs()
			return len(jobs) > 0 && !jobs[0].State.Active()
		})
	}
	// The cap is on finished jobs. Anything still queued or running is work the
	// panel has promised to do and is never dropped to make room for a record.
	jobs := downloader.Jobs()
	finished := len(jobs) - countState(jobs, JobQueued) - countState(jobs, JobDownloading)
	if finished > maxHistory {
		t.Errorf("history grew to %d finished jobs, cap is %d", finished, maxHistory)
	}
}

func TestTheQueueIsNeverPrunedToMakeRoomForHistory(t *testing.T) {
	downloader, stub, items := downloaderFixture(t, true, 1)

	// Fill the history first, then queue more work than the cap. Dropping a
	// queued job to stay under a *history* limit would silently lose a download
	// the operator asked for.
	for i := range maxHistory + 2 {
		if _, err := downloader.Start(items[0].ID, fmt.Sprintf("v9.9.%d", i), ""); err != nil {
			t.Fatalf("Start: %v", err)
		}
		waitFor(t, "the job to finish", func() bool {
			jobs := downloader.Jobs()
			return len(jobs) > 0 && !jobs[0].State.Active()
		})
	}

	// Every one of these is a distinct request, so none of them dedups away.
	wanted := maxConcurrent + 4
	for i := range wanted {
		if _, err := downloader.Start(items[0].ID, "", fmt.Sprintf("Plug0-1.0.%d.jar", i)); err != nil {
			t.Fatalf("Start: %v", err)
		}
	}
	jobs := downloader.Jobs()
	live := countState(jobs, JobQueued) + countState(jobs, JobDownloading)
	if live != wanted {
		t.Errorf("%d of %d unfinished jobs survived the history cap", live, wanted)
	}
	stub.releaseAll()
}

func TestConcurrentStartsAreSerialised(t *testing.T) {
	downloader, stub, items := downloaderFixture(t, true, 4)

	// Two clicks racing on the same plugin from two tabs. The dedup runs under
	// the lock; without it both would resolve and both would open the same
	// .part file.
	var wg sync.WaitGroup
	for range 8 {
		for _, item := range items {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = downloader.Start(item.ID, "", "")
			}()
		}
	}
	wg.Wait()

	if jobs := downloader.Jobs(); len(jobs) != len(items) {
		t.Errorf("expected one job per plugin, got %d for %d plugins", len(jobs), len(items))
	}
	stub.releaseAll()
}
