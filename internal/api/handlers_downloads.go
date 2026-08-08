package api

import (
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/serverfiles"
	"github.com/lanscarlos/hypercraft/internal/serverjar"
)

// writeJarError maps the download package's sentinels onto HTTP statuses.
func (s *Server) writeJarError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, serverjar.ErrUnknownProject), errors.Is(err, serverjar.ErrUnknownVersion):
		writeError(w, http.StatusBadRequest, err.Error())
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

func (s *Server) handleCoreDownloadStatus(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	// No job at all is a normal state, not a 404 the client has to special
	// case; a JSON null means "nothing has been downloaded here". The typed nil
	// matters — an untyped one makes writeJSON send an empty body instead.
	if s.jars == nil {
		writeJSON(w, http.StatusOK, (*serverjar.Job)(nil))
		return
	}
	job, ok := s.jars.Status(inst.ID())
	if !ok {
		writeJSON(w, http.StatusOK, (*serverjar.Job)(nil))
		return
	}
	writeJSON(w, http.StatusOK, job)
}

type startDownloadRequest struct {
	Project   string `json:"project"`
	Version   string `json:"version"`
	Overwrite bool   `json:"overwrite"`
	SetAsJar  bool   `json:"setAsJar"`
}

// handleStartCoreDownload begins fetching a server core into the instance
// directory. It returns as soon as the transfer is under way: the download is
// owned by the daemon, so the operator can close the tab and come back to a
// finished jar.
func (s *Server) handleStartCoreDownload(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	if !s.downloadsAvailable(w) {
		return
	}

	var req startDownloadRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	// The instance directory is created up front: it may have been removed
	// since the instance was made, and os.OpenRoot needs it to exist.
	if err := os.MkdirAll(inst.Config().Directory, 0o755); err != nil {
		s.writeDomainError(w, err)
		return
	}

	job, err := s.jars.Start(inst.ID(), serverjar.Request{
		Project:   req.Project,
		Version:   req.Version,
		Overwrite: req.Overwrite,
		SetAsJar:  req.SetAsJar,
		OnDone:    s.applyDownloadedJar(inst, req),
	}, &browserSink{browser: s.browserFor(inst)})
	if err != nil {
		s.writeJarError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

// applyDownloadedJar returns the hook that points the instance at the jar it
// just downloaded, or nil when the operator did not ask for that.
func (s *Server) applyDownloadedJar(inst *instance.Instance, req startDownloadRequest) func(string) error {
	if !req.SetAsJar {
		return nil
	}
	project, _ := serverjar.LookupProject(req.Project)

	return func(fileName string) error {
		cfg := inst.Config()
		cfg.Jar = fileName
		if project.IsProxy() {
			// A proxy is not a Minecraft server: it takes no --nogui, and the
			// default server args would make it refuse to start.
			cfg.ServerArgs = []string{}
		}
		if _, err := s.mgr.Update(cfg.ID, cfg); err != nil {
			return err
		}
		s.log.Info("launch jar set from download", "instance", cfg.Name, "jar", fileName)
		return nil
	}
}

func (s *Server) handleCancelCoreDownload(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	if !s.downloadsAvailable(w) {
		return
	}
	if err := s.jars.Cancel(inst.ID()); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// browserSink adapts the instance file browser to what the downloader needs.
// Every path it is given is a bare file name in the instance root, and
// serverfiles keeps it that way even if upstream sends something stranger.
type browserSink struct {
	browser *serverfiles.Browser
}

func (b *browserSink) Exists(name string) (bool, error) {
	_, err := b.browser.Stat(name)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, serverfiles.ErrNotFound):
		return false, nil
	default:
		return false, err
	}
}

func (b *browserSink) Create(name string) (io.WriteCloser, error) {
	file, closer, err := b.browser.Create(name, false)
	if err != nil {
		return nil, err
	}
	return &sinkFile{file: file, release: closer}, nil
}

func (b *browserSink) Remove(name string) error { return b.browser.Remove(name) }

func (b *browserSink) Rename(from, to string) error { return b.browser.Rename(from, to) }

// sinkFile ties the file handle to the os.Root that produced it, so closing the
// writer releases both. The file is closed explicitly rather than through the
// combined closer because a close error on the last flush is the difference
// between a whole jar and a truncated one.
type sinkFile struct {
	file    *os.File
	release func()
	closed  bool
}

func (f *sinkFile) Write(p []byte) (int, error) { return f.file.Write(p) }

func (f *sinkFile) Close() error {
	if f.closed {
		return nil
	}
	f.closed = true
	err := f.file.Close()
	f.release() // closes the root; the second file close inside is a no-op
	return err
}
