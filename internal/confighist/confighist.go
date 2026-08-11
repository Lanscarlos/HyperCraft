// Package confighist keeps a Git history of one server's *configuration*.
//
// What it is: a timeline of the config. It answers "it started yesterday and
// does not today, what changed in between" and "that plugin upgrade wrote some
// keys of its own, which ones".
//
// What it is not, and the UI has to keep saying so: a backup. Worlds, player
// data and databases are outside the collection rules on purpose, and a real
// backup strategy has to exist alongside this. The feeling of having history
// must not be allowed to stand in for having backups.
//
// Also not: collaborative version control. There are no branches, no merges
// and — as a hard rule rather than a missing feature — no remotes. Server
// configuration holds the rcon password, bot tokens and database credentials;
// a panel that can push them somewhere is a panel that will one day push them
// somewhere wrong. See the design's §7. What is exposed is a timeline, a diff,
// and restoring one file. Git is the implementation, not the interface.
package confighist

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lanscarlos/hypercraft/internal/gitlite"
)

// Trigger is what caused a commit. It is the badge on the timeline, and the
// thing the default filter hides: an operator scrolling their history wants to
// see the edits and the upgrades, not two rows for every start and stop.
type Trigger string

const (
	TriggerLifecycle   Trigger = "lifecycle"
	TriggerTransaction Trigger = "transaction"
	TriggerUser        Trigger = "user"
	TriggerRestore     Trigger = "restore"
)

// The authors of automatic commits. A real operator's name goes in the same
// field, which is what lets one timeline show both.
const (
	ActorLifecycle   = "system:lifecycle"
	ActorTransaction = "system:plugin-txn"
)

// Commit trailers. Readable on purpose: an operator who runs `git log` against
// the repository by hand should be able to see everything the panel knows,
// without the panel's own sidecar files.
const (
	trailerTrigger = "HyperCraft-Trigger"
	trailerRunning = "HyperCraft-Running"
	trailerCore    = "HyperCraft-Core"
	trailerPlugin  = "HyperCraft-Plugin"
)

var (
	// ErrDisabled is returned for an instance the module is switched off for.
	ErrDisabled = errors.New("配置历史在这个实例上未启用")
	// ErrNoHistory is returned before the first commit exists.
	ErrNoHistory = errors.New("这个实例还没有配置历史")
	// ErrNotFound is returned for a commit or a path that is not in the
	// history.
	ErrNotFound = errors.New("配置历史里没有这个提交或文件")
	// ErrRunning blocks the operations that cannot be done under a live
	// server.
	ErrRunning = errors.New("服务器正在运行")
)

// Manifest is what was installed at the moment a commit was taken.
//
// Recorded so a restore can warn about the mismatch the design's §5.3 step 3 is
// about: putting 5.5.70's config back under 5.5.71's jar can stop a server
// booting, and nothing about clicking "还原 config.yml" suggests that it might.
type Manifest struct {
	Core    string
	Plugins []string // "<name> <version>", one per entry
}

// ManifestFunc supplies the manifest for an instance. Injected rather than
// imported: this package would otherwise have to know about the plugin ledger
// and the core library, neither of which it has any other business with.
type ManifestFunc func(instanceID string) Manifest

// Service owns every instance's config repository.
type Service struct {
	root         string // parent of <id>/config.git
	settingsPath string
	log          *slog.Logger

	// manifest is optional; without it a commit simply records no versions and
	// a restore skips the consistency warning.
	manifest ManifestFunc

	mu       sync.Mutex
	settings settingsFile
	// locks serialise everything touching one repository. Per instance rather
	// than global: a start on one server must not wait behind another server's
	// history being compacted.
	locks map[string]*sync.Mutex
	// stats memoises per-commit line counts. Commits are immutable, so an entry
	// is valid for the life of the process.
	stats map[string]CommitStats
}

func New(root, settingsPath string, logger *slog.Logger) *Service {
	return &Service{
		root:         root,
		settingsPath: settingsPath,
		log:          logger,
		locks:        map[string]*sync.Mutex{},
		stats:        map[string]CommitStats{},
	}
}

// SetManifest installs the version lookup. Called once at wiring time.
func (s *Service) SetManifest(fn ManifestFunc) { s.manifest = fn }

func (s *Service) repoDir(instanceID string) string {
	return filepath.Join(s.root, safeID(instanceID), "config.git")
}

// safeID keeps an instance id from naming a path outside the history root.
// Ids are hex today, so nothing here should ever fire; it is the cheap guard
// against that stopping being true.
func safeID(id string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_':
			return r
		}
		return '-'
	}, id)
	if clean == "" {
		return "unnamed"
	}
	return clean
}

// lockFor returns the mutex guarding one instance's repository.
func (s *Service) lockFor(instanceID string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()

	lock := s.locks[instanceID]
	if lock == nil {
		lock = &sync.Mutex{}
		s.locks[instanceID] = lock
	}
	return lock
}

// repo returns the instance's repository, initialising it if needed. The
// caller holds the instance lock.
func (s *Service) repo(instanceID, directory string) (*gitlite.Repo, error) {
	repo := gitlite.Open(s.repoDir(instanceID), directory)
	if err := repo.Init(); err != nil {
		return nil, err
	}
	return repo, nil
}

// openRepo returns an existing repository without creating one. Read paths use
// it so that merely opening the 配置历史 tab on an instance the module is off
// for does not leave a repository behind.
func (s *Service) openRepo(instanceID, directory string) (*gitlite.Repo, error) {
	repo := gitlite.Open(s.repoDir(instanceID), directory)
	if !repo.Exists() {
		return nil, ErrNoHistory
	}
	return repo, nil
}

// Enabled reports whether commits are being taken for an instance.
func (s *Service) Enabled(instanceID string) bool {
	return !s.Settings(instanceID).Disabled
}

// SetEnabled turns the module on or off for one instance.
func (s *Service) SetEnabled(instanceID string, on bool) error {
	_, err := s.UpdateSettings(instanceID, func(entry *InstanceSettings) {
		entry.Disabled = !on
	})
	return err
}

// ---------------------------------------------------------------- committing

// CommitRequest is one snapshot to take.
type CommitRequest struct {
	InstanceID string
	Directory  string
	Message    string
	Trigger    Trigger
	// Actor is the commit's author: an operator's name, or one of the
	// ActorLifecycle / ActorTransaction constants. The committer is always the
	// panel — see the design's §4.
	Actor string
	// Running marks a snapshot taken while the server is up. The panel never
	// takes one by itself in that state (the files are being written, so what
	// lands is a torn state), but an operator may ask for one, and the commit
	// carries the caveat so the timeline can show it.
	Running bool
}

// CommitResult says what happened. A skipped commit is a success: nothing
// changed is the common case for a start/stop pair, and the design says not to
// produce empty commits for it.
type CommitResult struct {
	Ref     string      `json:"ref,omitempty"`
	Skipped bool        `json:"skipped"`
	Reason  string      `json:"reason,omitempty"`
	Stats   CommitStats `json:"stats"`
}

// CommitStats is the "3 个文件 +47 −2" on a timeline row.
type CommitStats struct {
	Files      int `json:"files"`
	Insertions int `json:"insertions"`
	Deletions  int `json:"deletions"`
}

// Commit records the instance's current configuration.
//
// The whole design of *when* this is called lives in the callers, not here:
// snapshots are taken at semantic boundaries — before a start, after a stop,
// either side of a plugin transaction, on a save — rather than by watching the
// filesystem. A watcher would be easier and would ruin the feature: plugins
// rewrite their own YAML on every boot and shutdown, so the timeline would fill
// with commits nobody caused and nobody can read. See the design's §4.
func (s *Service) Commit(req CommitRequest) (CommitResult, error) {
	lock := s.lockFor(req.InstanceID)
	lock.Lock()
	defer lock.Unlock()

	settings := s.Settings(req.InstanceID)
	if settings.Disabled {
		return CommitResult{}, ErrDisabled
	}
	if _, err := os.Stat(req.Directory); err != nil {
		return CommitResult{Skipped: true, Reason: "实例目录不存在"}, nil
	}

	repo, err := s.repo(req.InstanceID, req.Directory)
	if err != nil {
		return CommitResult{}, err
	}

	plan, err := s.plan(repo, req.Directory, settings)
	if err != nil {
		return CommitResult{}, err
	}

	// Gates first, *before* the nothing-changed shortcut, and the order is the
	// whole point. A file over the size ceiling is left out of the tree, so it
	// contributes nothing to the change set — and if it is the only thing that
	// changed, checking "did anything change" first reports "没有配置变更" and
	// the operator is never told their file was refused. That is precisely the
	// silent skip the design's §2 forbids: they would believe it was recorded.
	//
	// Everything here also runs before a single object is written, so a blocked
	// commit leaves the repository exactly as it found it. Otherwise "中止"
	// would still grow the thing the gate exists to protect.
	if err := s.checkGates(repo, plan, settings); err != nil {
		return CommitResult{}, err
	}
	if len(plan.changed) == 0 && !plan.firstCommit {
		return CommitResult{Skipped: true, Reason: "没有配置变更"}, nil
	}

	ref, stats, err := s.write(repo, plan, req)
	if err != nil {
		return CommitResult{}, err
	}
	s.afterCommit(req.InstanceID, repo)

	s.log.Info("配置历史已记录",
		"instance", req.InstanceID, "trigger", req.Trigger,
		"files", stats.Files, "message", req.Message)
	return CommitResult{Ref: ref, Stats: stats}, nil
}

// commitPlan is a prepared commit: what the tree will hold, and which of it is
// new. Nothing here has been written to the repository yet.
type commitPlan struct {
	files       []gitlite.File
	changed     []string
	addedBytes  int64
	oversized   []OversizedFile
	parent      string
	parentTree  string
	firstCommit bool
	// pending holds the content of blobs that are not in the repository yet,
	// keyed by object id. Read once, written once the gates pass.
	pending map[string][]byte
}

// plan reads the instance directory and works out the commit without writing.
func (s *Service) plan(repo *gitlite.Repo, dir string, settings InstanceSettings) (commitPlan, error) {
	var plan commitPlan

	scan, err := Collect(dir, settings.Exclude)
	if err != nil {
		return plan, err
	}
	if scan.Truncated {
		return plan, &GateError{
			Kind:    GateTruncated,
			Limits:  settings.limits(),
			Message: "实例目录层级过深或文件过多，扫描没能走完，本次没有提交",
		}
	}

	head, err := repo.Head()
	if err != nil {
		return plan, err
	}
	plan.parent = head
	plan.firstCommit = head == ""
	if head != "" {
		commit, err := repo.ReadCommit(head)
		if err != nil {
			return plan, err
		}
		plan.parentTree = commit.Tree
	}

	known := map[string]gitlite.File{}
	if plan.parentTree != "" {
		files, err := repo.ListTree(plan.parentTree)
		if err != nil {
			return plan, err
		}
		for _, file := range files {
			known[file.Path] = file
		}
	}

	limits := settings.limits()
	plan.pending = map[string][]byte{}
	for _, candidate := range scan.Files {
		// Oversized files are measured, never read. Slurping a 2 GB file to
		// find out it is too big is the failure this ordering avoids.
		if candidate.Size > limits.FileBytes && !settings.allows(candidate.Path) {
			plan.oversized = append(plan.oversized, OversizedFile{Path: candidate.Path, Size: candidate.Size})
			continue
		}

		content, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(candidate.Path)))
		if err != nil {
			// A file that vanished between the scan and the read is normal on a
			// live server; it simply is not in this commit.
			continue
		}
		hash := gitlite.HashBlob(content)
		plan.files = append(plan.files, gitlite.File{Path: candidate.Path, Mode: candidate.Mode, Hash: hash})

		previous, seen := known[candidate.Path]
		if seen && previous.Hash == hash && previous.Mode == candidate.Mode {
			continue
		}
		plan.changed = append(plan.changed, candidate.Path)
		if !repo.Has(hash) {
			plan.pending[hash] = content
			plan.addedBytes += int64(len(content))
		}
	}

	// Deletions count as changes: a config file the operator removed is
	// something the timeline has to be able to show.
	present := make(map[string]bool, len(plan.files))
	for _, file := range plan.files {
		present[file.Path] = true
	}
	for path := range known {
		if !present[path] {
			plan.changed = append(plan.changed, path)
		}
	}
	return plan, nil
}

// write turns a planned commit into objects and moves the branch.
func (s *Service) write(repo *gitlite.Repo, plan commitPlan, req CommitRequest) (string, CommitStats, error) {
	for hash, content := range plan.pending {
		written, err := repo.WriteBlob(content)
		if err != nil {
			return "", CommitStats{}, err
		}
		if written != hash {
			return "", CommitStats{}, fmt.Errorf("blob id changed between planning and writing (%s vs %s)", hash, written)
		}
	}

	tree, err := repo.WriteTreeFromFiles(plan.files)
	if err != nil {
		return "", CommitStats{}, err
	}
	if tree == plan.parentTree {
		return "", CommitStats{}, nil
	}

	now := time.Now()
	var parents []string
	if plan.parent != "" {
		parents = []string{plan.parent}
	}
	ref, err := repo.WriteCommit(gitlite.CommitInput{
		Tree:      tree,
		Parents:   parents,
		Author:    gitlite.Signature{Name: actorOr(req.Actor), Email: "hypercraft@local", When: now},
		Committer: gitlite.Signature{Name: "HyperCraft", Email: "panel@hypercraft", When: now},
		Message:   s.message(req),
	})
	if err != nil {
		return "", CommitStats{}, err
	}
	if err := repo.SetHead(ref); err != nil {
		return "", CommitStats{}, err
	}

	stats, err := s.statsFor(repo, ref)
	if err != nil {
		s.log.Warn("配置历史统计失败", "instance", req.InstanceID, "err", err)
	}
	return ref, stats, nil
}

// message builds the commit message: the operator-visible subject, then the
// trailers the panel reads back.
func (s *Service) message(req CommitRequest) string {
	subject := strings.TrimSpace(req.Message)
	subject = strings.ReplaceAll(subject, "\r", "")
	if line, _, cut := strings.Cut(subject, "\n"); cut {
		subject = line
	}
	if subject == "" {
		subject = "快照"
	}

	var b strings.Builder
	b.WriteString(subject)
	b.WriteString("\n\n")
	b.WriteString(trailerTrigger + ": " + string(req.Trigger) + "\n")
	if req.Running {
		b.WriteString(trailerRunning + ": true\n")
	}
	if s.manifest != nil {
		manifest := s.manifest(req.InstanceID)
		if manifest.Core != "" {
			b.WriteString(trailerCore + ": " + sanitiseTrailer(manifest.Core) + "\n")
		}
		for _, entry := range manifest.Plugins {
			b.WriteString(trailerPlugin + ": " + sanitiseTrailer(entry) + "\n")
		}
	}
	return b.String()
}

func sanitiseTrailer(s string) string {
	return strings.TrimSpace(strings.NewReplacer("\n", " ", "\r", " ").Replace(s))
}

func actorOr(actor string) string {
	if strings.TrimSpace(actor) == "" {
		return "unknown"
	}
	return actor
}

// pruneEvery is how many commits pass before unreachable objects are swept.
// The design asks for `git gc` on this cadence; with a loose-object store and
// no packfile writer, sweeping the debris is the part of gc that applies. See
// Repo.Prune.
const pruneEvery = 100

// afterCommit runs the housekeeping a successful commit owes.
func (s *Service) afterCommit(instanceID string, repo *gitlite.Repo) {
	due := false
	if _, err := s.UpdateSettings(instanceID, func(entry *InstanceSettings) {
		entry.SincePrune++
		if entry.SincePrune >= pruneEvery {
			entry.SincePrune = 0
			due = true
		}
	}); err != nil {
		s.log.Warn("配置历史设置写入失败", "instance", instanceID, "err", err)
	}
	if !due {
		return
	}
	// Off the caller's thread: this runs behind a server start, and a start
	// must not wait on housekeeping.
	go func() {
		lock := s.lockFor(instanceID)
		lock.Lock()
		defer lock.Unlock()

		removed, err := repo.Prune()
		if err != nil {
			s.log.Warn("配置历史整理失败", "instance", instanceID, "err", err)
			return
		}
		if removed > 0 {
			s.log.Info("配置历史已清理无用对象", "instance", instanceID, "objects", removed)
		}
	}()
}
