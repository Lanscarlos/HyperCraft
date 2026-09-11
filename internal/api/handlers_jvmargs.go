package api

import (
	"net/http"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/confighist"
	"github.com/lanscarlos/hypercraft/internal/jvmargs"
)

// Once a script owns the command line the panel's memory fields build nothing:
// -Xmx goes wherever the script puts it, and for Forge and NeoForge that is
// user_jvm_args.txt, a file whose entire purpose is to be the one place an
// operator sets the heap without touching run.sh.
//
// So the memory control does not disappear in script mode — it moves. Same
// slider, different destination, and the destination is a real file the
// operator can also edit by hand, which is why every comment in it survives a
// save.

type jvmArgsResponse struct {
	// Exists is false when there is no such file, which is the normal state
	// for anything that is not Forge-shaped. The UI hides the control then
	// rather than offering to write a file nothing will read.
	Exists   bool   `json:"exists"`
	FileName string `json:"fileName"`
	// MinMemoryMB and MaxMemoryMB are what the file says, 0 for unset.
	MinMemoryMB int `json:"minMemoryMB"`
	MaxMemoryMB int `json:"maxMemoryMB"`
	// Args is everything in the file, so the page can show the flags it does
	// not offer a control for instead of implying there are none.
	Args []string `json:"args"`
}

func (s *Server) handleGetJVMArgs(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}

	out := jvmArgsResponse{FileName: jvmargs.FileName, Args: []string{}}
	// Not the file manager: user_jvm_args.txt is a launch setting that happens
	// to live in a file, and it answers to CapInstanceLaunch rather than to the
	// two file capabilities a directory rule narrows. A role confined to one
	// plugin's folder either holds that capability — in which case it decides
	// what the server runs and the folder rule is beside the point — or never
	// reaches this route at all.
	text, err := unconfinedBrowser(inst.Config().Directory).ReadText(jvmargs.FileName)
	if err != nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	file, err := jvmargs.Parse(strings.NewReader(text))
	if err != nil {
		writeJSON(w, http.StatusOK, out)
		return
	}

	out.Exists = true
	out.MinMemoryMB = file.MinMemoryMB()
	out.MaxMemoryMB = file.MaxMemoryMB()
	out.Args = file.Args()
	writeJSON(w, http.StatusOK, out)
}

type jvmArgsRequest struct {
	MinMemoryMB int `json:"minMemoryMB"`
	MaxMemoryMB int `json:"maxMemoryMB"`
}

func (s *Server) handlePutJVMArgs(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	var req jvmArgsRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if req.MinMemoryMB < 0 || req.MaxMemoryMB < 0 {
		writeError(w, http.StatusBadRequest, "内存不能是负数")
		return
	}
	if req.MinMemoryMB > 0 && req.MaxMemoryMB > 0 && req.MinMemoryMB > req.MaxMemoryMB {
		writeError(w, http.StatusBadRequest, "最小内存不能大于最大内存")
		return
	}

	browser := unconfinedBrowser(inst.Config().Directory)
	text, err := browser.ReadText(jvmargs.FileName)
	if err != nil {
		// Creating it would not help: only a launcher that passes
		// @user_jvm_args.txt reads this file, and one that does cannot start
		// without it — so an absent file means this server is not that shape.
		writeError(w, http.StatusConflict, "这个实例目录里没有 "+jvmargs.FileName+"，Forge / NeoForge 的服务端才有这个文件")
		return
	}
	file, err := jvmargs.Parse(strings.NewReader(text))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读不了 "+jvmargs.FileName)
		return
	}

	file.SetMinMemoryMB(req.MinMemoryMB)
	file.SetMaxMemoryMB(req.MaxMemoryMB)
	if err := browser.WriteText(jvmargs.FileName, string(file.Render())); err != nil {
		s.writeFileError(w, err)
		return
	}
	s.snapshotAfter(inst, confighist.TriggerUser, actorOf(r), "编辑 "+jvmargs.FileName)

	writeJSON(w, http.StatusOK, jvmArgsResponse{
		Exists:      true,
		FileName:    jvmargs.FileName,
		MinMemoryMB: file.MinMemoryMB(),
		MaxMemoryMB: file.MaxMemoryMB(),
		Args:        file.Args(),
	})
}
