package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/javaruntime"
)

type registerJavaRequest struct {
	Path string `json:"path"`
	// Detected marks a registration the panel proposed off PATH and the
	// operator accepted, as opposed to one they typed. Only a label.
	Detected bool `json:"detected"`
}

// deleteJavaResponse names who was pointing at the entry that just went away,
// so the page can say so rather than leaving it to be discovered at start-up.
type deleteJavaResponse struct {
	UsedBy []string `json:"usedBy"`
}

// handleRegisterJava records a Java the operator pointed the panel at.
//
// The path is probed before it is written: an entry whose version nobody read
// is an entry the launch check cannot reason about, and the whole point of the
// registry is that every option in the dropdown carries a major version.
func (s *Server) handleRegisterJava(w http.ResponseWriter, r *http.Request) {
	if !s.javaAvailable(w) {
		return
	}

	var req registerJavaRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		writeError(w, http.StatusBadRequest, "要登记的 Java 路径不能为空")
		return
	}

	probed, ok := javaruntime.Probe(r.Context(), path)
	if !ok {
		writeError(w, http.StatusBadRequest,
			"这个路径问不出 Java 版本：确认它指向 java 可执行文件本身（不是它所在的目录），"+
				"并且面板运行的账号有权执行它。")
		return
	}

	addedBy := javaruntime.AddedManual
	if req.Detected {
		addedBy = javaruntime.AddedDetected
	}
	entry, err := s.java.Registry().Add(javaruntime.Entry{
		JavaPath: path,
		Vendor:   probed.Vendor,
		Version:  probed.Version,
		Major:    probed.Major,
		AddedBy:  addedBy,
	})
	if err != nil {
		if errors.Is(err, javaruntime.ErrDuplicate) {
			writeError(w, http.StatusConflict, "这个 Java 已经登记过了")
			return
		}
		s.writeJavaError(w, err)
		return
	}

	s.log.Info("java registered", "path", entry.JavaPath, "major", entry.Major, "addedBy", entry.AddedBy)
	writeJSON(w, http.StatusCreated, entry)
}

// handleUnregisterJava drops a registered path.
//
// Same rule as deleting an installed runtime (see handleDeleteJava): refused
// while a server is running on it, allowed when the instances pointing at it
// are stopped. Those instances keep launching — the launch path reads the
// config, not this list — and the page is told which ones they were.
func (s *Server) handleUnregisterJava(w http.ResponseWriter, r *http.Request) {
	if !s.javaAvailable(w) {
		return
	}

	id := r.PathValue("id")
	entry, err := s.java.Registry().Get(id)
	if err != nil {
		s.writeJavaError(w, err)
		return
	}

	view := javaruntime.Available{
		ID: entry.ID, JavaPath: entry.JavaPath, Origin: javaruntime.OriginExternal,
	}
	// Every instance, not just the visible ones: a server running on this Java
	// breaks whether or not the caller can see it. See allInstances.
	used := []string{}
	for _, inst := range usersOf(s.allInstances(), view) {
		if inst.State().Running() {
			writeError(w, http.StatusConflict,
				"实例「"+inst.Config().Name+"」正在用这个 Java 运行，先停掉它再删除")
			return
		}
		used = append(used, inst.Config().Name)
	}

	if err := s.java.Registry().Remove(id); err != nil {
		s.writeJavaError(w, err)
		return
	}
	s.log.Info("java unregistered", "path", entry.JavaPath, "usedBy", len(used))
	writeJSON(w, http.StatusOK, deleteJavaResponse{UsedBy: used})
}

// handleProbeJava re-reads the version of a registered path, for a JDK that
// was upgraded in place behind the panel's back.
func (s *Server) handleProbeJava(w http.ResponseWriter, r *http.Request) {
	if !s.javaAvailable(w) {
		return
	}

	id := r.PathValue("id")
	entry, err := s.java.Registry().Get(id)
	if err != nil {
		s.writeJavaError(w, err)
		return
	}

	probed, ok := javaruntime.Probe(r.Context(), entry.JavaPath)
	if !ok {
		writeError(w, http.StatusBadRequest,
			"这个路径现在问不出 Java 版本，可能已经被卸载或移动了。")
		return
	}
	entry.Vendor, entry.Version, entry.Major = probed.Vendor, probed.Version, probed.Major

	updated, err := s.java.Registry().Replace(id, entry)
	if err != nil {
		s.writeJavaError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
