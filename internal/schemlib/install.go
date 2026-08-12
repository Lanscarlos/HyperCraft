package schemlib

import (
	"fmt"
	"path"
	"strings"
)

// Where a schematic has to land for a server to be able to load it.
//
// There is no single answer, which is the whole reason this file exists. //load
// reads one directory and which one it is depends on the editor the server runs
// and on the platform it runs on:
//
//   - WorldEdit on Bukkit and its descendants: plugins/WorldEdit/schematics
//   - FastAsyncWorldEdit, which most large servers run instead of it, keeps its
//     own: plugins/FastAsyncWorldEdit/schematics
//   - WorldEdit on Fabric and Forge is not a plugin at all and reads
//     config/worldedit/schematics
//
// Copying into the wrong one is not an error anybody sees: the file lands, the
// server starts, and //load says the schematic does not exist. So the panel
// looks at what the server actually has installed rather than picking a default
// and hoping.
const (
	// DirWorldEdit is WorldEdit's own folder on a Bukkit-family server.
	DirWorldEdit = "plugins/WorldEdit/schematics"
	// DirFastAsync is FastAsyncWorldEdit's. It is checked first: a server with
	// both installed is running FAWE, because FAWE replaces WorldEdit and the
	// leftover folder is the old one.
	DirFastAsync = "plugins/FastAsyncWorldEdit/schematics"
	// DirModded is where WorldEdit reads on Fabric and Forge.
	DirModded = "config/worldedit/schematics"
)

// Target is one directory a schematic could be installed into.
type Target struct {
	Dir string `json:"dir"`
	// Editor names what reads this directory, for the picker.
	Editor string `json:"editor"`
	// Present is true when the server already has this directory, or the plugin
	// directory above it. That is the whole signal: an operator should not have
	// to know which editor their server runs, and a server that has never had
	// WorldEdit should not silently grow a plugins/WorldEdit folder because
	// somebody pressed 安装到实例.
	Present bool `json:"present"`
}

// Targets ranks the places a schematic can be installed into on one server,
// best first. `exists` answers whether a path inside the instance is there;
// the caller supplies it because only it has the instance's confined view of
// the filesystem.
//
// Every candidate is always returned, present or not, so the picker can offer
// "install it anyway" for a server whose editor is not installed yet — putting
// the builds in place before the plugin is a normal way round for somebody
// setting a server up.
func Targets(exists func(rel string) bool) []Target {
	candidates := []struct{ dir, editor, marker string }{
		{DirFastAsync, "FastAsyncWorldEdit", "plugins/FastAsyncWorldEdit"},
		{DirWorldEdit, "WorldEdit", "plugins/WorldEdit"},
		{DirModded, "WorldEdit（Fabric / Forge）", "config/worldedit"},
	}

	out := make([]Target, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, Target{
			Dir:     candidate.dir,
			Editor:  candidate.editor,
			Present: exists(candidate.dir) || exists(candidate.marker),
		})
	}

	// A present directory outranks the fixed order above: the point of the
	// ranking is to name the editor this server actually runs. Stable within
	// each half, so a server with neither still offers FAWE first.
	best := make([]Target, 0, len(out))
	for _, target := range out {
		if target.Present {
			best = append(best, target)
		}
	}
	for _, target := range out {
		if !target.Present {
			best = append(best, target)
		}
	}
	return best
}

// DefaultTarget is where an install with no directory named goes.
func DefaultTarget(exists func(rel string) bool) string {
	return Targets(exists)[0].Dir
}

// CleanTargetDir validates a directory an install was asked to write into. It
// is a path inside the instance, so the same rules the file manager applies
// hold: relative, no escaping, no absolute paths.
func CleanTargetDir(dir string) (string, error) {
	dir = strings.TrimSpace(strings.ReplaceAll(dir, "\\", "/"))
	dir = strings.Trim(dir, "/")
	if dir == "" {
		return "", fmt.Errorf("%w: 目标目录是空的", ErrInvalidID)
	}
	if strings.Contains(dir, "\x00") {
		return "", fmt.Errorf("%w: %q", ErrInvalidID, dir)
	}
	cleaned := path.Clean(dir)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("%w: %q 必须在实例目录里面", ErrInvalidID, dir)
	}
	return cleaned, nil
}
