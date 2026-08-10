package plugin

// Plugin discovery across the three places server plugins actually live.
//
// The library half of this package answers "which versions do I hold"; this
// half answers the question that comes before it — "what should I install" —
// and it is deliberately a different shape. A registry is searched, not
// tracked: nothing here is stored, every call goes out to the source, and the
// result is a list of candidates the operator has not decided about yet.
//
// Three sources rather than one because server owners do not shop in one
// place. Modrinth has the modern catalogue and the best metadata; Hangar is
// PaperMC's own and is where a lot of Paper-first plugins publish; SpigotMC is
// where the fifteen-year back catalogue is, reachable through the community
// Spiget API. They disagree about almost everything — what a category is, how
// a version is named, whether the download link is theirs to give — so each
// gets its own reader and they meet at Listing and Release.
//
// Failures are per source. One registry being down or blocked must not empty
// the page: the search returns what it got and names what it did not, which is
// also the honest answer for an operator on a network that can reach one host
// and not the others.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The source kinds a Plugin's Source may carry beside SourceGitHub. They are
// stored in the registry, so they are constants rather than strings written
// out at each use.
const (
	SourceModrinth = "modrinth"
	SourceHangar   = "hangar"
	SourceSpigot   = "spigot"
)

// ErrUnknownSource rejects a source id that is not one of the four kinds.
var ErrUnknownSource = errors.New("unknown plugin source")

// ErrNotDownloadable marks a listing whose jar the panel cannot fetch itself —
// a SpigotMC resource hosted somewhere else, or a paid one. It is its own
// error because the fix is "open the source page", not "try again".
var ErrNotDownloadable = errors.New("this plugin cannot be downloaded by the panel")

// searchTimeout bounds one registry call. Shorter than the GitHub metadata
// budget: a search is something a person is waiting on with the results list
// blank, and a source that takes longer than this is better reported as slow
// than waited for.
const searchTimeout = 15 * time.Second

// searchPage is how many results one source is asked for. The rail's filters
// narrow far more effectively than paging does, and nobody scrolls to the
// hundredth plugin.
const searchPage = 30

// Sort orders the discovery page offers. Relevance is the default because it
// is the only one that uses the search text.
const (
	SortRelevance = "relevance"
	SortDownloads = "downloads"
	SortUpdated   = "updated"
)

// RegistrySource describes one place plugins can be searched, for the filter
// rail. Static per build, but it travels with the search response so the page
// needs no second request to render its checkboxes.
type RegistrySource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Note string `json:"note"`
	// Installable is false for a source the panel can only link to. Nothing is
	// so far, but Spiget resources are individually not installable and the
	// field is what the row's button reads.
	Installable bool `json:"installable"`
}

// RegistrySources is the catalogue the rail offers, in the order it shows them.
func RegistrySources() []RegistrySource {
	return []RegistrySource{
		{ID: SourceModrinth, Name: "Modrinth", Note: "元数据最全，兼容性判断最准", Installable: true},
		{ID: SourceHangar, Name: "Hangar", Note: "PaperMC 官方平台", Installable: true},
		{ID: SourceSpigot, Name: "SpigotMC", Note: "老牌资源站，部分资源需前往源站下载", Installable: true},
	}
}

// Category is one entry of the rail's category filter.
//
// The three sources have three unrelated taxonomies — Modrinth has
// "game-mechanics", Hangar has "world_management", Spiget has none that its
// search accepts — so the panel offers a small canonical set and translates.
// A source with no equivalent for the chosen category is skipped and said to
// be skipped, which beats both silently dropping it and inventing a mapping
// that returns the wrong plugins.
type Category struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// modrinth and hangar are the ids this category is called upstream. Empty
	// means the source has nothing close enough, and it sits this search out.
	modrinth string
	hangar   string
}

var categories = []Category{
	{ID: "admin", Name: "管理工具", modrinth: "management", hangar: "admin_tools"},
	{ID: "economy", Name: "经济", modrinth: "economy", hangar: "economy"},
	{ID: "chat", Name: "聊天与社交", modrinth: "social", hangar: "chat"},
	{ID: "protection", Name: "领地与保护", modrinth: "", hangar: "protection"},
	{ID: "worldgen", Name: "世界生成", modrinth: "world-generation", hangar: "world_management"},
	{ID: "minigame", Name: "小游戏", modrinth: "minigame", hangar: "games"},
	{ID: "gameplay", Name: "玩法扩展", modrinth: "game-mechanics", hangar: "gameplay"},
	{ID: "rpg", Name: "角色扮演", modrinth: "adventure", hangar: "role_playing"},
	{ID: "optimization", Name: "性能优化", modrinth: "optimization", hangar: ""},
	{ID: "library", Name: "前置库", modrinth: "library", hangar: "dev_tools"},
}

// Categories is the canonical category list for the filter rail.
func Categories() []Category {
	out := make([]Category, len(categories))
	copy(out, categories)
	return out
}

func lookupCategory(id string) (Category, bool) {
	for _, entry := range categories {
		if entry.ID == id {
			return entry, true
		}
	}
	return Category{}, false
}

// Listing is one search result, from whichever registry produced it.
//
// Everything on it is something the row shows or the compatibility check
// reads. What is deliberately absent is the description body, the gallery and
// the licence: they belong in the drawer, they are large, and fetching them
// for thirty rows nobody will open is most of what makes a plugin search slow.
type Listing struct {
	// Source is one of the registry kinds, and ID is what that source calls
	// this plugin. Together they are the pair every later call takes, and the
	// pair a library entry's Source records.
	Source string `json:"source"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Author string `json:"author,omitempty"`
	// Summary is the one-line description the row shows, already trimmed to a
	// single line — some sources put newlines in theirs.
	Summary   string    `json:"summary,omitempty"`
	IconURL   string    `json:"iconUrl,omitempty"`
	Downloads int64     `json:"downloads"`
	Updated   time.Time `json:"updated,omitempty"`
	// Loaders and GameVersions are what the compatibility badge is computed
	// from. Either being empty means the source did not say, which is reported
	// as unknown rather than assumed to be fine — see Judge.
	Loaders      []string `json:"loaders,omitempty"`
	GameVersions []string `json:"gameVersions,omitempty"`
	Categories   []string `json:"categories,omitempty"`
	// PageURL is the plugin's page on its own site, for the drawer's link out.
	PageURL string `json:"pageUrl,omitempty"`
	// Downloadable is false for a resource the panel can only link to. The
	// install button reads differently for those, and pressing it opens the
	// source page instead of starting a transfer.
	Downloadable bool `json:"downloadable"`
	// Compat is filled in by the API layer once it knows which instance the
	// operator is installing into. It is not part of what a registry returns.
	Compat *Compat `json:"compat,omitempty"`
}

// Query is one search of the discovery page.
type Query struct {
	Text string
	// Sources limits the search; empty means every source.
	Sources []string
	// Category is a canonical category id, or empty for all.
	Category string
	Sort     string
	Limit    int
	Offset   int
	// Loader narrows the query upstream where the source supports it. It is
	// the target instance's loader family, not a filter the operator set: the
	// rail's "仅显示兼容项" switch is applied after the fact, because a result
	// that was filtered out upstream cannot be shown greyed out.
	Loader string
	// ClientMods keeps the mods a server cannot load in the results.
	//
	// Off by default, and that default is the fix for the worst thing this
	// page did: Modrinth's catalogue is mostly client mods, so a search for
	// "optimization" on a server panel came back Sodium, Iris, Lithium —
	// renderers and shader loaders, none of which a server will ever load. The
	// switch exists because a handful of operators do hand client mods out to
	// their players, and for them an empty result would be the wrong answer.
	ClientMods bool
}

// SearchResult is what one search produced, including what it failed to reach.
type SearchResult struct {
	Listings []Listing `json:"listings"`
	// Notes explains, per source, why it contributed nothing — unreachable,
	// rate limited, or skipped because it cannot filter by the chosen
	// category. A source that worked is absent from the map.
	Notes map[string]string `json:"notes,omitempty"`
	// Truncated says at least one source had more to give, so the page can
	// offer "更多结果" rather than implying this is everything.
	Truncated bool `json:"truncated"`
}

// Dependency is another plugin a version needs, as its own registry describes
// it. Required is what decides whether a missing one breaks the server or only
// switches a feature off.
type Dependency struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	URL      string `json:"url,omitempty"`
}

// Registry reads the three plugin catalogues.
//
// Searches and version lists are not cached, and that is the point of the type:
// a search is typed fresh every time, and a version list is read at the moment
// of installing, which is the one moment where a stale answer installs the wrong
// file. The single exception is the curated shelf the empty query shows, which
// is not a query at all but a fixed list of eleven ids — see picks.go.
type Registry struct {
	http      *http.Client
	userAgent string

	// The three hosts, as fields so the tests can point them at a local server.
	modrinthBase string
	hangarBase   string
	spigetBase   string
	spigotBase   string

	picks picksCache
}

func NewRegistry(userAgent string) *Registry {
	return &Registry{
		http:         &http.Client{Timeout: searchTimeout},
		userAgent:    userAgent,
		modrinthBase: "https://api.modrinth.com/v2",
		hangarBase:   "https://hangar.papermc.io/api/v1",
		spigetBase:   "https://api.spiget.org/v2",
		spigotBase:   "https://www.spigotmc.org",
		picks: picksCache{
			ttl:        picksTTL,
			partialTTL: picksPartialTTL,
			emptyTTL:   picksEmptyTTL,
		},
	}
}

// Search asks every selected source at once and merges what comes back.
//
// Concurrent rather than sequential because the three are independent and the
// slowest of them is the whole wait either way; merged rather than grouped
// because the operator is choosing a plugin, not choosing a registry, and
// three separate lists would make them do the comparison the sort is for.
func (r *Registry) Search(ctx context.Context, q Query) SearchResult {
	sources := q.Sources
	if len(sources) == 0 {
		for _, entry := range RegistrySources() {
			sources = append(sources, entry.ID)
		}
	}
	if q.Limit <= 0 || q.Limit > searchPage {
		q.Limit = searchPage
	}

	var (
		mu     sync.Mutex
		result = SearchResult{Listings: []Listing{}, Notes: map[string]string{}}
		wg     sync.WaitGroup
	)

	for _, source := range sources {
		reader := r.readerFor(source)
		if reader == nil {
			continue
		}
		wg.Add(1)
		go func(source string, read func(context.Context, Query) ([]Listing, bool, error)) {
			defer wg.Done()
			listings, more, err := read(ctx, q)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				result.Notes[source] = err.Error()
				return
			}
			result.Listings = append(result.Listings, listings...)
			result.Truncated = result.Truncated || more
		}(source, reader)
	}
	wg.Wait()

	sortListings(result.Listings, q.Sort)
	if len(result.Notes) == 0 {
		result.Notes = nil
	}
	return result
}

func (r *Registry) readerFor(source string) func(context.Context, Query) ([]Listing, bool, error) {
	switch source {
	case SourceModrinth:
		return r.searchModrinth
	case SourceHangar:
		return r.searchHangar
	case SourceSpigot:
		return r.searchSpigot
	default:
		return nil
	}
}

// sortListings puts the merged results in the order the rail asked for.
//
// Relevance cannot be compared across sources — each one scored its own hits
// against its own index — so it is kept as the interleaving the sources
// returned, ordered by each source's own rank. Downloads and last-updated are
// facts about the plugin and do compare.
func sortListings(listings []Listing, order string) {
	switch order {
	case SortDownloads:
		sort.SliceStable(listings, func(a, b int) bool {
			return listings[a].Downloads > listings[b].Downloads
		})
	case SortUpdated:
		sort.SliceStable(listings, func(a, b int) bool {
			return listings[a].Updated.After(listings[b].Updated)
		})
	}
}

// Versions lists what a registry plugin could be installed at, newest first.
//
// The same Release type the GitHub reader produces, so everything downstream —
// the downloader, the library, the version picker — is unchanged by which
// source a plugin came from.
func (r *Registry) Versions(ctx context.Context, source, id string) ([]Release, error) {
	switch source {
	case SourceModrinth:
		return r.modrinthVersions(ctx, id)
	case SourceHangar:
		return r.hangarVersions(ctx, id)
	case SourceSpigot:
		return r.spigotVersions(ctx, id)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownSource, source)
	}
}

// Project reads one plugin's full record, for the detail drawer.
func (r *Registry) Project(ctx context.Context, source, id string) (Listing, string, error) {
	switch source {
	case SourceModrinth:
		return r.modrinthProject(ctx, id)
	case SourceHangar:
		return r.hangarProject(ctx, id)
	case SourceSpigot:
		return r.spigotProject(ctx, id)
	default:
		return Listing{}, "", fmt.Errorf("%w: %s", ErrUnknownSource, source)
	}
}

// ---------------------------------------------------------------- modrinth

type modrinthHit struct {
	ProjectID   string    `json:"project_id"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Author      string    `json:"author"`
	Categories  []string  `json:"categories"`
	Versions    []string  `json:"versions"`
	Downloads   int64     `json:"downloads"`
	IconURL     string    `json:"icon_url"`
	Modified    time.Time `json:"date_modified"`
}

type modrinthSearch struct {
	Hits      []modrinthHit `json:"hits"`
	TotalHits int           `json:"total_hits"`
}

func (r *Registry) searchModrinth(ctx context.Context, q Query) ([]Listing, bool, error) {
	params := url.Values{}
	params.Set("query", q.Text)
	params.Set("limit", strconv.Itoa(q.Limit))
	if q.Offset > 0 {
		params.Set("offset", strconv.Itoa(q.Offset))
	}
	params.Set("index", modrinthIndex(q.Sort))

	// Facets are AND-ed between the outer groups and OR-ed inside each one, so
	// this reads "a plugin or a mod, runnable on a server, in this category,
	// for this loader".
	facets := [][]string{{"project_type:plugin", "project_type:mod"}}
	if !q.ClientMods {
		// required and optional both stay: "optional" is the honest label for a
		// mod that does something on the server and more on the client, and
		// dropping it would take ViaVersion's neighbours out with the shaders.
		facets = append(facets, []string{"server_side:required", "server_side:optional"})
	}
	if q.Category != "" {
		category, ok := lookupCategory(q.Category)
		if !ok || category.modrinth == "" {
			return nil, false, fmt.Errorf("Modrinth 没有对应的「%s」分类", categoryName(q.Category))
		}
		facets = append(facets, []string{"categories:" + category.modrinth})
	}
	if family := loaderFamily(q.Loader); len(family) > 0 {
		group := make([]string, 0, len(family))
		for _, loader := range family {
			group = append(group, "categories:"+loader)
		}
		facets = append(facets, group)
	}
	encoded, err := json.Marshal(facets)
	if err != nil {
		return nil, false, err
	}
	params.Set("facets", string(encoded))

	var page modrinthSearch
	if err := r.getJSON(ctx, r.modrinthBase+"/search?"+params.Encode(), &page); err != nil {
		return nil, false, sourceError("Modrinth", err)
	}

	out := make([]Listing, 0, len(page.Hits))
	for _, hit := range page.Hits {
		id := hit.Slug
		if id == "" {
			id = hit.ProjectID
		}
		loaders, cats := splitLoaders(hit.Categories)
		out = append(out, Listing{
			Source:       SourceModrinth,
			ID:           id,
			Name:         hit.Title,
			Author:       hit.Author,
			Summary:      oneLine(hit.Description),
			IconURL:      hit.IconURL,
			Downloads:    hit.Downloads,
			Updated:      hit.Modified,
			Loaders:      loaders,
			GameVersions: hit.Versions,
			Categories:   cats,
			PageURL:      "https://modrinth.com/plugin/" + id,
			Downloadable: true,
		})
	}
	return out, page.TotalHits > q.Offset+len(page.Hits), nil
}

func modrinthIndex(sort string) string {
	switch sort {
	case SortDownloads:
		return "downloads"
	case SortUpdated:
		return "updated"
	default:
		return "relevance"
	}
}

type modrinthProject struct {
	ID           string    `json:"id"`
	Slug         string    `json:"slug"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	Body         string    `json:"body"`
	Categories   []string  `json:"categories"`
	Downloads    int64     `json:"downloads"`
	IconURL      string    `json:"icon_url"`
	Updated      time.Time `json:"updated"`
	GameVersions []string  `json:"game_versions"`
	Loaders      []string  `json:"loaders"`
}

func (r *Registry) modrinthProject(ctx context.Context, id string) (Listing, string, error) {
	var project modrinthProject
	if err := r.getJSON(ctx, r.modrinthBase+"/project/"+url.PathEscape(id), &project); err != nil {
		return Listing{}, "", sourceError("Modrinth", err)
	}
	return modrinthListing(project), project.Body, nil
}

func modrinthListing(project modrinthProject) Listing {
	slug := project.Slug
	if slug == "" {
		slug = project.ID
	}
	_, cats := splitLoaders(project.Categories)
	return Listing{
		Source:       SourceModrinth,
		ID:           slug,
		Name:         project.Title,
		Summary:      oneLine(project.Description),
		IconURL:      project.IconURL,
		Downloads:    project.Downloads,
		Updated:      project.Updated,
		Loaders:      project.Loaders,
		GameVersions: project.GameVersions,
		Categories:   cats,
		PageURL:      "https://modrinth.com/plugin/" + slug,
		Downloadable: true,
	}
}

// modrinthProjects reads several projects in one request, for the shelf the
// empty query shows. Best effort: Modrinth simply leaves out an id it does not
// recognise, and a whole failed call costs the Modrinth half of the shelf.
func (r *Registry) modrinthProjects(ctx context.Context, ids []string) []Listing {
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil
	}

	var projects []modrinthProject
	if err := r.getJSON(ctx, r.modrinthBase+"/projects?ids="+url.QueryEscape(string(encoded)), &projects); err != nil {
		return nil
	}

	out := make([]Listing, 0, len(projects))
	for _, project := range projects {
		out = append(out, modrinthListing(project))
	}
	return out
}

type modrinthVersion struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	VersionNumber string    `json:"version_number"`
	Changelog     string    `json:"changelog"`
	GameVersions  []string  `json:"game_versions"`
	VersionType   string    `json:"version_type"`
	Loaders       []string  `json:"loaders"`
	Published     time.Time `json:"date_published"`
	Downloads     int64     `json:"downloads"`
	Dependencies  []struct {
		ProjectID      string `json:"project_id"`
		FileName       string `json:"file_name"`
		DependencyType string `json:"dependency_type"`
	} `json:"dependencies"`
	Files []struct {
		URL      string `json:"url"`
		FileName string `json:"filename"`
		Primary  bool   `json:"primary"`
		Size     int64  `json:"size"`
	} `json:"files"`
}

func (r *Registry) modrinthVersions(ctx context.Context, id string) ([]Release, error) {
	var versions []modrinthVersion
	if err := r.getJSON(ctx, r.modrinthBase+"/project/"+url.PathEscape(id)+"/version", &versions); err != nil {
		return nil, sourceError("Modrinth", err)
	}

	// Dependency entries name a project id and nothing readable, so the ids are
	// resolved in one batched call rather than one call each. A failure here
	// costs the names, not the version list.
	names := r.modrinthNames(ctx, dependencyIDs(versions))

	// One release per version *number*, not per Modrinth version.
	//
	// Modrinth files each platform's build as a version of its own — LuckPerms
	// publishes 5.5.71 five times over, once for bukkit, once for velocity,
	// once for fabric — and it is the version *number* that says they are one
	// release. Read one version to one release and the library ends up holding
	// five copies of a plugin that shipped once, reports the fleet as running
	// five different versions, and offers to "upgrade" a Velocity proxy to a
	// Paper jar. The number is the release; the builds under it are its jars.
	out := make([]Release, 0, len(versions))
	for _, version := range versions {
		file, ok := primaryFile(version)
		if !ok {
			continue
		}
		number, platform := releaseNumber(version.VersionNumber)
		if platform == "" {
			platform = firstLoader(version.Loaders)
		}
		asset := Asset{
			Name:         file.FileName,
			Size:         file.Size,
			URL:          file.URL,
			Platform:     platform,
			Loaders:      version.Loaders,
			GameVersions: version.GameVersions,
		}

		if number == "" {
			// Nothing to group on. Its own release, addressed the way it
			// always was.
			number = version.ID
		}

		deps := make([]Dependency, 0, len(version.Dependencies))
		for _, dep := range version.Dependencies {
			if dep.DependencyType != "required" && dep.DependencyType != "optional" {
				continue
			}
			name := names[dep.ProjectID]
			if name == "" {
				name = dep.FileName
			}
			if name == "" {
				name = dep.ProjectID
			}
			deps = append(deps, Dependency{
				Name:     name,
				Required: dep.DependencyType == "required",
				URL:      "https://modrinth.com/plugin/" + dep.ProjectID,
			})
		}
		out = append(out, Release{
			Tag:          number,
			Name:         version.Name,
			Version:      number,
			Notes:        version.Changelog,
			Prerelease:   version.VersionType != "release",
			PublishedAt:  version.Published,
			Asset:        asset,
			Assets:       []Asset{asset},
			GameVersions: version.GameVersions,
			Loaders:      version.Loaders,
			Dependencies: deps,
			Downloads:    version.Downloads,
		})
	}
	out = groupReleases(out)
	sortReleases(out)
	return out, nil
}

// releaseNumber splits a published version number into the release it names
// and the platform that build is for.
//
// The registries have two ways of saying "these are one release": Hangar puts
// the platforms inside one version, and Modrinth files a version per platform
// with the platform written into the number — LuckPerms publishes
// "v5.5.71-bukkit", "v5.5.71-velocity", "v5.5.71-bungee". Grouping on the
// number alone therefore groups nothing, which is exactly the shape that had a
// Paper server being offered the proxy build as its next version.
//
// Only a suffix that names a platform is taken off. "1.2.3-beta" and
// "2.0-rc1" are versions, not builds, and the list below is the whole of what
// this is willing to believe is a platform.
func releaseNumber(number string) (release, platform string) {
	number = strings.TrimSpace(number)
	cut := strings.LastIndex(number, "-")
	if cut <= 0 || cut == len(number)-1 {
		return number, ""
	}
	suffix := strings.ToLower(number[cut+1:])
	canonical, ok := platformSuffixes[suffix]
	if !ok {
		return number, ""
	}
	return number[:cut], canonical
}

// platformSuffixes is every token a version number may end with that names the
// server this build is for, mapped to the loader name the compatibility check
// uses. "bungee" is Modrinth's own shorthand for BungeeCord; the rest are
// spelled the way they are judged.
var platformSuffixes = map[string]string{
	"bukkit":     "bukkit",
	"spigot":     "spigot",
	"paper":      "paper",
	"purpur":     "purpur",
	"folia":      "folia",
	"velocity":   "velocity",
	"bungee":     "bungeecord",
	"bungeecord": "bungeecord",
	"waterfall":  "waterfall",
	"sponge":     "sponge",
	"fabric":     "fabric",
	"forge":      "forge",
	"neoforge":   "neoforge",
	"quilt":      "quilt",
	"nukkit":     "nukkit",
}

// groupReleases folds releases that share a tag into one.
//
// Both registry readers produce one entry per *build*, and this is where a
// release stops being several of them: the assets join, and everything the
// release as a whole claims becomes the union of what its builds claim —
// because that is what the plugin as published supports, and it is what a
// search badge is claiming. Which of it a given server can actually load is a
// property of one jar, and that stays on the jar.
func groupReleases(list []Release) []Release {
	at := map[string]int{}
	out := make([]Release, 0, len(list))
	for _, release := range list {
		index, seen := at[release.Tag]
		if !seen {
			at[release.Tag] = len(out)
			out = append(out, release)
			continue
		}
		out[index] = mergeRelease(out[index], release)
	}
	for i := range out {
		out[i] = orderAssets(out[i])
	}
	return out
}

func mergeRelease(into, extra Release) Release {
	held := assetNames(into.Assets)
	for _, asset := range extra.Assets {
		// The same file twice is one file: a build that genuinely covers
		// several loaders, listed once per loader.
		if sameAsset(into.Assets, asset) {
			continue
		}
		if held[strings.ToLower(asset.Name)] {
			asset.Name = platformName(asset.Name, asset.Platform)
		}
		held[strings.ToLower(asset.Name)] = true
		into.Assets = append(into.Assets, asset)
	}
	into.GameVersions = union(into.GameVersions, extra.GameVersions)
	into.Loaders = union(into.Loaders, extra.Loaders)
	into.Downloads += extra.Downloads
	// The release is dated by its earliest build: that is when it was
	// published, and a later platform build is the same release catching up.
	if !extra.PublishedAt.IsZero() && extra.PublishedAt.Before(into.PublishedAt) {
		into.PublishedAt = extra.PublishedAt
	}
	// A release is a prerelease only if every build of it is one.
	into.Prerelease = into.Prerelease && extra.Prerelease
	if into.Notes == "" {
		into.Notes = extra.Notes
	}
	if len(into.Dependencies) == 0 {
		into.Dependencies = extra.Dependencies
	}
	return into
}

func sameAsset(held []Asset, asset Asset) bool {
	for _, one := range held {
		if strings.EqualFold(one.Name, asset.Name) && one.URL == asset.URL {
			return true
		}
	}
	return false
}
func assetNames(assets []Asset) map[string]bool {
	out := make(map[string]bool, len(assets))
	for _, asset := range assets {
		out[strings.ToLower(asset.Name)] = true
	}
	return out
}

func union(into, extra []string) []string {
	seen := make(map[string]bool, len(into))
	for _, entry := range into {
		seen[entry] = true
	}
	for _, entry := range extra {
		if !seen[entry] {
			seen[entry] = true
			into = append(into, entry)
		}
	}
	return into
}

func firstLoader(loaders []string) string {
	if len(loaders) == 0 {
		return ""
	}
	best := loaders[0]
	for _, loader := range loaders[1:] {
		if loaderRank(loader) < loaderRank(best) {
			best = loader
		}
	}
	return strings.ToLower(best)
}

// serverLoaders is the order a jar is picked in when a release ships several.
// Plugin platforms before mod loaders, and the busiest first: whatever ends up
// at the head of the list is what a plain "下载到库" fetches, and on this panel
// that should be the jar a Minecraft *server* loads.
var serverLoaders = []string{
	"paper", "purpur", "folia", "spigot", "bukkit",
	"velocity", "waterfall", "bungeecord", "sponge",
}

func loaderRank(loader string) int {
	loader = strings.ToLower(strings.TrimSpace(loader))
	for i, known := range serverLoaders {
		if known == loader {
			return i
		}
	}
	return len(serverLoaders)
}

// orderAssets puts the jar a plain download should take first, and points the
// release's primary at it.
func orderAssets(release Release) Release {
	if len(release.Assets) < 2 {
		return release
	}
	sort.SliceStable(release.Assets, func(a, b int) bool {
		return loaderRank(release.Assets[a].Platform) < loaderRank(release.Assets[b].Platform)
	})
	release.Asset = release.Assets[0]
	return release
}

func primaryFile(version modrinthVersion) (struct {
	URL      string `json:"url"`
	FileName string `json:"filename"`
	Primary  bool   `json:"primary"`
	Size     int64  `json:"size"`
}, bool) {
	for _, file := range version.Files {
		if file.Primary && strings.HasSuffix(strings.ToLower(file.FileName), ".jar") {
			return file, true
		}
	}
	for _, file := range version.Files {
		if strings.HasSuffix(strings.ToLower(file.FileName), ".jar") {
			return file, true
		}
	}
	var zero struct {
		URL      string `json:"url"`
		FileName string `json:"filename"`
		Primary  bool   `json:"primary"`
		Size     int64  `json:"size"`
	}
	return zero, false
}

func dependencyIDs(versions []modrinthVersion) []string {
	seen := map[string]bool{}
	var out []string
	for _, version := range versions {
		for _, dep := range version.Dependencies {
			if dep.ProjectID == "" || seen[dep.ProjectID] {
				continue
			}
			seen[dep.ProjectID] = true
			out = append(out, dep.ProjectID)
		}
	}
	return out
}

// modrinthNames turns project ids into titles. Best effort by design: a
// dependency list that says "P7dR8mSH" is worse than one that says
// "Fabric API", and both are better than no version list at all.
func (r *Registry) modrinthNames(ctx context.Context, ids []string) map[string]string {
	out := map[string]string{}
	if len(ids) == 0 {
		return out
	}
	// Bounded so a plugin with a hundred optional dependencies cannot turn one
	// drawer into a request the size of the search itself.
	if len(ids) > 20 {
		ids = ids[:20]
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return out
	}
	var projects []struct {
		ID    string `json:"id"`
		Slug  string `json:"slug"`
		Title string `json:"title"`
	}
	if err := r.getJSON(ctx, r.modrinthBase+"/projects?ids="+url.QueryEscape(string(encoded)), &projects); err != nil {
		return out
	}
	for _, project := range projects {
		out[project.ID] = project.Title
		out[project.Slug] = project.Title
	}
	return out
}

// ------------------------------------------------------------------ hangar

type hangarProjectJSON struct {
	Name      string `json:"name"`
	Namespace struct {
		Owner string `json:"owner"`
		Slug  string `json:"slug"`
	} `json:"namespace"`
	Description string    `json:"description"`
	Category    string    `json:"category"`
	AvatarURL   string    `json:"avatarUrl"`
	LastUpdated time.Time `json:"lastUpdated"`
	Stats       struct {
		Downloads int64 `json:"downloads"`
	} `json:"stats"`
	// SupportedPlatforms is present on some Hangar responses and absent from
	// others, which is why the badge for a Hangar row is often "未知兼容性"
	// until the drawer reads the version list.
	SupportedPlatforms map[string][]string `json:"supportedPlatforms"`
}

type hangarSearch struct {
	Pagination struct {
		Count int `json:"count"`
	} `json:"pagination"`
	Result []hangarProjectJSON `json:"result"`
}

func (r *Registry) searchHangar(ctx context.Context, q Query) ([]Listing, bool, error) {
	if q.Category != "" {
		category, ok := lookupCategory(q.Category)
		if !ok || category.hangar == "" {
			return nil, false, fmt.Errorf("Hangar 没有对应的「%s」分类", categoryName(q.Category))
		}
	}

	params := url.Values{}
	if q.Text != "" {
		params.Set("query", q.Text)
	}
	params.Set("limit", strconv.Itoa(q.Limit))
	if q.Offset > 0 {
		params.Set("offset", strconv.Itoa(q.Offset))
	}
	if order := hangarSort(q.Sort); order != "" {
		params.Set("sort", order)
	}
	if q.Category != "" {
		category, _ := lookupCategory(q.Category)
		params.Set("category", category.hangar)
	}
	if platform := hangarPlatform(q.Loader); platform != "" {
		params.Set("platform", platform)
	}

	var page hangarSearch
	if err := r.getJSON(ctx, r.hangarBase+"/projects?"+params.Encode(), &page); err != nil {
		return nil, false, sourceError("Hangar", err)
	}

	out := make([]Listing, 0, len(page.Result))
	for _, project := range page.Result {
		out = append(out, hangarListing(project))
	}
	return out, page.Pagination.Count > q.Offset+len(page.Result), nil
}

func hangarListing(project hangarProjectJSON) Listing {
	slug := project.Namespace.Slug
	if slug == "" {
		slug = project.Name
	}
	var loaders, versions []string
	for platform, supported := range project.SupportedPlatforms {
		loaders = append(loaders, strings.ToLower(platform))
		versions = append(versions, supported...)
	}
	sort.Strings(loaders)
	sort.Strings(versions)

	var cats []string
	if project.Category != "" {
		cats = []string{project.Category}
	}
	return Listing{
		Source:       SourceHangar,
		ID:           slug,
		Name:         project.Name,
		Author:       project.Namespace.Owner,
		Summary:      oneLine(project.Description),
		IconURL:      project.AvatarURL,
		Downloads:    project.Stats.Downloads,
		Updated:      project.LastUpdated,
		Loaders:      loaders,
		GameVersions: versions,
		Categories:   cats,
		PageURL:      "https://hangar.papermc.io/" + project.Namespace.Owner + "/" + slug,
		Downloadable: true,
	}
}

func hangarSort(order string) string {
	switch order {
	case SortDownloads:
		return "-downloads"
	case SortUpdated:
		return "-updated"
	default:
		return ""
	}
}

// hangarPlatform maps a loader family onto the one platform name Hangar knows
// it by. Hangar only publishes for these three, so anything else — Fabric,
// Forge — narrows to nothing and is left unset rather than sent as a filter
// that would return an empty list.
func hangarPlatform(loader string) string {
	switch strings.ToLower(loader) {
	case "paper", "spigot", "bukkit", "purpur", "folia":
		return "PAPER"
	case "velocity":
		return "VELOCITY"
	case "waterfall", "bungeecord":
		return "WATERFALL"
	default:
		return ""
	}
}

func (r *Registry) hangarProject(ctx context.Context, id string) (Listing, string, error) {
	var project hangarProjectJSON
	if err := r.getJSON(ctx, r.hangarBase+"/projects/"+url.PathEscape(id), &project); err != nil {
		return Listing{}, "", sourceError("Hangar", err)
	}
	return hangarListing(project), project.Description, nil
}

type hangarVersion struct {
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"createdAt"`
	Description string    `json:"description"`
	Channel     struct {
		Name string `json:"name"`
	} `json:"channel"`
	Downloads          map[string]hangarDownload `json:"downloads"`
	PluginDependencies map[string][]struct {
		Name        string `json:"name"`
		Required    bool   `json:"required"`
		ExternalURL string `json:"externalUrl"`
	} `json:"pluginDependencies"`
	PlatformDependencies map[string][]string `json:"platformDependencies"`
	Stats                struct {
		TotalDownloads int64 `json:"totalDownloads"`
	} `json:"stats"`
}

// hangarDownload is one platform's file under a Hangar version.
type hangarDownload struct {
	FileInfo struct {
		Name      string `json:"name"`
		SizeBytes int64  `json:"sizeBytes"`
		SHA256    string `json:"sha256Hash"`
	} `json:"fileInfo"`
	ExternalURL string `json:"externalUrl"`
	DownloadURL string `json:"downloadUrl"`
}

type hangarVersions struct {
	Result []hangarVersion `json:"result"`
}

func (r *Registry) hangarVersions(ctx context.Context, id string) ([]Release, error) {
	var page hangarVersions
	params := url.Values{"limit": {strconv.Itoa(releasePage)}}
	if err := r.getJSON(ctx, r.hangarBase+"/projects/"+url.PathEscape(id)+"/versions?"+params.Encode(), &page); err != nil {
		return nil, sourceError("Hangar", err)
	}

	out := make([]Release, 0, len(page.Result))
	for _, version := range page.Result {
		assets := hangarAssets(id, version)
		if len(assets) == 0 {
			continue
		}
		var loaders, versions []string
		for name, supported := range version.PlatformDependencies {
			loaders = append(loaders, strings.ToLower(name))
			versions = append(versions, supported...)
		}
		sort.Strings(loaders)
		sort.Strings(versions)

		var deps []Dependency
		for _, entries := range version.PluginDependencies {
			for _, dep := range entries {
				deps = append(deps, Dependency{Name: dep.Name, Required: dep.Required, URL: dep.ExternalURL})
			}
		}

		// The version's name within the project, and nothing else. The tag
		// used to carry the platform too — "1.2.3@PAPER" — which made one
		// release look like as many releases as it has builds, and made the
		// panel report a plugin published once as inconsistent across a fleet.
		// The platform belongs to the jar, and it is on the jar. A project
		// that writes the platform into the version name instead is folded the
		// same way, by groupReleases below.
		number, _ := releaseNumber(version.Name)
		out = append(out, Release{
			Tag:          number,
			Name:         version.Name,
			Version:      number,
			Notes:        version.Description,
			Prerelease:   !strings.EqualFold(version.Channel.Name, "release"),
			PublishedAt:  version.CreatedAt,
			Asset:        assets[0],
			Assets:       assets,
			GameVersions: versions,
			Loaders:      loaders,
			Dependencies: deps,
			Downloads:    version.Stats.TotalDownloads,
			SHA256:       assets[0].SHA256,
		})
	}
	out = groupReleases(out)
	sortReleases(out)
	return out, nil
}

// hangarAssets is every platform build under one Hangar version.
//
// Paper first, because that is what almost every server here runs and the
// first asset is what a plain "download this version" fetches; then whatever
// else is on offer, in a stable order — an arbitrary map iteration would
// otherwise make the same version resolve to a different file on different
// page loads.
func hangarAssets(id string, version hangarVersion) []Asset {
	names := make([]string, 0, len(version.Downloads))
	for name := range version.Downloads {
		names = append(names, name)
	}
	sort.Strings(names)
	sort.SliceStable(names, func(a, b int) bool {
		return loaderRank(names[a]) < loaderRank(names[b])
	})

	out := make([]Asset, 0, len(names))
	taken := map[string]bool{}
	for _, platform := range names {
		download := version.Downloads[platform]
		asset := Asset{
			Name:     download.FileInfo.Name,
			Size:     download.FileInfo.SizeBytes,
			URL:      download.DownloadURL,
			Platform: strings.ToLower(platform),
			Loaders:  []string{strings.ToLower(platform)},
			SHA256:   download.FileInfo.SHA256,
		}
		if asset.URL == "" {
			asset.URL = download.ExternalURL
		}
		if asset.URL == "" {
			continue
		}
		if supported, ok := version.PlatformDependencies[platform]; ok {
			asset.GameVersions = supported
		}
		if asset.Name == "" {
			asset.Name = sanitiseFileName(id + "-" + version.Name + "-" + asset.Platform + ".jar")
		}
		// Two platforms publishing the same file name would land on one file
		// on disk, and the release would look like it holds one jar because it
		// would. The platform is what tells them apart, so it goes in the name.
		if taken[strings.ToLower(asset.Name)] {
			asset.Name = platformName(asset.Name, asset.Platform)
		}
		taken[strings.ToLower(asset.Name)] = true
		out = append(out, asset)
	}
	return out
}

// platformName spells the platform into a file name, for the rare release
// whose builds are all called the same thing.
func platformName(name, platform string) string {
	if platform == "" {
		return name
	}
	if ext := strings.LastIndex(name, "."); ext > 0 {
		return name[:ext] + "-" + platform + name[ext:]
	}
	return name + "-" + platform
}

// ------------------------------------------------------------------ spigot

type spigetResource struct {
	ID             int      `json:"id"`
	Name           string   `json:"name"`
	Tag            string   `json:"tag"`
	Downloads      int64    `json:"downloads"`
	TestedVersions []string `json:"testedVersions"`
	UpdateDate     int64    `json:"updateDate"`
	External       bool     `json:"external"`
	Premium        bool     `json:"premium"`
	Icon           struct {
		URL string `json:"url"`
	} `json:"icon"`
	File struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"file"`
	Description string `json:"description"`
}

func (r *Registry) searchSpigot(ctx context.Context, q Query) ([]Listing, bool, error) {
	if q.Category != "" {
		return nil, false, fmt.Errorf("SpigotMC 的搜索不支持按分类筛选，本次已跳过")
	}
	if strings.TrimSpace(q.Text) == "" {
		// Spiget's search endpoint needs a term in the path. With nothing typed
		// the honest thing is to say so rather than send "/search/resources/".
		return nil, false, errors.New("SpigotMC 只能按关键词搜索，请先输入名称")
	}

	params := url.Values{}
	params.Set("size", strconv.Itoa(q.Limit))
	params.Set("page", strconv.Itoa(q.Offset/max(q.Limit, 1)+1))
	params.Set("sort", spigetSort(q.Sort))
	params.Set("fields", "id,name,tag,downloads,testedVersions,updateDate,icon,external,premium,file")

	endpoint := r.spigetBase + "/search/resources/" + url.PathEscape(q.Text) + "?" + params.Encode()
	var resources []spigetResource
	if err := r.getJSON(ctx, endpoint, &resources); err != nil {
		return nil, false, sourceError("SpigotMC", err)
	}

	out := make([]Listing, 0, len(resources))
	for _, resource := range resources {
		out = append(out, r.spigotListing(resource))
	}
	return out, len(resources) >= q.Limit, nil
}

func (r *Registry) spigotListing(resource spigetResource) Listing {
	icon := ""
	if resource.Icon.URL != "" {
		icon = r.spigotBase + "/" + strings.TrimPrefix(resource.Icon.URL, "/")
	}
	// SpigotMC hosts a lot of resources it does not hold the file for — the
	// page is a link to the author's own site — and premium ones need a
	// purchase. Neither can be fetched here, and the row says so rather than
	// offering a button that fails.
	downloadable := !resource.External && !resource.Premium &&
		!strings.EqualFold(resource.File.Type, "external")

	summary := oneLine(resource.Tag)
	if summary == "" {
		summary = oneLine(decodeBase64(resource.Description))
	}
	return Listing{
		Source:       SourceSpigot,
		ID:           strconv.Itoa(resource.ID),
		Name:         resource.Name,
		Summary:      summary,
		IconURL:      icon,
		Downloads:    resource.Downloads,
		Updated:      time.Unix(resource.UpdateDate, 0).UTC(),
		GameVersions: resource.TestedVersions,
		// Spiget says nothing about loaders. Every SpigotMC resource is a
		// Bukkit-family plugin by construction, which is a fact about the site
		// rather than a guess about the file.
		Loaders:      []string{"spigot"},
		PageURL:      r.spigotBase + "/resources/" + strconv.Itoa(resource.ID) + "/",
		Downloadable: downloadable,
	}
}

func spigetSort(order string) string {
	switch order {
	case SortDownloads:
		return "-downloads"
	case SortUpdated:
		return "-updateDate"
	default:
		return "-downloads"
	}
}

func (r *Registry) spigotProject(ctx context.Context, id string) (Listing, string, error) {
	var resource spigetResource
	if err := r.getJSON(ctx, r.spigetBase+"/resources/"+url.PathEscape(id), &resource); err != nil {
		return Listing{}, "", sourceError("SpigotMC", err)
	}
	return r.spigotListing(resource), decodeBase64(resource.Description), nil
}

type spigetVersion struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	ReleaseDate int64  `json:"releaseDate"`
	Downloads   int64  `json:"downloads"`
}

func (r *Registry) spigotVersions(ctx context.Context, id string) ([]Release, error) {
	var resource spigetResource
	if err := r.getJSON(ctx, r.spigetBase+"/resources/"+url.PathEscape(id), &resource); err != nil {
		return nil, sourceError("SpigotMC", err)
	}
	listing := r.spigotListing(resource)

	params := url.Values{"size": {strconv.Itoa(releasePage)}, "sort": {"-releaseDate"}}
	var versions []spigetVersion
	if err := r.getJSON(ctx, r.spigetBase+"/resources/"+url.PathEscape(id)+"/versions?"+params.Encode(), &versions); err != nil {
		return nil, sourceError("SpigotMC", err)
	}

	out := make([]Release, 0, len(versions))
	for _, version := range versions {
		asset := Asset{
			Name: sanitiseFileName(resource.Name + "-" + version.Name + ".jar"),
			// Spiget redirects this to wherever the jar actually is. For an
			// external resource that redirect leaves SpigotMC entirely, which
			// is why those are marked not downloadable above rather than
			// fetched and hoped for.
			URL: r.spigetBase + "/resources/" + url.PathEscape(id) + "/versions/" + strconv.Itoa(version.ID) + "/download",
		}
		out = append(out, Release{
			Tag:          strconv.Itoa(version.ID),
			Name:         version.Name,
			Version:      version.Name,
			PublishedAt:  time.Unix(version.ReleaseDate, 0).UTC(),
			Asset:        asset,
			Assets:       []Asset{asset},
			GameVersions: listing.GameVersions,
			Loaders:      listing.Loaders,
			Downloads:    version.Downloads,
			// Only the newest version's compatibility is published, so an older
			// one carries the resource's list with the caveat that it describes
			// the resource today. Saying nothing would be worse: the drawer
			// would show a version list with no compatibility at all.
			Unverified: true,
		})
	}
	sortReleases(out)
	return out, nil
}

// ------------------------------------------------------------------ shared

// getJSON reads one registry response.
//
// Its own reader rather than the GitHub client's: there is no token to attach,
// no rate-limit header worth interpreting, and a registry that answers 404 for
// a plugin is not the "maybe it is private" case the GitHub reader has to
// worry about.
func (r *Registry) getJSON(ctx context.Context, endpoint string, dst any) error {
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", r.userAgent)

	resp, err := r.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, endpoint)
	case resp.StatusCode == http.StatusTooManyRequests:
		return ErrRateLimited
	case resp.StatusCode >= 300:
		return fmt.Errorf("%w: HTTP %d", ErrUpstream, resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMetadataBytes))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}

// sourceError names which registry failed. Merged results mean an unlabelled
// "connection refused" would not say which of the three to worry about.
func sourceError(name string, err error) error {
	switch {
	case errors.Is(err, ErrRateLimited):
		return fmt.Errorf("%s 限流了，稍后再试", name)
	case errors.Is(err, ErrNotFound):
		return fmt.Errorf("%s 上没有这个插件", name)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%s 响应超时", name)
	default:
		return fmt.Errorf("%s 读取失败：%v", name, err)
	}
}

func categoryName(id string) string {
	if category, ok := lookupCategory(id); ok {
		return category.Name
	}
	return id
}

// knownLoaders are the loader names that turn up in a source's category list
// mixed in with real categories. Splitting them out is what lets the row show
// "Paper / Velocity" beside a category chip instead of one undifferentiated
// pile of tags.
var knownLoaders = map[string]bool{
	"bukkit": true, "spigot": true, "paper": true, "purpur": true, "folia": true,
	"velocity": true, "bungeecord": true, "waterfall": true,
	"fabric": true, "forge": true, "neoforge": true, "quilt": true,
	"sponge": true,
}

func splitLoaders(tags []string) (loaders, rest []string) {
	for _, tag := range tags {
		if knownLoaders[strings.ToLower(tag)] {
			loaders = append(loaders, strings.ToLower(tag))
			continue
		}
		rest = append(rest, tag)
	}
	return loaders, rest
}

// sortReleases puts a version list newest first, which is what every picker in
// the panel assumes and what "latest" means to the downloader.
func sortReleases(releases []Release) {
	sort.SliceStable(releases, func(a, b int) bool {
		return releases[a].PublishedAt.After(releases[b].PublishedAt)
	})
}

func oneLine(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	return text
}

func decodeBase64(raw string) string {
	if raw == "" {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return raw
	}
	return string(decoded)
}

// sanitiseFileName makes a jar name out of whatever a source called a version.
// Version names carry spaces, slashes and the occasional emoji, and this ends
// up as a file in someone's plugins directory.
func sanitiseFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" || !strings.HasSuffix(strings.ToLower(out), ".jar") {
		out += ".jar"
	}
	return out
}
