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

import (
	"context"
	"sync"
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

// Picks resolves the shelf against the registries.
//
// One batched call for the Modrinth entries and one per entry for the others,
// all at once — three requests in practice, which is fewer than the search it
// replaces. Best effort throughout: an unreachable registry costs its own
// entries and the groups that end up empty are left out entirely, so the page
// never shows a heading with nothing under it.
func (r *Registry) Picks(ctx context.Context) []PickGroup {
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
