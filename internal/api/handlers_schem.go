package api

import (
	"errors"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/lanscarlos/hypercraft/internal/schematic"
)

// A schematic is the one binary in a server directory an operator regularly
// needs to identify without opening it: a schematics folder is thirty files
// called build_final_v2.schem, and the only way to tell them apart is to load
// the world and paste one. The parse happens on the daemon rather than in the
// browser because the file is already there — shipping 40 MB of gzipped NBT to
// the panel to answer "which one is the castle" would be the expensive way
// round.
type schematicResponse struct {
	Path string `json:"path"`
	// File rather than Name: the embedded preview already has a Name — the one
	// the builder saved inside the file — and the two are routinely different.
	File     string    `json:"file"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	*schematic.Preview
}

func (s *Server) handleSchematic(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}

	target := r.URL.Query().Get("path")
	if !IsSchematic(target) {
		writeError(w, http.StatusUnsupportedMediaType, "只有 .schem / .schematic 文件可以预览")
		return
	}

	file, info, closer, err := s.browserFor(inst).Open(target)
	if err != nil {
		s.writeFileError(w, err)
		return
	}
	defer closer()

	// Checked before the read as well as inside the parser: the size is already
	// known here, so an oversized file costs a stat instead of 64 MB of I/O.
	if info.Size() > schematic.MaxFileBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "文件太大，无法预览")
		return
	}

	preview, err := schematic.Parse(file)
	if err != nil {
		s.writeSchematicError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, schematicResponse{
		Path:     strings.Trim(target, "/"),
		File:     path.Base(info.Name()),
		Size:     info.Size(),
		Modified: info.ModTime(),
		Preview:  preview,
	})
}

// writeSchematicError answers in the language the panel is written in, and
// keeps the parser's own English sentence for the log. "unrecognised file
// header" is the right thing to have when working out why one file fails and
// tells an operator staring at a dialog nothing.
func (s *Server) writeSchematicError(w http.ResponseWriter, err error) {
	s.log.Info("schematic preview refused", "err", err)
	switch {
	case errors.Is(err, schematic.ErrUnsupported), errors.Is(err, schematic.ErrMalformed):
		// 415 rather than 500: the daemon is fine, the file is not a schematic
		// it knows how to read, and the UI says so instead of offering a retry.
		writeError(w, http.StatusUnsupportedMediaType,
			"这个文件不是能识别的 schematic：可能没下载完，或者是别的工具存的格式。")
	case errors.Is(err, schematic.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "文件太大，无法预览。")
	case errors.Is(err, schematic.ErrTooComplex):
		writeError(w, http.StatusUnprocessableEntity, "这个 schematic 太复杂，超出了预览的上限。")
	default:
		s.log.Error("schematic preview failed", "err", err)
		writeError(w, http.StatusInternalServerError, "读取 schematic 失败。")
	}
}

// IsSchematic reports whether a name is one of the two WorldEdit formats the
// preview reads. The UI keeps its own copy of this list; both are short and
// both have to agree, so each names the other.
func IsSchematic(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".schem", ".schematic":
		return true
	default:
		return false
	}
}
