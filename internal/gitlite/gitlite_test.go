package gitlite

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newRepo(t *testing.T) *Repo {
	t.Helper()
	root := t.TempDir()
	repo := Open(filepath.Join(root, "config.git"), filepath.Join(root, "work"))
	if err := os.MkdirAll(repo.WorkTree(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := repo.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	return repo
}

func commit(t *testing.T, repo *Repo, message string, files map[string]string) string {
	t.Helper()

	list := make([]File, 0, len(files))
	for path, content := range files {
		hash, err := repo.WriteBlob([]byte(content))
		if err != nil {
			t.Fatalf("blob %s: %v", path, err)
		}
		list = append(list, File{Path: path, Mode: ModeFile, Hash: hash})
	}
	tree, err := repo.WriteTreeFromFiles(list)
	if err != nil {
		t.Fatalf("tree: %v", err)
	}

	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	var parents []string
	if head != "" {
		parents = []string{head}
	}

	who := Signature{Name: "tester", Email: "tester@example.invalid", When: time.Unix(1700000000, 0).UTC()}
	hash, err := repo.WriteCommit(CommitInput{
		Tree: tree, Parents: parents, Author: who, Committer: who, Message: message,
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := repo.SetHead(hash); err != nil {
		t.Fatal(err)
	}
	return hash
}

func TestHeadIsEmptyBeforeFirstCommit(t *testing.T) {
	repo := newRepo(t)
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	if head != "" {
		t.Fatalf("expected an unborn branch, got %q", head)
	}
}

func TestBlobIdMatchesGitsOwn(t *testing.T) {
	// The well-known id of an empty blob. If this changes, the store has
	// stopped being a Git repository.
	if got := HashBlob(nil); got != "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391" {
		t.Fatalf("empty blob id = %s", got)
	}
	if got := HashBlob([]byte("hello\n")); got != "ce013625030ba8dba906f756967f9e9ca394464a" {
		t.Fatalf("blob id = %s", got)
	}
}

func TestRoundTripsTreesAndCommits(t *testing.T) {
	repo := newRepo(t)
	first := commit(t, repo, "出厂状态", map[string]string{
		"server.properties":            "motd=hi\n",
		"plugins/LuckPerms/config.yml": "server: global\n",
	})

	commit(t, repo, "编辑 server.properties", map[string]string{
		"server.properties":            "motd=hello\n",
		"plugins/LuckPerms/config.yml": "server: global\n",
		"bukkit.yml":                   "settings:\n",
	})

	log, err := repo.Log(mustHead(t, repo), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 2 {
		t.Fatalf("expected 2 commits, got %d", len(log))
	}
	if strings.TrimSpace(log[1].Message) != "出厂状态" {
		t.Fatalf("oldest message = %q", log[1].Message)
	}
	if log[1].Hash != first {
		t.Fatalf("history does not reach the first commit")
	}
	if log[0].Author.Name != "tester" {
		t.Fatalf("author = %q", log[0].Author.Name)
	}

	changes, err := repo.DiffTrees(log[1].Tree, log[0].Tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("expected 2 changed paths, got %+v", changes)
	}
	if changes[0].Path != "bukkit.yml" || changes[0].Old != nil || changes[0].New == nil {
		t.Fatalf("bukkit.yml should be an addition: %+v", changes[0])
	}
	if changes[1].Path != "server.properties" || changes[1].Old == nil || changes[1].New == nil {
		t.Fatalf("server.properties should be a modification: %+v", changes[1])
	}

	// The untouched plugin config must not appear, which is also the check that
	// equal subtrees are skipped rather than walked.
	for _, change := range changes {
		if strings.HasPrefix(change.Path, "plugins/") {
			t.Fatalf("unchanged subtree reported: %s", change.Path)
		}
	}
}

func TestFindInTree(t *testing.T) {
	repo := newRepo(t)
	commit(t, repo, "x", map[string]string{"plugins/Essentials/config.yml": "a: 1\n"})
	head, err := repo.ReadCommit(mustHead(t, repo))
	if err != nil {
		t.Fatal(err)
	}

	file, ok, err := repo.FindInTree(head.Tree, "plugins/Essentials/config.yml")
	if err != nil || !ok {
		t.Fatalf("not found: ok=%v err=%v", ok, err)
	}
	content, err := repo.ReadBlob(file.Hash)
	if err != nil || string(content) != "a: 1\n" {
		t.Fatalf("content = %q err=%v", content, err)
	}

	// A directory is not a file, and neither is something that is not there.
	if _, ok, _ := repo.FindInTree(head.Tree, "plugins/Essentials"); ok {
		t.Fatal("a directory should not resolve as a file")
	}
	if _, ok, _ := repo.FindInTree(head.Tree, "plugins/Nope/config.yml"); ok {
		t.Fatal("missing path resolved")
	}
}

func TestPruneKeepsReachableObjects(t *testing.T) {
	repo := newRepo(t)
	commit(t, repo, "keep", map[string]string{"server.properties": "motd=hi\n"})

	// An object written but never committed — what an aborted commit leaves.
	orphan, err := repo.WriteBlob([]byte("nobody points at me\n"))
	if err != nil {
		t.Fatal(err)
	}
	kept := HashBlob([]byte("motd=hi\n"))

	removed, err := repo.Prune()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 object pruned, got %d", removed)
	}
	if repo.Has(orphan) {
		t.Fatal("orphan survived the prune")
	}
	if !repo.Has(kept) {
		t.Fatal("reachable blob was pruned")
	}
}

// TestGitCanReadWhatWeWrote is the check that matters: the repository has to be
// readable by the real thing, not merely by the code that produced it. Skipped
// where git is not installed, which is the normal case on a user's server and
// exactly why this package exists.
func TestGitCanReadWhatWeWrote(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}

	repo := newRepo(t)
	commit(t, repo, "出厂状态", map[string]string{
		"server.properties":            "motd=hi\r\n",
		"plugins/LuckPerms/config.yml": "server: global\n",
		"config/paper-global.yml":      "proxies: {}\n",
	})
	commit(t, repo, "编辑 server.properties", map[string]string{
		"server.properties":            "motd=hello\r\n",
		"plugins/LuckPerms/config.yml": "server: global\n",
		"config/paper-global.yml":      "proxies: {}\n",
	})

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(git, append([]string{"--git-dir=" + repo.GitDir()}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}

	// fsck is the strict reader: it rejects a tree whose entries are ordered
	// the way a naive sort would order them.
	if out := run("fsck", "--strict"); strings.Contains(out, "error") {
		t.Fatalf("git fsck complained:\n%s", out)
	}
	if out := run("log", "--oneline"); strings.Count(strings.TrimSpace(out), "\n") != 1 {
		t.Fatalf("git log did not show two commits:\n%s", out)
	}
	if out := run("log", "-1", "--format=%s"); strings.TrimSpace(out) != "编辑 server.properties" {
		t.Fatalf("git read the subject as %q", strings.TrimSpace(out))
	}

	// The CRLF in server.properties has to survive verbatim: normalising it
	// would be the bug info/attributes exists to prevent.
	if out := run("cat-file", "-p", "HEAD:server.properties"); out != "motd=hello\r\n" {
		t.Fatalf("content came back as %q", out)
	}
	if out := run("diff", "--stat", "HEAD~1", "HEAD"); !strings.Contains(out, "1 file changed") {
		t.Fatalf("git saw more than the one changed file:\n%s", out)
	}
}

func mustHead(t *testing.T, repo *Repo) string {
	t.Helper()
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	return head
}
