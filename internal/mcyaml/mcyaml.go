// Package mcyaml reads and writes the YAML configuration files a Minecraft
// server keeps beside server.properties — bukkit.yml, spigot.yml, and Paper's
// paper.yml or config/paper-global.yml.
//
// It is to those files what mcprops is to server.properties and velocitycfg is
// to velocity.toml, and it exists for the same reason all three do: the panel
// edits files the operator also hand-edits, so comments, blank lines, key order
// and indentation have to survive a save. Paper's files in particular are
// mostly comments — throwing them away would take the documentation with them.
//
// This is deliberately not a YAML library. It understands the one shape these
// files are written in — nested maps of scalars, two-space indent — and treats
// everything else (lists, block scalars, anchors) as text to be preserved
// rather than parsed. A key whose value it cannot read is a key it refuses to
// write, which is the honest failure: the panel simply does not offer to edit
// that part of the file.
package mcyaml

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// DefaultIndent is the step used when a file gives no hint of its own — an
// empty file, or one that has no nesting yet. Every file Bukkit, Spigot and
// Paper write uses two spaces.
const DefaultIndent = 2

// ErrNotScalar is returned for a path the package will not write: a list, a
// block scalar, or a key that already holds a value and is being asked to hold
// a map instead. Refusing is the point — the alternative is corrupting a file
// the server has to parse.
var ErrNotScalar = errors.New("mcyaml: path does not hold a scalar")

// Entry is one setting as the API passes it around: a dotted path and the
// decoded value at it.
type Entry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// File is a parsed YAML config that remembers its original layout.
type File struct {
	lines []line
	// indent is the file's own nesting step, so keys inserted into a file
	// written with four spaces are not indented with two.
	indent int
}

// line is one source line: either verbatim text (a comment, a blank line, a
// list item, anything unrecognised) or a key with its value.
type line struct {
	raw string // used when key == ""

	indent  int
	keyRaw  string // as written, quoting included
	key     string // decoded
	value   string // raw scalar text; "" when the key only opens a map
	comment string // trailing comment, kept verbatim
	path    []string

	// opaque marks a key whose value must not be rewritten — a block scalar or
	// a list. It is still parsed so its children are placed correctly.
	opaque bool
}

func (l line) isKey() bool { return l.key != "" }

// Parse reads a YAML config from r.
func Parse(r io.Reader) (*File, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var raw []string
	for scanner.Scan() {
		raw = append(raw, strings.TrimRight(scanner.Text(), "\r"))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	f := &File{}
	// stack holds the enclosing keys, innermost last, so a key's full path is
	// read off the indentation rather than guessed from the file's shape.
	type frame struct {
		indent int
		key    string
	}
	var stack []frame

	for index := 0; index < len(raw); index++ {
		text := raw[index]
		trimmed := strings.TrimLeft(text, " ")
		indent := len(text) - len(trimmed)

		// A tab-indented file is not YAML the server can load either, so it is
		// kept verbatim rather than guessed at.
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(text, "\t") ||
			strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "---") {
			// A list item belongs to the key above it, which therefore holds a
			// list and must never be written as a scalar.
			if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
				f.markLastOpaque()
			}
			f.lines = append(f.lines, line{raw: text})
			continue
		}

		keyRaw, value, ok := splitKey(trimmed)
		if !ok {
			f.lines = append(f.lines, line{raw: text})
			continue
		}

		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 {
			// The file's own step, so keys added to a four-space Paper config
			// are not indented with two.
			if step := indent - stack[len(stack)-1].indent; step > 0 && (f.indent == 0 || step < f.indent) {
				f.indent = step
			}
		}

		key := decodeKey(keyRaw)
		path := make([]string, 0, len(stack)+1)
		for _, entry := range stack {
			path = append(path, entry.key)
		}
		path = append(path, key)

		value, comment := splitComment(value)
		value = strings.TrimSpace(value)

		current := line{
			indent:  indent,
			keyRaw:  keyRaw,
			key:     key,
			value:   value,
			comment: comment,
			path:    path,
		}

		// A block scalar owns every deeper line after it. Reading those as keys
		// would invent settings out of a MOTD's second line.
		if strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">") {
			current.opaque = true
			f.lines = append(f.lines, current)
			for index+1 < len(raw) {
				next := raw[index+1]
				body := strings.TrimLeft(next, " ")
				if body != "" && len(next)-len(body) <= indent {
					break
				}
				index++
				f.lines = append(f.lines, line{raw: next})
			}
			continue
		}
		// An inline flow value — [a, b] or {x: 1} — is a value this package
		// preserves but does not take apart.
		if strings.HasPrefix(value, "[") || strings.HasPrefix(value, "{") {
			current.opaque = true
		}

		f.lines = append(f.lines, current)
		stack = append(stack, frame{indent: indent, key: key})
	}
	return f, nil
}

// markLastOpaque flags the key a list item belongs to. The item's own
// indentation is not consulted: YAML allows a list to sit at its parent's
// indent, so the enclosing key is the last one parsed either way.
func (f *File) markLastOpaque() {
	for index := len(f.lines) - 1; index >= 0; index-- {
		if f.lines[index].isKey() {
			f.lines[index].opaque = true
			return
		}
	}
}

// Load reads a config file. A missing file parses as empty, which is what lets
// the panel configure a server that has never been started — Paper writes its
// config on first boot, and waiting for that would mean an empty page at
// exactly the moment there is most to fill in.
func Load(path string) (*File, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &File{}, nil
		}
		return nil, err
	}
	defer file.Close()
	return Parse(file)
}

// Empty reports whether the file holds no keys at all.
func (f *File) Empty() bool {
	for _, l := range f.lines {
		if l.isKey() {
			return false
		}
	}
	return true
}

// Render returns the file as it should be written.
func (f *File) Render() string {
	var out strings.Builder
	for _, l := range f.lines {
		switch {
		case !l.isKey():
			out.WriteString(l.raw)
		case l.value == "":
			out.WriteString(strings.Repeat(" ", l.indent))
			out.WriteString(l.keyRaw)
			out.WriteString(":")
			out.WriteString(l.comment)
		default:
			out.WriteString(strings.Repeat(" ", l.indent))
			out.WriteString(l.keyRaw)
			out.WriteString(": ")
			out.WriteString(l.value)
			out.WriteString(l.comment)
		}
		out.WriteString("\n")
	}
	return out.String()
}

// Save writes the file, creating it if it does not exist.
func (f *File) Save(path string) error {
	return os.WriteFile(path, []byte(f.Render()), 0o644)
}

// ------------------------------------------------------------------ reading

// Get returns the decoded scalar at a dotted path — "settings.bungeecord".
// A key that holds a map, a list or a block scalar is reported as absent: the
// caller asked for a value, and there is not one there to give.
func (f *File) Get(path string) (string, bool) {
	index := f.find(split(path))
	if index < 0 || f.lines[index].opaque || f.lines[index].value == "" {
		return "", false
	}
	return DecodeScalar(f.lines[index].value), true
}

// Bool reads a path as a boolean, falling back to fallback when the key is
// absent or holds something that is not one.
func (f *File) Bool(path string, fallback bool) bool {
	value, ok := f.Get(path)
	if !ok {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "on":
		return true
	case "false", "no", "off":
		return false
	default:
		return fallback
	}
}

// Has reports whether the path names a key at all, map or scalar.
func (f *File) Has(path string) bool { return f.find(split(path)) >= 0 }

func (f *File) find(segments []string) int {
	if len(segments) == 0 {
		return -1
	}
	for index, l := range f.lines {
		if l.isKey() && samePath(l.path, segments) {
			return index
		}
	}
	return -1
}

func samePath(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

// ------------------------------------------------------------------ writing

// SetString writes a string value, quoting it when YAML would otherwise read
// it as something else.
func (f *File) SetString(path, value string) error {
	return f.SetRaw(path, EncodeString(value))
}

// SetBool writes true or false.
func (f *File) SetBool(path string, value bool) error {
	if value {
		return f.SetRaw(path, "true")
	}
	return f.SetRaw(path, "false")
}

// SetRaw writes a value that is already YAML — a number, a boolean, a string
// the caller has quoted itself.
//
// A path whose parents do not exist yet is created: Paper only writes the keys
// it has an opinion about, so proxies.velocity.secret regularly has to be added
// to a file that has never heard of proxies.
func (f *File) SetRaw(path, raw string) error {
	segments := split(path)
	if len(segments) == 0 {
		return fmt.Errorf("mcyaml: empty path")
	}

	if index := f.find(segments); index >= 0 {
		// A key with a block under it is a map. Writing a scalar over it would
		// leave its children indented under a value, which is a file the server
		// refuses to load.
		if f.lines[index].opaque || f.blockEnd(index) > index+1 {
			return fmt.Errorf("%w: %s", ErrNotScalar, path)
		}
		f.lines[index].value = raw
		return nil
	}

	// The deepest ancestor the file already has. Everything below it is written
	// out fresh, in order, under the block that ancestor owns.
	depth := len(segments) - 1
	anchor := -1
	for ; depth > 0; depth-- {
		if anchor = f.find(segments[:depth]); anchor >= 0 {
			break
		}
	}

	indent, at := 0, len(f.lines)
	if anchor >= 0 {
		parent := f.lines[anchor]
		if parent.opaque || parent.value != "" {
			return fmt.Errorf("%w: %s", ErrNotScalar, strings.Join(segments[:depth], "."))
		}
		indent = parent.indent + f.step()
		at = f.blockEnd(anchor)
	}

	fresh := make([]line, 0, len(segments)-depth)
	for offset, segment := range segments[depth:] {
		entry := line{
			indent: indent + offset*f.step(),
			keyRaw: encodeKey(segment),
			key:    segment,
			path:   append(append([]string{}, segments[:depth+offset]...), segment),
		}
		if depth+offset == len(segments)-1 {
			entry.value = raw
		}
		fresh = append(fresh, entry)
	}

	f.lines = append(f.lines[:at], append(fresh, f.lines[at:]...)...)
	return nil
}

// blockEnd is the index just past the last line belonging to the key at index.
// Comments and blank lines that trail the block stay below whatever is
// inserted, since they usually introduce the *next* key rather than close the
// previous one.
func (f *File) blockEnd(index int) int {
	parent := f.lines[index].indent
	end := index + 1
	for cursor := index + 1; cursor < len(f.lines); cursor++ {
		l := f.lines[cursor]
		if !l.isKey() {
			trimmed := strings.TrimLeft(l.raw, " ")
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue // undecided until we see what comes after it
			}
			if len(l.raw)-len(trimmed) <= parent {
				break
			}
			end = cursor + 1
			continue
		}
		if l.indent <= parent {
			break
		}
		end = cursor + 1
	}
	return end
}

func (f *File) step() int {
	if f.indent <= 0 {
		return DefaultIndent
	}
	return f.indent
}

// ------------------------------------------------------------------- syntax

func split(path string) []string {
	out := []string{}
	for _, segment := range strings.Split(path, ".") {
		if segment = strings.TrimSpace(segment); segment != "" {
			out = append(out, segment)
		}
	}
	return out
}

// splitKey cuts "key: value" at the first colon that ends a key — one followed
// by a space or by the end of the line. A colon inside a quoted key, or inside
// a value such as a MOTD, is not one.
func splitKey(trimmed string) (key, value string, ok bool) {
	quote := byte(0)
	for index := 0; index < len(trimmed); index++ {
		char := trimmed[index]
		switch {
		case quote != 0:
			if char == '\\' && quote == '"' {
				index++
			} else if char == quote {
				quote = 0
			}
		case char == '"' || char == '\'':
			if index > 0 {
				// A quote in the middle of a bare key is not a quoted key; it
				// is a line this package should leave alone.
				return "", "", false
			}
			quote = char
		case char == '#':
			return "", "", false
		case char == ':':
			if index+1 < len(trimmed) && trimmed[index+1] != ' ' {
				// "12:30" and "http://…" are values, not key separators.
				return "", "", false
			}
			key = strings.TrimSpace(trimmed[:index])
			if key == "" {
				return "", "", false
			}
			return key, strings.TrimSpace(trimmed[index+1:]), true
		}
	}
	return "", "", false
}

// splitComment separates a value from a comment written after it. In YAML a
// '#' only starts a comment when a space comes before it, so a bare colour code
// like #ff0000 stays part of the value.
func splitComment(value string) (string, string) {
	quote := byte(0)
	for index := 0; index < len(value); index++ {
		char := value[index]
		switch {
		case quote != 0:
			if char == '\\' && quote == '"' {
				index++
			} else if char == quote {
				quote = 0
			}
		case char == '"' || char == '\'':
			quote = char
		case char == '#' && index > 0 && (value[index-1] == ' ' || value[index-1] == '\t'):
			// The space before the comment belongs to the comment: the value is
			// re-rendered with its own spacing.
			return value[:index-1], " " + value[index:]
		}
	}
	return value, ""
}

func decodeKey(raw string) string {
	if len(raw) >= 2 && (raw[0] == '"' || raw[0] == '\'') && raw[len(raw)-1] == raw[0] {
		return DecodeScalar(raw)
	}
	return raw
}

// encodeKey quotes a key YAML would otherwise misread. Paper's feature-seeds
// and Spigot's per-world sections are the ones that need it.
func encodeKey(key string) string {
	if key == "" {
		return `""`
	}
	for _, char := range key {
		bare := char == '-' || char == '_' || char == '/' ||
			(char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9')
		if !bare {
			return EncodeString(key)
		}
	}
	return key
}

// DecodeScalar turns a raw YAML scalar into the text the panel shows.
func DecodeScalar(raw string) string {
	raw = strings.TrimSpace(raw)
	switch {
	case len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'':
		// A single-quoted YAML string escapes one thing and one thing only.
		return strings.ReplaceAll(raw[1:len(raw)-1], "''", "'")
	case len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"':
		return unescape(raw[1 : len(raw)-1])
	default:
		return raw
	}
}

// EncodeString writes a value as YAML, quoting whatever would not read back as
// the same string. These files are UTF-8, so Chinese goes in literally —
// unlike server.properties, which has to escape it.
func EncodeString(value string) string {
	if needsQuotes(value) {
		var out strings.Builder
		out.WriteByte('"')
		for _, char := range value {
			switch char {
			case '"':
				out.WriteString(`\"`)
			case '\\':
				out.WriteString(`\\`)
			case '\n':
				out.WriteString(`\n`)
			case '\r':
				out.WriteString(`\r`)
			case '\t':
				out.WriteString(`\t`)
			default:
				if char < 0x20 || char == 0x7f {
					fmt.Fprintf(&out, `\u%04X`, char)
					continue
				}
				out.WriteRune(char)
			}
		}
		out.WriteByte('"')
		return out.String()
	}
	return value
}

// needsQuotes leans towards quoting: two extra characters cost nothing, while
// a value that needed them and did not get them turns into a parse error the
// server reports as "could not load bukkit.yml".
//
// It stops short of quoting everything, though, and the case that decides it is
// a MOTD: "<#09add3>Hello" is a plain scalar YAML reads back exactly as
// written, and wrapping it in quotes every time the page is saved would be the
// panel visibly rewriting a line the operator did not touch.
func needsQuotes(value string) bool {
	if value == "" || value != strings.TrimSpace(value) {
		return true
	}
	switch strings.ToLower(value) {
	case "true", "false", "yes", "no", "on", "off", "null", "~":
		return true
	}
	// A value that would read back as a number has to say it is text.
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return true
	}
	if strings.ContainsAny(value, "\n\r\t\"'") {
		return true
	}
	// ": " ends a key and " #" starts a comment; a trailing colon does both.
	if strings.Contains(value, ": ") || strings.HasSuffix(value, ":") || strings.Contains(value, " #") {
		return true
	}
	// The indicator characters, which only indicate anything in first place.
	switch value[0] {
	case '-', '?', ':', ',', '[', ']', '{', '}', '#', '&', '*', '!', '|', '>', '%', '@', '`':
		return true
	}
	return false
}

func unescape(value string) string {
	if !strings.Contains(value, `\`) {
		return value
	}
	var out strings.Builder
	runes := []rune(value)
	for index := 0; index < len(runes); index++ {
		if runes[index] != '\\' || index+1 >= len(runes) {
			out.WriteRune(runes[index])
			continue
		}
		index++
		switch runes[index] {
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 't':
			out.WriteByte('\t')
		case '0':
			out.WriteByte(0)
		case '"', '\\', '/', '\'':
			out.WriteRune(runes[index])
		case 'u', 'U', 'x':
			width := map[rune]int{'x': 2, 'u': 4, 'U': 8}[runes[index]]
			if index+width >= len(runes) {
				out.WriteRune(runes[index])
				continue
			}
			code, err := parseHex(string(runes[index+1 : index+1+width]))
			if err != nil {
				out.WriteRune(runes[index])
				continue
			}
			index += width
			out.WriteRune(rune(code))
		default:
			// Not an escape YAML defines. Keeping both characters is the least
			// destructive reading of a file we did not write.
			out.WriteByte('\\')
			out.WriteRune(runes[index])
		}
	}
	return out.String()
}

func parseHex(digits string) (uint32, error) {
	var value uint32
	for _, char := range digits {
		switch {
		case char >= '0' && char <= '9':
			value = value<<4 | uint32(char-'0')
		case char >= 'a' && char <= 'f':
			value = value<<4 | uint32(char-'a'+10)
		case char >= 'A' && char <= 'F':
			value = value<<4 | uint32(char-'A'+10)
		default:
			return 0, fmt.Errorf("not a hex digit: %q", char)
		}
	}
	return value, nil
}
