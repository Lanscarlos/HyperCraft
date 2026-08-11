// Package gitlite reads and writes Git repositories directly — no git binary
// on the host, no third-party Git library.
//
// It covers exactly the slice of Git the config history needs: loose objects,
// trees, a single branch of linear history, and reading it all back. There is
// no index, no merge, no packfile writer and — deliberately — no network. The
// on-disk result is an ordinary repository, so an operator can point their own
// `git log` at it; that compatibility is the whole reason for speaking Git's
// object format rather than inventing a snapshot store.
//
// Why not go-git: more than half of what it carries is transport (SSH, HTTPS,
// GPG signing, known-hosts), and the config history is forbidden from ever
// pushing anywhere — see the design's §7. Twenty transitive modules, most of
// them crypto, to reach a feature that must never open a socket is a bad trade
// for a panel that ships as one binary.
//
// Concurrency: a Repo holds no state, and object writes are content-addressed
// and atomic, so several may run at once. Ref updates are not serialised here;
// the caller is expected to hold one lock per repository, which the config
// history does.
package gitlite

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultBranch is the only branch a config-history repository ever has.
// Branching is not a concept this module exposes; the name exists because HEAD
// has to point somewhere.
const DefaultBranch = "main"

// File modes, in the spelling Git uses inside a tree. Note the missing leading
// zero on a directory: Git writes the mode as octal with no padding, so a tree
// is "40000" and not "040000".
const (
	ModeFile    = "100644"
	ModeExec    = "100755"
	ModeTree    = "40000"
	ModeSymlink = "120000"
)

// ErrNotFound is returned for an object or ref that is not in the repository.
var ErrNotFound = errors.New("git object not found")

// Repo is one repository, with its git-dir and work-tree held apart.
//
// The separation is the point rather than a detail: the history of a server's
// configuration lives in the panel's data directory, while the files it
// describes live in the instance directory. See the design's §3 — an operator
// rummaging around over SFTP cannot delete a .git that is not there, and
// tarring up an instance to move it does not drag the history along.
type Repo struct {
	gitDir   string
	workTree string
}

func Open(gitDir, workTree string) *Repo {
	return &Repo{gitDir: gitDir, workTree: workTree}
}

func (r *Repo) GitDir() string   { return r.gitDir }
func (r *Repo) WorkTree() string { return r.workTree }

// Exists reports whether Init has ever run against this git-dir.
func (r *Repo) Exists() bool {
	_, err := os.Stat(filepath.Join(r.gitDir, "HEAD"))
	return err == nil
}

// Init creates the repository, or refreshes the metadata of one that is
// already there. Idempotent on purpose: the work-tree path is recorded in the
// config, and an instance whose directory was moved has to end up with a
// config that says where it went.
func (r *Repo) Init() error {
	for _, dir := range []string{
		filepath.Join(r.gitDir, "objects"),
		filepath.Join(r.gitDir, "refs", "heads"),
		filepath.Join(r.gitDir, "info"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	head := filepath.Join(r.gitDir, "HEAD")
	if _, err := os.Stat(head); os.IsNotExist(err) {
		if err := writeFileAtomic(head, []byte("ref: refs/heads/"+DefaultBranch+"\n"), 0o600); err != nil {
			return err
		}
	}

	if err := writeFileAtomic(filepath.Join(r.gitDir, "config"), []byte(r.configFile()), 0o600); err != nil {
		return err
	}
	// info/attributes rather than a .gitattributes in the work tree, and
	// likewise info/exclude rather than a .gitignore: both are the git-native
	// place for rules that belong to this repository and not to the files it
	// tracks, and writing either one into the instance directory would drop a
	// file the operator never asked for into their server.
	//
	// "* -text" turns off end-of-line normalisation. Plugins write CRLF, LF and
	// the occasional BOM as they please; letting Git rewrite line endings would
	// turn a two-line change into a whole-file diff and cost the history the
	// only thing it is for. See the design's §3.
	if err := writeFileAtomic(filepath.Join(r.gitDir, "info", "attributes"), []byte("* -text\n"), 0o600); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(r.gitDir, "info", "exclude"), []byte(excludeDoc), 0o600)
}

// excludeDoc is documentation, not machinery. The panel computes the file list
// itself and adds it explicitly — see the design's §1.2 — because signature
// detection ("this directory holds a level.dat, so it is a world") is not
// something gitignore syntax can express. These patterns are here so that an
// operator who runs git by hand against the repository is not shown ten
// thousand untracked region files.
const excludeDoc = `# Written by HyperCraft. Documentation only.
#
# The panel does not use Git's ignore engine: it works out which files are
# configuration on its own and adds exactly those. These patterns exist so a
# hand-run "git status" against this repository shows something readable.
/logs/
/crash-reports/
/cache/
/libraries/
/versions/
/bundler/
/mods/
/backups/
/dumps/
/.fabric/
/usercache.json
*.jar
*.log
*.gz
*.zip
*.db
*.mv.db
*.sqlite*
*.dat
*.dat_old
*.mca
*.lock
*.png
*.jpg
*.ogg
*.nbt
*.schem
`

func (r *Repo) configFile() string {
	var b strings.Builder
	b.WriteString("[core]\n")
	b.WriteString("\trepositoryformatversion = 0\n")
	b.WriteString("\tfilemode = true\n")
	b.WriteString("\tbare = false\n")
	b.WriteString("\tlogallrefupdates = false\n")
	// autocrlf/safecrlf off for the same reason info/attributes says "-text".
	b.WriteString("\tautocrlf = false\n")
	b.WriteString("\tsafecrlf = false\n")
	if r.workTree != "" {
		b.WriteString("\tworktree = " + r.workTree + "\n")
	}
	return b.String()
}

// Head returns the commit the branch points at, or "" for a repository with no
// commits yet.
func (r *Repo) Head() (string, error) {
	data, err := os.ReadFile(r.branchRef())
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", DefaultBranch, err)
	}
	hash := strings.TrimSpace(string(data))
	if !validHash(hash) {
		return "", fmt.Errorf("ref %s holds %q, which is not a commit id", DefaultBranch, hash)
	}
	return hash, nil
}

// SetHead moves the branch. Every caller here only ever moves it forward — the
// config history never rewrites what it recorded, see the design's §5.1 — but
// that is the caller's discipline, not something this function enforces.
func (r *Repo) SetHead(hash string) error {
	if !validHash(hash) {
		return fmt.Errorf("%q is not a commit id", hash)
	}
	return writeFileAtomic(r.branchRef(), []byte(hash+"\n"), 0o600)
}

func (r *Repo) branchRef() string {
	return filepath.Join(r.gitDir, "refs", "heads", DefaultBranch)
}

// writeFileAtomic writes through a temporary file in the same directory, so a
// reader never sees a half-written ref and a crash never leaves one truncated.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(name)
	}()

	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func validHash(s string) bool {
	if len(s) != 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
