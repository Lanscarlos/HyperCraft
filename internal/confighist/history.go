package confighist

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lanscarlos/hypercraft/internal/gitlite"
)

// Snapshot is one row of the timeline.
type Snapshot struct {
	Ref     string      `json:"ref"`
	Short   string      `json:"short"`
	At      time.Time   `json:"at"`
	Message string      `json:"message"`
	Trigger Trigger     `json:"trigger"`
	Author  string      `json:"author"`
	Running bool        `json:"running,omitempty"`
	Stats   CommitStats `json:"stats"`
	// Core and Plugins are what was installed when this was taken; a restore
	// compares them against now. Empty on a commit written before a manifest
	// was available.
	Core    string   `json:"core,omitempty"`
	Plugins []string `json:"plugins,omitempty"`
}

// FileChange is one path in a commit, as the change list shows it.
type FileChange struct {
	Path       string `json:"path"`
	Status     string `json:"status"` // added | modified | deleted
	Insertions int    `json:"insertions"`
	Deletions  int    `json:"deletions"`
	Binary     bool   `json:"binary,omitempty"`
}

// Stats is the footer of the tab: what the history costs and when it was last
// tidied.
type Stats struct {
	Commits   int   `json:"commits"`
	RepoBytes int64 `json:"repoBytes"`
	Files     int   `json:"files"`
	// A pointer, so "never compacted" is absent from the JSON rather than the
	// zero time. omitempty does not drop a zero time.Time — a struct is never
	// empty to encoding/json — and the UI would have shown "上次整理" followed
	// by the year 1.
	CompactedAt *time.Time `json:"compactedAt,omitempty"`
}

// History returns the timeline, newest first. limit <= 0 means everything.
func (s *Service) History(instanceID, directory string, limit int) ([]Snapshot, error) {
	lock := s.lockFor(instanceID)
	lock.Lock()
	defer lock.Unlock()

	repo, err := s.openRepo(instanceID, directory)
	if err != nil {
		return nil, err
	}
	head, err := repo.Head()
	if err != nil {
		return nil, err
	}
	if head == "" {
		return nil, nil
	}

	commits, err := repo.Log(head, limit)
	if err != nil {
		return nil, err
	}

	out := make([]Snapshot, 0, len(commits))
	for _, commit := range commits {
		snapshot := describe(commit)
		stats, err := s.statsFor(repo, commit.Hash)
		if err != nil {
			return nil, err
		}
		snapshot.Stats = stats
		out = append(out, snapshot)
	}
	return out, nil
}

// describe reads a commit's message and trailers into a timeline row.
func describe(commit *gitlite.Commit) Snapshot {
	subject, body, _ := strings.Cut(commit.Message, "\n")
	snapshot := Snapshot{
		Ref:     commit.Hash,
		Short:   commit.Hash[:8],
		At:      commit.Committer.When,
		Message: strings.TrimSpace(subject),
		Author:  commit.Author.Name,
		Trigger: TriggerUser,
	}
	if snapshot.At.IsZero() {
		snapshot.At = commit.Author.When
	}

	for _, line := range strings.Split(body, "\n") {
		key, value, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case trailerTrigger:
			snapshot.Trigger = Trigger(value)
		case trailerRunning:
			snapshot.Running = value == "true"
		case trailerCore:
			snapshot.Core = value
		case trailerPlugin:
			snapshot.Plugins = append(snapshot.Plugins, value)
		}
	}
	return snapshot
}

// statsFor counts what one commit changed against its parent. Memoised:
// commits are immutable, and the timeline asks for the same numbers on every
// page load.
func (s *Service) statsFor(repo *gitlite.Repo, ref string) (CommitStats, error) {
	s.mu.Lock()
	cached, ok := s.stats[ref]
	s.mu.Unlock()
	if ok {
		return cached, nil
	}

	commit, err := repo.ReadCommit(ref)
	if err != nil {
		return CommitStats{}, err
	}
	changes, err := s.changesFor(repo, commit)
	if err != nil {
		return CommitStats{}, err
	}
	stats := CommitStats{Files: len(changes)}
	for _, change := range changes {
		stats.Insertions += change.Insertions
		stats.Deletions += change.Deletions
	}

	s.mu.Lock()
	s.stats[ref] = stats
	s.mu.Unlock()
	return stats, nil
}

// Changes lists the paths one commit touched.
func (s *Service) Changes(instanceID, directory, ref string) ([]FileChange, error) {
	lock := s.lockFor(instanceID)
	lock.Lock()
	defer lock.Unlock()

	repo, err := s.openRepo(instanceID, directory)
	if err != nil {
		return nil, err
	}
	commit, err := s.resolve(repo, ref)
	if err != nil {
		return nil, err
	}
	return s.changesFor(repo, commit)
}

func (s *Service) changesFor(repo *gitlite.Repo, commit *gitlite.Commit) ([]FileChange, error) {
	parentTree := ""
	if len(commit.Parents) > 0 {
		parent, err := repo.ReadCommit(commit.Parents[0])
		if err != nil {
			return nil, err
		}
		parentTree = parent.Tree
	} else {
		// The first commit has no parent, so it is compared against the empty
		// tree — which is written here rather than hard-coded so the repository
		// definitely holds it.
		empty, err := repo.WriteTree(nil)
		if err != nil {
			return nil, err
		}
		parentTree = empty
	}

	raw, err := repo.DiffTrees(parentTree, commit.Tree)
	if err != nil {
		return nil, err
	}

	out := make([]FileChange, 0, len(raw))
	for _, change := range raw {
		entry := FileChange{Path: change.Path, Status: statusOf(change)}
		old, next, err := readSides(repo, change)
		if err != nil {
			return nil, err
		}
		diff := diffText(change.Path, old, next, entry.Status)
		entry.Insertions, entry.Deletions, entry.Binary = diff.Insertions, diff.Deletions, diff.Binary
		out = append(out, entry)
	}
	return out, nil
}

func statusOf(change gitlite.Change) string {
	switch {
	case change.Old == nil:
		return "added"
	case change.New == nil:
		return "deleted"
	default:
		return "modified"
	}
}

func readSides(repo *gitlite.Repo, change gitlite.Change) (old, next []byte, err error) {
	if change.Old != nil {
		if old, err = repo.ReadBlob(change.Old.Hash); err != nil {
			return nil, nil, err
		}
	}
	if change.New != nil {
		if next, err = repo.ReadBlob(change.New.Hash); err != nil {
			return nil, nil, err
		}
	}
	return old, next, nil
}

// Diff renders one file's change inside one commit.
func (s *Service) Diff(instanceID, directory, ref, path string) (FileDiff, error) {
	lock := s.lockFor(instanceID)
	lock.Lock()
	defer lock.Unlock()

	repo, err := s.openRepo(instanceID, directory)
	if err != nil {
		return FileDiff{}, err
	}
	commit, err := s.resolve(repo, ref)
	if err != nil {
		return FileDiff{}, err
	}

	clean := normalisePath(path)
	next, hasNext, err := blobAt(repo, commit.Tree, clean)
	if err != nil {
		return FileDiff{}, err
	}

	var old []byte
	hasOld := false
	if len(commit.Parents) > 0 {
		parent, err := repo.ReadCommit(commit.Parents[0])
		if err != nil {
			return FileDiff{}, err
		}
		if old, hasOld, err = blobAt(repo, parent.Tree, clean); err != nil {
			return FileDiff{}, err
		}
	}
	if !hasOld && !hasNext {
		return FileDiff{}, ErrNotFound
	}
	return diffText(clean, old, next, sideStatus(hasOld, hasNext)), nil
}

// DiffAgainstCurrent compares a recorded version with what is on disk now. It
// is the "与当前对比" on each file row, and the one diff an operator reaches
// for while a server is misbehaving.
func (s *Service) DiffAgainstCurrent(instanceID, directory, ref, path string) (FileDiff, error) {
	lock := s.lockFor(instanceID)
	lock.Lock()
	defer lock.Unlock()

	repo, err := s.openRepo(instanceID, directory)
	if err != nil {
		return FileDiff{}, err
	}
	commit, err := s.resolve(repo, ref)
	if err != nil {
		return FileDiff{}, err
	}

	clean := normalisePath(path)
	old, hasOld, err := blobAt(repo, commit.Tree, clean)
	if err != nil {
		return FileDiff{}, err
	}

	next, readErr := os.ReadFile(filepath.Join(directory, filepath.FromSlash(clean)))
	hasNext := readErr == nil
	if !hasOld && !hasNext {
		return FileDiff{}, ErrNotFound
	}
	return diffText(clean, old, next, sideStatus(hasOld, hasNext)), nil
}

func sideStatus(hasOld, hasNext bool) string {
	switch {
	case !hasOld:
		return "added"
	case !hasNext:
		return "deleted"
	default:
		return "modified"
	}
}

// FileAt returns one file's recorded content. This is the single-file export
// the design allows; there is deliberately no way to download the repository.
func (s *Service) FileAt(instanceID, directory, ref, path string) ([]byte, error) {
	lock := s.lockFor(instanceID)
	lock.Lock()
	defer lock.Unlock()

	repo, err := s.openRepo(instanceID, directory)
	if err != nil {
		return nil, err
	}
	commit, err := s.resolve(repo, ref)
	if err != nil {
		return nil, err
	}
	content, ok, err := blobAt(repo, commit.Tree, normalisePath(path))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotFound
	}
	return content, nil
}

func blobAt(repo *gitlite.Repo, tree, path string) ([]byte, bool, error) {
	file, ok, err := repo.FindInTree(tree, path)
	if err != nil || !ok {
		return nil, false, err
	}
	content, err := repo.ReadBlob(file.Hash)
	if err != nil {
		return nil, false, err
	}
	return content, true, nil
}

// Pending is what has changed on disk since the last snapshot: the answer to
// "is what I am looking at recorded". Empty means the history is up to date.
func (s *Service) Pending(instanceID, directory string) ([]FileChange, error) {
	lock := s.lockFor(instanceID)
	lock.Lock()
	defer lock.Unlock()

	repo, err := s.openRepo(instanceID, directory)
	if err != nil {
		return nil, err
	}
	head, err := repo.Head()
	if err != nil || head == "" {
		return nil, err
	}
	commit, err := repo.ReadCommit(head)
	if err != nil {
		return nil, err
	}

	settings := s.Settings(instanceID)
	scan, err := Collect(directory, settings.Exclude)
	if err != nil {
		return nil, err
	}

	recorded := map[string]gitlite.File{}
	files, err := repo.ListTree(commit.Tree)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		recorded[file.Path] = file
	}

	limits := settings.limits()
	var out []FileChange
	seen := map[string]bool{}
	for _, candidate := range scan.Files {
		seen[candidate.Path] = true
		if candidate.Size > limits.FileBytes && !settings.allows(candidate.Path) {
			continue
		}
		content, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(candidate.Path)))
		if err != nil {
			continue
		}
		previous, known := recorded[candidate.Path]
		hash := gitlite.HashBlob(content)
		if known && previous.Hash == hash {
			continue
		}

		var old []byte
		status := "added"
		if known {
			status = "modified"
			if old, err = repo.ReadBlob(previous.Hash); err != nil {
				return nil, err
			}
		}
		diff := diffText(candidate.Path, old, content, status)
		out = append(out, FileChange{
			Path: candidate.Path, Status: status,
			Insertions: diff.Insertions, Deletions: diff.Deletions, Binary: diff.Binary,
		})
	}
	for path := range recorded {
		if seen[path] {
			continue
		}
		old, err := repo.ReadBlob(recorded[path].Hash)
		if err != nil {
			return nil, err
		}
		diff := diffText(path, old, nil, "deleted")
		out = append(out, FileChange{
			Path: path, Status: "deleted",
			Insertions: diff.Insertions, Deletions: diff.Deletions, Binary: diff.Binary,
		})
	}

	sort.Slice(out, func(a, b int) bool { return out[a].Path < out[b].Path })
	return out, nil
}

// Stats reports the footer numbers.
func (s *Service) Stats(instanceID, directory string) (Stats, error) {
	lock := s.lockFor(instanceID)
	lock.Lock()
	defer lock.Unlock()

	settings := s.Settings(instanceID)
	stats := Stats{}
	if !settings.CompactedAt.IsZero() {
		when := settings.CompactedAt
		stats.CompactedAt = &when
	}

	repo, err := s.openRepo(instanceID, directory)
	if err != nil {
		if errors.Is(err, ErrNoHistory) {
			return stats, nil
		}
		return stats, err
	}
	if stats.RepoBytes, err = repo.Size(); err != nil {
		return stats, err
	}

	head, err := repo.Head()
	if err != nil || head == "" {
		return stats, err
	}
	commits, err := repo.Log(head, 0)
	if err != nil {
		return stats, err
	}
	stats.Commits = len(commits)

	files, err := repo.ListTree(commits[0].Tree)
	if err != nil {
		return stats, err
	}
	stats.Files = len(files)
	return stats, nil
}

// Coverage explains what the rules would record right now, without committing.
// It is what the tab shows an instance with no history yet, and what makes
// "why is my world not in here" answerable before the first snapshot.
type Coverage struct {
	Files     int             `json:"files"`
	Bytes     int64           `json:"bytes"`
	Worlds    []string        `json:"worlds,omitempty"`
	Oversized []OversizedFile `json:"oversized,omitempty"`
	Truncated bool            `json:"truncated,omitempty"`
}

func (s *Service) Coverage(instanceID, directory string) (Coverage, error) {
	settings := s.Settings(instanceID)
	scan, err := Collect(directory, settings.Exclude)
	if err != nil {
		return Coverage{}, err
	}

	limits := settings.limits()
	out := Coverage{Worlds: scan.Worlds, Truncated: scan.Truncated}
	for _, candidate := range scan.Files {
		if candidate.Size > limits.FileBytes && !settings.allows(candidate.Path) {
			out.Oversized = append(out.Oversized, OversizedFile{Path: candidate.Path, Size: candidate.Size})
			continue
		}
		out.Files++
		out.Bytes += candidate.Size
	}
	return out, nil
}

// resolve turns a ref the API was handed into a full commit id, accepting the
// abbreviated form the UI shows.
func (s *Service) resolve(repo *gitlite.Repo, ref string) (*gitlite.Commit, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, ErrNotFound
	}
	if len(ref) == 40 {
		commit, err := repo.ReadCommit(ref)
		if err != nil {
			return nil, ErrNotFound
		}
		return commit, nil
	}

	head, err := repo.Head()
	if err != nil || head == "" {
		return nil, ErrNotFound
	}
	commits, err := repo.Log(head, 0)
	if err != nil {
		return nil, err
	}
	for _, commit := range commits {
		if strings.HasPrefix(commit.Hash, ref) {
			return commit, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrNotFound, ref)
}
