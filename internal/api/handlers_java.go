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
	// Distributions are the OpenJDK builds an install can pick from, default
	// first. A fixed list like Sources, for the same reason.
	Distributions []javaruntime.Distribution `json:"distributions"`
	// Distribution is the one the last install used, which is what the page
	// preselects.
	Distribution string `json:"distribution"`
}

// javaSource is the remembered download source, or the automatic one when
// nothing has been chosen yet — or when panel.json names a source this build
// does not have, which is how a mirror that gets retired stops being a
// permanently failing install.
func (s *Server) javaSource() string {
	s.panelMu.RLock()
	stored := s.panel.JavaSource
	s.panelMu.RUnlock()

	// A source remembered for one distribution means nothing to another: the
	// Adoptium mirrors carry no Zulu. Falling back to the automatic order is
	// the only honest reading of "清华" once the distribution has changed —
	// unlike a source this request explicitly asked for, which is still
	// refused outright by Installer.Start.
	source, err := javaruntime.ResolveSource(s.javaDistribution(), stored)
	if err != nil {
		return javaruntime.SourceAuto
	}
	return source
}

// javaDistribution is the remembered distribution, or the default when nothing
// has been chosen yet — or when panel.json names one this build does not have,
// which is how a distribution that gets retired stops being a permanently
// failing install.
func (s *Server) javaDistribution() string {
	s.panelMu.RLock()
	stored := s.panel.JavaDistribution
	s.panelMu.RUnlock()

	dist, err := javaruntime.ResolveDistribution(stored)
	if err != nil {
		return javaruntime.DefaultDistribution
	}
	return dist
}

// rememberJavaDistribution persists the distribution an install was started
// with, so the next one defaults to it. A failure to save is logged and
// otherwise ignored, for the same reason rememberJavaSource ignores one.
func (s *Server) rememberJavaDistribution(dist string) {
	s.panelMu.Lock()
	if s.panel.JavaDistribution == dist {
		s.panelMu.Unlock()
		return
	}
	s.panel.JavaDistribution = dist
	panel := s.panel
	s.panelMu.Unlock()

	if err := s.store.SavePanel(panel); err != nil {
		s.log.Error("could not persist the java distribution", "err", err)
		return
	}
	s.log.Info("java distribution changed", "distribution", dist)
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
		errors.Is(err, javaruntime.ErrUnknownSource),
		errors.Is(err, javaruntime.ErrUnknownDistribution):
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
			Runtimes:      []runtimeView{},
			Sources:       javaruntime.Sources(s.javaDistribution()),
			Source:        s.javaSource(),
			Distributions: javaruntime.Distributions(),
			Distribution:  s.javaDistribution(),
		})
		return
	}

	runtimes, err := s.java.Store().List()
	if err != nil {
		s.writeJavaError(w, err)
		return
	}

	overview := javaOverview{
		Root:          s.java.Store().Root(),
		Runtimes:      make([]runtimeView, 0, len(runtimes)),
		Sources:       javaruntime.Sources(s.javaDistribution()),
		Source:        s.javaSource(),
		Distributions: javaruntime.Distributions(),
		Distribution:  s.javaDistribution(),
	}
	// A platform we cannot install for is still worth reporting: the page says
	// so instead of offering a download that would fail.
	if platform, err := javaruntime.CurrentPlatform(); err == nil {
		// Whether this platform is a problem depends on the distribution, so
		// the warning is asked for here rather than carried on the platform.
		platform.Warning = javaruntime.PlatformWarning(s.javaDistribution(), platform)
		overview.Platform = platform
	} else {
		overview.Platform = javaruntime.Platform{Warning: err.Error()}
	}

	instances := s.visibleInstances(r)
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
	// The two distributions do not ship the same feature releases — Zulu has
	// 13, 14 and 15 — so the list has to follow whichever one is selected.
	dist := r.URL.Query().Get("distribution")
	if dist == "" {
		dist = s.javaDistribution()
	}
	majors, err := s.java.Client().Majors(r.Context(), dist)
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
	// Distribution names the OpenJDK build; empty is the remembered one.
	Distribution string `json:"distribution"`
	Major        int    `json:"major"`
	ImageType    string `json:"imageType"`
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
	// A request that names neither gets what the last install used, not the
	// built-in default: an operator who moved off them did so for a reason.
	dist := req.Distribution
	if dist == "" {
		dist = s.javaDistribution()
	}
	// The remembered source only carries over within the same distribution:
	// the two lists share nothing but auto and official, so a mirror chosen
	// for the other one would just 404.
	source := req.Source
	if source == "" && dist == s.javaDistribution() {
		source = s.javaSource()
	}

	job, err := s.java.Start(dist, req.Major, req.ImageType, source)
	if err != nil {
		s.writeJavaError(w, err)
		return
	}
	// What the job holds has been through Resolve*, unlike what came in.
	s.rememberJavaDistribution(job.Distribution)
	s.rememberJavaSource(job.Source)
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
	// Every instance: deleting a runtime a server is running on breaks that
	// server whether or not the caller can see it. See allInstances.
	for _, inst := range usersOf(s.allInstances(), runtime) {
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
