// Package velocitycfg reads and writes Velocity's velocity.toml.
//
// It is the proxy's answer to mcprops, and it exists for the same reason: the
// panel edits a file the operator also hand-edits, so comments, blank lines and
// key order have to survive a save. What is different is the format — TOML with
// tables, where the sub-server list ([servers]) is not one setting but the whole
// point of the file.
//
// This is deliberately not a TOML library. It understands the shapes Velocity
// actually writes — scalars, string arrays, one level of tables — and treats
// everything else as text to be preserved rather than parsed. A config that
// uses something more exotic loses nothing: the panel just does not offer to
// edit that part of it.
package velocitycfg

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"unicode/utf16"
)

// Entry is one key/value pair inside a table, in file order. Value is decoded:
// a quoted string arrives without its quotes, a number or a boolean as written.
type Entry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// File is a parsed velocity.toml that remembers its original layout.
type File struct {
	lines []line
}

// line is one source line: either verbatim text (comment, blank, anything we
// do not understand), a table header, or a key/value pair.
type line struct {
	raw string // used when table == "" && key == ""

	// table is set on a "[name]" header line.
	table string

	// section is the table a key line belongs to, "" at the top level.
	section string
	indent  string
	keyRaw  string // as written, quoting included
	key     string // decoded
	value   string // raw TOML value text; may span lines for an array
	suffix  string // trailing comment, kept verbatim
}

func (l line) isKey() bool { return l.key != "" }

// Parse reads a velocity.toml from r.
func Parse(r io.Reader) (*File, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var raw []string
	for scanner.Scan() {
		raw = append(raw, strings.TrimRight(scanner.Text(), "\r"))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	f := &File{}
	section := ""
	for index := 0; index < len(raw); index++ {
		text := raw[index]
		trimmed := strings.TrimLeft(text, " \t")

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			f.lines = append(f.lines, line{raw: text})
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			if name, ok := tableName(trimmed); ok {
				section = name
				f.lines = append(f.lines, line{raw: text, table: name})
				continue
			}
			f.lines = append(f.lines, line{raw: text})
			continue
		}

		keyPart, valuePart, ok := splitAssignment(trimmed)
		if !ok {
			f.lines = append(f.lines, line{raw: text})
			continue
		}

		// An array Velocity wrote across several lines — try = [\n "lobby"\n]
		// — is one value. Reading it as one line each would turn the closing
		// bracket into an unparseable line and lose the entry.
		for !balanced(valuePart) && index+1 < len(raw) {
			index++
			valuePart += "\n" + raw[index]
		}

		value, suffix := splitComment(valuePart)
		f.lines = append(f.lines, line{
			section: section,
			indent:  text[:len(text)-len(trimmed)],
			keyRaw:  strings.TrimSpace(keyPart),
			key:     decodeKey(strings.TrimSpace(keyPart)),
			value:   strings.TrimSpace(value),
			suffix:  suffix,
		})
	}
	return f, nil
}

// Load reads a velocity.toml. A missing file is not an error: it parses as
// empty, which is what lets the panel configure a proxy that has never run.
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

// Empty reports whether the file had no content at all — no keys, no tables.
// The caller uses it to decide between editing what is there and starting from
// Velocity's own defaults.
func (f *File) Empty() bool {
	for _, l := range f.lines {
		if l.isKey() || l.table != "" {
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
		case l.isKey():
			out.WriteString(l.indent)
			out.WriteString(l.keyRaw)
			out.WriteString(" = ")
			out.WriteString(l.value)
			out.WriteString(l.suffix)
		default:
			out.WriteString(l.raw)
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

// Value returns the decoded scalar at section.key. A quoted string comes back
// unquoted; a number or a boolean comes back as written.
func (f *File) Value(section, key string) (string, bool) {
	if index := f.find(section, key); index >= 0 {
		return DecodeScalar(f.lines[index].value), true
	}
	return "", false
}

// List returns a string array such as [servers]'s try list.
func (f *File) List(section, key string) ([]string, bool) {
	index := f.find(section, key)
	if index < 0 {
		return nil, false
	}
	return decodeStringArray(f.lines[index].value), true
}

// Entries returns every scalar key in a table, in file order. Keys named in
// skip are left out, which is how the sub-server list is read without the try
// list that shares its table.
func (f *File) Entries(section string, skip ...string) []Entry {
	out := []Entry{}
	for _, l := range f.lines {
		if !l.isKey() || l.section != section || slices.Contains(skip, l.key) {
			continue
		}
		out = append(out, Entry{Key: l.key, Value: DecodeScalar(l.value)})
	}
	return out
}

// ListEntry is one key of a table whose values are string arrays —
// [forced-hosts], where a hostname maps to the servers it sends players to.
type ListEntry struct {
	Key    string   `json:"key"`
	Values []string `json:"values"`
}

// ListEntries returns every array-valued key in a table, in file order. A key
// in that table holding something else is skipped rather than mangled.
func (f *File) ListEntries(section string) []ListEntry {
	out := []ListEntry{}
	for _, l := range f.lines {
		if !l.isKey() || l.section != section || !strings.HasPrefix(strings.TrimSpace(l.value), "[") {
			continue
		}
		out = append(out, ListEntry{Key: l.key, Values: decodeStringArray(l.value)})
	}
	return out
}

// ------------------------------------------------------------------ writing

// SetString writes a string value, quoting and escaping it.
func (f *File) SetString(section, key, value string) {
	f.set(section, key, EncodeString(value))
}

// SetRaw writes a value that is already TOML — a number, a boolean, anything
// the caller has validated.
func (f *File) SetRaw(section, key, value string) {
	f.set(section, key, value)
}

// SetList writes a string array on one line. Velocity writes these across
// several lines; collapsing to one is a formatting change and nothing else, and
// it only happens to a list the operator just edited.
func (f *File) SetList(section, key string, values []string) {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, EncodeString(value))
	}
	f.set(section, key, "["+strings.Join(quoted, ", ")+"]")
}

func (f *File) set(section, key, encoded string) {
	if index := f.find(section, key); index >= 0 {
		f.lines[index].value = encoded
		return
	}
	f.insert(section, []line{{
		section: section,
		keyRaw:  encodeKey(key),
		key:     key,
		value:   encoded,
	}})
}

// SetEntries replaces a table's scalar entries with exactly the ones given,
// keeping the keys named in keep and every comment around them.
//
// Rewriting rather than merging is what the sub-server list needs: removing a
// server has to remove its line, and there is no other call that can say so.
func (f *File) SetEntries(section string, entries []Entry, keep ...string) {
	order := make([]string, 0, len(entries))
	want := make(map[string]string, len(entries))
	for _, entry := range entries {
		order = append(order, entry.Key)
		want[entry.Key] = EncodeString(entry.Value)
	}
	f.setEncoded(section, order, want, keep)
}

// SetListEntries does the same for a table of string arrays: [forced-hosts] is
// rewritten rather than merged, so deleting a hostname deletes its line.
func (f *File) SetListEntries(section string, entries []ListEntry, keep ...string) {
	order := make([]string, 0, len(entries))
	want := make(map[string]string, len(entries))
	for _, entry := range entries {
		quoted := make([]string, 0, len(entry.Values))
		for _, value := range entry.Values {
			quoted = append(quoted, EncodeString(value))
		}
		order = append(order, entry.Key)
		want[entry.Key] = "[" + strings.Join(quoted, ", ") + "]"
	}
	f.setEncoded(section, order, want, keep)
}

func (f *File) setEncoded(section string, order []string, want map[string]string, keep []string) {
	out := make([]line, 0, len(f.lines)+len(order))
	// Where a key this table does not have yet should go: after the last one
	// it still has, or right after the header when it has none. Never after
	// the kept keys — a new server belongs above the try list, not below it.
	insertAt := -1
	seen := make(map[string]bool, len(order))

	for _, l := range f.lines {
		switch {
		case l.table == section:
			out = append(out, l)
			insertAt = len(out)
		case l.isKey() && l.section == section && !slices.Contains(keep, l.key):
			encoded, ok := want[l.key]
			if !ok {
				continue // removed by the operator
			}
			l.value = encoded
			seen[l.key] = true
			out = append(out, l)
			insertAt = len(out)
		default:
			out = append(out, l)
		}
	}

	fresh := make([]line, 0, len(order))
	for _, key := range order {
		if seen[key] {
			continue
		}
		fresh = append(fresh, line{
			section: section,
			keyRaw:  encodeKey(key),
			key:     key,
			value:   want[key],
		})
	}

	f.lines = out
	if len(fresh) == 0 {
		return
	}
	if insertAt < 0 {
		f.insert(section, fresh)
		return
	}
	f.lines = append(f.lines[:insertAt], append(fresh, f.lines[insertAt:]...)...)
}

// insert places new key lines in their table, creating the table at the end of
// the file when it has none.
func (f *File) insert(section string, fresh []line) {
	if section == "" {
		// A top-level key has to go before the first table header, or it would
		// be read as belonging to that table.
		at := len(f.lines)
		for index, l := range f.lines {
			if l.table != "" {
				at = index
				break
			}
		}
		f.lines = append(f.lines[:at], append(fresh, f.lines[at:]...)...)
		return
	}

	at := -1
	for index, l := range f.lines {
		if l.table == section {
			at = index + 1
			continue
		}
		if at >= 0 && l.section == section {
			at = index + 1
		}
	}
	if at < 0 {
		header := []line{{raw: ""}, {raw: "[" + encodeKey(section) + "]", table: section}}
		f.lines = append(f.lines, header...)
		f.lines = append(f.lines, fresh...)
		return
	}
	f.lines = append(f.lines[:at], append(fresh, f.lines[at:]...)...)
}

func (f *File) find(section, key string) int {
	for index, l := range f.lines {
		if l.isKey() && l.section == section && l.key == key {
			return index
		}
	}
	return -1
}

// ------------------------------------------------------------------- syntax

// tableName reads "[servers]". Anything else — an array of tables, a line that
// only looks like a header — is left as text.
func tableName(trimmed string) (string, bool) {
	if !strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "[[") {
		return "", false
	}
	end := strings.Index(trimmed, "]")
	if end < 0 {
		return "", false
	}
	if rest := strings.TrimSpace(trimmed[end+1:]); rest != "" && !strings.HasPrefix(rest, "#") {
		return "", false
	}
	name := strings.TrimSpace(trimmed[1:end])
	if name == "" {
		return "", false
	}
	return decodeKey(name), true
}

// splitAssignment cuts at the first "=" that is not inside a string.
func splitAssignment(trimmed string) (key, value string, ok bool) {
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
			quote = char
		case char == '=':
			return trimmed[:index], trimmed[index+1:], true
		}
	}
	return "", "", false
}

// splitComment separates a value from the comment written after it.
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
		case char == '#':
			// The space before the comment belongs to the comment: the value
			// is re-rendered with its own spacing.
			return value[:index], " " + value[index:]
		}
	}
	return value, ""
}

// balanced reports whether every bracket opened in value is closed again,
// ignoring brackets inside strings. An unbalanced value continues on the next
// line.
func balanced(value string) bool {
	depth := 0
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
		case char == '#':
			return depth == 0
		case char == '[':
			depth++
		case char == ']':
			depth--
		}
	}
	return depth <= 0
}

func decodeKey(raw string) string {
	if len(raw) >= 2 && (raw[0] == '"' || raw[0] == '\'') && raw[len(raw)-1] == raw[0] {
		return DecodeScalar(raw)
	}
	return raw
}

// encodeKey quotes a key that is not a bare TOML key. Forced hosts are the
// case that needs it: "lobby.example.com" is one key, not two.
func encodeKey(key string) string {
	if key == "" {
		return `""`
	}
	for _, char := range key {
		bare := char == '-' || char == '_' ||
			(char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9')
		if !bare {
			return EncodeString(key)
		}
	}
	return key
}

// DecodeScalar turns a raw TOML scalar into the text the panel shows. Numbers
// and booleans are already that text; strings lose their quotes and escapes.
func DecodeScalar(raw string) string {
	raw = strings.TrimSpace(raw)
	switch {
	case len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'':
		// A literal string means what it says, escapes included.
		return raw[1 : len(raw)-1]
	case len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"':
		return unescape(raw[1 : len(raw)-1])
	default:
		return raw
	}
}

// EncodeString writes a TOML basic string. TOML files are UTF-8, so a Chinese
// MOTD goes in literally — unlike server.properties, which has to escape it.
func EncodeString(value string) string {
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
				out.WriteString(fmt.Sprintf(`\u%04X`, char))
				continue
			}
			out.WriteRune(char)
		}
	}
	out.WriteByte('"')
	return out.String()
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
		case 'b':
			out.WriteByte('\b')
		case 'f':
			out.WriteByte('\f')
		case '"', '\\', '\'':
			out.WriteRune(runes[index])
		case 'u', 'U':
			width := 4
			if runes[index] == 'U' {
				width = 8
			}
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
			if utf16.IsSurrogate(rune(code)) {
				out.WriteRune('�')
				continue
			}
			out.WriteRune(rune(code))
		default:
			// Not an escape TOML defines. Keeping both characters is the least
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

func decodeStringArray(raw string) []string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "[") {
		return nil
	}
	raw = strings.TrimPrefix(raw, "[")
	raw = strings.TrimSuffix(strings.TrimSpace(raw), "]")

	out := []string{}
	var current strings.Builder
	quote := byte(0)
	flush := func() {
		if item := strings.TrimSpace(current.String()); item != "" {
			out = append(out, DecodeScalar(item))
		}
		current.Reset()
	}
	for index := 0; index < len(raw); index++ {
		char := raw[index]
		switch {
		case quote != 0:
			current.WriteByte(char)
			if char == '\\' && quote == '"' && index+1 < len(raw) {
				index++
				current.WriteByte(raw[index])
			} else if char == quote {
				quote = 0
			}
		case char == '"' || char == '\'':
			quote = char
			current.WriteByte(char)
		case char == ',':
			flush()
		case char == '#':
			// A comment inside a multi-line array runs to the end of its line.
			for index < len(raw) && raw[index] != '\n' {
				index++
			}
		case char == '\n':
			// Nothing: whitespace between items.
		default:
			current.WriteByte(char)
		}
	}
	flush()
	return out
}
