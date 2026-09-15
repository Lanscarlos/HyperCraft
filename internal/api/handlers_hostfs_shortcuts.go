package api

import (
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/config"
	"github.com/lanscarlos/hypercraft/internal/hostfs"
)

// maxHostShortcuts caps the saved list. The shortcuts are a row of chips above
// a 280px listing: past a dozen the row is taller than the thing it exists to
// save you scrolling through.
const maxHostShortcuts = 12

// maxShortcutLabelRunes caps a label. A chip is one line and does not wrap, so
// a long label is one that gets clipped — better to refuse it while the
// operator is still looking at the box they typed it into.
const maxShortcutLabelRunes = 24

type hostShortcutsResponse struct {
	Shortcuts []hostfs.Shortcut `json:"shortcuts"`
}

type hostShortcutRequest struct {
	// Label is the operator's own name for the place. Empty on an add means
	// "call it whatever the directory is called"; on a rename it is refused,
	// since a nameless chip is not a rename anyone asked for.
	Label string `json:"label"`
	// Path is only read when adding. A shortcut's id follows its path, so a
	// shortcut that moved would be a different shortcut — the picker removes
	// and re-adds for that.
	Path string `json:"path"`
}

// handleAddHostShortcut saves a directory as a starting point in the picker.
//
// The directory is not required to exist: an unmounted NAS is exactly the kind
// of place worth keeping a chip for, and the picker already draws a path that
// does not exist yet without complaining.
func (s *Server) handleAddHostShortcut(w http.ResponseWriter, r *http.Request) {
	var req hostShortcutRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	path, err := hostfs.CleanShortcutPath(req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "快捷位置要填绝对路径")
		return
	}
	label, err := shortcutLabel(req.Label)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if label == "" {
		label = defaultShortcutLabel(path)
	}

	saved := s.customShortcuts()
	if len(saved) >= maxHostShortcuts {
		writeError(w, http.StatusConflict,
			"快捷位置最多 "+strconv.Itoa(maxHostShortcuts)+" 个，先删掉一个再加")
		return
	}
	// Dedup is against the merged list, not just the saved one: a chip on a
	// path a built-in already holds would be dropped by the merge, and the
	// operator would see their click do nothing.
	if existing, ok := findShortcut(s.hostShortcuts(), path); ok {
		writeError(w, http.StatusConflict,
			"这个目录已经在快捷位置里了，叫「"+existing.Label+"」")
		return
	}

	entry := config.HostShortcut{ID: hostfs.ShortcutID(path), Label: label, Path: path}
	if !s.applyHostShortcuts(w, append(saved, entry)) {
		return
	}
	s.log.Info("host shortcut saved", "shortcut", entry.ID, "label", entry.Label)
	writeJSON(w, http.StatusCreated, hostShortcutsResponse{Shortcuts: s.hostShortcuts()})
}

// handleRenameHostShortcut retitles a saved shortcut.
//
// The label is the only thing about one that can change: the id follows the
// path, so a shortcut that moved would be a different shortcut.
func (s *Server) handleRenameHostShortcut(w http.ResponseWriter, r *http.Request) {
	var req hostShortcutRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	label, err := shortcutLabel(req.Label)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if label == "" {
		writeError(w, http.StatusBadRequest, "快捷位置的名字不能是空的")
		return
	}

	id := r.PathValue("id")
	saved := s.customShortcuts()
	at := indexOfHostShortcut(saved, id)
	if at < 0 {
		writeError(w, http.StatusNotFound, "没有这个快捷位置")
		return
	}
	saved[at].Label = label
	if !s.applyHostShortcuts(w, saved) {
		return
	}
	s.log.Info("host shortcut renamed", "shortcut", id, "label", label)
	writeJSON(w, http.StatusOK, hostShortcutsResponse{Shortcuts: s.hostShortcuts()})
}

// handleRemoveHostShortcut forgets a saved shortcut. Nothing points at one —
// an instance stores its directory, not the chip it was picked through — so
// this cannot orphan anything.
func (s *Server) handleRemoveHostShortcut(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	saved := s.customShortcuts()
	at := indexOfHostShortcut(saved, id)
	if at < 0 {
		writeError(w, http.StatusNotFound, "没有这个快捷位置")
		return
	}
	if !s.applyHostShortcuts(w, append(saved[:at], saved[at+1:]...)) {
		return
	}
	s.log.Info("host shortcut removed", "shortcut", id)
	writeJSON(w, http.StatusOK, hostShortcutsResponse{Shortcuts: s.hostShortcuts()})
}

// shortcutLabel trims a label and refuses one that cannot fit a chip. An empty
// result means "not given", which each caller reads its own way.
func shortcutLabel(raw string) (string, error) {
	label := strings.TrimSpace(raw)
	if len([]rune(label)) > maxShortcutLabelRunes {
		return "", errors.New("快捷位置的名字最多 " +
			strconv.Itoa(maxShortcutLabelRunes) + " 个字")
	}
	if strings.ContainsAny(label, "\n\r\t") {
		return "", errors.New("快捷位置的名字里不能有换行")
	}
	return label, nil
}

// defaultShortcutLabel names a shortcut after its directory, for the common
// case where the operator clicks 收藏 and types nothing.
func defaultShortcutLabel(path string) string {
	// Base of a filesystem root is the separator, which is what the root's own
	// built-in chip is already called — so fall back to the whole path there.
	if base := filepath.Base(path); base != "" && base != string(filepath.Separator) {
		return base
	}
	return path
}

func findShortcut(list []hostfs.Shortcut, path string) (hostfs.Shortcut, bool) {
	for _, entry := range list {
		if sameDirectory(entry.Path, path) {
			return entry, true
		}
	}
	return hostfs.Shortcut{}, false
}

func indexOfHostShortcut(list []config.HostShortcut, id string) int {
	for i, entry := range list {
		if entry.ID == id {
			return i
		}
	}
	return -1
}

// customShortcuts copies the saved list out from under the lock, so a caller
// that appends to it cannot write into the config the rest of the panel reads.
func (s *Server) customShortcuts() []config.HostShortcut {
	s.panelMu.RLock()
	defer s.panelMu.RUnlock()
	return append([]config.HostShortcut(nil), s.panel.HostShortcuts...)
}

// applyHostShortcuts stores the list and writes panel.json.
//
// Same shape as applyGitHubTokens: in memory first so the picker sees the
// change immediately, then to disk, and a failed write is reported as "it
// worked but will not survive a restart" rather than silently.
func (s *Server) applyHostShortcuts(w http.ResponseWriter, list []config.HostShortcut) bool {
	s.panelMu.Lock()
	panel := s.panel
	panel.HostShortcuts = list
	s.panel = panel
	s.panelMu.Unlock()

	if err := s.persistPanel(); err != nil {
		s.log.Error("could not persist the host shortcuts", "err", err)
		writeError(w, http.StatusInternalServerError,
			"快捷位置已生效，但保存失败，重启面板后会丢失")
		return false
	}
	return true
}
