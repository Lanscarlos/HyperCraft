package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/lanscarlos/hypercraft/internal/confighist"
	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/serverfiles"
	"github.com/lanscarlos/hypercraft/internal/serverjar"
)

// writeJarError maps the download package's sentinels onto HTTP statuses.
func (s *Server) writeJarError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, serverjar.ErrUnknownProject), errors.Is(err, serverjar.ErrUnknownVersion):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, serverjar.ErrNotFound), errors.Is(err, serverjar.ErrInvalidID):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, serverjar.ErrBusy), errors.Is(err, serverjar.ErrExists):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, serverjar.ErrUpstream):
		// The panel is fine; PaperMC or the network in between is not.
		writeError(w, http.StatusBadGateway, err.Error())
	default:
		s.writeFileError(w, err)
	}
}

// downloadsAvailable reports the "not wired up" case in one place. The panel can
// run without a downloader (tests, and anyone building a trimmed binary), and
// the UI hides the feature when the catalogue comes back empty.
func (s *Server) downloadsAvailable(w http.ResponseWriter) bool {
	if s.jars == nil {
		writeError(w, http.StatusNotFound, "core downloads are not enabled on this panel")
		return false
	}
	return true
}

func (s *Server) handleListCoreProjects(w http.ResponseWriter, r *http.Request) {
	if s.jars == nil {
		// Not an error: an empty catalogue is exactly what "nothing to offer"
		// means, and the UI already renders that as "上传 jar 吧".
		writeJSON(w, http.StatusOK, []serverjar.Project{})
		return
	}
	writeJSON(w, http.StatusOK, serverjar.Projects)
}

func (s *Server) handleListCoreVersions(w http.ResponseWriter, r *http.Request) {
	if !s.downloadsAvailable(w) {
		return
	}
	versions, err := s.jars.Client().Versions(r.Context(), r.PathValue("project"))
	if err != nil {
		s.writeJarError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, versions)
}

// handleLatestCoreBuild resolves what a download would actually fetch, so the
// UI can show the build number, size and channel before anything is written.
func (s *Server) handleLatestCoreBuild(w http.ResponseWriter, r *http.Request) {
	if !s.downloadsAvailable(w) {
		return
	}
	build, err := s.jars.Client().LatestBuild(r.Context(), r.PathValue("project"), r.PathValue("version"))
	if err != nil {
		s.writeJarError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, build)
}

// coreView is a stored core plus which instances are running a copy of it, so
// the library page can say what a jar is actually being used for.
type coreView struct {
	serverjar.Core
	// UsedBy names the instances whose launch jar has this file name. It is a
	// name match, not a content one: an instance holds its own copy, and that
	// copy is what it launches.
	UsedBy []string `json:"usedBy"`
}

type coreLibraryResponse struct {
	Root  string         `json:"root"`
	Cores []coreView     `json:"cores"`
	Job   *serverjar.Job `json:"job"`
}

// handleCoreLibrary answers everything the core library page needs in one
// request: what has been downloaded, and how any download is going.
func (s *Server) handleCoreLibrary(w http.ResponseWriter, r *http.Request) {
	if s.jars == nil {
		writeJSON(w, http.StatusOK, coreLibraryResponse{Cores: []coreView{}})
		return
	}

	cores, err := s.jars.Library().List()
	if err != nil {
		s.writeJarError(w, err)
		return
	}

	users := make(map[string][]string)
	for _, inst := range s.mgr.List() {
		cfg := inst.Config()
		if cfg.Jar != "" {
			users[cfg.Jar] = append(users[cfg.Jar], cfg.Name)
		}
	}

	resp := coreLibraryResponse{
		Root:  s.jars.Library().Root(),
		Cores: make([]coreView, 0, len(cores)),
	}
	for _, core := range cores {
		view := coreView{Core: core, UsedBy: users[core.FileName]}
		if view.UsedBy == nil {
			view.UsedBy = []string{}
		}
		resp.Cores = append(resp.Cores, view)
	}
	if job, ok := s.jars.Status(); ok {
		resp.Job = &job
	}
	writeJSON(w, http.StatusOK, resp)
}

type startDownloadRequest struct {
	Project   string `json:"project"`
	Version   string `json:"version"`
	Overwrite bool   `json:"overwrite"`
}

// handleStartCoreDownload begins fetching a server core into the library. It
// returns as soon as the transfer is under way: the download is owned by the
// daemon, so the operator can close the tab and come back to a finished jar.
func (s *Server) handleStartCoreDownload(w http.ResponseWriter, r *http.Request) {
	if !s.downloadsAvailable(w) {
		return
	}

	var req startDownloadRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	job, err := s.jars.Start(serverjar.Request{
		Project:   req.Project,
		Version:   req.Version,
		Overwrite: req.Overwrite,
	})
	if err != nil {
		s.writeJarError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handleCancelCoreDownload(w http.ResponseWriter, r *http.Request) {
	if !s.downloadsAvailable(w) {
		return
	}
	if err := s.jars.Cancel(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteCore removes a core from the library. The copies instances were
// stamped out of are untouched, which is why this needs no running check: it
// cannot pull a file out from under a live JVM.
func (s *Server) handleDeleteCore(w http.ResponseWriter, r *http.Request) {
	if !s.downloadsAvailable(w) {
		return
	}
	id := r.PathValue("id")
	if err := s.jars.Library().Remove(id); err != nil {
		s.writeJarError(w, err)
		return
	}
	s.log.Info("core removed from library", "core", id)
	w.WriteHeader(http.StatusNoContent)
}

type applyCoreRequest struct {
	CoreID    string `json:"coreId"`
	SetAsJar  bool   `json:"setAsJar"`
	Overwrite bool   `json:"overwrite"`
}

type applyCoreResponse struct {
	FileName string          `json:"fileName"`
	Instance instance.Status `json:"instance"`
}

// handleApplyCore copies a core out of the library into an instance directory.
//
// A copy rather than a link or a shared path: the instance owns its jar, so
// deleting the library entry — or downloading a newer build over it — cannot
// change what a running server is launched from.
func (s *Server) handleApplyCore(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	if !s.downloadsAvailable(w) {
		return
	}

	var req applyCoreRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	source, core, err := s.jars.Library().Open(req.CoreID)
	if err != nil {
		s.writeJarError(w, err)
		return
	}
	defer source.Close()

	// The instance directory is created up front: it may have been removed
	// since the instance was made, and os.OpenRoot needs it to exist.
	if err := os.MkdirAll(inst.Config().Directory, 0o755); err != nil {
		s.writeDomainError(w, err)
		return
	}
	if err := copyIntoInstance(s.browserFor(inst), source, core.FileName, req.Overwrite); err != nil {
		s.writeJarError(w, err)
		return
	}
	s.log.Info("core copied into instance",
		"instance", inst.Config().Name, "core", core.ID, "dir", inst.Config().Directory)

	updated := inst.Status()
	if req.SetAsJar {
		if updated, err = s.setLaunchJar(inst, core); err != nil {
			s.writeDomainError(w, err)
			return
		}
		// Only when the core actually changed: dropping a jar into the
		// directory without pointing the instance at it changes nothing the
		// history collects.
		s.snapshotAfter(inst, confighist.TriggerTransaction, actorOf(r),
			fmt.Sprintf("切换核心至 %s", core.FileName))
	}
	writeJSON(w, http.StatusOK, applyCoreResponse{FileName: core.FileName, Instance: updated})
}

// setLaunchJar points an instance at a jar it has just been given.
func (s *Server) setLaunchJar(inst *instance.Instance, core serverjar.Core) (instance.Status, error) {
	cfg := inst.Config()
	cfg.Jar = core.FileName
	if core.IsProxy() {
		// A proxy is not a Minecraft server: it takes no --nogui, and the
		// default server args would make it refuse to start.
		cfg.ServerArgs = []string{}
	}
	updated, err := s.mgr.Update(cfg.ID, cfg)
	if err != nil {
		return instance.Status{}, err
	}
	s.log.Info("launch jar set from library", "instance", cfg.Name, "jar", core.FileName)
	return updated.Status(), nil
}

// copyIntoInstance writes the core to a temporary name inside the instance
// directory and renames it into place, so an interrupted copy cannot leave a
// truncated jar looking like a working one.
func copyIntoInstance(browser *serverfiles.Browser, source io.Reader, name string, overwrite bool) error {
	switch _, err := browser.Stat(name); {
	case err == nil && !overwrite:
		return serverfiles.ErrExists
	case err != nil && !errors.Is(err, serverfiles.ErrNotFound):
		return err
	}

	temp := name + ".hypercraft-part"
	_ = browser.Remove(temp)

	file, closer, err := browser.Create(temp, true)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, source)
	closeErr := file.Close()
	closer()
	if copyErr != nil || closeErr != nil {
		_ = browser.Remove(temp)
		return errors.Join(copyErr, closeErr)
	}

	// Rename refuses to clobber, so anything already at the target name goes
	// first — only now, with the replacement complete on disk.
	if _, err := browser.Stat(name); err == nil {
		if err := browser.Remove(name); err != nil {
			_ = browser.Remove(temp)
			return err
		}
	}
	if err := browser.Rename(temp, name); err != nil {
		_ = browser.Remove(temp)
		return err
	}
	return nil
}
