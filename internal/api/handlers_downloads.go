package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

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

// handleListCoreBuilds answers a version's whole build list, newest first.
//
// The 添加核心 dialog shows versions and builds as the parent and child they
// are, so it needs all of them rather than the one handleLatestCoreBuild
// resolves.
func (s *Server) handleListCoreBuilds(w http.ResponseWriter, r *http.Request) {
	if !s.downloadsAvailable(w) {
		return
	}
	builds, err := s.jars.Client().Builds(r.Context(), r.PathValue("project"), r.PathValue("version"))
	if err != nil {
		s.writeJarError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, builds)
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
	for _, inst := range s.visibleInstances(r) {
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
	Project string `json:"project"`
	Version string `json:"version"`
	// Build is which build to fetch, 0 or absent for the newest. The 添加核心
	// dialog names one; the creation wizard does not.
	Build     int  `json:"build"`
	Overwrite bool `json:"overwrite"`
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
		Build:     req.Build,
		Overwrite: req.Overwrite,
	})
	if err != nil {
		s.writeJarError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
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

// uploadLimit caps an uploaded core. Paper is around 50 MB and a modpack
// server jar can be several hundred, so this is generous on purpose; what it
// exists to stop is a stream with no end writing until the disk is full.
const uploadLimit = 1 << 30 // 1 GiB

// handleUploadCore stores a jar the operator uploaded, with the metadata they
// filled in.
//
// The panel's catalogue is Paper and Velocity; everything else — Forge,
// Fabric, a modpack's own server jar — arrives this way. Dropping a file into
// the cores directory has always worked and still does, but it leaves a row
// with no version and no Java requirement on it, which is the one thing the
// library page cannot work out for itself. So the form asks.
func (s *Server) handleUploadCore(w http.ResponseWriter, r *http.Request) {
	if !s.downloadsAvailable(w) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, uploadLimit)
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "上传里没有找到 jar 文件")
		return
	}
	defer file.Close()

	// Only the base name: a browser sends what the operator picked, and on
	// some of them that is a whole path.
	name := filepath.Base(header.Filename)
	kind := r.FormValue("kind")
	if kind != "server" && kind != "proxy" {
		kind = ""
	}
	javaMin, _ := strconv.Atoi(r.FormValue("javaMinimum"))
	if javaMin < 0 {
		javaMin = 0
	}

	core, err := s.jars.Library().Import(name, file, serverjar.Core{
		Kind:        kind,
		Version:     strings.TrimSpace(r.FormValue("version")),
		JavaMinimum: javaMin,
		Minecraft:   strings.TrimSpace(r.FormValue("minecraft")),
	})
	if err != nil {
		s.writeJarError(w, err)
		return
	}
	s.log.Info("core uploaded to library", "core", core.ID, "size", core.Size)
	writeJSON(w, http.StatusCreated, core)
}

// handleFetchCore serves a core's bytes, for 下载到本地 in the row menu.
//
// The panel downloads onto the machine it runs on, which is the whole point of
// the library — but that leaves no way to get a jar back off it, and an
// operator who wants to run the same build somewhere else should not have to
// go and find it upstream again.
func (s *Server) handleFetchCore(w http.ResponseWriter, r *http.Request) {
	if !s.downloadsAvailable(w) {
		return
	}
	file, core, err := s.jars.Library().Open(r.PathValue("id"))
	if err != nil {
		s.writeJarError(w, err)
		return
	}
	defer file.Close()

	w.Header().Set("Content-Type", "application/java-archive")
	w.Header().Set("Content-Length", strconv.FormatInt(core.Size, 10))
	// The file name is a validated core id — no separators, no quotes — so it
	// is safe to put in the header as it stands.
	w.Header().Set("Content-Disposition", `attachment; filename="`+core.FileName+`"`)
	http.ServeContent(w, r, core.FileName, core.AddedAt, file)
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
	// Unconfined on purpose: the jar lands at the instance root, and choosing
	// which core a server runs is CapInstanceLaunch — a capability that already
	// reaches the whole machine, so narrowing where it may write would be
	// theatre. See scope.go.
	if err := copyIntoInstance(unconfinedBrowser(inst.Config().Directory), source, core.FileName, req.Overwrite); err != nil {
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
	// The jar decides what the instance is. Applying a Velocity core to an
	// instance created as a server is how most proxies come into being — the
	// operator picks the core first and never thinks about "kind" at all — so
	// the switch happens here rather than being a setting to remember.
	if was := cfg.Kind; core.IsProxy() != cfg.IsProxy() {
		if core.IsProxy() {
			cfg.Kind = instance.KindProxy
		} else {
			cfg.Kind = instance.KindServer
		}
		// Only the launch settings still sitting at the old kind's defaults
		// follow it across. A stop command or a set of arguments the operator
		// typed themselves is theirs, and switching the jar is not permission
		// to overwrite it.
		if cfg.StopCommand == instance.DefaultStopCommand(was) {
			cfg.StopCommand = instance.DefaultStopCommand(cfg.Kind)
		}
		if slices.Equal(cfg.ServerArgs, instance.DefaultServerArgs(was)) {
			cfg.ServerArgs = instance.DefaultServerArgs(cfg.Kind)
		}
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
