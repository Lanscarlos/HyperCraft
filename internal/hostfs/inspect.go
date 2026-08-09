package hostfs

import (
	"os"
	"path/filepath"
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

	// Any one of these on its own is enough: a directory that has only ever
	// been unpacked has a jar and nothing else, and one whose jar was deleted
	// still has the world you want back.
	out.Server = out.Jar != "" || out.Properties != nil || len(out.Worlds) > 0 ||
		out.EULA != EULAMissing
	return out, nil
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
