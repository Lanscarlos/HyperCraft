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
}

func (s *Server) writeJavaError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, javaruntime.ErrNotFound), errors.Is(err, javaruntime.ErrInvalidID):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, javaruntime.ErrUnknownRelease), errors.Is(err, javaruntime.ErrUnsupported):
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
		writeJSON(w, http.StatusOK, javaOverview{Runtimes: []runtimeView{}})
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

	job, err := s.java.Start(req.Major, req.ImageType)
	if err != nil {
		s.writeJavaError(w, err)
		return
	}
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
