package confighist

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lanscarlos/hypercraft/internal/gitlite"
)

// Restoring is where this module can actually break a server.
//
// Reading history is safe whatever it does. Writing files back is not, and two
// decisions carry most of the safety:
//
//  1. A restore is a new commit holding old content — never a reset, a revert,
//     or any other rewrite. The history is append-only, so "the restore made it
//     worse" always has a way back. See the design's §5.1.
//  2. One file is the default, the whole tree is the advanced operation.
//     "还原配置" in an operator's head means "put back the file I broke", not
//     "move the whole directory back three days" — and the second one deletes
//     everything added since, which is almost never what was wanted.

// RestorePlan is the preview shown before anything is written.
type RestorePlan struct {
	Ref     string    `json:"ref"`
	Short   string    `json:"short"`
	At      time.Time `json:"at"`
	Message string    `json:"message"`
	// Whole is true for a tree restore, false for the single-file default.
	Whole bool   `json:"whole"`
	Path  string `json:"path,omitempty"`
	// Changes is what would land on disk. Empty means there is nothing to do.
	Changes []FileChange `json:"changes"`
	// Removals are the tracked files a tree restore would delete because they
	// did not exist at the target commit. Listed separately: this is the part
	// of a tree restore people do not expect.
	Removals []string `json:"removals,omitempty"`
	// Mismatch is the plugin/core drift between then and now, and it is the
	// reason a restore asks twice. Config from 5.5.70 under 5.5.71's jar can
	// stop a server booting, and nothing about the click suggests it might.
	Mismatch *Mismatch `json:"mismatch,omitempty"`
	// BlockedBy is set when the restore cannot proceed as asked — a tree
	// restore on a running server, mostly.
	BlockedBy string `json:"blockedBy,omitempty"`
	// Warning is advice rather than a refusal: restoring one file under a live
	// server works, it just does not take effect until a restart.
	Warning string `json:"warning,omitempty"`
}

// Mismatch lists what was installed then and is not now, or the reverse.
type Mismatch struct {
	CoreThen string   `json:"coreThen,omitempty"`
	CoreNow  string   `json:"coreNow,omitempty"`
	Plugins  []string `json:"plugins,omitempty"`
}

// RestoreRequest asks for a restore, or for the preview of one.
type RestoreRequest struct {
	InstanceID string
	Directory  string
	Ref        string
	// Path empty means the whole tree, which is the advanced operation.
	Path    string
	Actor   string
	Running bool
	// Confirmed acknowledges the version mismatch. Without it a restore with a
	// mismatch is refused rather than merely warned about.
	Confirmed bool
}

// PlanRestore builds the preview. It writes nothing.
func (s *Service) PlanRestore(req RestoreRequest) (RestorePlan, error) {
	lock := s.lockFor(req.InstanceID)
	lock.Lock()
	defer lock.Unlock()
	return s.planRestore(req)
}

func (s *Service) planRestore(req RestoreRequest) (RestorePlan, error) {
	repo, err := s.openRepo(req.InstanceID, req.Directory)
	if err != nil {
		return RestorePlan{}, err
	}
	commit, err := s.resolve(repo, req.Ref)
	if err != nil {
		return RestorePlan{}, err
	}

	target, err := repo.ListTree(commit.Tree)
	if err != nil {
		return RestorePlan{}, err
	}

	snapshot := describe(commit)
	plan := RestorePlan{
		Ref:     commit.Hash,
		Short:   commit.Hash[:8],
		At:      snapshot.At,
		Message: snapshot.Message,
		Whole:   strings.TrimSpace(req.Path) == "",
		Path:    normalisePath(req.Path),
	}

	// Step 1 of the design's §5.3: what the server's state allows.
	if plan.Whole && req.Running {
		plan.BlockedBy = "整树还原要求服务器处于停止状态"
	} else if req.Running {
		plan.Warning = "服务器正在运行，还原后需要重启才会生效；插件也可能在关服时把文件写回去"
	}

	wanted := target
	if !plan.Whole {
		wanted = nil
		for _, file := range target {
			if file.Path == plan.Path {
				wanted = []gitlite.File{file}
				break
			}
		}
		if wanted == nil {
			return RestorePlan{}, fmt.Errorf("%w: %s", ErrNotFound, plan.Path)
		}
	}

	for _, file := range wanted {
		recorded, err := repo.ReadBlob(file.Hash)
		if err != nil {
			return RestorePlan{}, err
		}
		current, readErr := os.ReadFile(filepath.Join(req.Directory, filepath.FromSlash(file.Path)))
		if readErr == nil && string(current) == string(recorded) {
			continue
		}
		status := "modified"
		if readErr != nil {
			status = "added"
			current = nil
		}
		diff := diffText(file.Path, current, recorded, status)
		plan.Changes = append(plan.Changes, FileChange{
			Path: file.Path, Status: status,
			Insertions: diff.Insertions, Deletions: diff.Deletions, Binary: diff.Binary,
		})
	}

	if plan.Whole {
		removals, err := s.removalsFor(repo, target)
		if err != nil {
			return RestorePlan{}, err
		}
		plan.Removals = removals
	}
	sort.Slice(plan.Changes, func(a, b int) bool { return plan.Changes[a].Path < plan.Changes[b].Path })

	plan.Mismatch = s.mismatch(req.InstanceID, snapshot)
	return plan, nil
}

// removalsFor lists the currently *tracked* files that the target commit does
// not hold. Only tracked ones: a tree restore must never delete a file the
// history was not recording, whatever it is.
func (s *Service) removalsFor(repo *gitlite.Repo, target []gitlite.File) ([]string, error) {
	head, err := repo.Head()
	if err != nil || head == "" {
		return nil, err
	}
	commit, err := repo.ReadCommit(head)
	if err != nil {
		return nil, err
	}
	current, err := repo.ListTree(commit.Tree)
	if err != nil {
		return nil, err
	}

	wanted := make(map[string]bool, len(target))
	for _, file := range target {
		wanted[file.Path] = true
	}
	var out []string
	for _, file := range current {
		if !wanted[file.Path] {
			out = append(out, file.Path)
		}
	}
	sort.Strings(out)
	return out, nil
}

// mismatch compares the versions recorded on a commit with what is installed
// now. Step 3 of the design's §5.3.
func (s *Service) mismatch(instanceID string, snapshot Snapshot) *Mismatch {
	if s.manifest == nil {
		return nil
	}
	now := s.manifest(instanceID)

	out := &Mismatch{}
	if snapshot.Core != "" && now.Core != "" && snapshot.Core != now.Core {
		out.CoreThen, out.CoreNow = snapshot.Core, now.Core
	}

	then := versionMap(snapshot.Plugins)
	current := versionMap(now.Plugins)
	for name, thenVersion := range then {
		switch nowVersion, installed := current[name]; {
		case !installed:
			out.Plugins = append(out.Plugins, fmt.Sprintf("%s：当时 %s，现在已卸载", name, thenVersion))
		case nowVersion != thenVersion:
			out.Plugins = append(out.Plugins, fmt.Sprintf("%s：当时 %s，现在 %s", name, thenVersion, nowVersion))
		}
	}
	for name, nowVersion := range current {
		if _, existed := then[name]; !existed {
			out.Plugins = append(out.Plugins, fmt.Sprintf("%s：当时未安装，现在 %s", name, nowVersion))
		}
	}
	sort.Strings(out.Plugins)

	if out.CoreThen == "" && len(out.Plugins) == 0 {
		return nil
	}
	return out
}

func versionMap(entries []string) map[string]string {
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		name, version, ok := strings.Cut(strings.TrimSpace(entry), " ")
		if !ok {
			name, version = entry, ""
		}
		out[name] = version
	}
	return out
}

// Restore writes the recorded content back and records a new commit for it.
//
// Three phases, each taking the instance lock on its own rather than holding
// it across all of them: snapshot what is there now, write the old content
// back, commit the result. The two commits go through the ordinary Commit path
// — gates and all — because a restore is just another change to the
// configuration and earns no exemption from the thing that keeps the
// repository small.
func (s *Service) Restore(req RestoreRequest) (CommitResult, RestorePlan, error) {
	plan, err := s.PlanRestore(req)
	if err != nil {
		return CommitResult{}, RestorePlan{}, err
	}
	if plan.BlockedBy != "" {
		return CommitResult{}, plan, fmt.Errorf("%w：%s", ErrRunning, plan.BlockedBy)
	}
	if plan.Mismatch != nil && !req.Confirmed {
		return CommitResult{}, plan, errVersionMismatch
	}
	if len(plan.Changes) == 0 && len(plan.Removals) == 0 {
		return CommitResult{Skipped: true, Reason: "当前内容与该版本一致，无需还原"}, plan, nil
	}

	// Step 2 of §5.3: whatever is on disk right now gets its own commit before
	// anything is overwritten. This is what makes an unwanted restore a
	// two-click round trip instead of a loss — and a gate tripping here stops
	// the restore, because a restore whose undo was never recorded is exactly
	// the operation this module must not perform.
	if _, err := s.Commit(CommitRequest{
		InstanceID: req.InstanceID,
		Directory:  req.Directory,
		Message:    "还原前自动快照",
		Trigger:    TriggerRestore,
		Actor:      req.Actor,
		Running:    req.Running,
	}); err != nil {
		return CommitResult{}, plan, fmt.Errorf("还原前快照没能提交，没有动任何文件：%w", err)
	}

	if err := s.writeBack(req, plan); err != nil {
		return CommitResult{}, plan, err
	}

	message := fmt.Sprintf("还原 %s 至 %s", plan.Path, plan.At.Format("2006-01-02 15:04"))
	if plan.Whole {
		message = fmt.Sprintf("整树还原至 %s（%s）", plan.At.Format("2006-01-02 15:04"), plan.Short)
	}
	result, err := s.Commit(CommitRequest{
		InstanceID: req.InstanceID,
		Directory:  req.Directory,
		Message:    message,
		Trigger:    TriggerRestore,
		Actor:      req.Actor,
		Running:    req.Running,
	})
	return result, plan, err
}

// errVersionMismatch is what the UI turns into the second confirmation.
var errVersionMismatch = errors.New("还原目标与当前的插件 / 核心版本不一致，确认后才能继续")

// IsVersionMismatch reports the refusal a second click can override.
func IsVersionMismatch(err error) bool { return errors.Is(err, errVersionMismatch) }

// Snapshot records the configuration inside a plugin transaction and returns
// the commit. It satisfies plugin.ConfigHistory, which is why the signature is
// shaped the way it is rather than taking a CommitRequest.
//
// An instance with the module switched off, or one whose configuration has not
// changed, gives an empty ref and no error: the transaction carries on and
// falls back to copying the plugin's config directory.
func (s *Service) Snapshot(instanceID, directory, message, actor string) (string, error) {
	if strings.TrimSpace(actor) == "" {
		actor = ActorTransaction
	}
	result, err := s.Commit(CommitRequest{
		InstanceID: instanceID,
		Directory:  directory,
		Message:    message,
		Trigger:    TriggerTransaction,
		Actor:      actor,
	})
	switch {
	case errors.Is(err, ErrDisabled):
		return "", nil
	case err != nil:
		return "", err
	}
	return result.Ref, nil
}

// RestoreSubtree writes one recorded directory back onto disk. It does not
// commit: the caller is a plugin rollback, which brackets the whole operation
// with its own snapshots, and a third commit in the middle would put a row on
// the timeline for a state that existed for a hundred milliseconds.
func (s *Service) RestoreSubtree(instanceID, directory, ref, prefix, actor string) error {
	lock := s.lockFor(instanceID)
	lock.Lock()
	defer lock.Unlock()

	repo, err := s.openRepo(instanceID, directory)
	if err != nil {
		return err
	}
	commit, err := s.resolve(repo, ref)
	if err != nil {
		return err
	}
	files, err := repo.ListTree(commit.Tree)
	if err != nil {
		return err
	}

	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()

	under := normalisePath(prefix) + "/"
	written := 0
	for _, file := range files {
		if !strings.HasPrefix(file.Path, under) {
			continue
		}
		content, err := repo.ReadBlob(file.Hash)
		if err != nil {
			return err
		}
		if parent := path.Dir(file.Path); parent != "." {
			if err := root.MkdirAll(parent, 0o755); err != nil {
				return err
			}
		}
		perm := os.FileMode(0o644)
		if file.Mode == gitlite.ModeExec {
			perm = 0o755
		}
		if err := root.WriteFile(file.Path, content, perm); err != nil {
			return fmt.Errorf("写回 %s 失败：%w", file.Path, err)
		}
		written++
	}
	if written == 0 {
		return fmt.Errorf("%w：%s 在该快照里没有记录到任何配置文件", ErrNotFound, prefix)
	}
	return nil
}

func (s *Service) writeBack(req RestoreRequest, plan RestorePlan) error {
	lock := s.lockFor(req.InstanceID)
	lock.Lock()
	defer lock.Unlock()

	repo, err := s.openRepo(req.InstanceID, req.Directory)
	if err != nil {
		return err
	}
	commit, err := s.resolve(repo, req.Ref)
	if err != nil {
		return err
	}
	return s.applyRestore(repo, commit, req, plan)
}

// applyRestore writes the target content into the instance directory. Every
// write goes through os.Root, so a path out of the repository cannot name
// anything outside the instance — the tree was built by this package, but a
// restore is the one place where a bad path would be executed rather than
// merely displayed.
func (s *Service) applyRestore(repo *gitlite.Repo, commit *gitlite.Commit, req RestoreRequest, plan RestorePlan) error {
	root, err := os.OpenRoot(req.Directory)
	if err != nil {
		return err
	}
	defer root.Close()

	files, err := repo.ListTree(commit.Tree)
	if err != nil {
		return err
	}
	for _, file := range files {
		if !plan.Whole && file.Path != plan.Path {
			continue
		}
		content, err := repo.ReadBlob(file.Hash)
		if err != nil {
			return err
		}
		if parent := path.Dir(file.Path); parent != "." {
			if err := root.MkdirAll(parent, 0o755); err != nil {
				return err
			}
		}
		perm := os.FileMode(0o644)
		if file.Mode == gitlite.ModeExec {
			perm = 0o755
		}
		if err := root.WriteFile(file.Path, content, perm); err != nil {
			return fmt.Errorf("写回 %s 失败：%w", file.Path, err)
		}
	}

	for _, removed := range plan.Removals {
		if err := root.Remove(removed); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("删除 %s 失败：%w", removed, err)
		}
	}
	return nil
}
