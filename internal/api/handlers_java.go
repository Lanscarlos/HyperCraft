package api

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/javaruntime"
)

// runtimeView is an installed runtime plus what the panel knows about how it
// is being used, which is what makes deleting one a safe decision.
type runtimeView struct {
	javaruntime.Runtime
	// UsedBy names the instances whose launch config points into this runtime.
	UsedBy []string `json:"usedBy"`
	// Live is true while one of those instances is running on it.
	Live bool `json:"live"`
}

type javaOverview struct {
	Root     string                  `json:"root"`
	Platform javaruntime.Platform    `json:"platform"`
	Runtimes []runtimeView           `json:"runtimes"`
	System   *javaruntime.SystemJava `json:"system"`
	Job      *javaruntime.Job        `json:"job"`
	// Sources are the download sources an install can pick from. They are a
	// fixed list rather than an endpoint of their own: nothing has to be
	// fetched to produce them, and the page needs them to name the source a
	// running job is downloading from.
	Sources []javaruntime.Source `json:"sources"`
	// Source is the one the last install used, which is what the page
	// preselects.
	Source string `json:"source"`
}

// javaSource is the remembered download source, or the automatic one when
// nothing has been chosen yet — or when panel.json names a source this build
// does not have, which is how a mirror that gets retired stops being a
// permanently failing install.
func (s *Server) javaSource() string {
	s.panelMu.RLock()
	stored := s.panel.JavaSource
	s.panelMu.RUnlock()

	source, err := javaruntime.ResolveSource(stored)
	if err != nil {
		return javaruntime.SourceAuto
	}
	return source
}

// rememberJavaSource persists the source an install was started with, so the
// next one defaults to it. A failure to save is logged and otherwise ignored:
// the install is already running, and losing a preference is not worth
// failing it over.
func (s *Server) rememberJavaSource(source string) {
	s.panelMu.Lock()
	if s.panel.JavaSource == source {
		s.panelMu.Unlock()
		return
	}
	s.panel.JavaSource = source
	panel := s.panel
	s.panelMu.Unlock()

	if err := s.store.SavePanel(panel); err != nil {
		s.log.Error("could not persist the java download source", "err", err)
		return
	}
	s.log.Info("java download source changed", "source", source)
}

func (s *Server) writeJavaError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, javaruntime.ErrNotFound), errors.Is(err, javaruntime.ErrInvalidID):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, javaruntime.ErrUnknownRelease), errors.Is(err, javaruntime.ErrUnsupported),
		errors.Is(err, javaruntime.ErrUnknownSource):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, javaruntime.ErrBusy), errors.Is(err, javaruntime.ErrExists):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, javaruntime.ErrUpstream):
		writeError(w, http.StatusBadGateway, err.Error())
	default:
		s.log.Error("java runtime request failed", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (s *Server) javaAvailable(w http.ResponseWriter) bool {
	if s.java == nil {
		writeError(w, http.StatusNotFound, "java management is not enabled on this panel")
		return false
	}
	return true
}

// handleJavaOverview answers everything the Java page needs in one request:
// what is installed, what the system has, and how any install is going.
func (s *Server) handleJavaOverview(w http.ResponseWriter, r *http.Request) {
	if s.java == nil {
		writeJSON(w, http.StatusOK, javaOverview{
			Runtimes: []runtimeView{},
			Sources:  javaruntime.Sources(),
			Source:   s.javaSource(),
		})
		return
	}

	runtimes, err := s.java.Store().List()
	if err != nil {
		s.writeJavaError(w, err)
		return
	}

	overview := javaOverview{
		Root:     s.java.Store().Root(),
		Runtimes: make([]runtimeView, 0, len(runtimes)),
		Sources:  javaruntime.Sources(),
		Source:   s.javaSource(),
	}
	// A platform we cannot install for is still worth reporting: the page says
	// so instead of offering a download that would fail.
	if platform, err := javaruntime.CurrentPlatform(); err == nil {
		overview.Platform = platform
	} else {
		overview.Platform = javaruntime.Platform{Warning: err.Error()}
	}

	instances := s.mgr.List()
	for _, runtime := range runtimes {
		view := runtimeView{Runtime: runtime, UsedBy: []string{}}
		for _, inst := range usersOf(instances, runtime) {
			view.UsedBy = append(view.UsedBy, inst.Config().Name)
			if inst.State().Running() {
				view.Live = true
			}
		}
		overview.Runtimes = append(overview.Runtimes, view)
	}

	if system, ok := s.systemJava(r.Context()); ok {
		overview.System = &system
	}
	if job, ok := s.java.Status(); ok {
		overview.Job = &job
	}
	writeJSON(w, http.StatusOK, overview)
}

// systemJava reports the java already on the machine, cached.
//
// Detection forks a JVM to ask it its version, and this endpoint is polled
// once a second while an install runs. The answer only changes when someone
// installs a JDK behind the panel's back, so a few minutes of staleness costs
// nothing and a fork per poll costs real CPU.
func (s *Server) systemJava(ctx context.Context) (javaruntime.SystemJava, bool) {
	s.systemJavaMu.Lock()
	defer s.systemJavaMu.Unlock()

	if time.Now().Before(s.systemJavaAt) {
		return s.systemJavaCache, s.systemJavaFound
	}
	detected, ok := javaruntime.DetectSystem(ctx)
	s.systemJavaCache, s.systemJavaFound = detected, ok
	s.systemJavaAt = time.Now().Add(5 * time.Minute)
	return detected, ok
}

// usersOf returns the instances launched with a runtime. An instance points at
// the binary, but a custom command could name anything under the directory, so
// the whole tree counts as "in use".
func usersOf(instances []*instance.Instance, runtime javaruntime.Runtime) []*instance.Instance {
	prefix := runtime.Path + string(filepath.Separator)

	var users []*instance.Instance
	for _, inst := range instances {
		cfg := inst.Config()
		candidates := append([]string{cfg.Java}, cfg.Command...)
		for _, candidate := range candidates {
			if candidate == runtime.JavaPath || candidate == runtime.Path ||
				strings.HasPrefix(candidate, prefix) {
				users = append(users, inst)
				break
			}
		}
	}
	return users
}

// availableMajor is a Java feature release the panel can install. The fields
// are spelled out rather than embedding javaruntime.Major, whose own field is
// also called Major.
type availableMajor struct {
	Major int  `json:"major"`
	LTS   bool `json:"lts"`
	// Installed is true when some build of this major is already on disk, so
	// the UI can say "已安装" instead of offering it again.
	Installed bool `json:"installed"`
}

func (s *Server) handleListJavaMajors(w http.ResponseWriter, r *http.Request) {
	if !s.javaAvailable(w) {
		return
	}
	majors, err := s.java.Client().Majors(r.Context())
	if err != nil {
		s.writeJavaError(w, err)
		return
	}
	installed, err := s.java.Store().List()
	if err != nil {
		s.writeJavaError(w, err)
		return
	}

	have := make(map[int]bool, len(installed))
	for _, runtime := range installed {
		have[runtime.Major] = true
	}
	out := make([]availableMajor, 0, len(majors))
	for _, major := range majors {
		out = append(out, availableMajor{
			Major:     major.Major,
			LTS:       major.LTS,
			Installed: have[major.Major],
		})
	}
	writeJSON(w, http.StatusOK, out)
}

type installJavaRequest struct {
	Major     int    `json:"major"`
	ImageType string `json:"imageType"`
	// Source names where to download from; empty is the automatic choice.
	Source string `json:"source"`
}

func (s *Server) handleInstallJava(w http.ResponseWriter, r *http.Request) {
	if !s.javaAvailable(w) {
		return
	}

	var req installJavaRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if req.ImageType == "" {
		req.ImageType = javaruntime.ImageJRE
	}
	// A request that names no source gets the one the last install used, not
	// the built-in default: an operator who moved off it did so for a reason.
	source := req.Source
	if source == "" {
		source = s.javaSource()
	}

	job, err := s.java.Start(req.Major, req.ImageType, source)
	if err != nil {
		s.writeJavaError(w, err)
		return
	}
	s.rememberJavaSource(source)
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handleCancelJavaInstall(w http.ResponseWriter, r *http.Request) {
	if !s.javaAvailable(w) {
		return
	}
	if err := s.java.Cancel(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteJava removes an installed runtime.
//
// It refuses while a server is running on it: those files are mapped into a
// live JVM, and pulling them out from under it is a crash with a confusing
// stack trace. A stopped instance that still points at it is allowed — the
// operator was told which ones, and the next start fails with a clear message.
func (s *Server) handleDeleteJava(w http.ResponseWriter, r *http.Request) {
	if !s.javaAvailable(w) {
		return
	}

	id := r.PathValue("id")
	runtime, err := s.java.Store().Get(id)
	if err != nil {
		s.writeJavaError(w, err)
		return
	}
	for _, inst := range usersOf(s.mgr.List(), runtime) {
		if inst.State().Running() {
			writeError(w, http.StatusConflict,
				"实例「"+inst.Config().Name+"」正在用这个 Java 运行，先停掉它再删除")
			return
		}
	}

	if err := s.java.Store().Remove(id); err != nil {
		s.writeJavaError(w, err)
		return
	}
	s.log.Info("java runtime removed", "runtime", id, "path", runtime.Path)
	w.WriteHeader(http.StatusNoContent)
}
