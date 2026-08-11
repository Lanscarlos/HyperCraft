package confighist

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/gitlite"
)

// The collection rules, in the order the design's §1.1 lays them out.
//
// The rule that matters is that this is a whitelist. A blacklist — exclude
// world/, exclude *.jar, exclude cache/ — fails on its first day, and not on
// the files anyone thinks of: it fails on plugins/Essentials/userdata/*.yml,
// thirty thousand player files with a config file's extension sitting inside
// plugins/. What a server's runtime state is named is up to whoever wrote the
// plugin, so nothing keyed on names can keep up. Default to not recording, and
// record what is recognised.

// excludedDirs never contribute anything, wherever they appear. Every dot
// directory is excluded too — that covers .fabric and, more to the point, a
// .git somebody put in their server directory by hand.
var excludedDirs = map[string]bool{
	"logs":          true,
	"crash-reports": true,
	"cache":         true,
	"libraries":     true,
	"versions":      true,
	"bundler":       true,
	"mods":          true,
	"backups":       true,
	"dumps":         true,
}

// pluginSubdirs are the only directories below plugins/<Name>/ that are
// descended into. Everything else under a plugin is assumed to be its data —
// because it usually is, and because guessing wrong the other way is what
// commits a player database.
var pluginSubdirs = map[string]bool{
	"lang":       true,
	"messages":   true,
	"schematics": true,
	"scripts":    true,
	"worlds":     true,
}

// allowedExts is the general rule for anything not otherwise decided. .sk is in
// the list on purpose: a Skript file is both configuration and code, and it is
// edited far more often than any yml on the server.
var allowedExts = map[string]bool{
	".yml":        true,
	".yaml":       true,
	".conf":       true,
	".json":       true,
	".json5":      true,
	".toml":       true,
	".properties": true,
	".txt":        true,
	".cfg":        true,
	".ini":        true,
	".hocon":      true,
	".lang":       true,
	".mcmeta":     true,
	".sk":         true,
}

// deniedExts beat every other rule. A file named config.db is not a config.
var deniedExts = map[string]bool{
	".db":        true,
	".dat":       true,
	".dat_old":   true,
	".mca":       true,
	".mcr":       true,
	".jar":       true,
	".log":       true,
	".gz":        true,
	".zip":       true,
	".png":       true,
	".jpg":       true,
	".jpeg":      true,
	".ogg":       true,
	".nbt":       true,
	".schem":     true,
	".schematic": true,
	".lock":      true,
}

// rootScripts are the launch scripts, which no extension rule would catch and
// which are edited about as often as server.properties.
var rootScripts = map[string]bool{
	".sh":  true,
	".bat": true,
	".cmd": true,
	".ps1": true,
}

// rootDenied are the root-level files that look like configuration and are
// not. usercache.json is rewritten every time a player joins; recording it
// would put a commit's worth of noise on the timeline for every login.
//
// ops.json, whitelist.json and banned-*.json are deliberately *not* here.
// They are runtime state in the sense that the server writes them, but what
// they hold is the operator's own decisions — who has permissions, who is
// banned — and "when did we op this person" is exactly the question somebody
// comes back to the timeline with.
var rootDenied = map[string]bool{
	"usercache.json": true,
	"session.lock":   true,
}

// maxWalkDepth and maxWalkEntries bound the scan. Neither should ever be hit
// on a real server — worlds are cut off by signature detection and the heavy
// plugin directories by rule 3 — so hitting one means the rules missed
// something, which is reported rather than silently truncated.
const (
	maxWalkDepth   = 16
	maxWalkEntries = 200000
)

// Candidate is one file the rules decided to record.
type Candidate struct {
	Path string // slash-separated, relative to the instance directory
	Size int64
	Mode string
}

// Scan is the result of applying the rules to an instance directory.
type Scan struct {
	Files []Candidate
	// Worlds are the directories that were recognised as worlds by their
	// contents rather than by their name. Surfaced so the UI can say what was
	// skipped and why — "survival is a world" is the single most useful thing
	// this module can tell an operator about its own coverage.
	Worlds []string
	// Truncated is set when the walk hit its own bounds. It means the answer is
	// incomplete, which the caller must treat as a reason to stop rather than
	// as a smaller set of files.
	Truncated bool
}

// scanner carries the per-instance overrides through the walk.
type scanner struct {
	root    string
	exclude map[string]bool
	entries int
	scan    Scan
}

// Collect applies the rules to one instance directory. excluded holds paths the
// operator has permanently opted out of.
func Collect(dir string, excluded []string) (Scan, error) {
	s := &scanner{root: dir, exclude: map[string]bool{}}
	for _, entry := range excluded {
		s.exclude[normalisePath(entry)] = true
	}
	if err := s.walk("", 0); err != nil {
		return s.scan, err
	}
	sort.Slice(s.scan.Files, func(a, b int) bool { return s.scan.Files[a].Path < s.scan.Files[b].Path })
	sort.Strings(s.scan.Worlds)
	return s.scan, nil
}

func (s *scanner) walk(rel string, depth int) error {
	if depth > maxWalkDepth || s.entries > maxWalkEntries {
		s.scan.Truncated = true
		return nil
	}

	entries, err := os.ReadDir(filepath.Join(s.root, filepath.FromSlash(rel)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		// A directory the panel cannot read is not a reason to lose the rest of
		// the configuration; the timeline would rather be one file short than
		// absent.
		return nil
	}
	s.entries += len(entries)

	// Rule 1: signature detection. A world is a directory holding level.dat or
	// region/, whatever it is called — "survival", "lobby", "生存服" are all
	// worlds and none of them matches world*.
	if rel != "" && looksLikeWorld(entries) {
		s.scan.Worlds = append(s.scan.Worlds, rel)
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			ext := strings.ToLower(path.Ext(entry.Name()))
			if ext != ".yml" && ext != ".yaml" {
				continue
			}
			s.add(join(rel, entry.Name()), entry)
		}
		return nil
	}

	for _, entry := range entries {
		name := entry.Name()
		child := join(rel, name)
		if s.exclude[child] {
			continue
		}

		if entry.IsDir() {
			if !s.descend(child, name) {
				continue
			}
			if err := s.walk(child, depth+1); err != nil {
				return err
			}
			continue
		}
		// Symlinks are neither followed nor recorded. Following one leaves the
		// instance directory, and recording the link itself would restore a
		// path that means something different on the next machine.
		if entry.Type()&fs.ModeSymlink != 0 {
			continue
		}
		if s.include(child, name) {
			s.add(child, entry)
		}
	}
	return nil
}

// descend decides whether a directory is walked at all.
func (s *scanner) descend(rel, name string) bool {
	if strings.HasPrefix(name, ".") || excludedDirs[strings.ToLower(name)] {
		return false
	}

	// Rule 3: inside plugins/, only a plugin's own root level and a short list
	// of subdirectories. plugins/<Name>/ is depth 2, so the decision is made on
	// the segment right below it — and only on that one, so
	// plugins/Skript/scripts/<anything>/ comes along while
	// plugins/Essentials/userdata/lang/ does not.
	parts := strings.Split(rel, "/")
	if parts[0] == "plugins" && len(parts) >= 3 {
		return pluginSubdirs[strings.ToLower(parts[2])]
	}
	return true
}

// include decides one file, given that its directory was walked.
func (s *scanner) include(rel, name string) bool {
	lower := strings.ToLower(name)
	ext := path.Ext(lower)

	// Rule 5, which beats everything: .sqlite, .sqlite3, .sqlite-wal and the
	// rest all start the same way, so that family is matched by prefix.
	if deniedExts[ext] || strings.HasPrefix(ext, ".sqlite") {
		return false
	}

	root := !strings.Contains(rel, "/")
	if root {
		// Rule 6.
		if rootDenied[lower] {
			return false
		}
		if rootScripts[ext] {
			return true
		}
	}
	// Rule 4, and rule 7 by falling through it.
	return allowedExts[ext]
}

func (s *scanner) add(rel string, entry os.DirEntry) {
	info, err := entry.Info()
	if err != nil {
		return
	}
	if !info.Mode().IsRegular() {
		return
	}
	mode := gitlite.ModeFile
	if info.Mode().Perm()&0o111 != 0 {
		// Keeping the executable bit is what makes a restored start.sh still
		// runnable.
		mode = gitlite.ModeExec
	}
	s.scan.Files = append(s.scan.Files, Candidate{Path: rel, Size: info.Size(), Mode: mode})
}

// looksLikeWorld is the signature: level.dat, or a region directory.
func looksLikeWorld(entries []os.DirEntry) bool {
	for _, entry := range entries {
		switch {
		case !entry.IsDir() && entry.Name() == "level.dat":
			return true
		case entry.IsDir() && entry.Name() == "region":
			return true
		}
	}
	return false
}

func join(rel, name string) string {
	if rel == "" {
		return name
	}
	return rel + "/" + name
}

// normalisePath puts an operator-supplied path into the form the scan speaks:
// slash-separated, relative, no leading or trailing slash.
func normalisePath(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	return path.Clean(p)
}
