package confighist

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lanscarlos/hypercraft/internal/gitlite"
)

// Compacting history.
//
// Nothing here runs on its own. Configuration is plain text and Git stores each
// distinct version once, so a thousand commits is tens of megabytes — the
// design's §9 says not to prune by default, and the default is not to. This is
// the button for the operator who wants the space back anyway.
//
// It rebuilds rather than rewrites. Every kept commit is replayed into a fresh
// repository in its original order, with its original message, author and
// timestamps; the old repository is swapped out only once the new one is whole
// on disk. A rewrite in place would have a window where the history is neither
// the old one nor the new one, and this is the operator's only copy.

// DefaultKeep is how many recent commits a compaction keeps outright.
const DefaultKeep = 100

// CompactResult reports what a compaction did.
type CompactResult struct {
	Before      int   `json:"before"`
	After       int   `json:"after"`
	BytesBefore int64 `json:"bytesBefore"`
	BytesAfter  int64 `json:"bytesAfter"`
	// Remap maps every kept commit's old id to its new one. A rebuild has to
	// give the replayed commits new ids — their parents changed — and the
	// plugin ledger holds refs into this history, so the caller has to be able
	// to fix them up. Dropped commits are absent from the map.
	Remap map[string]string `json:"-"`
}

// Compact rebuilds an instance's history, keeping the recent commits, the
// first commit of each month, and every plugin-transaction snapshot.
//
// Transaction snapshots are kept whatever their age because a plugin rollback
// restores its config from one; pruning them would quietly break the undo on
// an upgrade from six months ago. See the design's §6 and §9.
func (s *Service) Compact(instanceID, directory string, keep int) (CompactResult, error) {
	if keep <= 0 {
		keep = DefaultKeep
	}

	lock := s.lockFor(instanceID)
	lock.Lock()
	defer lock.Unlock()

	repo, err := s.openRepo(instanceID, directory)
	if err != nil {
		return CompactResult{}, err
	}
	head, err := repo.Head()
	if err != nil {
		return CompactResult{}, err
	}
	if head == "" {
		return CompactResult{}, ErrNoHistory
	}

	newest, err := repo.Log(head, 0)
	if err != nil {
		return CompactResult{}, err
	}
	before, err := repo.Size()
	if err != nil {
		return CompactResult{}, err
	}

	kept := chooseKept(newest, keep)
	result := CompactResult{Before: len(newest), After: len(kept), BytesBefore: before}
	if len(kept) == len(newest) {
		// Nothing to drop. Sweeping unreachable objects is still worth doing —
		// it is the other half of what the button promises.
		if _, err := repo.Prune(); err != nil {
			return result, err
		}
		result.BytesAfter, _ = repo.Size()
		return result, s.markCompacted(instanceID)
	}

	staging := repo.GitDir() + ".rebuild"
	if err := os.RemoveAll(staging); err != nil {
		return result, err
	}
	rebuilt := gitlite.Open(staging, directory)
	if err := rebuilt.Init(); err != nil {
		return result, err
	}

	remap, err := replay(repo, rebuilt, kept)
	if err != nil {
		os.RemoveAll(staging)
		return result, fmt.Errorf("重建配置历史失败，原历史没有改动：%w", err)
	}
	result.Remap = remap

	// The swap. Both moves are renames inside the same directory, so the window
	// where neither is in place is as short as the filesystem can make it.
	retired := repo.GitDir() + ".retired"
	_ = os.RemoveAll(retired)
	if err := os.Rename(repo.GitDir(), retired); err != nil {
		os.RemoveAll(staging)
		return result, err
	}
	if err := os.Rename(staging, repo.GitDir()); err != nil {
		// Put the original back rather than leaving the instance with no
		// history at all.
		_ = os.Rename(retired, repo.GitDir())
		os.RemoveAll(staging)
		return result, err
	}
	if err := os.RemoveAll(retired); err != nil {
		s.log.Warn("旧配置历史目录没能删除", "instance", instanceID, "dir", retired, "err", err)
	}

	result.BytesAfter, _ = gitlite.Open(repo.GitDir(), directory).Size()
	s.log.Info("配置历史已压缩",
		"instance", instanceID, "before", result.Before, "after", result.After,
		"bytesBefore", result.BytesBefore, "bytesAfter", result.BytesAfter)

	s.forgetStats()
	return result, s.markCompacted(instanceID)
}

// chooseKept picks the commits that survive, returned oldest first.
func chooseKept(newestFirst []*gitlite.Commit, keep int) []*gitlite.Commit {
	survives := make(map[string]bool, len(newestFirst))
	months := map[string]bool{}

	// Walk oldest first so "first commit of the month" means the first one.
	for i := len(newestFirst) - 1; i >= 0; i-- {
		commit := newestFirst[i]
		when := commit.Committer.When
		if when.IsZero() {
			when = commit.Author.When
		}
		month := when.Format("2006-01")
		if !months[month] {
			months[month] = true
			survives[commit.Hash] = true
		}
		if describe(commit).Trigger == TriggerTransaction {
			survives[commit.Hash] = true
		}
	}
	// The oldest commit is the base every later tree is read against, and it is
	// 出厂状态 — the one thing 恢复出厂 needs.
	survives[newestFirst[len(newestFirst)-1].Hash] = true
	for i := 0; i < len(newestFirst) && i < keep; i++ {
		survives[newestFirst[i].Hash] = true
	}

	out := make([]*gitlite.Commit, 0, len(survives))
	for i := len(newestFirst) - 1; i >= 0; i-- {
		if survives[newestFirst[i].Hash] {
			out = append(out, newestFirst[i])
		}
	}
	return out
}

// replay copies the kept commits into the new repository, oldest first.
func replay(from, to *gitlite.Repo, kept []*gitlite.Commit) (map[string]string, error) {
	remap := make(map[string]string, len(kept))
	parent := ""

	for _, commit := range kept {
		files, err := from.ListTree(commit.Tree)
		if err != nil {
			return nil, err
		}
		for i, file := range files {
			if to.Has(file.Hash) {
				continue
			}
			content, err := from.ReadBlob(file.Hash)
			if err != nil {
				return nil, err
			}
			written, err := to.WriteBlob(content)
			if err != nil {
				return nil, err
			}
			files[i].Hash = written
		}

		tree, err := to.WriteTreeFromFiles(files)
		if err != nil {
			return nil, err
		}
		var parents []string
		if parent != "" {
			parents = []string{parent}
		}
		// Message, author and both timestamps are carried over verbatim: after
		// a compaction the timeline has fewer rows, and every row it still has
		// says exactly what it said before.
		hash, err := to.WriteCommit(gitlite.CommitInput{
			Tree:      tree,
			Parents:   parents,
			Author:    commit.Author,
			Committer: commit.Committer,
			Message:   commit.Message,
		})
		if err != nil {
			return nil, err
		}
		if err := to.SetHead(hash); err != nil {
			return nil, err
		}
		remap[commit.Hash] = hash
		parent = hash
	}
	return remap, nil
}

func (s *Service) markCompacted(instanceID string) error {
	_, err := s.UpdateSettings(instanceID, func(entry *InstanceSettings) {
		entry.CompactedAt = time.Now()
		entry.SincePrune = 0
	})
	return err
}

// forgetStats drops the memoised line counts. Commit ids changed, so every
// cached entry is now keyed on a commit that no longer exists.
func (s *Service) forgetStats() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats = map[string]CommitStats{}
}

// ExportPath is where a "仅导出历史" hands the operator their repository
// directory. There is no download endpoint for it — see the design's §7 — so
// this exists for the delete-confirmation copy to be able to name the path.
func (s *Service) ExportPath(instanceID string) string {
	return filepath.Clean(s.repoDir(instanceID))
}
