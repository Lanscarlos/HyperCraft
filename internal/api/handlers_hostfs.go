package api

import (
	"errors"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/hostfs"
)

type hostDirResponse struct {
	hostfs.Listing
	Shortcuts []hostfs.Shortcut `json:"shortcuts"`
}

// handleBrowseHost lists a directory on the machine the panel runs on.
//
// It backs the path picker in the instance forms, and the jar dropdown that
// follows a hand-typed directory. Unlike the file manager this is not confined
// to an instance: an instance directory may legitimately be anywhere, and the
// field has always accepted any absolute path — this only saves the operator
// from having to remember it. Read-only, no file contents, authenticated like
// everything else under /api.
func (s *Server) handleBrowseHost(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("path")
	if dir == "" {
		dir = s.paths.ServersRoot()
	}

	listing, err := hostfs.List(dir)
	if err != nil {
		if errors.Is(err, hostfs.ErrInvalidPath) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.writeFileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hostDirResponse{
		Listing:   listing,
		Shortcuts: s.hostShortcuts(),
	})
}

type hostInspectResponse struct {
	hostfs.Inspection
	// TakenBy names the instance already pointing at this directory, if there
	// is one. Two instances on one world directory means two servers writing
	// the same chunks, so the import dialog refuses rather than warns.
	TakenBy string `json:"takenBy,omitempty"`
}

// handleInspectHost describes a directory as a candidate for import: which jar
// starts it, what its server.properties says, how many worlds and plugins are
// in it, and whether the panel already has an instance on it.
//
// It exists so 「导入现有目录」 can be one dialog rather than a form the
// operator fills in from memory — the panel is standing in the directory
// already, so it may as well read what is there.
func (s *Server) handleInspectHost(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("path")
	if strings.TrimSpace(dir) == "" {
		writeError(w, http.StatusBadRequest, "缺少 path 参数")
		return
	}

	inspection, err := hostfs.Inspect(dir)
	if err != nil {
		if errors.Is(err, hostfs.ErrInvalidPath) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.writeFileError(w, err)
		return
	}

	response := hostInspectResponse{Inspection: inspection}
	for _, inst := range s.mgr.List() {
		if sameDirectory(inst.Config().Directory, inspection.Path) {
			response.TakenBy = inst.Config().Name
			break
		}
	}
	writeJSON(w, http.StatusOK, response)
}

// sameDirectory compares two paths as the filesystem would. Case is honoured on
// Linux, where two names differing in case really are two directories.
func sameDirectory(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// hostShortcuts are the starting points the picker offers first: where the
// panel puts servers by default, and where it keeps its own data.
func (s *Server) hostShortcuts() []hostfs.Shortcut {
	return hostfs.Shortcuts([]hostfs.Shortcut{
		{Label: "面板服务器目录", Path: s.paths.ServersRoot()},
		{Label: "面板数据目录", Path: s.paths.Root},
	})
}
