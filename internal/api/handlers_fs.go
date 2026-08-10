package api

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/serverfiles"
)

// browserFor returns a file browser scoped to an instance's directory.
func (s *Server) browserFor(inst *instance.Instance) *serverfiles.Browser {
	return serverfiles.New(inst.Config().Directory)
}

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
}

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}

	dir := r.URL.Query().Get("path")
	entries, err := s.browserFor(inst).List(dir)
	if err != nil {
		s.writeFileError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, listFilesResponse{
		Path:             strings.Trim(dir, "/"),
		Root:             inst.Config().Directory,
		Entries:          entries,
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
	content, err := s.browserFor(inst).ReadText(target)
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

	if err := s.browserFor(inst).WriteText(req.Path, req.Content); err != nil {
		s.writeFileError(w, err)
		return
	}
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
	if err := s.browserFor(inst).Mkdir(req.Path); err != nil {
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
	if err := s.browserFor(inst).Rename(req.From, req.To); err != nil {
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
	if err := s.browserFor(inst).Remove(target); err != nil {
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
	file, info, closer, err := s.browserFor(inst).Open(target)
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
	browser := s.browserFor(inst)
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
