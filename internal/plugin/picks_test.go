package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// shelfHost answers the two endpoints the shelf reads — Modrinth's batched
// project lookup and one Spiget resource — and counts what it was asked.
//
// Only two ids come back, one per source, which is enough to prove both halves
// of the shelf resolve and short enough to keep the fixture readable. It also
// means every resolve here is a *partial* one as far as the cache is concerned;
// the tests that care set the lifetimes they need.
func shelfHost(t *testing.T, gate <-chan struct{}) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) > 2 && gate != nil {
			// Rounds after the first hang until the test lets them go, which is
			// how "served without waiting for the refresh" is asserted.
			<-gate
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/projects"):
			_, _ = w.Write([]byte(`[{"slug":"essentialsx","title":"EssentialsX","description":"服务器基础指令",
				"downloads":1000,"loaders":["paper"],"game_versions":["1.20.4"],"categories":["management"]}]`))
		case strings.HasPrefix(r.URL.Path, "/resources/"):
			_, _ = w.Write([]byte(`{"id":34315,"name":"Vault","tag":"前置库","downloads":50,
				"testedVersions":["1.20"],"file":{"type":".jar","url":"resources/vault.34315/download"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func shelfRegistry(t *testing.T, gate <-chan struct{}) (*Registry, *atomic.Int32) {
	t.Helper()
	server, calls := shelfHost(t, gate)
	registry := NewRegistry("test")
	registry.modrinthBase = server.URL
	registry.spigetBase = server.URL
	return registry, calls
}

func shelfNames(groups []PickGroup) []string {
	var names []string
	for _, group := range groups {
		for _, listing := range group.Listings {
			names = append(names, listing.Name)
		}
	}
	return names
}

// The shelf is the same eleven ids on every page load, so reading it twice is
// two rounds of upstream calls to draw the same thing — and that read is on the
// critical path of 插件市场 opening, which is what made opening it several
// seconds of blank page.
func TestShelfIsReadOnceAndThenServedFromMemory(t *testing.T) {
	registry, calls := shelfRegistry(t, nil)
	// Both sources answer, but with fewer ids than the shelf lists, so the
	// entry is stored as partial. Give that the long lifetime for this test:
	// what is under test is the hit, not how long a short shelf is kept.
	registry.picks.partialTTL = picksTTL

	first := registry.Picks(context.Background())
	if got := shelfNames(first); len(got) != 2 {
		t.Fatalf("shelf resolved to %v, want both sources", got)
	}
	after := calls.Load()
	if after == 0 {
		t.Fatal("nothing was asked upstream at all")
	}

	second := registry.Picks(context.Background())
	if got := shelfNames(second); len(got) != 2 {
		t.Fatalf("cached shelf came back as %v, want the same two rows", got)
	}
	if calls.Load() != after {
		t.Errorf("second call made %d more upstream requests, want none",
			calls.Load()-after)
	}
}

// Every row that leaves here gets stamped with a compatibility verdict computed
// against the servers *this* operator ticked. Handing out the cache's own rows
// would write one operator's badges into shared memory and then show them to
// the next one, which is worse than the slow page: it is a wrong answer.
func TestShelfRowsAreCopiedSoOneOperatorsBadgesStayTheirs(t *testing.T) {
	registry, _ := shelfRegistry(t, nil)
	registry.picks.partialTTL = picksTTL

	first := registry.Picks(context.Background())
	if len(first) == 0 || len(first[0].Listings) == 0 {
		t.Fatal("shelf came back empty")
	}
	first[0].Listings[0].Compat = &Compat{State: CompatBad, Detail: "只对这一台服成立"}
	first[0].Listings[0].Name = "改过的名字"

	second := registry.Picks(context.Background())
	if second[0].Listings[0].Compat != nil {
		t.Errorf("cached row carries %+v, want no verdict at all",
			second[0].Listings[0].Compat)
	}
	if second[0].Listings[0].Name == "改过的名字" {
		t.Error("a caller's edit reached the cached shelf")
	}
}

// A resolve that produced nothing is remembered only long enough to stop the
// panel spending the registry timeout on every page load. It must not become
// the answer: the moment upstream works, the shelf fills in.
func TestAFailedShelfIsNotTheAnswer(t *testing.T) {
	var broken atomic.Bool
	broken.Store(true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if broken.Load() {
			http.Error(w, "上游炸了", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/projects") {
			_, _ = w.Write([]byte(`[{"slug":"essentialsx","title":"EssentialsX"}]`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	registry := NewRegistry("test")
	registry.modrinthBase = server.URL
	registry.spigetBase = server.URL
	// No retry window: what is under test is that the failure is not sticky,
	// not how long the panel waits before asking again.
	registry.picks.emptyTTL = 0
	registry.picks.partialTTL = picksTTL

	if got := registry.Picks(context.Background()); len(got) != 0 {
		t.Fatalf("a dead upstream produced %v, want an empty shelf", shelfNames(got))
	}

	broken.Store(false)
	if got := shelfNames(registry.Picks(context.Background())); len(got) != 1 {
		t.Fatalf("shelf is %v after upstream recovered, want the row it can now read", got)
	}
}

// Once the shelf has been read once, nobody waits on a registry again. A stale
// entry is handed over as it is and the new one is fetched behind the page —
// asserted here by hanging the second round of upstream calls outright: a
// refresh on the critical path would hang with it.
func TestAStaleShelfIsServedWhileTheRefreshHappensBehindThePage(t *testing.T) {
	gate := make(chan struct{})
	registry, calls := shelfRegistry(t, gate)
	// Let the background refresh finish even if this test fails early;
	// httptest's Close waits for handlers still in flight.
	t.Cleanup(func() { close(gate) })
	// Stale the moment it is stored.
	registry.picks.ttl = 0
	registry.picks.partialTTL = 0

	if got := shelfNames(registry.Picks(context.Background())); len(got) != 2 {
		t.Fatalf("first read produced %v, want both rows", got)
	}
	before := calls.Load()

	served := make(chan []PickGroup, 1)
	go func() { served <- registry.Picks(context.Background()) }()

	select {
	case groups := <-served:
		if got := shelfNames(groups); len(got) != 2 {
			t.Errorf("stale read produced %v, want the shelf it already had", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a stale shelf made the page wait on the registries")
	}

	// And the refresh did go out, rather than the entry being served forever.
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() == before {
		if time.Now().After(deadline) {
			t.Fatal("no refresh followed the stale read")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Opening the market fires two browse requests, not one — the page ticks a
// default server as soon as it learns the target list, and that re-asks. With a
// cold cache both are in flight at once, and they must share one resolve.
func TestConcurrentColdReadsShareOneResolve(t *testing.T) {
	registry, calls := shelfRegistry(t, nil)
	registry.picks.partialTTL = picksTTL

	const readers = 8
	done := make(chan []PickGroup, readers)
	for range readers {
		go func() { done <- registry.Picks(context.Background()) }()
	}
	for range readers {
		if got := shelfNames(<-done); len(got) != 2 {
			t.Errorf("a concurrent reader got %v, want both rows", got)
		}
	}

	// Two endpoints, one resolve: eight readers must not be eight rounds.
	if got := calls.Load(); got > 2 {
		t.Errorf("%d upstream requests for %d readers, want the 2 of a single resolve",
			got, readers)
	}
}
