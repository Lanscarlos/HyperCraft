package api

import (
	"errors"
	"net/http"

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

// hostShortcuts are the starting points the picker offers first: where the
// panel puts servers by default, and where it keeps its own data.
func (s *Server) hostShortcuts() []hostfs.Shortcut {
	return hostfs.Shortcuts([]hostfs.Shortcut{
		{Label: "面板服务器目录", Path: s.paths.ServersRoot()},
		{Label: "面板数据目录", Path: s.paths.Root},
	})
}
