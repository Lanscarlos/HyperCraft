package serverfiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkdirAll(t *testing.T, dir, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(rel)), 0o755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func paths(hits []Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Path
	}
	return out
}

func TestFindMatchesSubsequenceAcrossSegments(t *testing.T) {
	b, dir := newBrowser(t)
	write(t, dir, "plugins/Vulpecula/config.yml", "a: 1\n")
	write(t, dir, "plugins/Otherwise/config.yml", "b: 2\n")

	hits, err := b.Find("vulp con", 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Path != "plugins/Vulpecula/config.yml" {
		t.Fatalf("want plugins/Vulpecula/config.yml first, got %v", paths(hits))
	}
}

func TestFindScoresBasenameAboveDirectory(t *testing.T) {
	b, dir := newBrowser(t)
	write(t, dir, "config/readme.md", "x")
	write(t, dir, "plugins/config.txt", "x")

	hits, err := b.Find("config", 10, false)
	if err != nil {
		t.Fatal(err)
	}
	// A file whose *name* is the query beats one that merely lives in a
	// directory called that. Said as a relative order rather than as "first",
	// because the directory called config is itself a legitimate best answer
	// and pinning the top slot would be testing that instead.
	at := func(want string) int {
		for i, p := range paths(hits) {
			if p == want {
				return i
			}
		}
		t.Fatalf("%s missing from %v", want, paths(hits))
		return -1
	}
	if at("plugins/config.txt") > at("config/readme.md") {
		t.Fatalf("name match should outrank directory match, got %v", paths(hits))
	}
}

func TestFindReportsMatchedIndices(t *testing.T) {
	b, dir := newBrowser(t)
	write(t, dir, "plugins/Vulpecula/config.yml", "a: 1\n")

	hits, err := b.Find("vulp", 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	got := hits[0]
	if len(got.Match) != 4 {
		t.Fatalf("want four matched indices, got %v", got.Match)
	}
	for i, at := range got.Match {
		if at < 0 || at >= len(got.Path) {
			t.Fatalf("index %d out of range: %d", i, at)
		}
		if strings.ToLower(got.Path)[at] != "vulp"[i] {
			t.Fatalf("index %d points at %q, want %q", at, got.Path[at], "vulp"[i])
		}
	}
}

func TestFindSkipsHeavyDirsUnlessAsked(t *testing.T) {
	b, dir := newBrowser(t)
	write(t, dir, "world/region/r.0.0.mca", "x")
	write(t, dir, "libraries/net/foo.jar", "x")

	if hits, _ := b.Find("mca", 10, false); len(hits) != 0 {
		t.Fatalf("region should be skipped by default, got %v", paths(hits))
	}
	if hits, _ := b.Find("foo.jar", 10, false); len(hits) != 0 {
		t.Fatalf("libraries should be skipped by default, got %v", paths(hits))
	}
	if hits, _ := b.Find("mca", 10, true); len(hits) != 1 {
		t.Fatalf("all=true should reach region, got %v", paths(hits))
	}
}

func TestFindEmptyQueryReturnsNothing(t *testing.T) {
	b, _ := newBrowser(t)

	hits, err := b.Find("   ", 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("an empty query has no answer here, got %v", paths(hits))
	}
}

func TestFindHonoursLimit(t *testing.T) {
	b, dir := newBrowser(t)
	for i := 0; i < 12; i++ {
		write(t, dir, filepath.ToSlash(filepath.Join("many", string(rune('a'+i))+"config.yml")), "x")
	}

	hits, err := b.Find("config", 5, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 5 {
		t.Fatalf("want five hits, got %d", len(hits))
	}
}

func TestFindRespectsScope(t *testing.T) {
	b, dir := newBrowser(t)
	write(t, dir, "plugins/Mine/config.yml", "a: 1\n")
	write(t, dir, "secret.txt", "x")

	hits, err := b.Restrict([]string{"plugins/Mine"}).Find("t", 50, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths(hits) {
		if p == "secret.txt" {
			t.Fatalf("a confined browser must not index outside its scope: %v", paths(hits))
		}
	}
}

func TestGrepReportsLineAndColumn(t *testing.T) {
	b, dir := newBrowser(t)
	write(t, dir, "server.properties", "motd=hi\nmax-players=20\n")

	out, err := b.Grep("max-players", 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || len(out[0].Lines) != 1 {
		t.Fatalf("want one file with one line, got %+v", out)
	}
	line := out[0].Lines[0]
	if line.N != 2 || line.Col != 0 || line.Len != len("max-players") {
		t.Fatalf("want line 2 col 0 len 11, got %+v", line)
	}
}

func TestGrepIsCaseInsensitive(t *testing.T) {
	b, dir := newBrowser(t)
	write(t, dir, "server.properties", "MOTD=hi\n")

	out, err := b.Grep("motd", 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("want one file, got %+v", out)
	}
}

func TestGrepSkipsBinary(t *testing.T) {
	b, dir := newBrowser(t)
	write(t, dir, "plugin.jar", "PK\x03\x00\x04needle")

	if out, _ := b.Grep("needle", 10, false); len(out) != 0 {
		t.Fatalf("binary files must not be grepped, got %+v", out)
	}
}

func TestGrepAlsoMatchesFileNames(t *testing.T) {
	b, dir := newBrowser(t)
	write(t, dir, "plugins/Vulpecula/vulpecula.yml", "nothing in here\n")

	out, err := b.Grep("vulpecula.yml", 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Path != "plugins/Vulpecula/vulpecula.yml" {
		t.Fatalf("a name-only match is still a match, got %+v", out)
	}
	if len(out[0].Lines) != 0 {
		t.Fatalf("a name-only match has no lines to show, got %+v", out[0].Lines)
	}
}

func TestUsageCountsBytesUnderTheRoot(t *testing.T) {
	b, dir := newBrowser(t)
	write(t, dir, "a.txt", "12345")
	mkdirAll(t, dir, "sub")
	write(t, dir, "sub/b.txt", "123")

	u, err := b.Usage()
	if err != nil {
		t.Fatal(err)
	}
	// newBrowser leaves server.properties (8 bytes) and plugins/config.yml (5).
	if u.Bytes != 5+3+8+5 {
		t.Fatalf("want 21 bytes, got %d", u.Bytes)
	}
	if u.Files != 4 {
		t.Fatalf("want four files, got %d", u.Files)
	}
}

func TestUsageWalksTheHeavyDirsToo(t *testing.T) {
	b, dir := newBrowser(t)
	write(t, dir, "world/region/r.0.0.mca", "0123456789")

	u, err := b.Usage()
	if err != nil {
		t.Fatal(err)
	}
	// A region folder is most of what a server weighs; a size that leaves it
	// out is not a size.
	if u.Bytes < 10 {
		t.Fatalf("region bytes must be counted, got %d", u.Bytes)
	}
}

func TestWritesDropTheCachedIndex(t *testing.T) {
	b, dir := newBrowser(t)

	if hits, _ := b.Find("fresh", 10, false); len(hits) != 0 {
		t.Fatal("not written yet")
	}
	// Through the browser, which is what invalidates: a walk cached a moment
	// ago must not outlive the write that made it wrong.
	if err := b.WriteText("fresh.yml", "a: 1\n"); err != nil {
		t.Fatal(err)
	}
	hits, err := b.Find("fresh", 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("want the new file, got %v", paths(hits))
	}
	_ = dir
}
