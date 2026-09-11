package hostfs

import (
	"os"
	"path/filepath"
	"runtime"
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
	// LaunchScript is the start script the installer wrote, relative to the
	// directory. Empty when there is none — which for a modern Forge install
	// means someone deleted it.
	LaunchScript string `json:"launchScript,omitempty"`
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

// launchScripts are what those installers write, this platform's first.
var launchScripts = []string{"run.sh", "run.bat"}

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
	out.LaunchScript = findLaunchScript(listing.Path)

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

// findLaunchScript returns the installer's start script, this platform's
// first: a Windows host cannot run run.sh and a Linux host will not run
// run.bat, and offering the wrong one produces a start that fails for a reason
// that has nothing to do with the server.
func findLaunchScript(dir string) string {
	ordered := launchScripts
	if runtime.GOOS == "windows" {
		ordered = []string{"run.bat", "run.sh"}
	}
	for _, name := range ordered {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
			return name
		}
	}
	return ""
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
