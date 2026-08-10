package plugin

// What to show before anything has been typed.
//
// The discovery page used to open by asking the three registries for their
// front page, which on Modrinth means the all-time download chart — and that
// chart is Sodium, Iris, Lithium, FerriteCore: client rendering mods, six of
// them, none of which a server can load. A server panel whose first screen is
// six things that cannot be installed is not a neutral default, it is a wrong
// answer that the operator has to know enough to ignore.
//
// So the empty query is not a query. It is this: a short, hand-kept shelf of
// the plugins a Minecraft server actually gets built out of, grouped by the job
// they do, because "what do I install" is a question about jobs and not about
// popularity. Typing anything at all switches to a real search.
//
// The shelf is a list of ids, resolved live so the versions, download counts
// and compatibility metadata are the registry's own and not a snapshot rotting
// in the binary. Anything that fails to resolve is dropped without comment: a
// renamed slug should cost one entry, never the page.
//
// "Resolved live" used to mean resolved on every page load, which is what made
// opening 插件市场 several seconds of blank shelf — twice over, because the page
// ticks a default server as soon as it knows the target list and that re-asks.
// It is cached now; see picksCache below for on what terms.

import (
	"context"
	"sync"
	"time"
)

// Pick is one entry of the shelf.
type Pick struct {
	Group  string
	Source string
	ID     string
}

// PickGroup is one shelf section, as it travels to the browser.
type PickGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Note says what the group is for. The page has room for one line per
	// section and an operator who does not yet know what Vault is needs it more
	// than they need a fourth plugin in the list.
	Note     string    `json:"note,omitempty"`
	Listings []Listing `json:"listings"`
}

// The shelf. Kept short on purpose — this is the set worth reading top to
// bottom, and a curated list long enough to need scrolling is a search result
// with extra steps.
var picks = []Pick{
	{"basics", SourceModrinth, "essentialsx"},
	// Vault has never been on Modrinth or Hangar; the canonical copy is the
	// SpigotMC resource, and half the plugins below hard-depend on it.
	{"basics", SourceSpigot, "34315"},
	{"basics", SourceModrinth, "placeholderapi"},

	{"perms", SourceModrinth, "luckperms"},

	{"world", SourceModrinth, "worldedit"},
	{"world", SourceModrinth, "worldguard"},
	{"world", SourceModrinth, "coreprotect"},

	{"compat", SourceModrinth, "viaversion"},
	{"compat", SourceModrinth, "viabackwards"},

	{"perf", SourceModrinth, "spark"},
	{"perf", SourceModrinth, "chunky"},
}

var pickGroups = []PickGroup{
	{ID: "basics", Name: "基础", Note: "几乎每台服都会装的那几个 —— 别的插件常常直接依赖它们"},
	{ID: "perms", Name: "权限", Note: "谁能用哪条命令、进哪个世界、拿哪个前缀"},
	{ID: "world", Name: "世界", Note: "改地形、圈领地、把熊孩子干的事回滚掉"},
	{ID: "compat", Name: "版本兼容", Note: "让别的版本的客户端也能连进这台服"},
	{ID: "perf", Name: "性能", Note: "先量再优化：卡在哪是查出来的，不是猜出来的"},
}

// How long a resolved shelf is served before it is resolved again.
//
// Six hours because of what the shelf actually is: eleven ids that were chosen
// by hand and change when somebody edits this file, and per id a name, an icon,
// a supported-version list and a download count. None of those is a number
// anybody watches tick. What the freshness costs, on the other hand, is two
// upstream calls on the critical path of a page opening — several seconds of
// empty shelf, every time, for an answer that was the same at breakfast.
//
// The thing that must never be cached is the version list, and it is not: that
// is read at the moment of installing, where a stale answer installs the wrong
// file. See Registry.Versions.
const picksTTL = 6 * time.Hour

// picksPartialTTL is the lifetime of a shelf that resolved but came back short
// — Modrinth answered and SpigotMC did not, or a slug has been renamed. Serving
// what did resolve is right; sitting on it for six hours is not, because the
// missing rows should come back on their own rather than at the next restart.
const picksPartialTTL = 10 * time.Minute

// picksEmptyTTL is how long a resolve that produced nothing at all is
// remembered. Remembering a failure sounds wrong until you count what the
// alternative costs an operator whose host cannot reach these registries — that
// panel would spend the full registry timeout on every single page load to draw
// the same empty shelf. A minute is short enough that fixing the network heals
// the page while they are still looking at it.
const picksEmptyTTL = time.Minute

// picksCache is the shelf as it was last resolved, with a clock on it.
//
// Two of its properties matter more than the hit rate:
//
//   - A stale shelf is served immediately and refreshed behind the page. Only
//     the very first caller can ever be made to wait, and thanks to the warm-up
//     at startup that caller is usually nobody.
//   - A resolve is claimed. Two browsers opening the market in the same second
//     — or the same browser's two requests, the second one caused by the
//     default server tick — produce one set of upstream calls, not four.
type picksCache struct {
	mu     sync.Mutex
	groups []PickGroup
	// stored says an attempt has finished, which is not the same as groups
	// being non-empty: a resolve that found nothing is remembered too, briefly.
	stored     bool
	freshUntil time.Time
	// inflight is closed by whoever is resolving right now, and nil when
	// nobody is.
	inflight chan struct{}

	// The three lifetimes above, as fields so the tests can move the clock.
	ttl        time.Duration
	partialTTL time.Duration
	emptyTTL   time.Duration
}

// read returns a copy of the cached shelf, whether it is still fresh, and
// whether it has anything on it worth showing.
func (c *picksCache) read() (groups []PickGroup, fresh, usable bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.stored {
		return []PickGroup{}, false, false
	}
	return clonePickGroups(c.groups), time.Now().Before(c.freshUntil), len(c.groups) > 0
}

// store keeps a finished resolve, for a lifetime that depends on how completely
// it came back.
//
// An empty result never replaces a shelf that resolved: a registry being down
// for a minute must cost the page nothing at all, so the old shelf stays and
// only its retry clock is touched.
func (c *picksCache) store(groups []PickGroup) {
	c.mu.Lock()
	defer c.mu.Unlock()

	ttl := c.ttl
	switch resolved := countPickListings(groups); {
	case resolved == 0:
		if len(c.groups) > 0 {
			// Keep the last good shelf. Push the retry out by the failure
			// window so a broken upstream is asked once a minute rather than
			// once per page load, and never pull a still-fresh entry forward.
			if retry := time.Now().Add(c.emptyTTL); retry.After(c.freshUntil) {
				c.freshUntil = retry
			}
			return
		}
		ttl = c.emptyTTL
	case resolved < len(picks):
		ttl = c.partialTTL
	}

	c.groups = clonePickGroups(groups)
	c.stored = true
	c.freshUntil = time.Now().Add(ttl)
}

// begin claims the right to resolve. The winner gets a channel to close when it
// has stored its result; everybody else gets that same channel and false, and
// should wait on it rather than start a second resolve.
func (c *picksCache) begin() (chan struct{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inflight != nil {
		return c.inflight, false
	}
	c.inflight = make(chan struct{})
	return c.inflight, true
}

// finish releases the claim and wakes the waiters. Called after store, so a
// waiter that wakes up finds the answer already there.
func (c *picksCache) finish(claim chan struct{}) {
	c.mu.Lock()
	c.inflight = nil
	c.mu.Unlock()
	close(claim)
}

// clonePickGroups copies the shelf on its way into the cache and on its way out
// again.
//
// Not defensive habit — required. The browse handler stamps every listing it is
// handed with a compatibility verdict computed against whichever servers this
// operator ticked, so a row handed out by reference would write one operator's
// badges into shared memory and show them to the next. Nothing writes the
// strings inside a listing, so a per-row struct copy is the whole job.
func clonePickGroups(groups []PickGroup) []PickGroup {
	out := make([]PickGroup, len(groups))
	for i, group := range groups {
		group.Listings = make([]Listing, len(groups[i].Listings))
		copy(group.Listings, groups[i].Listings)
		out[i] = group
	}
	return out
}

func countPickListings(groups []PickGroup) int {
	total := 0
	for _, group := range groups {
		total += len(group.Listings)
	}
	return total
}

// Picks is the shelf, from the cache when it can be and from the registries
// when it cannot.
//
// A stale shelf is handed over as it is and replaced behind the page: the
// operator is looking at eleven plugins that have not moved since breakfast,
// and making them wait to confirm that is the bug this cache exists to fix.
// Only a cold cache blocks, and only one caller at a time.
func (r *Registry) Picks(ctx context.Context) []PickGroup {
	groups, fresh, usable := r.picks.read()
	switch {
	case fresh:
		return groups
	case usable:
		r.RefreshPicks()
		return groups
	}

	claim, mine := r.picks.begin()
	if !mine {
		// Somebody else is already asking; their answer is this answer.
		select {
		case <-claim:
		case <-ctx.Done():
			return []PickGroup{}
		}
		cached, _, _ := r.picks.read()
		return cached
	}

	resolved := r.resolvePicks(ctx)
	r.picks.store(resolved)
	r.picks.finish(claim)
	return resolved
}

// RefreshPicks resolves the shelf in the background, unless something already
// is.
//
// Called at startup, so the first operator to open the market does not pay for
// it either, and whenever a stale shelf has just been served. Deliberately not
// given the request's context: that one is cancelled the moment the response is
// written, which would cancel the refresh along with it and leave the cache
// stale forever.
func (r *Registry) RefreshPicks() {
	claim, mine := r.picks.begin()
	if !mine {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
		defer cancel()
		r.picks.store(r.resolvePicks(ctx))
		r.picks.finish(claim)
	}()
}

// resolvePicks reads the shelf from the registries.
//
// One batched call for the Modrinth entries and one per entry for the others,
// all at once — three requests in practice, which is fewer than the search it
// replaces. Best effort throughout: an unreachable registry costs its own
// entries and the groups that end up empty are left out entirely, so the page
// never shows a heading with nothing under it.
func (r *Registry) resolvePicks(ctx context.Context) []PickGroup {
	var (
		mu    sync.Mutex
		found = map[string]Listing{}
		wg    sync.WaitGroup
	)

	remember := func(pick Pick, listing Listing) {
		mu.Lock()
		defer mu.Unlock()
		found[pick.Source+":"+pick.ID] = listing
	}

	var modrinthIDs []string
	for _, pick := range picks {
		if pick.Source == SourceModrinth {
			modrinthIDs = append(modrinthIDs, pick.ID)
			continue
		}

		wg.Add(1)
		go func(pick Pick) {
			defer wg.Done()
			listing, _, err := r.Project(ctx, pick.Source, pick.ID)
			if err != nil {
				return
			}
			remember(pick, listing)
		}(pick)
	}

	if len(modrinthIDs) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, listing := range r.modrinthProjects(ctx, modrinthIDs) {
				remember(Pick{Source: SourceModrinth, ID: listing.ID}, listing)
			}
		}()
	}
	wg.Wait()

	out := make([]PickGroup, 0, len(pickGroups))
	for _, group := range pickGroups {
		section := group
		section.Listings = []Listing{}
		for _, pick := range picks {
			if pick.Group != group.ID {
				continue
			}
			if listing, ok := found[pick.Source+":"+pick.ID]; ok {
				section.Listings = append(section.Listings, listing)
			}
		}
		if len(section.Listings) > 0 {
			out = append(out, section)
		}
	}
	return out
}
