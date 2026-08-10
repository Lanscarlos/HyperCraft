package api

// The 获取插件 half of the plugin API: searching the registries, and the
// cross-instance view of what is installed everywhere.
//
// Both are here rather than in handlers_plugins.go because both are about
// plugins the panel does not yet track, or about instances in the plural —
// where that file is about one tracked plugin at a time.

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/plugin"
)

// installTarget is one server the 安装到 block offers, with everything the
// compatibility badge needs.
//
// It travels with the search response so the discovery page can render its
// context block and judge every row without a second request — and so that the
// panel-wide entry, where nothing is selected yet, has a list to choose from.
type installTarget struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	State  instance.State `json:"state"`
	Target plugin.Target  `json:"target"`
	// PluginDir is where a plugin would land on this server, which differs for
	// a Fabric instance and is worth showing before anything is written.
	PluginDir string `json:"pluginDir"`
}

type browseResponse struct {
	Sources    []plugin.RegistrySource `json:"sources"`
	Categories []plugin.Category       `json:"categories"`
	// Targets is every instance that could be installed into, so the rail can
	// offer them. Always sent: the panel-scope entry has nothing selected and
	// has to ask.
	Targets  []installTarget  `json:"targets"`
	Listings []plugin.Listing `json:"listings"`
	// Picks is the curated shelf, sent instead of Listings when nothing has
	// been typed. See plugin.Picks: the front page of a registry is a download
	// chart, and a download chart on a server panel is client mods.
	Picks []plugin.PickGroup `json:"picks,omitempty"`
	// Notes says, per source, why it contributed nothing. Rendered as a line
	// under the results rather than as an error: the other sources answered.
	Notes     map[string]string `json:"notes,omitempty"`
	Truncated bool              `json:"truncated"`
	// Incompatible counts the rows that came back and do not fit the target, so
	// the count line can say "128 个结果 · 12 项不兼容 1.20.4" instead of
	// leaving the operator to notice the dimming.
	Incompatible int `json:"incompatible"`
}

// handleBrowsePlugins searches the registries.
//
// The compatibility judgement is made here rather than in the browser because
// it needs the target instance's game version and loader, which are read off
// the server's own directory. Every row is judged; none are dropped for being
// incompatible.
//
// The one narrowing that does happen upstream is by loader, and only when the
// operator left 仅显示兼容项 on. A plugin for the wrong loader will never run
// on this server under any version, and a Paper operator searching "地图"
// should not get twenty Fabric minimap mods. A plugin for the right loader but
// the wrong game version is the opposite case — that is the abandoned plugin
// they are looking for, and it comes back with a yellow badge saying how far
// it got, because "no results" would read as the panel being broken.
func (s *Server) handleBrowsePlugins(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}

	query := r.URL.Query()
	targets := s.installTargets()
	chosen := chosenTargets(targets, query.Get("instances"))

	onlyCompatible := query.Get("onlyCompatible") != "false"
	q := plugin.Query{
		Text:       strings.TrimSpace(query.Get("q")),
		Sources:    splitList(query.Get("sources")),
		Category:   strings.TrimSpace(query.Get("category")),
		Sort:       strings.TrimSpace(query.Get("sort")),
		Offset:     atoiOr(query.Get("offset"), 0),
		ClientMods: query.Get("clientMods") == "true",
	}
	if onlyCompatible {
		// One loader or none. Narrowing upstream is only sound when every
		// selected server agrees about what a plugin has to be built for —
		// with a Paper server and a Velocity proxy both ticked, either loader
		// sent here would silently delete the other one's plugins from the
		// results, which is the opposite of what ticking two servers asked
		// for. Mixed selections fall back to judging after the fact.
		q.Loader = commonLoader(chosen)
	}

	registry := s.plugins.Client().Registry()

	resp := browseResponse{
		Sources:    plugin.RegistrySources(),
		Categories: plugin.Categories(),
		Targets:    targets,
		Listings:   []plugin.Listing{},
	}

	// Nothing typed and nothing narrowed: the shelf, not a search. A category
	// on its own is still a real question ("what protection plugins are
	// there"), so that one goes upstream.
	if q.Text == "" && q.Category == "" {
		resp.Picks = registry.Picks(r.Context())
		for i := range resp.Picks {
			judgeAll(resp.Picks[i].Listings, chosen, nil)
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	result := registry.Search(r.Context(), q)
	resp.Listings = result.Listings
	resp.Notes = result.Notes
	resp.Truncated = result.Truncated
	judgeAll(resp.Listings, chosen, &resp.Incompatible)

	writeJSON(w, http.StatusOK, resp)
}

// judgeAll stamps every listing with its verdict, counting the bad ones for
// the page's count line. A nil verdict — nothing selected to judge against —
// is left nil, and the row draws no badge at all.
func judgeAll(listings []plugin.Listing, against []plugin.NamedTarget, incompatible *int) {
	for i := range listings {
		verdict := plugin.JudgeAcross(against, listings[i].Loaders, listings[i].GameVersions)
		listings[i].Compat = verdict
		if incompatible != nil && verdict != nil && verdict.State == plugin.CompatBad {
			*incompatible++
		}
	}
}

// chosenTargets resolves the rail's instance ticks, in the order the panel
// lists them rather than the order they were ticked — a badge's detail reads
// the same on every row that way.
func chosenTargets(targets []installTarget, raw string) []plugin.NamedTarget {
	wanted := map[string]bool{}
	for _, id := range splitList(raw) {
		wanted[id] = true
	}
	out := make([]plugin.NamedTarget, 0, len(wanted))
	for _, candidate := range targets {
		if wanted[candidate.ID] {
			out = append(out, plugin.NamedTarget{Name: candidate.Name, Target: candidate.Target})
		}
	}
	return out
}

// commonLoader is the loader every selected server runs, or "" when they do
// not agree — or when nothing is selected at all.
func commonLoader(targets []plugin.NamedTarget) string {
	loader := ""
	for _, entry := range targets {
		if entry.Target.Loader == "" {
			continue
		}
		if loader != "" && loader != entry.Target.Loader {
			return ""
		}
		loader = entry.Target.Loader
	}
	return loader
}

type browseDetailResponse struct {
	Listing plugin.Listing `json:"listing"`
	// Body is the plugin's own long description, as its source publishes it.
	// Markdown, generally — rendered as text, because a plugin description is
	// third-party content and the drawer is not worth an HTML sanitiser.
	Body     string           `json:"body"`
	Versions []browseVersion  `json:"versions"`
	Target   plugin.Target    `json:"target"`
	Tracked  *pluginViewLight `json:"tracked,omitempty"`
}

// browseVersion is one installable version, already judged.
type browseVersion struct {
	plugin.Release
	Compat *plugin.Compat `json:"compat,omitempty"`
	// Held is true when the library already has this jar, so the drawer can
	// offer "安装" rather than "下载并安装" and skip the transfer.
	Held bool `json:"held"`
}

// pluginViewLight says the panel already tracks this plugin, and under which
// id — which is what the install call needs.
type pluginViewLight struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	UsedBy []string `json:"usedBy"`
}

// handleBrowsePluginDetail reads one plugin's full record and version list.
//
// Two round trips to the source rather than one, and only on opening the
// drawer: nobody opens thirty of these, and fetching the version list for
// every search result would multiply the cost of a search by thirty for
// information the operator did not ask for.
func (s *Server) handleBrowsePluginDetail(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}

	source := r.PathValue("source")
	id := r.PathValue("id")
	registry := s.plugins.Client().Registry()

	listing, body, err := registry.Project(r.Context(), source, id)
	if err != nil {
		s.writePluginError(w, err)
		return
	}
	releases, err := registry.Versions(r.Context(), source, id)
	if err != nil {
		s.writePluginError(w, err)
		return
	}

	// The same servers the rail had ticked, so the drawer's badges are the row's
	// badges and not a second opinion. Target stays singular — it is what the
	// drawer prints as "这台服是 Paper 1.20.4", which only means something when
	// there is one of them.
	chosen := chosenTargets(s.installTargets(), r.URL.Query().Get("instances"))
	var target plugin.Target
	if len(chosen) == 1 {
		target = chosen[0].Target
	}

	tracked := s.trackedBy(source, id)
	resp := browseDetailResponse{
		Listing:  listing,
		Body:     body,
		Target:   target,
		Versions: make([]browseVersion, 0, len(releases)),
		Tracked:  tracked,
	}
	resp.Listing.Compat = plugin.JudgeAcross(chosen, listing.Loaders, listing.GameVersions)

	for _, release := range releases {
		held := false
		if tracked != nil {
			if item, err := s.plugins.Library().Get(tracked.ID); err == nil {
				held = item.HasVersion(release.Tag)
			}
		}
		resp.Versions = append(resp.Versions, browseVersion{
			Release: release,
			Compat:  plugin.JudgeAcross(chosen, release.Loaders, release.GameVersions),
			Held:    held,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

type trackRequest struct {
	Source string `json:"source"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	// TargetDir overrides where copies land. Empty lets the loader decide:
	// mods for Fabric and Forge, plugins for everything else.
	TargetDir string `json:"targetDir"`
}

// handleTrackPlugin makes a registry listing into a library entry, or returns
// the one that already exists.
//
// Installing from the discovery page goes through the library like everything
// else — download once, copy into as many servers as asked — so the first step
// of an install is always this. Idempotent on purpose: two operators clicking
// 安装 on the same plugin should get the same library entry, not two.
func (s *Server) handleTrackPlugin(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}

	var req trackRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	source := strings.TrimSpace(req.Source)
	id := strings.TrimSpace(req.ID)
	if source == "" || id == "" {
		writeError(w, http.StatusBadRequest, "source and id are required")
		return
	}

	if existing := s.trackedBy(source, id); existing != nil {
		item, err := s.plugins.Library().Get(existing.ID)
		if err != nil {
			s.writePluginError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
		return
	}

	item, err := s.plugins.Library().Add(
		strings.TrimSpace(req.Name),
		plugin.Source{Kind: source, Repo: id},
		strings.TrimSpace(req.TargetDir),
		"",
	)
	if err != nil {
		s.writePluginError(w, err)
		return
	}
	s.log.Info("plugin tracked from registry", "plugin", item.ID, "source", source, "id", id)
	writeJSON(w, http.StatusCreated, item)
}

// trackedBy finds the library entry for a registry listing, if there is one.
func (s *Server) trackedBy(source, id string) *pluginViewLight {
	for _, item := range s.plugins.Library().List() {
		if item.Source.Kind != source || !strings.EqualFold(item.Source.Repo, id) {
			continue
		}
		users := s.instancePlugins.UsedBy()[item.ID]
		names := make([]string, 0, len(users))
		for _, inst := range s.mgr.List() {
			cfg := inst.Config()
			for _, user := range users {
				if user == cfg.ID {
					names = append(names, cfg.Name)
				}
			}
		}
		return &pluginViewLight{ID: item.ID, Name: item.Name, UsedBy: names}
	}
	return nil
}

// installTargets describes every instance a plugin could go into.
func (s *Server) installTargets() []installTarget {
	instances := s.mgr.List()
	out := make([]installTarget, 0, len(instances))
	for _, inst := range instances {
		cfg := inst.Config()
		target := s.detectTarget(cfg)
		out = append(out, installTarget{
			ID:        cfg.ID,
			Name:      cfg.Name,
			State:     inst.State(),
			Target:    target,
			PluginDir: plugin.TargetDirFor(target.Loader),
		})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

// detectTarget works out an instance's game version and loader, using the core
// library as the middle of the three sources plugin.DetectTarget consults.
func (s *Server) detectTarget(cfg instance.Config) plugin.Target {
	var lookup func(string) (string, string, bool)
	if s.jars != nil {
		lookup = func(fileName string) (string, string, bool) {
			cores, err := s.jars.Library().List()
			if err != nil {
				return "", "", false
			}
			for _, core := range cores {
				if core.FileName == fileName {
					return core.Project, core.Version, true
				}
			}
			return "", "", false
		}
	}
	return plugin.DetectTarget(cfg.Directory, cfg.Jar, lookup)
}

// ------------------------------------------------- cross-instance overview

// overviewUse is one instance's copy of a plugin.
type overviewUse struct {
	InstanceID string         `json:"instanceId"`
	Name       string         `json:"name"`
	State      instance.State `json:"state"`
	Version    string         `json:"version"`
	Tag        string         `json:"tag"`
	// Outdated marks a copy behind the newest version the library holds. This
	// is the field the whole page exists for.
	Outdated bool `json:"outdated"`
}

// overviewRow aggregates one plugin across every instance.
type overviewRow struct {
	ID   string        `json:"id"`
	Name string        `json:"name"`
	Note string        `json:"note,omitempty"`
	Kind string        `json:"kind"`
	Repo string        `json:"repo"`
	Used []overviewUse `json:"used"`
	// Newest is the newest version the library holds, and Upstream is what the
	// last update check found. They differ when there is something to download.
	Newest   string `json:"newest,omitempty"`
	Upstream string `json:"upstream,omitempty"`
	// Status is what the second line of the 最新版本 column reads: "all",
	// "mixed" or "unused". Computed here because the rule — every instance on
	// the newest held version — needs the whole row.
	Status string `json:"status"`
	// Size is what this plugin's downloaded versions occupy, which is what
	// makes an unused entry worth cleaning up.
	Size     int64 `json:"size"`
	Versions int   `json:"versions"`
}

// Aggregate statuses. The page is scanned down this column, so there are three
// and no more.
const (
	overviewAllCurrent = "all"
	overviewMixed      = "mixed"
	overviewUnused     = "unused"
)

type overviewResponse struct {
	Rows []overviewRow `json:"rows"`
	Root string        `json:"root"`
	// Unused and UnusedSize are the cleanup summary: jars no instance
	// references, which nothing else in the panel would ever show.
	Unused     int   `json:"unused"`
	UnusedSize int64 `json:"unusedSize"`
}

// handlePluginOverview answers the cross-instance question: are my servers
// running the same versions of the same plugins.
//
// This is the only reason the panel-wide plugin page exists. Everything else
// on it — the version list, the sources, the update check — is reachable from
// one server's own page, and a library page that could not say "生存服 is two
// versions behind 创造服" would be a second list of the same plugins.
func (s *Server) handlePluginOverview(w http.ResponseWriter, r *http.Request) {
	if s.plugins == nil || s.instancePlugins == nil {
		writeJSON(w, http.StatusOK, overviewResponse{Rows: []overviewRow{}})
		return
	}

	// Every instance's records, read once: the alternative is a lookup per
	// plugin per instance, over a file that is already in memory.
	type held struct {
		id    string
		name  string
		state instance.State
		byID  map[string]plugin.Installed
	}
	instances := make([]held, 0)
	for _, inst := range s.mgr.List() {
		cfg := inst.Config()
		byID := map[string]plugin.Installed{}
		for _, record := range s.instancePlugins.Records(cfg.ID) {
			byID[record.PluginID] = record
		}
		instances = append(instances, held{id: cfg.ID, name: cfg.Name, state: inst.State(), byID: byID})
	}
	sort.Slice(instances, func(a, b int) bool { return instances[a].name < instances[b].name })

	items := s.plugins.Library().List()
	resp := overviewResponse{Rows: make([]overviewRow, 0, len(items)), Root: s.plugins.Library().Root()}

	for _, item := range items {
		row := overviewRow{
			ID:       item.ID,
			Name:     item.Name,
			Note:     item.Note,
			Kind:     item.Source.Kind,
			Repo:     item.Source.Repo,
			Used:     []overviewUse{},
			Versions: len(item.Versions),
		}
		for _, version := range item.Versions {
			row.Size += version.Size
		}
		if len(item.Versions) > 0 {
			row.Newest = item.Versions[0].Version
		}
		if item.Latest != nil {
			row.Upstream = item.Latest.Version
		}

		newestTag := ""
		if len(item.Versions) > 0 {
			newestTag = item.Versions[0].Tag
		}
		for _, inst := range instances {
			record, ok := inst.byID[item.ID]
			if !ok {
				continue
			}
			row.Used = append(row.Used, overviewUse{
				InstanceID: inst.id,
				Name:       inst.name,
				State:      inst.state,
				Version:    record.Version,
				Tag:        record.Tag,
				// "Behind the newest jar in the library", not "behind
				// upstream": upgrading is a copy from the library, and a
				// version nobody has downloaded is not something this page's
				// 批量升级 button could apply.
				Outdated: newestTag != "" && record.Tag != newestTag,
			})
		}

		switch {
		case len(row.Used) == 0:
			row.Status = overviewUnused
			resp.Unused++
			resp.UnusedSize += row.Size
		case anyOutdated(row.Used):
			row.Status = overviewMixed
		default:
			row.Status = overviewAllCurrent
		}
		resp.Rows = append(resp.Rows, row)
	}

	// Rows that need doing first. Mixed versions are the actionable state, and
	// unused entries sink to the bottom where the cleanup link lives.
	sort.SliceStable(resp.Rows, func(a, b int) bool {
		if rank := overviewRank(resp.Rows[a].Status) - overviewRank(resp.Rows[b].Status); rank != 0 {
			return rank < 0
		}
		return strings.ToLower(resp.Rows[a].Name) < strings.ToLower(resp.Rows[b].Name)
	})
	writeJSON(w, http.StatusOK, resp)
}

func overviewRank(status string) int {
	switch status {
	case overviewMixed:
		return 0
	case overviewAllCurrent:
		return 1
	default:
		return 2
	}
}

func anyOutdated(uses []overviewUse) bool {
	for _, use := range uses {
		if use.Outdated {
			return true
		}
	}
	return false
}

// ------------------------------------------------------------ bulk upgrade

type bulkUpgradeRequest struct {
	// PluginIDs are library entries to bring every instance up to the newest
	// version the library holds.
	PluginIDs []string `json:"pluginIds"`
}

// bulkImpact is what a bulk upgrade would do, for the confirmation.
type bulkImpact struct {
	Plugins   []bulkPlugin   `json:"plugins"`
	Instances []bulkInstance `json:"instances"`
	// Restarts counts the affected instances that are running, which is the
	// number the confirmation leads with. A stopped server takes the change on
	// its next start and costs nobody anything.
	Restarts int `json:"restarts"`
}

type bulkPlugin struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	To   string `json:"to"`
	// From lists the versions currently in the field, which is not one value
	// when the whole point of the row is that they disagree.
	From []string `json:"from"`
}

type bulkInstance struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	State instance.State `json:"state"`
	// Plugins is what changes on this server, so the confirmation can be read
	// per server rather than per plugin.
	Plugins []string `json:"plugins"`
}

// handleBulkUpgradePreview says exactly what a bulk upgrade would touch.
//
// Its own request rather than something the browser assembles, because the
// browser's copy of the aggregate may be a minute old and a cross-instance
// operation is the wrong place to be approximately right. What comes back is
// what the confirmation must show: which servers, which plugins, and how many
// of those servers are live and will need restarting.
func (s *Server) handleBulkUpgradePreview(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}

	var req bulkUpgradeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	writeJSON(w, http.StatusOK, s.bulkImpact(req.PluginIDs))
}

func (s *Server) bulkImpact(pluginIDs []string) bulkImpact {
	impact := bulkImpact{Plugins: []bulkPlugin{}, Instances: []bulkInstance{}}
	byInstance := map[string]*bulkInstance{}

	for _, id := range pluginIDs {
		item, err := s.plugins.Library().Get(id)
		if err != nil || len(item.Versions) == 0 {
			continue
		}
		newest := item.Versions[0]
		entry := bulkPlugin{ID: item.ID, Name: item.Name, To: newest.Version}

		for _, inst := range s.mgr.List() {
			cfg := inst.Config()
			for _, record := range s.instancePlugins.Records(cfg.ID) {
				if record.PluginID != item.ID || record.Tag == newest.Tag {
					continue
				}
				entry.From = append(entry.From, record.Version)

				target, ok := byInstance[cfg.ID]
				if !ok {
					target = &bulkInstance{ID: cfg.ID, Name: cfg.Name, State: inst.State()}
					byInstance[cfg.ID] = target
				}
				target.Plugins = append(target.Plugins, item.Name)
			}
		}
		if len(entry.From) > 0 {
			impact.Plugins = append(impact.Plugins, entry)
		}
	}

	for _, target := range byInstance {
		impact.Instances = append(impact.Instances, *target)
		if target.State.Running() {
			impact.Restarts++
		}
	}
	sort.Slice(impact.Instances, func(a, b int) bool {
		return impact.Instances[a].Name < impact.Instances[b].Name
	})
	return impact
}

type bulkUpgradeResult struct {
	Impact bulkImpact `json:"impact"`
	// Failures are the copies that did not happen, named so the operator knows
	// which servers are still on the old version. A bulk operation that
	// reported one error and stopped would leave the fleet half-upgraded with
	// no record of where it got to.
	Failures []bulkFailure `json:"failures"`
	Applied  int           `json:"applied"`
}

type bulkFailure struct {
	Instance string `json:"instance"`
	Plugin   string `json:"plugin"`
	Error    string `json:"error"`
}

// handleBulkUpgrade copies the newest held version of each plugin into every
// instance that is behind.
//
// It carries on past a failure rather than stopping at the first: the servers
// it already reached have new jars in them either way, and an operator who is
// told "one of these failed" without being told which one has to check all of
// them by hand.
func (s *Server) handleBulkUpgrade(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}

	var req bulkUpgradeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	// Computed before anything is written: afterwards the records match and
	// there is nothing left to describe.
	impact := s.bulkImpact(req.PluginIDs)
	result := bulkUpgradeResult{Impact: impact, Failures: []bulkFailure{}}

	for _, id := range req.PluginIDs {
		item, err := s.plugins.Library().Get(id)
		if err != nil || len(item.Versions) == 0 {
			continue
		}
		newest := item.Versions[0]

		for _, inst := range s.mgr.List() {
			cfg := inst.Config()
			holds := false
			for _, record := range s.instancePlugins.Records(cfg.ID) {
				if record.PluginID == item.ID && record.Tag != newest.Tag {
					holds = true
				}
			}
			if !holds {
				continue
			}

			if _, err := s.instancePlugins.Install(cfg.ID, cfg.Directory, item.ID, newest.Tag); err != nil {
				result.Failures = append(result.Failures, bulkFailure{
					Instance: cfg.Name, Plugin: item.Name, Error: err.Error(),
				})
				continue
			}
			s.recordPending(cfg.ID, plugin.Change{
				Key:    "plugin:" + item.ID,
				Name:   item.Name,
				Action: plugin.ActionUpgrade,
				At:     time.Now(),
			})
			result.Applied++
		}
	}
	s.log.Info("bulk plugin upgrade", "plugins", len(req.PluginIDs),
		"applied", result.Applied, "failed", len(result.Failures))
	writeJSON(w, http.StatusOK, result)
}

// ----------------------------------------------------------------- helpers

func splitList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func atoiOr(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 0 {
		return fallback
	}
	return value
}
