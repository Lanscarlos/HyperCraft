package hostfs

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/mcprops"
)

// Properties are the few settings worth showing about a server that already
// exists. They come from its server.properties and are read, never written:
// importing a directory must not change anything in it.
type Properties struct {
	MOTD       string `json:"motd,omitempty"`
	Port       string `json:"port,omitempty"`
	LevelName  string `json:"levelName,omitempty"`
	MaxPlayers string `json:"maxPlayers,omitempty"`
}

// EULA state, as the three answers the file can give.
const (
	EULAAccepted = "accepted"
	EULADeclined = "declined"
	EULAMissing  = "missing"
)

// Inspection is what can be told about a directory that may already hold a
// Minecraft server, without starting anything or writing to it.
//
// It backs 「导入现有目录」: someone who already runs a server by hand, or is
// moving off another panel, has a directory full of worlds and plugins and
// wants the panel to adopt it rather than to build a new one beside it. The
// answer the dialog needs is "is this a server, which jar starts it, and what
// is it called" — everything here is one of those three.
type Inspection struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	// Error explains a directory that exists but could not be read, usually
	// permissions. The rest of the fields are empty in that case.
	Error string `json:"error,omitempty"`
	// Name is what to call the instance if the operator does not say: the
	// directory's own name, which is nearly always the server's name too.
	Name string `json:"name"`
	Jars []Jar  `json:"jars"`
	// Jar is the one worth launching, picked by name and then by size. Empty
	// when the directory holds no jar at all.
	Jar string `json:"jar,omitempty"`
	// Properties is nil when there is no server.properties to read.
	Properties *Properties `json:"properties,omitempty"`
	EULA       string      `json:"eula"`
	// Worlds are the level directories found here — the thing that makes this
	// an existing server rather than an empty folder.
	Worlds  []string `json:"worlds,omitempty"`
	Plugins int      `json:"plugins"`
	Mods    int      `json:"mods"`
	// Server is the panel's verdict: something in here says a server has run
	// or is meant to. A false verdict is not a refusal — the operator may know
	// better — it only changes what the dialog says.
	Server bool `json:"server"`
	// Proxy says this directory holds a Velocity proxy rather than a world
	// server. It decides which kind the imported instance is created as, and
	// with it which config page, launch defaults and stop command it gets —
	// importing a proxy as a server is a mistake that only shows up as a
	// failure to start.
	Proxy bool `json:"proxy"`
	// Loader and GameVersion are what the directory's own layout says this
	// server is. Forge from 1.17 on has no runnable jar at all — the installer
	// leaves a libraries tree and a run.sh — so for these the usual "read the
	// jar's name" has nothing to read, and the layout is the only evidence
	// there is.
	Loader      string `json:"loader,omitempty"`
	GameVersion string `json:"gameVersion,omitempty"`
	// LaunchScript is the best candidate, the first of LaunchScripts, kept as
	// its own field because that is the one the import dialog offers first.
	LaunchScript string `json:"launchScript,omitempty"`
	// LaunchScripts are every file here that looks like a start script, best
	// first. They are read for the launch settings inside them, never run, so
	// a directory holding several is a list to choose from rather than an
	// ambiguity to resolve.
	LaunchScripts []string `json:"launchScripts,omitempty"`
}

// modLoaders are the layouts that identify a server with no jar to read a name
// off, in the order they are looked for. The path is where each installer puts
// its own artifacts, and the directory under it is named for the version.
var modLoaders = []struct {
	loader string
	path   string
	// gameVersioned marks a loader whose version directory begins with the
	// Minecraft version — Forge's "1.20.1-47.2.0". NeoForge's "21.1.72" is its
	// own version scheme and says nothing about the game version, so guessing
	// one from it would be worse than leaving it blank.
	gameVersioned bool
}{
	{"forge", "libraries/net/minecraftforge/forge", true},
	{"neoforge", "libraries/net/neoforged/neoforge", false},
}

// launchStems are the names a start script goes by, best first. Matched
// against the file name with its extension removed, as a prefix, so
// start_server.sh and 启动服务器.bat are covered without listing every variant
// anyone has ever typed.
//
// run is first on purpose: it is what Forge's and NeoForge's installers write,
// so where it exists it is the server's real launch and not somebody's helper.
var launchStems = []string{"run", "start", "launch", "server", "启动", "开服"}

// notLaunches are the scripts that live in the same directory and must never
// be offered. Matched anywhere in the name and checked first, because the
// mistake they prevent is the expensive one: an operator picks 安装.sh out of
// the list and the panel adopts their installer as the way to start the
// server. Failing to spot a launch script only costs them filling the form in
// by hand.
var notLaunches = []string{
	"install", "安装", "setup", "update", "更新", "upgrade", "stop", "停止",
	"restart", "重启", "backup", "备份", "uninstall", "卸载",
}

// serverJarHints are the file names a server jar is likely to have, best first.
// A directory can easily hold a dozen jars (a modpack's libraries, an old
// backup), so the name decides before the size does.
var serverJarHints = []string{
	"server.jar",
	"paper", "purpur", "folia", "pufferfish", "spigot", "craftbukkit", "leaves",
	"velocity", "waterfall", "bungeecord",
	"fabric-server", "forge", "neoforge", "quilt",
	"minecraft_server",
}

// worldMarkers identify a level directory: a world always has a level.dat, and
// nothing else does.
const worldMarker = "level.dat"

// Inspect describes a directory as a candidate for import. The path must be
// absolute; a path that does not exist is not an error, it is an answer.
func Inspect(dir string) (Inspection, error) {
	listing, err := List(dir)
	if err != nil {
		return Inspection{}, err
	}

	out := Inspection{
		Path:   listing.Path,
		Exists: listing.Exists,
		Error:  listing.Error,
		Name:   filepath.Base(listing.Path),
		Jars:   listing.Jars,
		EULA:   EULAMissing,
	}
	if !listing.Exists || listing.Error != "" {
		return out, nil
	}

	out.Jar = pickServerJar(listing.Jars)
	out.EULA = readEULA(filepath.Join(listing.Path, "eula.txt"))
	if props := readProperties(filepath.Join(listing.Path, "server.properties")); props != nil {
		out.Properties = props
	}

	for _, entry := range listing.Entries {
		if !entry.IsDir {
			continue
		}
		switch strings.ToLower(entry.Name) {
		case "plugins":
			out.Plugins = countJars(entry.Path)
		case "mods":
			out.Mods = countJars(entry.Path)
		}
		if _, err := os.Stat(filepath.Join(entry.Path, worldMarker)); err == nil {
			out.Worlds = append(out.Worlds, entry.Name)
		}
	}
	sort.Strings(out.Worlds)

	out.Loader, out.GameVersion = detectLoader(listing.Path)
	out.LaunchScripts = findLaunchScripts(listing.Entries)
	if len(out.LaunchScripts) > 0 {
		out.LaunchScript = out.LaunchScripts[0]
	}

	// Any one of these on its own is enough: a directory that has only ever
	// been unpacked has a jar and nothing else, and one whose jar was deleted
	// still has the world you want back. A Forge install is the case with no
	// jar at any point, which is why its layout counts on its own.
	out.Server = out.Jar != "" || out.Properties != nil || len(out.Worlds) > 0 ||
		out.EULA != EULAMissing || out.Loader != ""
	// Its own config file first: a jar renamed to server.jar says nothing, and
	// a directory Velocity has run in always has a velocity.toml.
	if _, err := os.Stat(filepath.Join(listing.Path, "velocity.toml")); err == nil {
		out.Proxy = true
	} else {
		out.Proxy = strings.HasPrefix(strings.ToLower(out.Jar), "velocity")
	}
	return out, nil
}

// detectLoader reads the libraries tree an installer leaves behind.
//
// This is the one detection that works for a server the panel does not build a
// command line for: there is no jar name, and version_history.json is written
// only by the Paper family.
func detectLoader(dir string) (string, string) {
	for _, candidate := range modLoaders {
		entries, err := os.ReadDir(filepath.Join(dir, filepath.FromSlash(candidate.path)))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			version := ""
			if candidate.gameVersioned {
				version, _, _ = strings.Cut(entry.Name(), "-")
			}
			return candidate.loader, version
		}
	}
	return "", ""
}

// scriptExts are the extensions worth reading, this platform's first.
//
// Both kinds are offered on either host. The panel reads these scripts for the
// launch settings written inside them and never executes them, so a run.bat on
// a Linux box is still a perfectly good answer to "what does this server start
// with" — it is just the less likely one, which is what the ordering says.
func scriptExts() []string {
	if runtime.GOOS == "windows" {
		return []string{".bat", ".cmd", ".sh"}
	}
	return []string{".sh", ".bat", ".cmd"}
}

// findLaunchScripts returns every file here that looks like a start script,
// best first.
func findLaunchScripts(entries []Entry) []string {
	exts := scriptExts()

	type candidate struct {
		name string
		stem int
		ext  int
	}
	var found []candidate

	for _, entry := range entries {
		if entry.IsDir {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name))
		extRank := slices.Index(exts, ext)
		if extRank < 0 {
			continue
		}
		stem := strings.ToLower(strings.TrimSuffix(entry.Name, filepath.Ext(entry.Name)))
		if slices.ContainsFunc(notLaunches, func(bad string) bool { return strings.Contains(stem, bad) }) {
			continue
		}
		stemRank := slices.IndexFunc(launchStems, func(good string) bool { return strings.HasPrefix(stem, good) })
		if stemRank < 0 {
			continue
		}
		found = append(found, candidate{name: entry.Name, stem: stemRank, ext: extRank})
	}

	sort.Slice(found, func(i, j int) bool {
		a, b := found[i], found[j]
		if a.stem != b.stem {
			return a.stem < b.stem
		}
		if a.ext != b.ext {
			return a.ext < b.ext
		}
		return a.name < b.name
	})

	names := make([]string, len(found))
	for i, c := range found {
		names[i] = c.name
	}
	return names
}

// pickServerJar chooses the jar most likely to start this server: the best
// name match, and among equals the largest file — a server jar is bigger than
// anything else that shares a directory with it.
func pickServerJar(jars []Jar) string {
	best, bestRank, bestSize := "", len(serverJarHints), int64(-1)
	for _, jar := range jars {
		name := strings.ToLower(jar.Name)
		rank := len(serverJarHints)
		for n, hint := range serverJarHints {
			if strings.HasPrefix(name, hint) || name == hint {
				rank = n
				break
			}
		}
		if rank < bestRank || (rank == bestRank && jar.Size > bestSize) {
			best, bestRank, bestSize = jar.Name, rank, jar.Size
		}
	}
	return best
}

// readEULA answers the one question the file exists to answer. Anything it
// cannot read counts as missing, which is also what the panel shows for a
// directory that has never been started.
func readEULA(path string) string {
	file, err := mcprops.Load(path)
	if err != nil {
		return EULAMissing
	}
	value, ok := file.Get("eula")
	if !ok {
		return EULAMissing
	}
	if strings.EqualFold(strings.TrimSpace(value), "true") {
		return EULAAccepted
	}
	return EULADeclined
}

// readProperties returns nil when the file is absent, which is how the caller
// tells "never started" from "started and left at the defaults".
func readProperties(path string) *Properties {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	file, err := mcprops.Load(path)
	if err != nil {
		return nil
	}
	get := func(key string) string {
		value, _ := file.Get(key)
		return strings.TrimSpace(value)
	}
	return &Properties{
		MOTD:       get("motd"),
		Port:       get("server-port"),
		LevelName:  get("level-name"),
		MaxPlayers: get("max-players"),
	}
}

// countJars is how many plugins or mods a directory holds. Cheap enough to run
// on two directories per inspection, and it is the number that tells an
// operator this is the server they meant.
func countJars(dir string) int {
	members, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, member := range members {
		if !member.IsDir() && strings.EqualFold(filepath.Ext(member.Name()), ".jar") {
			count++
		}
	}
	return count
}
