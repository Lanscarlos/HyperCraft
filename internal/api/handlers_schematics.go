package api

// The 建筑库 and 建筑市场 halves of the schematic API.
//
// A schematic reached the panel exactly one way before this: through the file
// manager, as a file in one server's directory, previewable in place and
// invisible from anywhere else. That is the right home for a build one server
// uses and the wrong one for the way builds are actually kept — a spawn that
// gets pasted into the new season's world, a set of shop stalls that goes onto
// every survival server, a warehouse of downloads from the last three years.
// Those are panel-wide assets, exactly like a plugin, so they get a library:
// held once, described once, and copied into whichever server needs them.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/schematic"
	"github.com/lanscarlos/hypercraft/internal/schemlib"
	"github.com/lanscarlos/hypercraft/internal/serverfiles"
)

// marketTimeout bounds one read of the market's sources, which is a network
// call to somebody else's host and must not hold a request open indefinitely.
const marketTimeout = 45 * time.Second

func (s *Server) schematicsAvailable(w http.ResponseWriter) bool {
	if s.schematics == nil || s.schemMarket == nil {
		writeError(w, http.StatusNotFound, "这个面板没有启用建筑库")
		return false
	}
	return true
}

// schematicLibraryResponse is the 建筑列表 page in one request.
type schematicLibraryResponse struct {
	Root    string            `json:"root"`
	Entries []schemlib.Entry  `json:"entries"`
	Total   int64             `json:"totalSize"`
	Targets []schematicTarget `json:"targets"`
}

// schematicTarget is one server a build can be installed onto, and where on it.
//
// It travels with the listing rather than being asked for per install, because
// the answer is what the 安装到 menu is made of: which servers there are, and
// for each of them which directory their editor actually reads. Three stats per
// server, on a page that already lists the library.
type schematicTarget struct {
	ID    string            `json:"id"`
	Name  string            `json:"name"`
	State instance.State    `json:"state"`
	Dirs  []schemlib.Target `json:"dirs"`
}

func (s *Server) schematicTargets() []schematicTarget {
	instances := s.mgr.List()
	out := make([]schematicTarget, 0, len(instances))
	for _, inst := range instances {
		cfg := inst.Config()
		browser := serverfiles.New(cfg.Directory)
		exists := func(rel string) bool {
			_, err := browser.Stat(rel)
			return err == nil
		}
		out = append(out, schematicTarget{
			ID:    cfg.ID,
			Name:  cfg.Name,
			State: inst.State(),
			Dirs:  schemlib.Targets(exists),
		})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

func (s *Server) handleSchematicLibrary(w http.ResponseWriter, r *http.Request) {
	if !s.schematicsAvailable(w) {
		return
	}
	entries := s.schematics.List()
	var total int64
	for _, entry := range entries {
		total += entry.Size
	}
	writeJSON(w, http.StatusOK, schematicLibraryResponse{
		Root:    s.schematics.Root(),
		Entries: entries,
		Total:   total,
		Targets: s.schematicTargets(),
	})
}

// handleUploadSchematics takes .schem files the operator uploaded.
//
// Streamed part by part rather than parsed into memory, the same way plugin
// jars are: a schematic is usually kilobytes and occasionally a hundred
// megabytes of gzipped NBT, and the panel is expected to share a small box with
// the servers it runs. Each file is staged, hashed and parsed before it is
// kept; see schemlib.Library.Add.
func (s *Server) handleUploadSchematics(w http.ResponseWriter, r *http.Request) {
	if !s.schematicsAvailable(w) {
		return
	}

	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "expected a multipart upload")
		return
	}

	limit := s.maxUploadBytes()
	if limit <= 0 || limit > schematic.MaxFileBytes {
		limit = schematic.MaxFileBytes
	}

	results := make([]schematicImportResult, 0, 2)
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "malformed upload")
			return
		}
		if part.FileName() == "" {
			part.Close()
			continue
		}

		name := part.FileName()
		entry, addErr := s.schematics.Add("", name, part, schemlib.Origin{Kind: schemlib.OriginUpload}, limit)
		part.Close()

		if addErr != nil {
			results = append(results, schematicImportResult{FileName: name, Error: friendlySchematicError(addErr)})
			s.log.Warn("schematic upload refused", "file", name, "err", addErr)
			continue
		}
		results = append(results, schematicImportResult{FileName: name, Entry: &entry})
		s.log.Info("schematic added", "id", entry.ID, "file", entry.FileName, "size", entry.Size)
	}

	if len(results) == 0 {
		writeError(w, http.StatusBadRequest, "no files in the upload")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"results": results})
}

// schematicImportResult is one file's outcome. A failure is reported per file
// rather than failing the whole upload: dragging in a folder of thirty builds
// where two are corrupt should store twenty-eight of them and say which two.
type schematicImportResult struct {
	FileName string          `json:"fileName"`
	Entry    *schemlib.Entry `json:"entry,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type importSchematicRequest struct {
	// Path is the file inside the instance, as the file manager names it.
	Path string `json:"path"`
	Name string `json:"name,omitempty"`
}

// handleImportInstanceSchematic copies a build out of one server into the
// library.
//
// The other direction of 安装到实例, and the one that makes the library worth
// filling: a build made in-game and saved with //schem save lives in one
// server's folder, and this is what turns it into something the panel holds.
// The file on the server is left exactly where it is.
func (s *Server) handleImportInstanceSchematic(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	if !s.schematicsAvailable(w) {
		return
	}

	var req importSchematicRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if !IsSchematic(req.Path) {
		writeError(w, http.StatusUnsupportedMediaType, "只有 .schem / .schematic 文件可以入库")
		return
	}

	file, info, closer, err := s.browserFor(inst).Open(req.Path)
	if err != nil {
		s.writeFileError(w, err)
		return
	}
	defer closer()

	if info.Size() > schematic.MaxFileBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "文件太大，无法入库")
		return
	}

	cfg := inst.Config()
	entry, err := s.schematics.Add(req.Name, path.Base(info.Name()), file, schemlib.Origin{
		Kind: schemlib.OriginInstance,
		From: cfg.Name,
	}, schematic.MaxFileBytes)
	if err != nil {
		s.writeSchemlibError(w, err)
		return
	}
	s.log.Info("schematic imported from instance",
		"instance", cfg.Name, "path", req.Path, "id", entry.ID)
	writeJSON(w, http.StatusCreated, entry)
}

// handleRescanSchematics adopts files dropped into the library directory by
// hand, and forgets entries whose files have gone.
//
// The migration path, and the reason the library directory is a plain folder of
// .schem files rather than a content-addressed store: somebody arriving from
// another panel has a directory of builds and no interest in uploading them one
// at a time through a browser.
func (s *Server) handleRescanSchematics(w http.ResponseWriter, r *http.Request) {
	if !s.schematicsAvailable(w) {
		return
	}
	added, dropped := s.schematics.Rescan()
	if added > 0 || dropped > 0 {
		s.log.Info("schematic library rescanned", "added", added, "dropped", dropped)
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": added, "dropped": dropped})
}

type editSchematicRequest struct {
	Name string   `json:"name"`
	Note string   `json:"note"`
	Tags []string `json:"tags"`
}

func (s *Server) handleUpdateSchematic(w http.ResponseWriter, r *http.Request) {
	if !s.schematicsAvailable(w) {
		return
	}
	var req editSchematicRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	entry, err := s.schematics.Edit(r.PathValue("id"), req.Name, req.Note, req.Tags)
	if err != nil {
		s.writeSchemlibError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

func (s *Server) handleDeleteSchematic(w http.ResponseWriter, r *http.Request) {
	if !s.schematicsAvailable(w) {
		return
	}
	id := r.PathValue("id")
	if err := s.schematics.Remove(id); err != nil {
		s.writeSchemlibError(w, err)
		return
	}
	s.log.Info("schematic removed", "id", id)
	w.WriteHeader(http.StatusNoContent)
}

// handleSchematicPreview is the file manager's preview, pointed at the library.
//
// The same parse and the same payload — see handlers_schem.go — so the browser
// draws a library entry with the renderer it already has. That is the whole
// point of the library holding real files in a real directory: there is one way
// to read a schematic in this panel, and it does not care whose directory the
// file is in.
func (s *Server) handleSchematicPreview(w http.ResponseWriter, r *http.Request) {
	if !s.schematicsAvailable(w) {
		return
	}

	preview, entry, err := s.schematics.Preview(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, schemlib.ErrNotFound) || errors.Is(err, schemlib.ErrInvalidID) {
			s.writeSchemlibError(w, err)
			return
		}
		s.writeSchematicError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, schematicResponse{
		Path:     entry.FileName,
		File:     entry.FileName,
		Size:     entry.Size,
		Modified: entry.AddedAt,
		Preview:  preview,
	})
}

func (s *Server) handleDownloadSchematic(w http.ResponseWriter, r *http.Request) {
	if !s.schematicsAvailable(w) {
		return
	}
	file, entry, err := s.schematics.Open(r.PathValue("id"))
	if err != nil {
		s.writeSchemlibError(w, err)
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取文件失败")
		return
	}
	// Attachment always: a .schem is bytes for WorldEdit, and there is nothing
	// a browser could usefully do with it inline.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", contentDisposition(entry.FileName))
	http.ServeContent(w, r, entry.FileName, info.ModTime(), file)
}

type installSchematicRequest struct {
	InstanceID string `json:"instanceId"`
	// Dir is where on the server it lands. Empty picks the directory that
	// server's editor reads; see schemlib.Targets.
	Dir string `json:"dir,omitempty"`
	// Overwrite replaces a build of the same name that is already there.
	// Spelled out rather than defaulted either way: overwriting is how you
	// update a build every server has a copy of, and it is also how you lose
	// the one somebody edited in-game and never saved anywhere else.
	Overwrite bool `json:"overwrite,omitempty"`
}

type installSchematicResponse struct {
	Path string `json:"path"`
	// Command is the //schem load line for the file that just landed, because
	// the next thing that happens after an install is somebody typing it.
	Command string `json:"command"`
}

// handleInstallSchematic copies one build into a server's schematics directory.
//
// Copied rather than linked, exactly like a plugin jar or a core: the file on
// the server is the server's from then on, so deleting the library entry never
// breaks a world that was built from it.
func (s *Server) handleInstallSchematic(w http.ResponseWriter, r *http.Request) {
	if !s.schematicsAvailable(w) {
		return
	}

	var req installSchematicRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	inst, err := s.mgr.Get(strings.TrimSpace(req.InstanceID))
	if err != nil {
		s.writeDomainError(w, err)
		return
	}

	file, entry, err := s.schematics.Open(r.PathValue("id"))
	if err != nil {
		s.writeSchemlibError(w, err)
		return
	}
	defer file.Close()

	browser := s.browserFor(inst)
	dir := strings.TrimSpace(req.Dir)
	if dir == "" {
		dir = schemlib.DefaultTarget(func(rel string) bool {
			_, err := browser.Stat(rel)
			return err == nil
		})
	}
	dir, err = schemlib.CleanTargetDir(dir)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Created rather than required to exist: putting the builds in place before
	// WorldEdit is installed is a normal way round for somebody setting a
	// server up, and an empty schematics folder costs nothing.
	if err := browser.Mkdir(dir); err != nil {
		s.writeFileError(w, err)
		return
	}

	target := dir + "/" + entry.FileName
	dst, closer, err := browser.Create(target, req.Overwrite)
	if err != nil {
		if errors.Is(err, serverfiles.ErrExists) {
			writeError(w, http.StatusConflict,
				fmt.Sprintf("%s 里已经有 %s 了，勾选覆盖再来一次。", dir, entry.FileName))
			return
		}
		s.writeFileError(w, err)
		return
	}
	defer closer()

	if _, err := io.Copy(dst, file); err != nil {
		s.log.Error("schematic install failed", "instance", inst.Config().Name, "err", err)
		writeError(w, http.StatusInternalServerError, "写入实例目录失败")
		return
	}

	s.log.Info("schematic installed",
		"instance", inst.Config().Name, "schematic", entry.ID, "path", target)
	writeJSON(w, http.StatusOK, installSchematicResponse{
		Path:    target,
		Command: "//schem load " + strings.TrimSuffix(entry.FileName, path.Ext(entry.FileName)),
	})
}

/* ------------------------------------------------------------------ 建筑市场 */

type schematicMarketResponse struct {
	Sources []schemlib.Source `json:"sources"`
	Items   []schemlib.Item   `json:"items"`
	// Held maps a market item's id to the library entry that came from it, so a
	// build already in the library says so instead of offering 入库 twice.
	Held      map[string]string `json:"held,omitempty"`
	Notes     map[string]string `json:"notes,omitempty"`
	FetchedAt time.Time         `json:"fetchedAt,omitempty"`
	// Total is how many builds the sources offer before the search narrowed
	// them, so "没有结果" can say what it is no results out of.
	Total int `json:"total"`
}

func (s *Server) handleBrowseSchematics(w http.ResponseWriter, r *http.Request) {
	if !s.schematicsAvailable(w) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), marketTimeout)
	defer cancel()

	query := r.URL.Query()
	catalogue := s.schemMarket.Read(ctx, query.Get("refresh") == "1")
	items := schemlib.Filter(catalogue.Items, query.Get("q"), query.Get("source"))

	held := map[string]string{}
	for _, entry := range s.schematics.List() {
		if entry.Origin.ItemID != "" {
			held[entry.Origin.ItemID] = entry.ID
		}
	}

	writeJSON(w, http.StatusOK, schematicMarketResponse{
		Sources:   catalogue.Sources,
		Items:     items,
		Held:      held,
		Notes:     catalogue.Notes,
		FetchedAt: catalogue.FetchedAt,
		Total:     len(catalogue.Items),
	})
}

type installMarketSchematicRequest struct {
	SourceID string `json:"sourceId"`
	ItemID   string `json:"itemId"`
}

// handleInstallMarketSchematic downloads one build into the library.
//
// Synchronous, unlike a plugin download, and that is a deliberate difference
// rather than an oversight: a plugin jar is tens of megabytes from a release
// CDN and a schematic is a few hundred kilobytes of gzipped NBT, so a queue,
// a job record and a progress socket would be three moving parts serving a
// request that finishes before the button finishes animating.
func (s *Server) handleInstallMarketSchematic(w http.ResponseWriter, r *http.Request) {
	if !s.schematicsAvailable(w) {
		return
	}

	var req installMarketSchematicRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), marketTimeout)
	defer cancel()

	entry, item, err := s.schemMarket.Install(ctx, s.schematics, req.SourceID, req.ItemID)
	if err != nil {
		s.writeSchemlibError(w, err)
		return
	}
	s.log.Info("schematic downloaded from market",
		"source", item.Source, "item", item.ID, "id", entry.ID, "size", entry.Size)
	writeJSON(w, http.StatusCreated, entry)
}

type schematicSourceRequest struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	URL  string `json:"url"`
	Note string `json:"note,omitempty"`
	// Disabled is a pointer so a rename does not silently switch a source back
	// on, and a switch does not clear a name the same form never sent.
	Disabled *bool `json:"disabled,omitempty"`
}

func (s *Server) handleAddSchematicSource(w http.ResponseWriter, r *http.Request) {
	if !s.schematicsAvailable(w) {
		return
	}
	var req schematicSourceRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	source, err := s.schemMarket.AddSource(req.Name, req.Kind, req.URL, req.Note)
	if err != nil {
		s.writeSchemlibError(w, err)
		return
	}
	s.log.Info("schematic source added", "id", source.ID, "kind", source.Kind, "url", source.URL)
	writeJSON(w, http.StatusCreated, source)
}

func (s *Server) handleUpdateSchematicSource(w http.ResponseWriter, r *http.Request) {
	if !s.schematicsAvailable(w) {
		return
	}
	var req schematicSourceRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	source, err := s.schemMarket.UpdateSource(r.PathValue("sourceId"), req.Name, req.Disabled)
	if err != nil {
		s.writeSchemlibError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, source)
}

func (s *Server) handleDeleteSchematicSource(w http.ResponseWriter, r *http.Request) {
	if !s.schematicsAvailable(w) {
		return
	}
	if err := s.schemMarket.RemoveSource(r.PathValue("sourceId")); err != nil {
		s.writeSchemlibError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

/* ------------------------------------------------------------------ errors */

// writeSchemlibError maps the library's and the market's sentinels onto HTTP
// statuses. The parser's own errors travel through writeSchematicError, which
// already answers in the panel's language; anything from here is about the
// library rather than about the file.
func (s *Server) writeSchemlibError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, schemlib.ErrNotFound), errors.Is(err, schemlib.ErrSourceNotFound),
		errors.Is(err, schemlib.ErrItemNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, schemlib.ErrInvalidID), errors.Is(err, schemlib.ErrBadSource):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, schemlib.ErrExists), errors.Is(err, schemlib.ErrBuiltinSource):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, schemlib.ErrTooLarge), errors.Is(err, schematic.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, friendlySchematicError(err))
	case errors.Is(err, schemlib.ErrNotSchematic), errors.Is(err, schematic.ErrUnsupported),
		errors.Is(err, schematic.ErrMalformed):
		writeError(w, http.StatusUnsupportedMediaType, friendlySchematicError(err))
	case errors.Is(err, schematic.ErrTooComplex):
		writeError(w, http.StatusUnprocessableEntity, friendlySchematicError(err))
	case errors.Is(err, schemlib.ErrDigestMismatch):
		// 502 rather than 500: the panel did its job and refused what it was
		// handed, and what went wrong is upstream of it.
		writeError(w, http.StatusBadGateway, err.Error())
	default:
		s.log.Error("schematic library request failed", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// friendlySchematicError turns a parser complaint into a sentence for an
// operator, keeping the technical one for the log. It is the per-file version
// of writeSchematicError, for the upload path where several files each have
// their own outcome.
func friendlySchematicError(err error) string {
	switch {
	case errors.Is(err, schematic.ErrUnsupported), errors.Is(err, schematic.ErrMalformed):
		return "这个文件不是能识别的 schematic：可能没下载完，或者是别的工具存的格式。"
	case errors.Is(err, schematic.ErrTooLarge), errors.Is(err, schemlib.ErrTooLarge):
		return "文件太大，超出了上限。"
	case errors.Is(err, schematic.ErrTooComplex):
		return "这个 schematic 太复杂，超出了解析的上限。"
	default:
		return err.Error()
	}
}
