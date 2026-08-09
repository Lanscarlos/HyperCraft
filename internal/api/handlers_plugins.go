package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/plugin"
)

// writePluginError maps the plugin package's sentinels onto HTTP statuses.
func (s *Server) writePluginError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, plugin.ErrInvalidRepo), errors.Is(err, plugin.ErrInvalidTarget),
		errors.Is(err, plugin.ErrInvalidID):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, plugin.ErrNotFound), errors.Is(err, plugin.ErrNotInstalled):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, plugin.ErrExists), errors.Is(err, plugin.ErrBusy):
		writeError(w, http.StatusConflict, err.Error())
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
	resp := pluginLibraryResponse{
		Root:    s.plugins.Library().Root(),
		Plugins: make([]pluginView, 0, len(items)),
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

type pluginRequest struct {
	Name         string `json:"name"`
	Repo         string `json:"repo"`
	AssetPattern string `json:"assetPattern"`
	Prerelease   bool   `json:"prerelease"`
	TargetDir    string `json:"targetDir"`
	Note         string `json:"note"`
}

func (req pluginRequest) source() plugin.Source {
	return plugin.Source{
		Kind:         plugin.SourceGitHub,
		Repo:         strings.TrimSpace(req.Repo),
		AssetPattern: strings.TrimSpace(req.AssetPattern),
		Prerelease:   req.Prerelease,
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
}

func (s *Server) handleListInstancePlugins(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	if s.plugins == nil || s.instancePlugins == nil {
		writeJSON(w, http.StatusOK, instancePluginsResponse{Entries: []plugin.Entry{}, Library: []pluginView{}})
		return
	}

	entries, err := s.instancePlugins.List(inst.Config().ID, inst.Config().Directory)
	if err != nil {
		s.writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, instancePluginsResponse{
		Entries: entries,
		Library: s.pluginLibrary().Plugins,
		Root:    inst.Config().Directory,
	})
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
	s.log.Info("plugin installed into instance",
		"instance", cfg.Name, "plugin", req.PluginID, "tag", entry.Tag)
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
	if err := s.instancePlugins.Uninstall(cfg.ID, cfg.Directory, key); err != nil {
		s.writePluginError(w, err)
		return
	}
	s.log.Info("plugin removed from instance", "instance", cfg.Name, "plugin", key)
	w.WriteHeader(http.StatusNoContent)
}
