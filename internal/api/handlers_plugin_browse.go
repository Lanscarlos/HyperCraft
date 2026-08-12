package api

// The 获取插件 half of the plugin API: searching the registries, and the
// cross-instance view of what is installed everywhere.
//
// Both are here rather than in handlers_plugins.go because both are about
// plugins the panel does not yet track, or about instances in the plural —
// where that file is about one tracked plugin at a time.

import (
	"net/http"
	"net/url"
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
	// Held is true when the library already has a jar of this release, so the
	// drawer can offer "安装" rather than "下载并安装" and skip the transfer.
	Held bool `json:"held"`
	// HeldJars names which of them, by file name. On a release that ships one
	// build per platform "have we got this version" is not one question: the
	// library can hold the paper jar and not the velocity one, and installing
	// onto a proxy then has to download after all.
	HeldJars []string `json:"heldJars,omitempty"`
	// Builds is one verdict per jar, keyed by file name.
	//
	// Compat above is the release's, folded optimistically: one build fitting
	// makes the release worth showing, which is the right answer for a row in
	// a version list. It is the wrong answer for the jar that actually comes
	// down — LuckPerms is compatible with a Velocity proxy and its Bukkit
	// build is not — so the drawer's platform picker gets its own verdicts
	// rather than inheriting a badge that was never about one file.
	Builds map[string]*plugin.Compat `json:"builds,omitempty"`
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
	// Backfills the artwork for a plugin tracked before the panel started
	// keeping it — opening its page is the one moment the icon is in hand.
	if tracked != nil {
		s.plugins.Library().SetIcon(tracked.ID, listing.IconURL)
	}
	resp := browseDetailResponse{
		Listing:  listing,
		Body:     body,
		Target:   target,
		Versions: make([]browseVersion, 0, len(releases)),
		Tracked:  tracked,
	}
	resp.Listing.Compat = plugin.JudgeAcross(chosen, listing.Loaders, listing.GameVersions)

	var library *plugin.Plugin
	if tracked != nil {
		if item, err := s.plugins.Library().Get(tracked.ID); err == nil {
			library = &item
		}
	}
	for _, release := range releases {
		var jars []string
		if library != nil {
			if version := library.Version(release.Tag); version != nil {
				for _, artifact := range version.Artifacts {
					jars = append(jars, artifact.FileName)
				}
			}
		}
		// Only for a release that ships several labelled builds: that is the
		// case where which jar is a question, and the case the picker appears
		// for. One jar, or a pile of unlabelled GitHub assets, and there is
		// nothing here the release's own verdict does not already say.
		var builds map[string]*plugin.Compat
		if labelled := labelledAssets(release); len(labelled) > 1 {
			builds = make(map[string]*plugin.Compat, len(labelled))
			for _, asset := range labelled {
				loaders, gameVersions := plugin.AssetClaims(release, asset)
				builds[asset.Name] = plugin.JudgeAcross(chosen, loaders, gameVersions)
			}
		}
		resp.Versions = append(resp.Versions, browseVersion{
			Release:  release,
			Compat:   plugin.JudgeAcross(chosen, release.Loaders, release.GameVersions),
			Held:     len(jars) > 0,
			HeldJars: jars,
			Builds:   builds,
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
	// IconURL is the registry's artwork, passed along so the library page can
	// show the same face the search result had.
	IconURL string `json:"iconUrl"`
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
	s.plugins.Library().SetIcon(item.ID, strings.TrimSpace(req.IconURL))
	item.IconURL = strings.TrimSpace(req.IconURL)
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

// installMatrixResponse answers "which of my servers can take which of the
// versions I already hold" in one request.
//
// A matrix rather than a verdict for one chosen version, because the install
// dialog needs it *before* the choice is made: a plugin that publishes one jar
// per platform — LuckPerms ships bukkit, velocity, fabric, forge and more under
// the same release number — has a right answer per server, and a dialog that
// could only judge the version already selected would let the operator find
// that out by picking wrong first. It is small: held versions times instances,
// both of which are single digits in practice.
type installMatrixResponse struct {
	Targets []installTarget `json:"targets"`
	// Verdicts is keyed by version tag, then by instance id. A nil verdict is
	// the honest answer for a jar whose source published no metadata — every
	// GitHub release, and Hangar until its versions are read — and the UI shows
	// no badge for it rather than a green one.
	//
	// A release's verdict is the best of its jars': "can this version go on
	// that server" is answered yes by one build fitting, even when the other
	// three do not. Which build that is, is the next field.
	Verdicts map[string]map[string]*plugin.Compat `json:"verdicts"`
	// Jars is the same judgement one level down: keyed by artifact digest,
	// then by instance id. This is what an install actually reads — a release
	// with a paper jar and a velocity jar has a different right answer per
	// server, and picking the release does not pick the file.
	Jars map[string]map[string]*plugin.Compat `json:"jars"`
}

// handlePluginInstallTargets judges every downloaded version of one plugin
// against every instance.
//
// Server-side because Judge is: the loader families (a Spigot plugin runs on
// Paper, the reverse does not) and the minor-line version matching are subtle
// enough that a second copy of them in the browser would drift, and the copy
// that drifts is the one telling somebody their jar is fine.
func (s *Server) handlePluginInstallTargets(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}

	item, err := s.plugins.Library().Get(r.PathValue("id"))
	if err != nil {
		s.writePluginError(w, err)
		return
	}

	targets := s.installTargets()
	resp := installMatrixResponse{
		Targets:  targets,
		Verdicts: make(map[string]map[string]*plugin.Compat, len(item.Versions)),
		Jars:     map[string]map[string]*plugin.Compat{},
	}
	for _, version := range item.Versions {
		release := make(map[string]*plugin.Compat, len(targets))
		for _, artifact := range version.Artifacts {
			loaders, gameVersions := plugin.Claims(version, artifact)
			jar := make(map[string]*plugin.Compat, len(targets))
			for _, target := range targets {
				verdict := plugin.JudgeAcross(
					[]plugin.NamedTarget{{Name: target.Name, Target: target.Target}},
					loaders,
					gameVersions,
				)
				jar[target.ID] = verdict
				release[target.ID] = better(release[target.ID], verdict)
			}
			resp.Jars[plugin.ArtifactKey(artifact)] = jar
		}
		resp.Verdicts[version.Tag] = release
	}
	writeJSON(w, http.StatusOK, resp)
}

// jarFor is which jar of a release this server should be given, when whoever
// asked did not say.
//
// Nobody outside this file should have to: "upgrade 生存服 to 5.5.71" and
// "upgrade 群组端 to 5.5.71" are the same sentence about the same release, and
// on a plugin that ships one build per platform they are two different files.
// A digest the caller *did* supply is honoured as-is — the install dialog
// picks per server and says which jar each one gets, and a resolved choice
// must not be second-guessed here.
func (s *Server) jarFor(cfg instance.Config, pluginID, tag, sha string) string {
	if strings.TrimSpace(sha) != "" {
		return sha
	}
	item, err := s.plugins.Library().Get(pluginID)
	if err != nil {
		return ""
	}
	version := item.Version(tag)
	if version == nil || len(version.Artifacts) < 2 {
		// One jar, or none the panel holds: there is nothing to choose and
		// the empty digest already means "the primary".
		return ""
	}
	return plugin.PickFor(*version, s.detectTarget(cfg)).SHA256
}

// better keeps the more encouraging of two verdicts, which is how a release is
// judged from its jars: one build fitting is the release fitting, and an
// unknown beats a refusal because unknown means the source said nothing.
func better(held, next *plugin.Compat) *plugin.Compat {
	if held == nil {
		return next
	}
	if next == nil {
		return held
	}
	if compatRank(next.State) < compatRank(held.State) {
		return next
	}
	return held
}

func compatRank(state string) int {
	switch state {
	case plugin.CompatOK:
		return 0
	case plugin.CompatUnknown:
		return 1
	default:
		return 2
	}
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
	target := plugin.DetectTarget(cfg.Directory, cfg.Jar, lookup)
	// What the instance says it is, when nothing on disk could say. A proxy
	// whose jar was renamed to server.jar detects as nothing at all, and a
	// nothing means every proxy plugin in the market is badged 未知 — while the
	// one fact needed to judge them is recorded on the instance itself.
	if target.Loader == "" && cfg.IsProxy() {
		target.Loader = "velocity"
		target.Source = "instance-kind"
	}
	return target
}

// ------------------------------------------------- cross-instance overview

// overviewUse is one instance's copy of a plugin.
type overviewUse struct {
	InstanceID string         `json:"instanceId"`
	Name       string         `json:"name"`
	State      instance.State `json:"state"`
	Version    string         `json:"version"`
	Tag        string         `json:"tag"`
	// Outdated marks a copy behind the newest version the library holds that
	// this server can take. This is the field the whole page exists for.
	Outdated bool `json:"outdated"`
	// Update is what "behind" means for this server: which release, and which
	// jar of it. Nil when there is nothing to move up to.
	Update *plugin.Offer `json:"update,omitempty"`
	// Recon is what the last reconciliation said about this copy: drift when
	// the bytes have changed under the record, missing when the file is gone,
	// empty when the two agree or nothing has looked yet. It outranks Outdated
	// on the row — see the status ladder in overviewStatus.
	Recon string `json:"recon,omitempty"`
	// FileName is what the jar is called on this server, which is not always
	// what the library calls it and is the first thing to look at when the
	// digests disagree.
	FileName string `json:"fileName,omitempty"`
	// CheckedAt is when this copy was last compared against the ledger. Zero
	// means never, and the page says so rather than implying a clean check.
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	// Enabled and Present describe the file itself: a plugin switched off by
	// renaming its jar is installed and not running, which is a third state
	// the version column cannot express.
	Enabled bool `json:"enabled"`
	Present bool `json:"present"`
	// RollbackTo names the version this instance's last snapshot would
	// restore, empty when there is nothing to go back to. Sent so the
	// deployment matrix can offer the button only where it would work.
	RollbackTo   string `json:"rollbackTo,omitempty"`
	RollbackNote string `json:"rollbackNote,omitempty"`
	ConfigSaved  bool   `json:"configSaved,omitempty"`
}

// The row states, in the order the ladder resolves them. Six of the seven the
// list page filters by; the seventh, 库外来源, belongs to no library row at all
// and is carried separately — see overviewResponse.Foreign.
const (
	rowSynced   = "ok"      // every copy on the newest held version
	rowUpdate   = "update"  // upstream has published past the library
	rowBehind   = "behind"  // an instance is behind what the library holds
	rowUnused   = "unused"  // held, and nobody has installed it
	rowDrift    = "drift"   // a copy's bytes no longer match the record
	rowMissing  = "missing" // a record whose file is gone
	rowConflict = "foreign" // a jar on a server that no record claims
)

// overviewRow aggregates one plugin across every instance.
type overviewRow struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Note string `json:"note,omitempty"`
	Kind string `json:"kind"`
	Repo string `json:"repo"`
	// IconURL is what the row shows beside the name — the registry's own
	// artwork where there is any, and the repository owner's avatar for a
	// GitHub source, which is the closest thing GitHub publishes to one.
	IconURL string        `json:"iconUrl,omitempty"`
	Used    []overviewUse `json:"used"`
	// Newest is the newest version the library holds, and Upstream is what the
	// last update check found. They differ when there is something to download.
	Newest    string `json:"newest,omitempty"`
	NewestTag string `json:"newestTag,omitempty"`
	Upstream  string `json:"upstream,omitempty"`
	// Status is the one word the 状态 column reads, from the ladder above.
	Status string `json:"status"`
	// Size is what this plugin's downloaded jars occupy, which is what makes
	// an unused entry worth cleaning up.
	Size int64 `json:"size"`
	// Versions counts releases and Artifacts counts jars. They differ for a
	// plugin that ships one build per platform, and the difference is exactly
	// the thing the old page reported as "2 versions, inconsistent" about a
	// plugin that had published one.
	Versions  int `json:"versions"`
	Artifacts int `json:"artifacts"`
	// Variants names the platforms the newest release ships builds for, when
	// it ships more than one. Shown as an explanation, never as a warning:
	// same version, different jars is what correct looks like here.
	Variants []string `json:"variants,omitempty"`
	// Pinned and SelfUpdate are the two policy settings that change how the
	// row should be read — one suppresses the update badge, the other
	// suppresses the drift alarm — so the row carries them rather than making
	// the page fetch each plugin to find out why a badge is missing.
	Pinned     string `json:"pinned,omitempty"`
	SelfUpdate bool   `json:"selfUpdate,omitempty"`
	// Jar is what this plugin's own jars declare — the plugin.yml the server
	// itself reads at startup, folded across the versions held.
	//
	// The row was assembled entirely from the *source* until now: the name is
	// the repository's or the registry listing's, and for a GitHub source that
	// is a repository name, which is regularly not the plugin's name and never
	// says what the plugin does. The library had all of this on disk and threw
	// it away at the door.
	Jar plugin.DescriptorFacts `json:"jar,omitempty"`
}

// overviewForeign is a jar sitting on a server that no record claims.
//
// Not a library row: it has no source, no version history and no update path,
// and folding it into the table as if it had would be the panel claiming to
// know something about a file it has never seen before. It gets its own section
// with one offer — 收编进库 — which is the act that would make it a row.
type overviewForeign struct {
	// Name is what the jar declares itself as, falling back to the file name
	// for a descriptor the panel cannot read.
	Name       string `json:"name"`
	Version    string `json:"version,omitempty"`
	InstanceID string `json:"instanceId"`
	Instance   string `json:"instance"`
	Dir        string `json:"dir"`
	FileName   string `json:"fileName"`
	// Adoptable is set when the jar turns out to be one of the library's own
	// downloads that simply was not installed through the panel. Only filled
	// in after a reconciliation, since matching it means hashing the file.
	Adoptable *plugin.Adoptable `json:"adoptable,omitempty"`
}

// rowIcon is the artwork for a library row.
//
// Registry plugins carry theirs from the moment they were tracked. A GitHub
// source never had one — releases have no artwork — but the owner does, and
// an owner avatar is a better answer than a grey square: it is stable, it is
// recognisable, and for the private repository case it is the operator's own.
func rowIcon(item plugin.Plugin) string {
	if item.IconURL != "" {
		return item.IconURL
	}
	if item.Source.Kind != plugin.SourceGitHub {
		return ""
	}
	owner, _, found := strings.Cut(item.Source.Repo, "/")
	if !found || owner == "" {
		return ""
	}
	return "https://github.com/" + url.PathEscape(owner) + ".png?size=64"
}

type overviewResponse struct {
	Rows []overviewRow `json:"rows"`
	Root string        `json:"root"`
	// Unused and UnusedSize are the cleanup summary: jars no instance
	// references, which nothing else in the panel would ever show.
	Unused     int   `json:"unused"`
	UnusedSize int64 `json:"unusedSize"`
	// TotalSize is the whole library on disk, for the footer line that reads
	// "共 386 MB · 可回收 124 MB".
	TotalSize int64 `json:"totalSize"`
	// Foreign are the jars found on servers that belong to no library row.
	Foreign []overviewForeign `json:"foreign"`
	// ReconciledAt is the oldest reconciliation across the instances that have
	// had one, which is the honest answer to "when was this last checked" for
	// a fleet: the fleet is as stale as its stalest server. Nil when nothing
	// has ever been reconciled.
	ReconciledAt *time.Time `json:"reconciledAt,omitempty"`
	// Unchecked counts instances that have never been reconciled at all.
	Unchecked int `json:"unchecked"`
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
		id     string
		name   string
		dir    string
		state  instance.State
		byID   map[string]plugin.Installed
		onDisk map[string]plugin.Deployment
		// target is what this server runs, read once per instance rather than
		// once per plugin: it decides whether a newer release in the library is
		// a newer release *for this server*, and reading it involves the
		// directory and the core library.
		target plugin.Target
	}
	instances := make([]held, 0)
	for _, inst := range s.mgr.List() {
		cfg := inst.Config()
		byID := map[string]plugin.Installed{}
		for _, record := range s.instancePlugins.Records(cfg.ID) {
			byID[record.PluginID] = record
		}
		instances = append(instances, held{
			id: cfg.ID, name: cfg.Name, dir: cfg.Directory, state: inst.State(), byID: byID,
			onDisk: s.instancePlugins.Deployments(cfg.ID, cfg.Directory),
			target: s.detectTarget(cfg),
		})
	}
	sort.Slice(instances, func(a, b int) bool { return instances[a].name < instances[b].name })

	items := s.plugins.Library().List()
	resp := overviewResponse{
		Rows:    make([]overviewRow, 0, len(items)),
		Root:    s.plugins.Library().Root(),
		Foreign: []overviewForeign{},
	}

	for _, item := range items {
		row := overviewRow{
			ID:         item.ID,
			Name:       item.Name,
			Note:       item.Note,
			Kind:       item.Source.Kind,
			Repo:       item.Source.Repo,
			IconURL:    rowIcon(item),
			Used:       []overviewUse{},
			Versions:   len(item.Versions),
			Pinned:     item.Policy.Pin,
			SelfUpdate: item.Policy.AllowSelfUpdate,
			Jar:        item.Descriptor(),
		}
		for _, version := range item.Versions {
			row.Size += version.TotalSize()
			row.Artifacts += len(version.Artifacts)
		}
		if len(item.Versions) > 0 {
			newest := item.Versions[0]
			row.Newest, row.NewestTag = newest.Version, newest.Tag
			row.Variants = variantsOf(newest)
		}
		if item.Latest != nil {
			row.Upstream = item.Latest.Version
		}

		for _, inst := range instances {
			record, ok := inst.byID[item.ID]
			if !ok {
				continue
			}
			use := overviewUse{
				InstanceID: inst.id,
				Name:       inst.name,
				State:      inst.state,
				Version:    record.Version,
				Tag:        record.Tag,
				FileName:   record.FileName,
				// "Behind the newest jar in the library that this server can
				// take", not "behind upstream" and not "behind the top of the
				// list": upgrading is a copy from the library, so a version
				// nobody has downloaded is not something 批量升级 could apply
				// — and neither is a release whose only jar is for another
				// platform, which is what a cross-platform plugin's newest
				// release regularly is. See plugin.UpdateFor.
				Update: plugin.UpdateFor(item, record.Tag, inst.target),
				Recon:  record.Recon,
			}
			use.Outdated = use.Update != nil
			// Drift on a plugin allowed to update itself is normal operation.
			// It stays visible on the plugin's own page and stops being a
			// finding here — an alarm that fires every week on a working
			// plugin is an alarm nobody reads on the week it matters.
			if use.Recon == plugin.ReconDrift && item.Policy.AllowSelfUpdate {
				use.Recon = ""
			}
			if use.Recon == plugin.ReconOK {
				use.Recon = ""
			}
			if !record.CheckedAt.IsZero() {
				at := record.CheckedAt
				use.CheckedAt = &at
			}
			if state, ok := inst.onDisk[item.ID]; ok {
				use.Enabled, use.Present = state.Enabled, state.Present
				use.RollbackTo, use.RollbackNote = state.RollbackTo, state.RollbackNote
				use.ConfigSaved = state.ConfigSaved
			}
			row.Used = append(row.Used, use)
		}

		row.Status = overviewStatus(item, row)
		if row.Status == rowUnused {
			resp.Unused++
			resp.UnusedSize += row.Size
		}
		resp.TotalSize += row.Size
		resp.Rows = append(resp.Rows, row)
	}

	// Jars on servers that belong to nothing in the library. Cheap enough to
	// answer here — a directory listing and a few kilobytes out of each zip —
	// where saying whether they are one of our own downloads is not, and waits
	// for a reconciliation.
	for _, inst := range instances {
		for _, finding := range s.instancePlugins.Foreign(inst.id, inst.dir) {
			entry := overviewForeign{
				Name:       finding.Name,
				InstanceID: inst.id,
				Instance:   inst.name,
				Dir:        finding.Dir,
				FileName:   finding.FileName,
				Adoptable:  finding.Adoptable,
			}
			if finding.Jar != nil {
				entry.Version = finding.Jar.Version
			}
			resp.Foreign = append(resp.Foreign, entry)
		}
		at := s.instancePlugins.ReconciledAt(inst.id)
		if at.IsZero() {
			resp.Unchecked++
			continue
		}
		if resp.ReconciledAt == nil || at.Before(*resp.ReconciledAt) {
			when := at
			resp.ReconciledAt = &when
		}
	}

	// Rows that need doing first, and the ladder decides what "first" means:
	// a ledger that does not describe the disk outranks any version news,
	// because a version number computed from a wrong ledger is not news.
	sort.SliceStable(resp.Rows, func(a, b int) bool {
		if rank := overviewRank(resp.Rows[a].Status) - overviewRank(resp.Rows[b].Status); rank != 0 {
			return rank < 0
		}
		return strings.ToLower(resp.Rows[a].Name) < strings.ToLower(resp.Rows[b].Name)
	})
	writeJSON(w, http.StatusOK, resp)
}

// overviewStatus is the one word the row reads, and the order it is decided in
// is the whole argument.
//
// Reconciliation first. If the books do not describe the directory, then
// "有更新" and "已同步" are conclusions drawn from a record about a file that
// is not there — and a panel that reports those with confidence is worse than
// one that reports nothing. Only once the two agree does the version ladder
// mean anything.
func overviewStatus(item plugin.Plugin, row overviewRow) string {
	for _, use := range row.Used {
		if use.Recon == plugin.ReconMissing {
			return rowMissing
		}
	}
	for _, use := range row.Used {
		if use.Recon == plugin.ReconDrift {
			return rowDrift
		}
	}
	if len(row.Used) == 0 {
		return rowUnused
	}
	// A pinned plugin is on the version somebody chose. Upstream having moved
	// past it is not a state this row is in.
	if item.Policy.Pin == "" && row.Upstream != "" && row.Upstream != row.Newest {
		return rowUpdate
	}
	for _, use := range row.Used {
		if use.Outdated {
			return rowBehind
		}
	}
	return rowSynced
}

func overviewRank(status string) int {
	switch status {
	case rowMissing:
		return 0
	case rowDrift:
		return 1
	case rowConflict:
		return 2
	case rowUpdate:
		return 3
	case rowBehind:
		return 4
	case rowSynced:
		return 5
	default:
		return 6
	}
}

// labelledAssets is the jars of a release whose platform the source named.
//
// The unlabelled ones are dropped rather than judged, and that is the whole
// point of the filter: a GitHub release's assets are a pile of files nobody
// broke down — the jar, the sources jar, the javadoc jar — and attaching a
// per-platform verdict to each would be the panel dressing up a guess as one
// more thing it knows.
func labelledAssets(release plugin.Release) []plugin.Asset {
	out := make([]plugin.Asset, 0, len(release.Assets))
	for _, asset := range release.Assets {
		if asset.Platform != "" {
			out = append(out, asset)
		}
	}
	return out
}

// variantsOf names the platforms a release ships separate builds for.
//
// Empty for the ordinary plugin that publishes one jar — there is no variance
// to explain — and empty again when several jars declare nothing to tell them
// apart, because "2 个构件" with no names is a fact nobody can act on.
func variantsOf(version plugin.Version) []string {
	if len(version.Artifacts) < 2 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(version.Artifacts))
	for _, artifact := range version.Artifacts {
		label := artifact.Platform
		if label == "" && len(artifact.Loaders) > 0 {
			label = artifact.Loaders[0]
		}
		if label == "" || seen[label] {
			continue
		}
		seen[label] = true
		out = append(out, label)
	}
	if len(out) < 2 {
		return nil
	}
	sort.Strings(out)
	return out
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
		entry := bulkPlugin{ID: item.ID, Name: item.Name}

		for _, inst := range s.mgr.List() {
			cfg := inst.Config()
			for _, record := range s.instancePlugins.Records(cfg.ID) {
				if record.PluginID != item.ID {
					continue
				}
				// Per server, not per library: the newest release is not a
				// release every server can take, and promising one that only
				// ships a proxy build to a Paper server is a promise the
				// upgrade would then have to keep. See plugin.UpdateFor.
				offer := plugin.UpdateFor(item, record.Tag, s.detectTarget(cfg))
				if offer == nil {
					continue
				}
				entry.From = append(entry.From, record.Version)
				entry.To = offer.Version

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
		for _, inst := range s.mgr.List() {
			cfg := inst.Config()
			// What this server can move up to, which is not always what the
			// library's newest release is — see plugin.UpdateFor. Nothing to
			// move up to means this server is not part of the operation.
			var offer *plugin.Offer
			for _, record := range s.instancePlugins.Records(cfg.ID) {
				if record.PluginID == item.ID {
					offer = plugin.UpdateFor(item, record.Tag, s.detectTarget(cfg))
				}
			}
			if offer == nil {
				continue
			}

			_, _, err := s.instancePlugins.InstallArtifact(cfg.ID, cfg.Directory, item.ID, offer.Tag,
				offer.SHA256, "")
			if err != nil {
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
