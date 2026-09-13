package api

import (
	"net/http"

	"github.com/lanscarlos/hypercraft/internal/authz"
	"github.com/lanscarlos/hypercraft/internal/download"
	"github.com/lanscarlos/hypercraft/internal/serverjar"
)

// The panel-wide downloads list.
//
// Four shelves used to each expose their own queue, hanging off their own
// route, gated by their own capability in the router. One list needs one route,
// and there is no capability that could stand at its door: whichever one were
// hung there would keep the other three shelves' jobs from an account that
// holds them. So the route asks only for a signed-in principal, and the filter
// moved into the handler — where it has to be applied to every path out,
// including the count.

// kindCaps says what an account needs to see each shelf's downloads.
//
// Spelled out rather than derived: adding a Kind without adding its capability
// here would leak it to everyone, and a map somebody has to edit is one a
// reviewer notices.
var kindCaps = map[download.Kind]authz.Cap{
	download.KindCore:     authz.CapLibraryCores,
	download.KindJava:     authz.CapPanelJava,
	download.KindDatabase: authz.CapPanelDatabases,
	download.KindPlugin:   authz.CapLibraryPlugins,
}

type downloadsResponse struct {
	Jobs []download.Job `json:"jobs"`
	// Active is how many of the *visible* jobs are still going. Counted here
	// rather than in the browser so it can never disagree with the list: a
	// badge that counts a job the account cannot see is a badge that tells it
	// something it is not entitled to know.
	Active int `json:"active"`
}

// visibleJobs is every queued job this principal may see, newest first.
func (s *Server) visibleJobs(r *http.Request) []download.Job {
	if s.downloads == nil {
		return nil
	}
	who, ok := principalFrom(r.Context())
	if !ok {
		// Unreachable behind requireAuth. Returning nothing is the safe
		// direction for a bug that should not exist.
		return nil
	}

	all := s.downloads.Jobs()
	out := make([]download.Job, 0, len(all))
	for _, job := range all {
		cap, known := kindCaps[job.Kind]
		// An unknown Kind is one this build has no capability mapping for, so
		// nobody sees it. The alternative — showing it to everyone — is how a
		// new shelf leaks on the day it is added.
		if !known || !who.can(cap) {
			continue
		}
		out = append(out, job)
	}
	return out
}

// handleDownloads lists what the panel is downloading, filtered per shelf.
func (s *Server) handleDownloads(w http.ResponseWriter, r *http.Request) {
	jobs := s.visibleJobs(r)
	active := 0
	for _, job := range jobs {
		if job.State.Active() {
			active++
		}
	}
	writeJSON(w, http.StatusOK, downloadsResponse{Jobs: jobs, Active: active})
}

// handleCancelDownload stops one download by id.
//
// An id naming a job this principal cannot see answers 404, not 403: whether
// that id exists is itself something the account is not entitled to learn, and
// the id is all the request carries.
func (s *Server) handleCancelDownload(w http.ResponseWriter, r *http.Request) {
	if s.downloads == nil {
		writeError(w, http.StatusNotFound, "没有这个下载任务")
		return
	}
	id := r.PathValue("id")
	found := false
	for _, job := range s.visibleJobs(r) {
		if job.ID == id {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "没有这个下载任务")
		return
	}
	if err := s.downloads.Cancel(id); err != nil {
		// The job ended between the check above and here, which is a stale
		// page rather than a server fault.
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

// handleClearDownloads forgets finished jobs.
//
// Only the ones this principal can see: an account that may not look at Java
// installs may not delete the record of them either.
func (s *Server) handleClearDownloads(w http.ResponseWriter, r *http.Request) {
	if s.downloads == nil {
		writeJSON(w, http.StatusOK, downloadsResponse{Jobs: []download.Job{}})
		return
	}
	who, ok := principalFrom(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, "没有权限")
		return
	}
	kinds := make([]download.Kind, 0, len(kindCaps))
	for kind, cap := range kindCaps {
		if who.can(cap) {
			kinds = append(kinds, kind)
		}
	}
	s.downloads.ClearFinished(kinds...)
	s.handleDownloads(w, r)
}

// routeChoice is one shelf's routing: what an operator may pick, and what is
// picked now.
type routeChoice struct {
	Kind    download.Kind    `json:"kind"`
	Name    string           `json:"name"`
	Routes  []download.Route `json:"routes"`
	Current string           `json:"current"`
}

// handleDownloadRoutes lists the routes each shelf can be pointed at.
//
// Per shelf rather than as one list, because a route only exists for the
// upstream it serves: ghfast cannot front cdn.azul.com, and the Adoptium
// mirrors carry no Paper jar. Filtered the same way the job list is.
func (s *Server) handleDownloadRoutes(w http.ResponseWriter, r *http.Request) {
	who, ok := principalFrom(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, "没有权限")
		return
	}

	out := make([]routeChoice, 0, 3)
	if who.can(authz.CapLibraryCores) && s.jars != nil {
		out = append(out, routeChoice{
			Kind:    download.KindCore,
			Name:    "服务端核心",
			Routes:  serverjar.Sources(),
			Current: s.jars.Source(),
		})
	}
	if who.can(authz.CapLibraryPlugins) && s.plugins != nil {
		out = append(out, routeChoice{
			Kind:    download.KindPlugin,
			Name:    "插件库",
			Routes:  download.RouteSets["github"].Routes,
			Current: s.plugins.Client().Mirror(),
		})
	}
	writeJSON(w, http.StatusOK, out)
}
