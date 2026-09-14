package serverfiles

import (
	"io/fs"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// The instance's files as one flat list, and the three questions asked of it.
//
// The tree is how you know where you are; it is not how you find a file.
// world/region holds several hundred .mca, a plugins/ directory holds a dozen
// files per plugin, and walking down to one of them by expanding folders is
// the slowest path there is. ⌘P, the sidebar's search panel and the disk
// figure at the foot of that sidebar all want the same walk, so it happens
// once and is kept for a little while.
//
// Matching is done here rather than in the browser for the same reason: an
// instance can hold tens of thousands of paths, and shipping them all to a
// text box that will show fifty is a megabyte of JSON per keystroke-that-
// misses-the-cache. The front end gets the fifty and the indices to highlight.

// heavy names directories whose contents are machine-generated bulk. Nobody
// looks for r.0.-1.mca by name, and walking a real world turns a 30ms index
// into a multi-second one. `all` on the endpoint reaches them; Usage always
// does, because a size that leaves out the region folder is not a size.
//
// Matched on the directory's own name at any depth, not on a path prefix: a
// server may have world, world_nether, world_the_end and whatever the operator
// renamed them to, and the bulk is one level down inside each.
var heavy = map[string]bool{
	"region":    true,
	"entities":  true,
	"poi":       true,
	"libraries": true,
	"cache":     true,
	"versions":  true,
}

// indexTTL is how long one walk is reused.
//
// The index backs a type-ahead, so it has to be cheap to consult and is
// allowed to be slightly stale: a file created a second ago that does not turn
// up in ⌘P for another half minute is a much smaller problem than a directory
// walk per keystroke. Writes made through this package drop it at once — see
// forget — so the only staleness that can be observed is a change made from
// outside the panel, which is also the only kind nothing else here notices
// either (the file pane polls for it on a ten-second timer).
const indexTTL = 30 * time.Second

// maxGrepBytes caps the file the content search will read. Above it a file is
// a log or a world, and a search box is not the instrument for those.
const maxGrepBytes = 1 << 20

// grepLinesPerFile caps how much of one file the search panel shows. Twenty
// hits is already more than fits without scrolling, and a file with two
// thousand matches is one the query was wrong about.
const grepLinesPerFile = 20

// Hit is one path the fuzzy matcher accepted.
type Hit struct {
	Path     string    `json:"path"`
	Name     string    `json:"name"`
	IsDir    bool      `json:"isDir"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	Score    int       `json:"score"`
	// Match holds the byte offsets in Path the query matched, ascending. The
	// front end paints them; it does not re-derive them, because the two
	// matchers would drift and the highlight would stop agreeing with the
	// ranking.
	Match []int `json:"match"`
}

// LineHit is one matching line inside a file.
type LineHit struct {
	N    int    `json:"n"`
	Text string `json:"text"`
	Col  int    `json:"col"`
	Len  int    `json:"len"`
}

// FileHits is one file the content search matched, and where.
type FileHits struct {
	Path string `json:"path"`
	Name string `json:"name"`
	// Lines is empty when only the file's name matched, which is still a hit —
	// looking for a plugin by name and finding its folder is the common case.
	Lines []LineHit `json:"lines"`
}

// Usage is what one instance weighs.
type Usage struct {
	Bytes int64 `json:"bytes"`
	Files int   `json:"files"`
	Dirs  int   `json:"dirs"`
}

// indexed is one walk of one instance directory, kept for indexTTL.
type indexed struct {
	at      time.Time
	entries []Entry
}

// Cached per directory rather than per Browser: handlers build a fresh Browser
// for every request (see the API layer), so a field on the struct would be a
// cache with a lifetime of one call. The key is the instance directory, which
// is what the walk is actually about — two Browsers over the same instance
// with different scopes share the walk and filter it separately.
var (
	indexMu    sync.Mutex
	indexCache = map[string]*indexed{}
)

// forget drops the cached walk for a directory. Called by every write path in
// browser.go: a walk cached a moment ago must not outlive the write that made
// it wrong.
func forget(dir string) {
	indexMu.Lock()
	delete(indexCache, dir)
	indexMu.Unlock()
}

// entries returns every path under the instance directory, from cache when it
// is fresh. The full walk is cached; `all` filters it on the way out, so
// turning the switch on does not cost a second walk.
func (b *Browser) entries() ([]Entry, error) {
	indexMu.Lock()
	if held, ok := indexCache[b.dir]; ok && time.Since(held.at) < indexTTL {
		indexMu.Unlock()
		return held.entries, nil
	}
	indexMu.Unlock()

	walked, err := b.walk()
	if err != nil {
		return nil, err
	}

	indexMu.Lock()
	indexCache[b.dir] = &indexed{at: time.Now(), entries: walked}
	indexMu.Unlock()
	return walked, nil
}

// walk reads the whole instance directory through os.Root, which is what keeps
// it inside: a symlink pointing at /etc is refused by the kernel-level open
// rather than by anything here. Scope is not applied — the cache is shared
// between browsers with different rules — so every reader filters.
func (b *Browser) walk() ([]Entry, error) {
	root, err := b.open()
	if err != nil {
		return nil, err
	}
	defer root.Close()

	out := make([]Entry, 0, 256)
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			// A directory that cannot be read is skipped, not fatal: one
			// unreadable folder must not cost the whole index. Returning the
			// error from the root itself is still fatal, which is handled by
			// the open above.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if name == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out = append(out, Entry{
			Name:     d.Name(),
			Path:     name,
			IsDir:    d.IsDir(),
			Size:     info.Size(),
			Modified: info.ModTime(),
			Editable: !d.IsDir() && isEditable(d.Name(), info.Size()),
			Symlink:  info.Mode()&fs.ModeSymlink != 0,
		})
		return nil
	})
	if err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// visible reports whether an indexed entry is one this browser may offer.
func (b *Browser) visible(e Entry, all bool) bool {
	if !b.within(e.Path) && !b.leadsTo(e.Path) {
		return false
	}
	return all || !underHeavy(e.Path)
}

// underHeavy reports whether a path passes through one of the bulk directories.
func underHeavy(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if heavy[part] {
			return true
		}
	}
	return false
}

// Find returns up to limit paths matching a fuzzy query, best first.
//
// The query is split on whitespace and every piece has to match as a
// subsequence of the lowercased path, so "vulp con" reaches
// plugins/Vulpecula/config.yml without anyone having to remember which half of
// it is the directory.
func (b *Browser) Find(query string, limit int, all bool) ([]Hit, error) {
	pieces := strings.Fields(strings.ToLower(query))
	if len(pieces) == 0 || limit <= 0 {
		// The answer to an empty box is not "everything you own" — it is
		// whatever the caller shows instead, which is the recently opened
		// files. Sending the whole index would also be the one response big
		// enough to matter.
		return []Hit{}, nil
	}

	indexed, err := b.entries()
	if err != nil {
		return nil, err
	}

	hits := make([]Hit, 0, limit*2)
	for _, e := range indexed {
		if !b.visible(e, all) {
			continue
		}
		lower := strings.ToLower(e.Path)
		score, marks, ok := matchAll(lower, e.Name, pieces)
		if !ok {
			continue
		}
		hits = append(hits, Hit{
			Path:     e.Path,
			Name:     e.Name,
			IsDir:    e.IsDir,
			Size:     e.Size,
			Modified: e.Modified,
			Score:    score,
			Match:    marks,
		})
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		// A tie between two equally good matches goes to the shallower one:
		// the file three directories down is the more specialised answer.
		return len(hits[i].Path) < len(hits[j].Path)
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// matchAll requires every piece of the query to appear in the path, and scores
// the whole. The marks it returns cover every piece, deduplicated and sorted,
// so the front end can paint them without knowing how the query was split.
func matchAll(lowerPath, name string, pieces []string) (int, []int, bool) {
	// Where the basename starts, so a hit on the name can be scored above a
	// hit on the directory it happens to sit in.
	nameAt := len(lowerPath) - len(name)

	total := 0
	seen := map[int]bool{}
	for _, piece := range pieces {
		score, marks, ok := matchOne(lowerPath, nameAt, piece)
		if !ok {
			return 0, nil, false
		}
		total += score
		for _, at := range marks {
			seen[at] = true
		}
	}

	marks := make([]int, 0, len(seen))
	for at := range seen {
		marks = append(marks, at)
	}
	sort.Ints(marks)
	// Shorter paths win among equal matches; expressed as part of the score so
	// one sort does the whole job.
	return total + max(0, 64-len(lowerPath)/4), marks, true
}

// matchOne finds one piece as a subsequence, greedily from the left.
//
// Greedy rather than optimal: the optimal placement is a dynamic program over
// path × query, and the difference it buys on paths this short is not visible
// in a list of fifty. What greedy would get wrong — preferring an early stray
// letter over a clean run later — is what the bonuses below correct for, since
// a run at a segment boundary outscores a scattered one anyway.
func matchOne(lowerPath string, nameAt int, piece string) (int, []int, bool) {
	marks := make([]int, 0, len(piece))
	score := 0
	at := 0
	for i := 0; i < len(piece); i++ {
		found := strings.IndexByte(lowerPath[at:], piece[i])
		if found < 0 {
			return 0, nil, false
		}
		pos := at + found
		marks = append(marks, pos)
		// Straight after the previous character: a run reads as the word the
		// reader typed rather than as letters that happen to be in order.
		if i > 0 && pos == marks[i-1]+1 {
			score += 8
		}
		// The start of a path segment, or of a word inside one. This is what
		// makes "vc" prefer Vulpecula/config over a file with a v and a c
		// somewhere in the middle.
		if boundary(lowerPath, pos) {
			score += 12
		}
		// Inside the basename rather than in a directory on the way to it:
		// people search for the file, and the directory is context.
		if pos >= nameAt {
			score += 20
		}
		at = pos + 1
	}
	return score, marks, true
}

// boundary reports whether an offset starts a segment or a word.
func boundary(lowerPath string, at int) bool {
	if at == 0 {
		return true
	}
	prev := rune(lowerPath[at-1])
	return prev == '/' || prev == '-' || prev == '_' || prev == '.' || prev == ' ' ||
		unicode.IsDigit(prev) != unicode.IsDigit(rune(lowerPath[at]))
}

// Grep searches file contents, and file names, for a literal string.
//
// Literal and case-insensitive, with no regular expressions: a pattern box
// here would be a second language to get right in a panel whose job is to find
// which config holds a setting, and a bad pattern silently matching nothing is
// worse than no box at all.
func (b *Browser) Grep(query string, limit int, all bool) ([]FileHits, error) {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" || limit <= 0 {
		return []FileHits{}, nil
	}

	indexed, err := b.entries()
	if err != nil {
		return nil, err
	}

	out := make([]FileHits, 0, limit)
	for _, e := range indexed {
		if len(out) >= limit {
			break
		}
		if e.IsDir || !b.visible(e, all) || !b.within(e.Path) {
			continue
		}

		named := strings.Contains(strings.ToLower(e.Name), needle)
		var lines []LineHit
		// Only files the editor would open, and only up to its own cap: the
		// rest are jars and region files, and reading one into memory to look
		// for a word in it is a way to stall the daemon on a 400MB world.
		if e.Editable && e.Size <= maxGrepBytes {
			body, err := b.ReadText(e.Path)
			if err == nil && !strings.ContainsRune(body, 0) {
				lines = grepLines(body, needle)
			}
		}
		if !named && len(lines) == 0 {
			continue
		}
		out = append(out, FileHits{Path: e.Path, Name: e.Name, Lines: lines})
	}
	return out, nil
}

func grepLines(body, needle string) []LineHit {
	var out []LineHit
	for i, line := range strings.Split(body, "\n") {
		at := strings.Index(strings.ToLower(line), needle)
		if at < 0 {
			continue
		}
		// Long lines are trimmed at the source rather than in CSS: a minified
		// JSON config is one line of 200 000 characters, and the panel should
		// not ship it to draw a 300px row.
		text, at := window(line, at, len(needle))
		out = append(out, LineHit{N: i + 1, Text: text, Col: at, Len: len(needle)})
		if len(out) >= grepLinesPerFile {
			break
		}
	}
	return out
}

// window trims a line to a readable span around the hit, and says where the
// hit landed in what is left.
func window(line string, at, length int) (string, int) {
	const before, after = 60, 140
	if len(line) <= before+after {
		return line, at
	}
	from := max(0, at-before)
	to := min(len(line), from+before+after)
	return line[from:to], at - from
}

// Usage is what the instance weighs on disk.
//
// It always walks the bulk directories, unlike Find: the region folder is most
// of what a server weighs, and a figure that leaves it out is not a figure. It
// reads the same cached walk, so opening the file page does not cost two.
func (b *Browser) Usage() (Usage, error) {
	indexed, err := b.entries()
	if err != nil {
		return Usage{}, err
	}
	var u Usage
	for _, e := range indexed {
		if !b.within(e.Path) {
			continue
		}
		if e.IsDir {
			u.Dirs++
			continue
		}
		u.Files++
		u.Bytes += e.Size
	}
	return u, nil
}
