package plugin

import (
	"archive/zip"
	"encoding/json"
	"io"
	"strings"
)

// Reading what a jar says about itself.
//
// A jar in an instance's plugins directory that the panel did not install is
// still a plugin the server is going to load, and listing it as
// "EssentialsX-2.20.1-dev+12-abc123f" — the file name, which is all the panel
// would otherwise know — is not the same as saying what it is. Every server
// platform requires the jar to declare itself in a descriptor file, so the
// answer is a few kilobytes away: name, version, and who wrote it, exactly as
// the server itself will read them at startup.
//
// Only the descriptor is read. Nothing is executed, nothing is unpacked, and a
// jar that is not a plugin — or not a zip at all — simply has no answer.

// maxDescriptorBytes caps one descriptor. These files are a dozen lines; the
// cap is there so a hostile jar cannot hand the panel a gigabyte of YAML.
const maxDescriptorBytes = 512 << 10

// JarInfo is what a jar declares about itself.
type JarInfo struct {
	Name    string   `json:"name,omitempty"`
	Version string   `json:"version,omitempty"`
	Authors []string `json:"authors,omitempty"`
	// Platform is which server the descriptor was written for: "bukkit" covers
	// Spigot and Paper too, since they read the same plugin.yml.
	Platform string `json:"platform,omitempty"`
	// APIVersion is the game version the plugin declares support for, when the
	// descriptor carries one. It is the field that explains "why did this stop
	// loading after I upgraded the server".
	APIVersion string `json:"apiVersion,omitempty"`
}

// Empty reports whether nothing useful was found.
func (j JarInfo) Empty() bool { return j.Name == "" && j.Version == "" }

// descriptors are the files a jar can declare itself in, most specific first.
//
// Order matters where a jar carries two: a Paper plugin that also ships the old
// plugin.yml for compatibility is a Paper plugin, and the newer file is the one
// its author maintains.
//
// Forge and NeoForge are missing on purpose. Their descriptor is TOML, which
// needs a real parser to read safely, and a wrong answer about a mod is worse
// than the file name the panel already shows.
var descriptors = []struct {
	path     string
	platform string
	parse    func([]byte) JarInfo
}{
	{"paper-plugin.yml", "paper", parsePluginYAML},
	{"plugin.yml", "bukkit", parsePluginYAML},
	{"bungee.yml", "bungeecord", parsePluginYAML},
	{"velocity-plugin.json", "velocity", parseVelocity},
	{"fabric.mod.json", "fabric", parseFabric},
}

// ReadJarInfo reads a jar's own description of itself. The second result is
// false when the file is not a readable archive or declares nothing.
func ReadJarInfo(reader io.ReaderAt, size int64) (JarInfo, bool) {
	if size <= 0 {
		return JarInfo{}, false
	}
	archive, err := zip.NewReader(reader, size)
	if err != nil {
		return JarInfo{}, false
	}

	files := make(map[string]*zip.File, len(archive.File))
	for _, file := range archive.File {
		// Descriptors live at the root of the jar; a plugin.yml bundled inside
		// a shaded dependency describes that dependency, not this plugin.
		if !strings.Contains(file.Name, "/") {
			files[file.Name] = file
		}
	}

	for _, descriptor := range descriptors {
		file, ok := files[descriptor.path]
		if !ok {
			continue
		}
		data, err := readZipEntry(file)
		if err != nil {
			continue
		}
		info := descriptor.parse(data)
		if info.Empty() {
			continue
		}
		info.Platform = descriptor.platform
		return info, true
	}
	return JarInfo{}, false
}

func readZipEntry(file *zip.File) ([]byte, error) {
	body, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return io.ReadAll(io.LimitReader(body, maxDescriptorBytes))
}

// parsePluginYAML reads the top-level scalars of a plugin descriptor.
//
// This is not a YAML parser and does not pretend to be one. What it reads is
// the shape every plugin.yml in existence has — a handful of "key: value" lines
// at column zero — and it deliberately ignores everything nested under a key,
// which in these files is commands and permissions: entire sections the panel
// has no use for and no business half-understanding. A jar whose descriptor is
// stranger than that gets no answer rather than a wrong one.
func parsePluginYAML(data []byte) JarInfo {
	var info JarInfo
	var inAuthors bool

	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, " \t\r")
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}

		// A list item under "authors:", which is the one nested shape worth
		// following: it is how most plugins name more than one author.
		if inAuthors && strings.HasPrefix(line, " ") {
			if item := strings.TrimSpace(line); strings.HasPrefix(item, "- ") {
				if value := yamlScalar(strings.TrimPrefix(item, "- ")); value != "" {
					info.Authors = append(info.Authors, value)
				}
				continue
			}
		}
		if line[0] == ' ' || line[0] == '\t' {
			continue // nested under something else
		}
		inAuthors = false

		key, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value := yamlScalar(rest)

		switch strings.ToLower(strings.TrimSpace(key)) {
		case "name":
			info.Name = value
		case "version":
			info.Version = value
		case "api-version":
			info.APIVersion = value
		case "author":
			if value != "" {
				info.Authors = append(info.Authors, value)
			}
		case "authors":
			// Either "authors: [a, b]" on one line or a list below it.
			if value == "" {
				inAuthors = true
				continue
			}
			info.Authors = append(info.Authors, splitInlineList(value)...)
		}
	}
	return info
}

// yamlScalar strips the quoting and trailing comment off a scalar value.
func yamlScalar(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if quote := value[0]; quote == '\'' || quote == '"' {
		if end := strings.IndexByte(value[1:], quote); end >= 0 {
			return value[1 : end+1]
		}
		return strings.Trim(value, `'"`)
	}
	// An unquoted scalar ends at a comment, but only one that follows a space —
	// "1.0#3" is a version, "1.0 # notes" is not.
	if hash := strings.Index(value, " #"); hash >= 0 {
		value = value[:hash]
	}
	return strings.TrimSpace(value)
}

// splitInlineList reads a "[a, b]" or "a, b" list of scalars.
func splitInlineList(raw string) []string {
	value := strings.TrimSpace(raw)
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")

	var out []string
	for _, part := range strings.Split(value, ",") {
		if item := yamlScalar(part); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func parseVelocity(data []byte) JarInfo {
	var raw struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		Version string   `json:"version"`
		Authors []string `json:"authors"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return JarInfo{}
	}
	name := strings.TrimSpace(raw.Name)
	if name == "" {
		// The id is required where the name is optional, and it is what the
		// server itself calls the plugin when the name is absent.
		name = strings.TrimSpace(raw.ID)
	}
	return JarInfo{Name: name, Version: strings.TrimSpace(raw.Version), Authors: raw.Authors}
}

func parseFabric(data []byte) JarInfo {
	var raw struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Version string `json:"version"`
		// Fabric allows either a plain name or an object per author.
		Authors []json.RawMessage `json:"authors"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return JarInfo{}
	}

	info := JarInfo{Version: strings.TrimSpace(raw.Version)}
	if info.Name = strings.TrimSpace(raw.Name); info.Name == "" {
		info.Name = strings.TrimSpace(raw.ID)
	}
	for _, author := range raw.Authors {
		var plain string
		if err := json.Unmarshal(author, &plain); err == nil {
			if plain = strings.TrimSpace(plain); plain != "" {
				info.Authors = append(info.Authors, plain)
			}
			continue
		}
		var object struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(author, &object); err == nil {
			if name := strings.TrimSpace(object.Name); name != "" {
				info.Authors = append(info.Authors, name)
			}
		}
	}
	return info
}
