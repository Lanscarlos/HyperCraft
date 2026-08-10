package plugin

import (
	"archive/zip"
	"encoding/json"
	"io"
	"sort"
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
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	// Description is the one or two sentences the author wrote about what the
	// plugin does. Not shown in the list — it is prose, and prose in a table
	// cell is either truncated to uselessness or wrecks the row heights — but
	// it is the thing that answers "what is this jar somebody left here".
	Description string   `json:"description,omitempty"`
	Authors     []string `json:"authors,omitempty"`
	// Platform is which server the descriptor was written for: "bukkit" covers
	// Spigot and Paper too, since they read the same plugin.yml.
	Platform string `json:"platform,omitempty"`
	// APIVersion is the game version the plugin declares support for, when the
	// descriptor carries one. It is the field that explains "why did this stop
	// loading after I upgraded the server".
	APIVersion string `json:"apiVersion,omitempty"`
	// Depend and SoftDepend are the plugin names this jar wants loaded before
	// it. The registries publish a dependency list of their own and it is a
	// different list — theirs is what the author wrote on the listing page,
	// this is what the server will actually refuse to start the plugin over.
	// Both are shown, side by side, and neither is treated as the other.
	Depend     []string `json:"depend,omitempty"`
	SoftDepend []string `json:"softDepend,omitempty"`
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
	// Which key's list is being read, when the previous line opened one. Four
	// of the fields here — authors, depend, softdepend, loadbefore — are written
	// either inline or as an indented list, and following the indented form is
	// the only way to read the dependency of a plugin whose author formatted it
	// the ordinary way.
	var listing *[]string
	// The other multi-line shape: a `description: |` or `description: >` whose
	// text is the indented lines below it. Common enough in real plugin.yml
	// files that ignoring it would mean showing no description for exactly the
	// plugins whose authors wrote the longest one.
	var block *string
	var lines []string
	var folded bool
	endBlock := func() {
		if block != nil {
			*block = joinBlock(lines, folded)
			block, lines = nil, nil
		}
	}

	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, " \t\r")

		// A block's body is literal text: a line starting with '#' is a hash
		// the author typed, and a blank line is a paragraph break. So it is
		// read before the comment and blank-line rules below, not after.
		if block != nil {
			if line == "" {
				lines = append(lines, "")
				continue
			}
			if line[0] == ' ' || line[0] == '\t' {
				lines = append(lines, strings.TrimSpace(line))
				continue
			}
			endBlock() // dedented back to column zero: the block is over
		}

		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}

		if listing != nil && strings.HasPrefix(line, " ") {
			if item := strings.TrimSpace(line); strings.HasPrefix(item, "- ") {
				if value := yamlScalar(strings.TrimPrefix(item, "- ")); value != "" {
					*listing = append(*listing, value)
				}
				continue
			}
		}
		if line[0] == ' ' || line[0] == '\t' {
			continue // nested under something else
		}
		listing = nil

		key, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value := yamlScalar(rest)

		// The list-valued keys, all read the same way: inline on this line, or
		// indented under it.
		var into *[]string
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "authors":
			into = &info.Authors
		case "depend":
			into = &info.Depend
		case "softdepend":
			into = &info.SoftDepend
		}
		if into != nil {
			if value == "" {
				listing = into
				continue
			}
			*into = append(*into, splitInlineList(value)...)
			continue
		}

		switch strings.ToLower(strings.TrimSpace(key)) {
		case "name":
			info.Name = value
		case "version":
			info.Version = value
		case "api-version":
			info.APIVersion = value
		case "description":
			if fold, ok := blockScalar(value); ok {
				block, folded, lines = &info.Description, fold, nil
				continue
			}
			info.Description = value
		case "author":
			if value != "" {
				info.Authors = append(info.Authors, value)
			}
		}
	}
	endBlock() // a descriptor that ends on its description
	return info
}

// blockScalar reports whether a value opens a YAML block — `|` or `>`, with any
// chomping or indentation indicator after it — and whether that block is folded
// into one paragraph rather than keeping the author's line breaks.
//
// Strict about what may follow the indicator on purpose: `description: |x` is a
// description that starts with a pipe, not a block, and reading it as one would
// swallow the rest of the file.
func blockScalar(value string) (fold bool, ok bool) {
	if value == "" || (value[0] != '|' && value[0] != '>') {
		return false, false
	}
	if strings.Trim(value[1:], "+-0123456789") != "" {
		return false, false
	}
	return value[0] == '>', true
}

// joinBlock puts a block scalar's lines back together the way its indicator
// asked: `>` wraps for the file's benefit and reads as one paragraph, `|` keeps
// the breaks because the author put them there.
func joinBlock(lines []string, folded bool) string {
	if !folded {
		return strings.TrimSpace(strings.Join(lines, "\n"))
	}
	var out []string
	for _, line := range lines {
		// A blank line in a folded scalar is the one break YAML keeps, and it
		// is how an author writes two paragraphs in one.
		if line == "" {
			out = append(out, "\n")
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, " "))
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
		ID           string   `json:"id"`
		Name         string   `json:"name"`
		Version      string   `json:"version"`
		Description  string   `json:"description"`
		Authors      []string `json:"authors"`
		Dependencies []struct {
			ID       string `json:"id"`
			Optional bool   `json:"optional"`
		} `json:"dependencies"`
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
	info := JarInfo{
		Name:        name,
		Version:     strings.TrimSpace(raw.Version),
		Description: strings.TrimSpace(raw.Description),
		Authors:     raw.Authors,
	}
	for _, dep := range raw.Dependencies {
		id := strings.TrimSpace(dep.ID)
		if id == "" {
			continue
		}
		if dep.Optional {
			info.SoftDepend = append(info.SoftDepend, id)
		} else {
			info.Depend = append(info.Depend, id)
		}
	}
	return info
}

func parseFabric(data []byte) JarInfo {
	var raw struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description"`
		// Fabric allows either a plain name or an object per author.
		Authors    []json.RawMessage `json:"authors"`
		Depends    map[string]any    `json:"depends"`
		Recommends map[string]any    `json:"recommends"`
		Suggests   map[string]any    `json:"suggests"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return JarInfo{}
	}

	info := JarInfo{
		Version:     strings.TrimSpace(raw.Version),
		Description: strings.TrimSpace(raw.Description),
	}
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

	// "minecraft", "java" and "fabricloader" are in every mod's depends block
	// and are not plugins anybody installs. Listing them would turn a real
	// dependency list into three rows of noise plus, sometimes, the answer.
	info.Depend = modIDs(raw.Depends)
	info.SoftDepend = append(modIDs(raw.Recommends), modIDs(raw.Suggests)...)
	return info
}

// platformIDs are the Fabric depends entries that name the environment rather
// than another mod.
var platformIDs = map[string]bool{"minecraft": true, "java": true, "fabricloader": true, "fabric": true, "fabric-api": false}

func modIDs(deps map[string]any) []string {
	out := make([]string, 0, len(deps))
	for id := range deps {
		if id = strings.TrimSpace(id); id != "" && !platformIDs[strings.ToLower(id)] {
			out = append(out, id)
		}
	}
	sort.Strings(out) // map order is random, and a dependency list that reshuffles per read is unreadable
	return out
}
