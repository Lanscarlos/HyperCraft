// Package mcprops reads and writes Minecraft's server.properties.
//
// The format is java.util.Properties, which matters in two ways the panel has
// to respect:
//
//   - Minecraft loads the file as ISO-8859-1, so anything outside ASCII must be
//     written as a \uXXXX escape. A UTF-8 Chinese MOTD written literally comes
//     out as mojibake in-game.
//   - Editing must preserve comments, blank lines and key order, otherwise the
//     first save through the panel scrambles a file the user may also hand-edit.
package mcprops

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Entry is one key/value pair, in file order.
type Entry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// File is a parsed server.properties that remembers its original layout.
type File struct {
	lines []line
}

// line is either a verbatim line (comment/blank) or a key/value pair.
type line struct {
	raw   string // used when key == ""
	key   string
	value string
}

// Parse reads properties from r.
func Parse(r io.Reader) (*File, error) {
	f := &File{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		raw := strings.TrimRight(scanner.Text(), "\r")
		trimmed := strings.TrimLeft(raw, " \t\f")

		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "!") {
			f.lines = append(f.lines, line{raw: raw})
			continue
		}

		key, value, ok := splitEntry(trimmed)
		if !ok {
			f.lines = append(f.lines, line{raw: raw})
			continue
		}
		f.lines = append(f.lines, line{key: unescape(key), value: unescape(value)})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return f, nil
}

// Load reads a properties file. A missing file parses as empty, so a brand new
// instance can be configured before its first launch generates the file.
func Load(path string) (*File, error) {
	data, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &File{}, nil
		}
		return nil, err
	}
	defer data.Close()
	return Parse(data)
}

// splitEntry finds the first unescaped '=' or ':' separator.
func splitEntry(s string) (key, value string, ok bool) {
	escaped := false
	for i, r := range s {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == '=' || r == ':':
			return strings.TrimRight(s[:i], " \t\f"),
				strings.TrimLeft(s[i+len(string(r)):], " \t\f"),
				true
		}
	}
	// A bare key with no separator is a valid empty-valued property.
	return strings.TrimRight(s, " \t\f"), "", true
}

// Entries returns every key/value pair in file order.
func (f *File) Entries() []Entry {
	out := make([]Entry, 0, len(f.lines))
	for _, l := range f.lines {
		if l.key != "" {
			out = append(out, Entry{Key: l.key, Value: l.value})
		}
	}
	return out
}

// Get returns the value for a key.
func (f *File) Get(key string) (string, bool) {
	for _, l := range f.lines {
		if l.key == key {
			return l.value, true
		}
	}
	return "", false
}

// Set updates an existing key in place, or appends a new one at the end.
func (f *File) Set(key, value string) {
	for idx := range f.lines {
		if f.lines[idx].key == key {
			f.lines[idx].value = value
			return
		}
	}
	f.lines = append(f.lines, line{key: key, value: value})
}

// Delete removes a key and its line.
func (f *File) Delete(key string) {
	out := f.lines[:0]
	for _, l := range f.lines {
		if l.key == key {
			continue
		}
		out = append(out, l)
	}
	f.lines = out
}

// Bytes renders the file back to its on-disk form.
func (f *File) Bytes() []byte {
	var b strings.Builder
	for _, l := range f.lines {
		if l.key == "" {
			b.WriteString(l.raw)
		} else {
			b.WriteString(escape(l.key, true))
			b.WriteByte('=')
			b.WriteString(escape(l.value, false))
		}
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// Save writes the file atomically so a crash mid-write cannot leave the server
// with a truncated server.properties.
func (f *File) Save(path string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".server.properties-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.Write(f.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// unescape resolves the backslash escapes java.util.Properties understands.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	runes := []rune(s)

	for i := 0; i < len(runes); i++ {
		if runes[i] != '\\' || i+1 >= len(runes) {
			b.WriteRune(runes[i])
			continue
		}
		i++
		switch runes[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'f':
			b.WriteByte('\f')
		case 'u':
			if i+4 < len(runes) {
				if v, err := strconv.ParseUint(string(runes[i+1:i+5]), 16, 32); err == nil {
					r := rune(v)
					i += 4
					// Surrogate pair: consume the low half so astral-plane
					// characters (emoji in a MOTD) survive the round trip.
					if utf16.IsSurrogate(r) && i+6 < len(runes) &&
						runes[i+1] == '\\' && runes[i+2] == 'u' {
						if lo, err := strconv.ParseUint(string(runes[i+3:i+7]), 16, 32); err == nil {
							if combined := utf16.DecodeRune(r, rune(lo)); combined != '�' {
								b.WriteRune(combined)
								i += 6
								continue
							}
						}
					}
					b.WriteRune(r)
					continue
				}
			}
			b.WriteRune('u')
		default:
			b.WriteRune(runes[i])
		}
	}
	return b.String()
}

// escape renders a key or value for the properties format. Non-ASCII becomes
// \uXXXX because Minecraft reads the file as ISO-8859-1.
func escape(s string, isKey bool) string {
	var b strings.Builder
	b.Grow(len(s))

	for i, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '=' || r == ':' || r == '#' || r == '!':
			// Only the key needs these escaped; in a value they are literal
			// except when they would start the line.
			if isKey || i == 0 {
				b.WriteByte('\\')
			}
			b.WriteRune(r)
		case r == ' ':
			// Leading spaces would be eaten on load; keys escape every space.
			if isKey || i == 0 {
				b.WriteByte('\\')
			}
			b.WriteRune(r)
		case r < 0x20 || r > 0x7e:
			for _, u := range utf16.Encode([]rune{r}) {
				fmt.Fprintf(&b, `\u%04X`, u)
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
