package api

import (
	"errors"
	"net/http"

	"github.com/lanscarlos/hypercraft/internal/dbruntime"
)

// installView is an installed engine plus what depends on it, which is what
// makes deleting one a safe decision.
type installView struct {
	dbruntime.Install
	// UsedBy names the services running on this engine.
	UsedBy []string `json:"usedBy"`
	// Live is true while one of them is up.
	Live bool `json:"live"`
}

// serviceView is a service plus the strings the page hands to the operator.
// They are computed here rather than in the browser so the panel and any other
// client agree on what to paste into a plugin config.
type serviceView struct {
	dbruntime.Status
	URI  string `json:"uri"`
	JDBC string `json:"jdbc,omitempty"`
}

type databaseOverview struct {
	Root     string             `json:"root"`
	Platform dbruntime.Platform `json:"platform"`
	Engines  []dbruntime.Engine `json:"engines"`
	Installs []installView      `json:"installs"`
	Services []serviceView      `json:"services"`
	Job      *dbruntime.Job     `json:"job"`
}

func (s *Server) databasesAvailable(w http.ResponseWriter) bool {
	if s.databases == nil || s.databaseInstalls == nil {
		writeError(w, http.StatusNotFound, "数据库管理没有在这个面板上启用")
		return false
	}
	return true
}

func (s *Server) writeDatabaseError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, dbruntime.ErrNotFound), errors.Is(err, dbruntime.ErrInvalidID):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, dbruntime.ErrUnknownEngine), errors.Is(err, dbruntime.ErrUnknownRelease),
		errors.Is(err, dbruntime.ErrUnsupported), errors.Is(err, dbruntime.ErrInvalidConfig):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, dbruntime.ErrBusy), errors.Is(err, dbruntime.ErrExists),
		errors.Is(err, dbruntime.ErrAlreadyRunning), errors.Is(err, dbruntime.ErrNotRunning),
		errors.Is(err, dbruntime.ErrInUse):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, dbruntime.ErrUpstream):
		writeError(w, http.StatusBadGateway, err.Error())
	default:
		s.log.Error("database request failed", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// handleDatabaseOverview answers everything the page needs in one request:
// which engines exist, what is installed, what is running, and how any install
// is going.
func (s *Server) handleDatabaseOverview(w http.ResponseWriter, r *http.Request) {
	overview := databaseOverview{
		Engines:  dbruntime.Engines(),
		Installs: []installView{},
		Services: []serviceView{},
	}
	// A platform the panel cannot install for is still worth reporting: the
	// page says so instead of offering a download that would fail.
	if platform, err := dbruntime.CurrentPlatform(); err == nil {
		overview.Platform = platform
	} else {
		overview.Platform = dbruntime.Platform{Warning: err.Error()}
	}
	if s.databases == nil || s.databaseInstalls == nil {
		writeJSON(w, http.StatusOK, overview)
		return
	}

	installs, err := s.databaseInstalls.Store().List()
	if err != nil {
		s.writeDatabaseError(w, err)
		return
	}
	overview.Root = s.databaseInstalls.Store().Root()

	for _, install := range installs {
		view := installView{Install: install, UsedBy: []string{}}
		for _, service := range s.databases.UsersOf(install.ID) {
			view.UsedBy = append(view.UsedBy, service.Name)
			if service.State.Live() {
				view.Live = true
			}
		}
		overview.Installs = append(overview.Installs, view)
	}
	for _, status := range s.databases.List() {
		overview.Services = append(overview.Services, serviceView{
			Status: status,
			URI:    status.ConnectionURI(),
			JDBC:   status.JDBCURL(),
		})
	}
	if job, ok := s.databaseInstalls.Status(); ok {
		overview.Job = &job
	}
	writeJSON(w, http.StatusOK, overview)
}

// availableVersion is a release on offer, with whether it is already here.
type availableVersion struct {
	dbruntime.Version
	Installed bool `json:"installed"`
}

func (s *Server) handleListDatabaseVersions(w http.ResponseWriter, r *http.Request) {
	if !s.databasesAvailable(w) {
		return
	}
	engine := r.PathValue("engine")
	platform, err := dbruntime.CurrentPlatform()
	if err != nil {
		s.writeDatabaseError(w, err)
		return
	}

	versions, err := s.databaseInstalls.Client().Versions(r.Context(), engine, platform)
	if err != nil {
		s.writeDatabaseError(w, err)
		return
	}
	installed, err := s.databaseInstalls.Store().List()
	if err != nil {
		s.writeDatabaseError(w, err)
		return
	}

	have := make(map[string]bool, len(installed))
	for _, install := range installed {
		if install.Engine == engine {
			have[install.Version] = true
		}
	}
	out := make([]availableVersion, 0, len(versions))
	for _, version := range versions {
		out = append(out, availableVersion{Version: version, Installed: have[version.Version]})
	}
	writeJSON(w, http.StatusOK, out)
}

type installDatabaseRequest struct {
	Engine  string `json:"engine"`
	Version string `json:"version"`
}

func (s *Server) handleInstallDatabase(w http.ResponseWriter, r *http.Request) {
	if !s.databasesAvailable(w) {
		return
	}

	var req installDatabaseRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	job, err := s.databaseInstalls.Start(req.Engine, req.Version)
	if err != nil {
		s.writeDatabaseError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handleCancelDatabaseInstall(w http.ResponseWriter, r *http.Request) {
	if !s.databasesAvailable(w) {
		return
	}
	if err := s.databaseInstalls.Cancel(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteDatabaseEngine removes an installed engine.
//
// It refuses while any service exists on it, running or not — unlike a Java
// runtime, whose users only lose the ability to start. A database service
// without its engine is a data directory nothing can open, and deleting the
// binaries out from under one is not a decision to make on the operator's
// behalf from a page about disk space.
func (s *Server) handleDeleteDatabaseEngine(w http.ResponseWriter, r *http.Request) {
	if !s.databasesAvailable(w) {
		return
	}

	id := r.PathValue("id")
	install, err := s.databaseInstalls.Store().Get(id)
	if err != nil {
		s.writeDatabaseError(w, err)
		return
	}
	if users := s.databases.UsersOf(id); len(users) > 0 {
		names := make([]string, 0, len(users))
		for _, user := range users {
			names = append(names, "「"+user.Name+"」")
		}
		writeError(w, http.StatusConflict,
			"还有数据库"+joinNames(names)+"跑在这个引擎上，先把它们删掉再删引擎")
		return
	}

	if err := s.databaseInstalls.Store().Remove(id); err != nil {
		s.writeDatabaseError(w, err)
		return
	}
	s.log.Info("database engine removed", "engine", id, "path", install.Path)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------- services

type createDatabaseRequest struct {
	Name      string `json:"name"`
	InstallID string `json:"installId"`
	Database  string `json:"database"`
	User      string `json:"user"`
	Password  string `json:"password"`
	Port      int    `json:"port"`
	Bind      string `json:"bind"`
	RunAs     string `json:"runAs"`
	AutoStart bool   `json:"autoStart"`
}

func (s *Server) handleCreateDatabase(w http.ResponseWriter, r *http.Request) {
	if !s.databasesAvailable(w) {
		return
	}

	var req createDatabaseRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	status, err := s.databases.Create(r.Context(), dbruntime.CreateOptions{
		Name:      req.Name,
		InstallID: req.InstallID,
		Database:  req.Database,
		User:      req.User,
		Password:  req.Password,
		Port:      req.Port,
		Bind:      req.Bind,
		RunAs:     req.RunAs,
		AutoStart: req.AutoStart,
	})
	if err != nil {
		s.writeDatabaseError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, serviceView{
		Status: status,
		URI:    status.ConnectionURI(),
		JDBC:   status.JDBCURL(),
	})
}

type updateDatabaseRequest struct {
	Name      *string `json:"name"`
	Port      *int    `json:"port"`
	Bind      *string `json:"bind"`
	AutoStart *bool   `json:"autoStart"`
}

func (s *Server) handleUpdateDatabase(w http.ResponseWriter, r *http.Request) {
	if !s.databasesAvailable(w) {
		return
	}

	var req updateDatabaseRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	status, err := s.databases.Update(r.PathValue("id"), dbruntime.UpdateOptions{
		Name:      req.Name,
		Port:      req.Port,
		Bind:      req.Bind,
		AutoStart: req.AutoStart,
	})
	if err != nil {
		s.writeDatabaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, serviceView{
		Status: status,
		URI:    status.ConnectionURI(),
		JDBC:   status.JDBCURL(),
	})
}

// handleDatabasePower starts or stops a service. Starting waits for the engine
// to open its port, so the response says whether it actually came up rather
// than only that the process was launched.
func (s *Server) handleDatabasePower(start bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.databasesAvailable(w) {
			return
		}
		id := r.PathValue("id")

		var err error
		if start {
			err = s.databases.Start(id)
		} else {
			err = s.databases.Stop(id)
		}
		if err != nil {
			s.writeDatabaseError(w, err)
			return
		}

		status, err := s.databases.Get(id)
		if err != nil {
			s.writeDatabaseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, serviceView{
			Status: status,
			URI:    status.ConnectionURI(),
			JDBC:   status.JDBCURL(),
		})
	}
}

func (s *Server) handleDatabaseLogs(w http.ResponseWriter, r *http.Request) {
	if !s.databasesAvailable(w) {
		return
	}
	lines, err := s.databases.Logs(r.PathValue("id"))
	if err != nil {
		s.writeDatabaseError(w, err)
		return
	}
	if lines == nil {
		lines = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

// handleDeleteDatabase removes a service. The data directory is only deleted
// when the request says so in as many words: everything a server ever wrote is
// in there, and "delete" on a panel button is not consent to lose it.
func (s *Server) handleDeleteDatabase(w http.ResponseWriter, r *http.Request) {
	if !s.databasesAvailable(w) {
		return
	}
	keepData := r.URL.Query().Get("data") != "delete"
	if err := s.databases.Delete(r.PathValue("id"), keepData); err != nil {
		s.writeDatabaseError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func joinNames(names []string) string {
	out := ""
	for i, name := range names {
		if i > 0 {
			out += "、"
		}
		out += name
	}
	return out
}
