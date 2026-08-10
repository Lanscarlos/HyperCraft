package api

import (
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/plugin"
)

// writePluginError maps the plugin package's sentinels onto HTTP statuses.
func (s *Server) writePluginError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, plugin.ErrInvalidRepo), errors.Is(err, plugin.ErrInvalidTarget),
		errors.Is(err, plugin.ErrInvalidID):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, plugin.ErrNeedsToken):
		// Not a 404: the repository may well exist, and repeating the request
		// unchanged will never work. What is missing is a credential the
		// operator has to supply, so this is their request to fix.
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, plugin.ErrNotFound), errors.Is(err, plugin.ErrNotInstalled):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, plugin.ErrExists), errors.Is(err, plugin.ErrBusy):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, plugin.ErrNotAJar):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, plugin.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, plugin.ErrRateLimited):
		// Not the panel's fault and not the operator's either; 429 is the one
		// status that says "the same request will work later".
		writeError(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, plugin.ErrUpstream), errors.Is(err, plugin.ErrNoRelease),
		errors.Is(err, plugin.ErrNoAsset):
		writeError(w, http.StatusBadGateway, err.Error())
	default:
		s.writeFileError(w, err)
	}
}

// pluginsAvailable reports the "not wired up" case in one place, matching how
// core downloads and Java installs behave when the panel is built without them.
func (s *Server) pluginsAvailable(w http.ResponseWriter) bool {
	if s.plugins == nil || s.instancePlugins == nil {
		writeError(w, http.StatusNotFound, "plugin management is not enabled on this panel")
		return false
	}
	return true
}

// pluginView is a tracked plugin plus the instances running it, so the library
// page can say what a plugin is actually being used for before it is deleted.
type pluginView struct {
	plugin.Plugin
	UsedBy []string `json:"usedBy"`
}

type pluginLibraryResponse struct {
	Root    string       `json:"root"`
	Plugins []pluginView `json:"plugins"`
	Job     *plugin.Job  `json:"job"`
	// TokenConfigured says whether the panel holds a GitHub access token, which
	// is what private repositories and a higher API rate limit both need. The
	// token itself never travels: the page only has to say whether one is there.
	TokenConfigured bool `json:"tokenConfigured"`
	// TokenHint is the last few characters of the stored token, so an operator
	// looking at two accounts' tokens can tell which one this panel holds
	// without being shown anything that could be replayed.
	TokenHint string `json:"tokenHint,omitempty"`
	// Mirrors are the download proxies to choose between, and Mirror is the one
	// in use. Static per build, but they travel with the listing so the settings
	// page needs no second request to render the picker.
	Mirrors []plugin.Mirror `json:"mirrors"`
	Mirror  string          `json:"mirror"`
}

// handlePluginLibrary answers everything the library page needs in one request.
func (s *Server) handlePluginLibrary(w http.ResponseWriter, _ *http.Request) {
	if s.plugins == nil || s.instancePlugins == nil {
		writeJSON(w, http.StatusOK, pluginLibraryResponse{Plugins: []pluginView{}})
		return
	}
	writeJSON(w, http.StatusOK, s.pluginLibrary())
}

func (s *Server) pluginLibrary() pluginLibraryResponse {
	names := make(map[string]string)
	for _, inst := range s.mgr.List() {
		cfg := inst.Config()
		names[cfg.ID] = cfg.Name
	}
	users := s.instancePlugins.UsedBy()

	items := s.plugins.Library().List()
	token := s.githubToken()
	resp := pluginLibraryResponse{
		Root:            s.plugins.Library().Root(),
		Plugins:         make([]pluginView, 0, len(items)),
		TokenConfigured: token != "",
		TokenHint:       tokenHint(token),
		Mirrors:         plugin.Mirrors(),
		Mirror:          s.plugins.Client().Mirror(),
	}
	for _, item := range items {
		view := pluginView{Plugin: item, UsedBy: []string{}}
		for _, instanceID := range users[item.ID] {
			// An instance deleted while the panel was down leaves a record
			// behind; name it only if it still exists.
			if name, ok := names[instanceID]; ok {
				view.UsedBy = append(view.UsedBy, name)
			}
		}
		resp.Plugins = append(resp.Plugins, view)
	}
	if job, ok := s.plugins.Status(); ok {
		resp.Job = &job
	}
	return resp
}

// githubToken is the stored access token, read under the panel lock like every
// other piece of panel config.
func (s *Server) githubToken() string {
	s.panelMu.RLock()
	defer s.panelMu.RUnlock()
	return s.panel.GitHubToken
}

// tokenHint is the tail of a token, enough to recognise one already stored and
// not enough to be worth stealing. Short strings get nothing: a "hint" that
// shows half a secret is not a hint.
func tokenHint(token string) string {
	if len(token) < 12 {
		return ""
	}
	return token[len(token)-4:]
}

type pluginTokenRequest struct {
	// Token is a GitHub personal access token, or "" to forget the one stored.
	Token string `json:"token"`
}

// handlePluginToken stores the credential private repositories are read with.
//
// The token is write-only across this API: it goes in here and is never sent
// back, so a panel session that is later hijacked cannot lift the operator's
// GitHub account out of it. Replacing it is how a wrong one gets fixed.
func (s *Server) handlePluginToken(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}

	var req pluginTokenRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	token := strings.TrimSpace(req.Token)
	if err := validateGitHubToken(token); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// The client first, so a token pasted to fix a failing repository works on
	// the retry that follows even if writing panel.json goes wrong.
	s.plugins.Client().SetToken(token)

	s.panelMu.Lock()
	panel := s.panel
	panel.GitHubToken = token
	s.panel = panel
	s.panelMu.Unlock()

	if err := s.persistPanel(); err != nil {
		s.log.Error("could not persist the GitHub token", "err", err)
		writeError(w, http.StatusInternalServerError, "令牌已生效，但保存失败，重启面板后会丢失")
		return
	}
	if token == "" {
		s.log.Info("GitHub token cleared")
	} else {
		s.log.Info("GitHub token configured")
	}
	writeJSON(w, http.StatusOK, s.pluginLibrary())
}

type pluginMirrorRequest struct {
	// Mirror is a mirror id from plugin.Mirrors(), a custom "https://…/"
	// prefix, or "" for the automatic order.
	Mirror string `json:"mirror"`
}

// handlePluginMirror chooses which proxy plugin jars are downloaded through.
//
// Deliberately not the same setting as the panel's own update mirror, even
// though both proxy the same CDN: plugins download weekly and the panel updates
// a few times a year, so the proxy that works for one is worth choosing without
// digging through a page about the other.
func (s *Server) handlePluginMirror(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}

	var req pluginMirrorRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	mirror, err := plugin.ResolveMirror(req.Mirror)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.plugins.Client().SetMirror(mirror)

	s.panelMu.Lock()
	panel := s.panel
	panel.PluginMirror = mirror
	s.panel = panel
	s.panelMu.Unlock()

	if err := s.persistPanel(); err != nil {
		s.log.Error("could not persist the plugin download mirror", "err", err)
		writeError(w, http.StatusInternalServerError, "已生效，但保存失败，重启面板后会丢失")
		return
	}
	s.log.Info("plugin download mirror changed", "mirror", mirror)
	writeJSON(w, http.StatusOK, s.pluginLibrary())
}

// validateGitHubToken checks the shape of what was pasted, not whether GitHub
// accepts it — that answer only exists at the next request, and the plugin card
// is where it belongs. What is rejected here is what could not be a token at
// all: whitespace or control characters inside it (a pasted line that brought a
// newline along is trimmed before this), or a length no token has.
func validateGitHubToken(token string) error {
	if token == "" {
		return nil
	}
	if len(token) > 512 {
		return errors.New("这不像是一个访问令牌：太长了")
	}
	for _, r := range token {
		if r <= ' ' || r == 0x7f {
			return errors.New("访问令牌里不应该有空格或换行，请只粘贴令牌本身")
		}
	}
	return nil
}

type pluginRequest struct {
	Name         string `json:"name"`
	Repo         string `json:"repo"`
	AssetPattern string `json:"assetPattern"`
	Prerelease   bool   `json:"prerelease"`
	Private      bool   `json:"private"`
	TargetDir    string `json:"targetDir"`
	Note         string `json:"note"`
}

func (req pluginRequest) source() plugin.Source {
	return plugin.Source{
		Kind:         plugin.SourceGitHub,
		Repo:         strings.TrimSpace(req.Repo),
		AssetPattern: strings.TrimSpace(req.AssetPattern),
		Prerelease:   req.Prerelease,
		Private:      req.Private,
	}
}

// handleAddPlugin starts tracking a plugin. It only registers the source —
// nothing is downloaded until the operator picks a version, so adding a plugin
// with a typo in the repository name costs a 404 rather than a stray jar.
func (s *Server) handleAddPlugin(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}

	var req pluginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	item, err := s.plugins.Library().Add(req.Name, req.source(), req.TargetDir, req.Note)
	if err != nil {
		s.writePluginError(w, err)
		return
	}
	s.log.Info("plugin added to library", "plugin", item.ID, "repo", item.Source.Repo)

	// A first check costs one API call and turns a freshly added plugin into
	// one the operator can install immediately, instead of a card that says
	// nothing until they click again.
	if _, err := s.plugins.Check(r.Context(), item.ID); err != nil {
		s.log.Warn("first plugin check failed", "plugin", item.ID, "err", err)
	}
	if checked, err := s.plugins.Library().Get(item.ID); err == nil {
		item = checked
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleUpdatePlugin(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}

	var req pluginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	item, err := s.plugins.Library().Edit(r.PathValue("id"), req.Name, req.source(), req.TargetDir, req.Note)
	if err != nil {
		s.writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// handleDeletePlugin stops tracking a plugin and deletes its downloads. The
// copies instances were given are untouched, which is why this needs no running
// check: it cannot pull a jar out from under a live server.
func (s *Server) handleDeletePlugin(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}
	id := r.PathValue("id")
	if err := s.plugins.Library().Remove(id); err != nil {
		s.writePluginError(w, err)
		return
	}
	s.log.Info("plugin removed from library", "plugin", id)
	w.WriteHeader(http.StatusNoContent)
}

// handlePluginReleases lists what a plugin could be installed at. It always
// hits the network, so it is a click rather than something the page does on
// load — see Plugin.Latest for why the cached answer exists.
func (s *Server) handlePluginReleases(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}
	releases, err := s.plugins.Releases(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, releases)
}

func (s *Server) handleCheckPlugin(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}
	item, err := s.plugins.Check(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// handleCheckPlugins refreshes every plugin. Failures are recorded per plugin
// rather than failing the request: one renamed repository should not hide the
// updates the other nineteen plugins have.
func (s *Server) handleCheckPlugins(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}
	s.plugins.CheckAll(r.Context())
	writeJSON(w, http.StatusOK, s.pluginLibrary())
}

type pluginDownloadRequest struct {
	// Tag names the release to fetch. Empty means whatever is newest, which is
	// what the update button asks for.
	Tag string `json:"tag"`
}

func (s *Server) handleDownloadPlugin(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}

	var req pluginDownloadRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	job, err := s.plugins.Start(r.PathValue("id"), strings.TrimSpace(req.Tag))
	if err != nil {
		s.writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handleCancelPluginDownload(w http.ResponseWriter, _ *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}
	if err := s.plugins.Cancel(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeletePluginVersion removes one downloaded version. The tag arrives as
// a query parameter rather than a path segment because tags contain slashes
// often enough ("release/1.2.0") that a segment would mangle them.
func (s *Server) handleDeletePluginVersion(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}
	tag := r.URL.Query().Get("tag")
	if tag == "" {
		writeError(w, http.StatusBadRequest, "tag is required")
		return
	}
	if err := s.plugins.Library().RemoveVersion(r.PathValue("id"), tag); err != nil {
		s.writePluginError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ------------------------------------------------------- per-instance

type instancePluginsResponse struct {
	Entries []plugin.Entry `json:"entries"`
	// Library travels with the listing so the instance page can offer the
	// versions on hand without a second request — the two are always read
	// together, and the picker is useless without both.
	Library []pluginView `json:"library"`
	Root    string       `json:"root"`
	// Target is what this server turned out to be — the basis of every
	// compatibility badge on the page, and worth showing so an operator can
	// tell "未知兼容性" caused by a plugin from "未知兼容性" caused by the panel
	// not recognising their server jar.
	Target plugin.Target `json:"target"`
	// Pending are the changes the running process has not seen. Empty for a
	// stopped server: there is nothing running that could have missed them.
	Pending []pendingChange `json:"pending"`
	// Live says whether there is a process to restart, which is what decides
	// between "待重启生效" and "下次启动时生效".
	Live bool `json:"live"`
	// Failures are the plugins the server said it could not load, read out of
	// this boot's console output. Sent alongside the entries as well as
	// attached to them, so a failure naming a plugin that is no longer in the
	// directory is still reported rather than silently dropped.
	Failures []plugin.Failure `json:"failures"`
	// LogAvailable is false when the server has not run since the panel
	// started, in which case an empty Failures list means "nothing to read"
	// rather than "nothing wrong".
	LogAvailable bool `json:"logAvailable"`
}

// pendingChange is one change awaiting a restart, with the label already
// written — the phrasing depends on the action and belongs next to the actions.
type pendingChange struct {
	plugin.Change
	Label string `json:"label"`
}

// handleListInstancePlugins answers everything one server's plugin page shows.
//
// Three sources joined here rather than in the browser: the directory, the
// panel's install records, and what the server printed while starting. The
// join has to happen where all three are, and the third is the one that turns
// this page from an inventory into something that reports problems — a
// Minecraft server that cannot load a plugin says so once, in the log, and
// then carries on as if nothing happened.
func (s *Server) handleListInstancePlugins(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	if s.plugins == nil || s.instancePlugins == nil {
		writeJSON(w, http.StatusOK, instancePluginsResponse{
			Entries: []plugin.Entry{}, Library: []pluginView{},
			Pending: []pendingChange{}, Failures: []plugin.Failure{},
		})
		return
	}

	cfg := inst.Config()
	entries, err := s.instancePlugins.List(cfg.ID, cfg.Directory)
	if err != nil {
		s.writePluginError(w, err)
		return
	}

	status := inst.Status()
	var startedAt time.Time
	if status.StartedAt != nil {
		startedAt = *status.StartedAt
	}

	resp := instancePluginsResponse{
		Entries:      entries,
		Library:      s.pluginLibrary().Plugins,
		Root:         cfg.Directory,
		Target:       s.detectTarget(cfg),
		Pending:      []pendingChange{},
		Live:         status.State.Running(),
		Failures:     s.scanPluginFailures(inst, startedAt),
		LogAvailable: !startedAt.IsZero(),
	}

	for _, change := range s.pendingChanges(cfg.ID, startedAt) {
		resp.Pending = append(resp.Pending, pendingChange{Change: change, Label: change.Label()})
	}

	pendingByKey := map[string]string{}
	for _, change := range resp.Pending {
		pendingByKey[change.Key] = change.Action
	}

	for i := range resp.Entries {
		entry := &resp.Entries[i]
		verdict := plugin.Judge(resp.Target, entry.Loaders, entry.GameVersions)
		entry.Compat = &verdict
		entry.PendingAction = pendingByKey[entry.Key]
		// Both names, because which one the server printed depends on how far
		// the load got: the jar's if it never read plugin.yml, the declared
		// name if it did.
		entry.Failure = plugin.MatchFailure(resp.Failures,
			entry.Name, strings.TrimSuffix(entry.FileName, ".jar"), jarInfoName(entry))
	}

	// Problems first. An operator opens this page because something is wrong
	// far more often than to admire the inventory, and a red row eleventh in
	// an alphabetical list is a red row nobody sees.
	sort.SliceStable(resp.Entries, func(a, b int) bool {
		if left, right := resp.Entries[a].Failure != nil, resp.Entries[b].Failure != nil; left != right {
			return left
		}
		if left, right := resp.Entries[a].Managed, resp.Entries[b].Managed; left != right {
			return left
		}
		return strings.ToLower(resp.Entries[a].Name) < strings.ToLower(resp.Entries[b].Name)
	})
	writeJSON(w, http.StatusOK, resp)
}

// entryName is what a listing calls one row, for a pending-change label. The
// key falls through when the row has already gone, which is the removal case.
func entryName(entries []plugin.Entry, key string) string {
	for _, entry := range entries {
		if entry.Key == key {
			return entry.Name
		}
	}
	return strings.TrimPrefix(strings.TrimPrefix(key, "plugin:"), "file:")
}

func jarInfoName(entry *plugin.Entry) string {
	if entry.Jar == nil {
		return ""
	}
	return entry.Jar.Name
}

// scanPluginFailures reads this boot's console output for plugins that did not
// load.
//
// Scoped to the current process by timestamp. The ring buffer holds the last
// two thousand lines whatever produced them, so a server restarted after a fix
// would otherwise keep showing the failure it was restarted to clear — which
// is exactly the moment an operator is looking at this page.
func (s *Server) scanPluginFailures(inst *instance.Instance, startedAt time.Time) []plugin.Failure {
	if startedAt.IsZero() {
		return []plugin.Failure{}
	}
	all := inst.LinesSince(0)
	lines := make([]string, 0, len(all))
	for _, line := range all {
		if line.Time.Before(startedAt) {
			continue
		}
		lines = append(lines, line.Text)
	}
	failures := plugin.ScanFailures(lines)
	if failures == nil {
		return []plugin.Failure{}
	}
	return failures
}

// pendingChanges is the pending list for an instance, or nothing when the
// store was not wired up.
func (s *Server) pendingChanges(instanceID string, startedAt time.Time) []plugin.Change {
	if s.pendingPlugins == nil {
		return nil
	}
	return s.pendingPlugins.Since(instanceID, startedAt)
}

// recordPending notes a change against an instance. Failures are logged and
// swallowed: the jar is already where it belongs, and refusing an install
// because the banner could not be written would be the wrong trade.
func (s *Server) recordPending(instanceID string, change plugin.Change) {
	if s.pendingPlugins == nil {
		return
	}
	if err := s.pendingPlugins.Record(instanceID, change); err != nil {
		s.log.Warn("could not record a pending plugin change",
			"instance", instanceID, "plugin", change.Name, "err", err)
	}
}

type installPluginRequest struct {
	PluginID string `json:"pluginId"`
	// Tag is the library version to install. It is required: "whichever" is
	// exactly the ambiguity a pinned plugin version exists to remove.
	Tag string `json:"tag"`
}

// handleInstallInstancePlugin copies a library version into an instance, or
// swaps the version of one it already has.
//
// A copy rather than a link: the instance owns its jar, so deleting the library
// entry cannot change what a running server has loaded.
func (s *Server) handleInstallInstancePlugin(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	if !s.pluginsAvailable(w) {
		return
	}

	var req installPluginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if strings.TrimSpace(req.PluginID) == "" || strings.TrimSpace(req.Tag) == "" {
		writeError(w, http.StatusBadRequest, "pluginId and tag are required")
		return
	}

	cfg := inst.Config()
	entry, err := s.instancePlugins.Install(cfg.ID, cfg.Directory, req.PluginID, req.Tag)
	if err != nil {
		s.writePluginError(w, err)
		return
	}
	// The jar is in place and the running server will not read it until it
	// restarts. Saying so is the point of the record — see plugin.Pending.
	s.recordPending(cfg.ID, plugin.Change{
		Key:    entry.Key,
		Name:   entry.Name,
		Action: entry.PendingAction,
		At:     time.Now(),
	})
	s.log.Info("plugin installed into instance",
		"instance", cfg.Name, "plugin", req.PluginID, "tag", entry.Tag)
	writeJSON(w, http.StatusOK, entry)
}

// importedResult is one uploaded jar's outcome, per file rather than per
// request: a five-jar upload where the third one is a zip should still land
// the other four and name the one it could not read.
type importedResult struct {
	FileName string           `json:"fileName"`
	Imported *plugin.Imported `json:"imported,omitempty"`
	Error    string           `json:"error,omitempty"`
}

// handleImportPlugins takes jars the operator uploaded into the library.
//
// The way in for everything the catalogues cannot reach: a marketplace plugin,
// a build from a fork, something a friend sent over. Those jars could always be
// dropped into a server's plugins directory through the file manager — this is
// the difference between doing that five times and doing it once.
//
// Streamed part by part rather than parsed into memory: plugin jars run to tens
// of megabytes and the panel is expected to share a small box with the servers
// it runs. Each part is staged, checksummed, and asked what it is; see
// plugin/importer.go.
func (s *Server) handleImportPlugins(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}

	// Optional: with an id, every jar joins that plugin. Without one, each jar
	// finds or creates its own entry from the name it declares.
	id := strings.TrimSpace(r.URL.Query().Get("plugin"))

	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "expected a multipart upload")
		return
	}

	limit := s.maxUploadBytes()
	results := make([]importedResult, 0, 2)
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "malformed upload")
			return
		}
		if part.FileName() == "" {
			part.Close()
			continue
		}

		imported, importErr := s.plugins.Library().ImportJar(id, part.FileName(), part, limit)
		part.Close()

		if importErr != nil {
			results = append(results, importedResult{FileName: part.FileName(), Error: importErr.Error()})
			s.log.Warn("plugin import failed", "file", part.FileName(), "err", importErr)
			continue
		}
		results = append(results, importedResult{FileName: part.FileName(), Imported: &imported})
		s.log.Info("plugin imported",
			"plugin", imported.Plugin.ID, "version", imported.Version.Version, "file", part.FileName())
	}

	if len(results) == 0 {
		writeError(w, http.StatusBadRequest, "no files in the upload")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"results": results})
}

type adoptPluginRequest struct {
	// Key is the unmanaged row to adopt, as handed out by the listing.
	Key string `json:"key"`
}

// handleAdoptInstancePlugin starts tracking a jar the panel found rather than
// installed, when it turns out to be one of the library's own downloads.
//
// Nothing on disk changes: the file is already there and already loading. What
// it adds is the record of which plugin and version it is, which is what makes
// updating and rolling it back possible afterwards.
func (s *Server) handleAdoptInstancePlugin(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	if !s.pluginsAvailable(w) {
		return
	}

	var req adoptPluginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if strings.TrimSpace(req.Key) == "" {
		writeError(w, http.StatusBadRequest, "key is required")
		return
	}

	cfg := inst.Config()
	entry, err := s.instancePlugins.Adopt(cfg.ID, cfg.Directory, req.Key)
	if err != nil {
		s.writePluginError(w, err)
		return
	}
	s.log.Info("instance plugin adopted",
		"instance", cfg.Name, "plugin", entry.PluginID, "tag", entry.Tag)
	writeJSON(w, http.StatusOK, entry)
}

type togglePluginRequest struct {
	Key     string `json:"key"`
	Enabled bool   `json:"enabled"`
}

// handleToggleInstancePlugin switches a plugin on or off for one instance by
// renaming its jar. It takes effect on the server's next start, which is what
// the UI says next to the switch.
func (s *Server) handleToggleInstancePlugin(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	if !s.pluginsAvailable(w) {
		return
	}

	var req togglePluginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	cfg := inst.Config()
	if err := s.instancePlugins.SetEnabled(cfg.ID, cfg.Directory, req.Key, req.Enabled); err != nil {
		s.writePluginError(w, err)
		return
	}
	s.log.Info("instance plugin toggled",
		"instance", cfg.Name, "plugin", req.Key, "enabled", req.Enabled)

	entries, err := s.instancePlugins.List(cfg.ID, cfg.Directory)
	if err != nil {
		s.writePluginError(w, err)
		return
	}

	// Switching a plugin off renames its jar; the server that already loaded
	// it goes on running it until it restarts. This is the case the panel used
	// to get wrong with an on/off switch that looked immediate.
	action := plugin.ActionEnable
	if !req.Enabled {
		action = plugin.ActionDisable
	}
	s.recordPending(cfg.ID, plugin.Change{
		Key:    req.Key,
		Name:   entryName(entries, req.Key),
		Action: action,
		At:     time.Now(),
	})
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) handleUninstallInstancePlugin(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	if !s.pluginsAvailable(w) {
		return
	}

	key := r.URL.Query().Get("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "key is required")
		return
	}

	cfg := inst.Config()
	// Read before the removal, or there is no row left to take the name from.
	name := key
	if entries, err := s.instancePlugins.List(cfg.ID, cfg.Directory); err == nil {
		name = entryName(entries, key)
	}

	if err := s.instancePlugins.Uninstall(cfg.ID, cfg.Directory, key); err != nil {
		s.writePluginError(w, err)
		return
	}
	s.recordPending(cfg.ID, plugin.Change{
		Key: key, Name: name, Action: plugin.ActionRemove, At: time.Now(),
	})
	s.log.Info("plugin removed from instance", "instance", cfg.Name, "plugin", key)
	w.WriteHeader(http.StatusNoContent)
}
