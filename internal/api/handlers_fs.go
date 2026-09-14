package api

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/confighist"
	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/serverfiles"
)

// browserFor returns a file browser for one instance, confined to whatever the
// caller's role allows.
//
// Every file-manager route goes through this rather than building a browser of
// its own, which is what makes the directory rule apply to all of them at once
// — see TestFileRoutesUseTheConfinedBrowser.
func (s *Server) browserFor(r *http.Request, inst *instance.Instance) *serverfiles.Browser {
	who, _ := principalFrom(r.Context())
	return serverfiles.New(inst.Config().Directory).Restrict(s.accounts.PathsFor(who.user))
}

// unconfinedBrowser reaches an instance directory without the caller's
// directory rule applied.
//
// The rule narrows the file manager — 浏览与下载文件 and 编辑、上传与删除文件 —
// and nothing else, so everything that reads or writes a file under a different
// capability comes through here: server.properties and velocity.toml under
// 编辑服务器配置, the .schem files under 导入建筑, the launch script and the
// core jar under 启动设置与核心, the two ends of a proxy link under 代理连线.
// Confining those would not be a finer rule, it would be a different and wrong
// one — somebody granted 编辑服务器配置 is being told they may edit the config,
// which lives at the instance root whatever their folder rule says.
//
// Naming it rather than calling serverfiles.New inline is the point: an
// unconfined browser should be something a person wrote down and can be asked
// about. TestFileRoutesUseTheConfinedBrowser makes sure it stays that way.
func unconfinedBrowser(dir string) *serverfiles.Browser { return serverfiles.New(dir) }

// writeFileError maps serverfiles sentinels onto HTTP statuses.
func (s *Server) writeFileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, serverfiles.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, serverfiles.ErrInvalidPath), errors.Is(err, serverfiles.ErrIsDirectory):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, serverfiles.ErrExists):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, serverfiles.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, err.Error())
	default:
		s.log.Error("file operation failed", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

type listFilesResponse struct {
	Path    string              `json:"path"`
	Root    string              `json:"root"`
	Entries []serverfiles.Entry `json:"entries"`
	// MaxEditableBytes lets the UI explain why a large file has no edit button.
	MaxEditableBytes int64 `json:"maxEditableBytes"`
	MaxUploadBytes   int64 `json:"maxUploadBytes"`
	// Writable says whether this directory accepts writes under the caller's
	// directory rule. False for the folders a confined account can only walk
	// through on the way to the one it may edit — the UI hides 上传 and 新建
	// there rather than offering a button whose request is refused.
	Writable bool `json:"writable"`
	// Scope is the rule itself, so the page can say what it is confined to
	// instead of leaving somebody to infer it from a short listing. Empty for
	// an unconfined account, which is everybody until a role says otherwise.
	Scope []string `json:"scope"`
}

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}

	dir := r.URL.Query().Get("path")
	browser := s.browserFor(r, inst)
	entries, err := browser.List(dir)
	if err != nil {
		s.writeFileError(w, err)
		return
	}

	scope := browser.Scope()
	if scope == nil {
		scope = []string{}
	}
	writeJSON(w, http.StatusOK, listFilesResponse{
		Path:             strings.Trim(dir, "/"),
		Root:             inst.Config().Directory,
		Entries:          entries,
		Writable:         browser.Allows(dir),
		Scope:            scope,
		MaxEditableBytes: serverfiles.MaxEditableBytes(),
		MaxUploadBytes:   s.maxUploadBytes(),
	})
}

type fileContentResponse struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (s *Server) handleReadFile(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}

	target := r.URL.Query().Get("path")
	content, err := s.browserFor(r, inst).ReadText(target)
	if err != nil {
		s.writeFileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fileContentResponse{Path: target, Content: content})
}

type writeFileRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (s *Server) handleWriteFile(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}

	// A config file can legitimately be larger than the default JSON cap.
	r.Body = http.MaxBytesReader(w, r.Body, serverfiles.MaxEditableBytes()+64*1024)
	var req writeFileRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	if err := s.browserFor(r, inst).WriteText(req.Path, req.Content); err != nil {
		s.writeFileError(w, err)
		return
	}
	s.snapshotAfter(inst, confighist.TriggerUser, actorOf(r), "编辑 "+path.Base(req.Path))
	s.log.Info("file saved", "instance", inst.Config().Name, "path", req.Path)
	w.WriteHeader(http.StatusNoContent)
}

type pathRequest struct {
	Path string `json:"path"`
}

func (s *Server) handleMkdir(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}

	var req pathRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if err := s.browserFor(r, inst).Mkdir(req.Path); err != nil {
		s.writeFileError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type renameRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (s *Server) handleRenameFile(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}

	var req renameRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if err := s.browserFor(r, inst).Rename(req.From, req.To); err != nil {
		s.writeFileError(w, err)
		return
	}
	s.log.Info("file renamed", "instance", inst.Config().Name, "from", req.From, "to", req.To)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}

	target := r.URL.Query().Get("path")
	if err := s.browserFor(r, inst).Remove(target); err != nil {
		s.writeFileError(w, err)
		return
	}
	s.log.Info("file deleted", "instance", inst.Config().Name, "path", target)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}

	target := r.URL.Query().Get("path")
	file, info, closer, err := s.browserFor(r, inst).Open(target)
	if err != nil {
		s.writeFileError(w, err)
		return
	}
	defer closer()

	name := path.Base(info.Name())
	if kind, ok := previewType(name); ok && r.URL.Query().Get("inline") == "1" {
		w.Header().Set("Content-Type", kind)
		// The panel is asking the browser to render bytes an operator uploaded,
		// on the panel's own origin. nosniff is what keeps that to the one type
		// named here: without it a "png" full of markup can still be sniffed as
		// HTML and run with the session cookie attached.
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", "inline; filename*=UTF-8''"+url.PathEscape(name))
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", contentDisposition(name))
	}
	// ServeContent handles Range requests, so a dropped 300 MB world download
	// can resume instead of restarting.
	http.ServeContent(w, r, name, info.ModTime(), file)
}

// previewTypes are the files the panel will serve for display rather than for
// saving, so the file manager can show server-icon.png without a round trip
// through the operator's downloads folder.
//
// Raster only, and deliberately so. SVG is a document — it carries script — and
// an HTML file rendered inline on this origin would be running inside the
// panel's session. Everything not on this list stays an attachment.
var previewTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
	".ico":  "image/x-icon",
}

func previewType(name string) (string, bool) {
	kind, ok := previewTypes[strings.ToLower(path.Ext(name))]
	return kind, ok
}

// contentDisposition builds a header that survives non-ASCII filenames.
// The plain filename is a mangled fallback for old clients; filename* carries
// the real UTF-8 name per RFC 5987, which is what browsers actually use.
func contentDisposition(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 32 || r > 126 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)

	return fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s",
		ascii, url.PathEscape(name))
}

type uploadedFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// handleUploadFile streams a multipart upload straight to disk.
//
// Server jars and modpacks run to hundreds of megabytes, so nothing here
// buffers a whole file: each part is copied to its destination as it arrives.
func (s *Server) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}

	dir := r.URL.Query().Get("path")
	overwrite := r.URL.Query().Get("overwrite") == "true"
	browser := s.browserFor(r, inst)
	limit := s.maxUploadBytes()

	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "expected a multipart upload")
		return
	}

	uploaded := make([]uploadedFile, 0, 2)
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			s.writeFileError(w, err)
			return
		}
		if part.FileName() == "" {
			part.Close()
			continue
		}

		// Browsers send a bare name, but a crafted client could send a path.
		// Base() plus the os.Root jail means neither can place a file outside
		// the directory the user is looking at.
		name := path.Base(strings.ReplaceAll(part.FileName(), "\\", "/"))
		if name == "." || name == ".." || name == "/" {
			part.Close()
			writeError(w, http.StatusBadRequest, "invalid file name")
			return
		}
		target := name
		if cleanedDir := strings.Trim(dir, "/"); cleanedDir != "" {
			target = cleanedDir + "/" + name
		}

		written, err := s.storePart(browser, target, part, limit, overwrite)
		part.Close()
		if err != nil {
			s.writeFileError(w, err)
			return
		}
		uploaded = append(uploaded, uploadedFile{Name: name, Size: written})
	}

	if len(uploaded) == 0 {
		writeError(w, http.StatusBadRequest, "no files in the upload")
		return
	}
	s.log.Info("files uploaded", "instance", inst.Config().Name, "dir", dir, "count", len(uploaded))
	writeJSON(w, http.StatusCreated, map[string]any{"uploaded": uploaded})
}

// storePart writes one multipart part, cleaning up a partial file on failure
// so a cancelled upload does not leave a truncated jar behind.
func (s *Server) storePart(
	browser *serverfiles.Browser,
	target string,
	body io.Reader,
	limit int64,
	overwrite bool,
) (int64, error) {
	file, closer, err := browser.Create(target, overwrite)
	if err != nil {
		return 0, err
	}

	written, copyErr := serverfiles.CopyLimited(file, body, limit)
	closer()
	if copyErr != nil {
		if !overwrite {
			// Only clean up a file this upload created. On an overwrite the
			// original is already truncated, and deleting it would turn a
			// failed replacement into data loss.
			_ = browser.Remove(target)
		}
		return 0, copyErr
	}
	return written, nil
}

func (s *Server) maxUploadBytes() int64 {
	s.panelMu.RLock()
	defer s.panelMu.RUnlock()
	return int64(s.panel.MaxUploadMB) << 20
}

func init() {
	// Register the extensions the download handler is most likely to serve so
	// ServeContent does not have to guess from an empty system mime table.
	for ext, kind := range map[string]string{
		".jar":        "application/java-archive",
		".properties": "text/plain; charset=utf-8",
		".yml":        "text/yaml; charset=utf-8",
		".mcmeta":     "application/json",
	} {
		_ = mime.AddExtensionType(ext, kind)
	}
}

// The three routes the file index backs: 转到文件, the sidebar's search panel,
// and the disk figure under it. All three go through browserFor, so a confined
// role's ⌘P cannot turn up a path it may not open — see
// TestFileRoutesUseTheConfinedBrowser.

// findLimit caps what any of the index routes will return in one response.
// Fifty is already more than the palette shows; the ceiling exists so a
// hand-written query string cannot ask for the whole index.
const (
	findLimitDefault = 50
	findLimitMax     = 200
)

// indexLimit reads the caller's limit, or the default.
func indexLimit(r *http.Request) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return findLimitDefault
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return findLimitDefault
	}
	return min(n, findLimitMax)
}

// indexAll reads the "include the bulk directories" switch.
func indexAll(r *http.Request) bool {
	switch r.URL.Query().Get("all") {
	case "1", "true", "yes":
		return true
	}
	return false
}

func (s *Server) handleFindFiles(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	hits, err := s.browserFor(r, inst).Find(r.URL.Query().Get("q"), indexLimit(r), indexAll(r))
	if err != nil {
		s.writeFileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hits)
}

func (s *Server) handleSearchFiles(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	hits, err := s.browserFor(r, inst).Grep(r.URL.Query().Get("q"), indexLimit(r), indexAll(r))
	if err != nil {
		s.writeFileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hits)
}

type fileUsageResponse struct {
	Bytes int64 `json:"bytes"`
	Files int   `json:"files"`
	Dirs  int   `json:"dirs"`
	// DiskTotal is the filesystem the panel's data directory sits on, carried
	// here so the sidebar can draw "24.6 / 80 GB" from one request. It is the
	// same reading 主机 shows; asking a second endpoint for it would mean the
	// two could disagree by a poll interval on the same screen.
	DiskTotal uint64 `json:"diskTotal"`
}

func (s *Server) handleFileUsage(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	usage, err := s.browserFor(r, inst).Usage()
	if err != nil {
		s.writeFileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fileUsageResponse{
		Bytes:     usage.Bytes,
		Files:     usage.Files,
		Dirs:      usage.Dirs,
		DiskTotal: s.metrics.Disk().Total,
	})
}
