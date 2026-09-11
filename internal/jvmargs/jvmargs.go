// Package jvmargs reads and writes an @-argument file: the plain list of JVM
// flags a launcher hands the JVM with java @file.
//
// Forge and NeoForge write user_jvm_args.txt for exactly this, and that makes
// it the only place an operator can set the heap of a server the panel does
// not launch itself. Once a run.sh owns the command line the panel's -Xmx
// never reaches the JVM, so the number in the instance config stops being the
// heap ceiling and becomes decoration — see Config.effectiveMaxMemoryMB.
//
// Editing preserves comments, blank lines and order, like every other config
// file the panel touches. It matters more here than usual: the file ships as
// six lines explaining itself with the -Xmx example commented out, and a save
// that threw that away would leave a worse file than it found.
package jvmargs

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// FileName is what Forge's run.sh passes as @user_jvm_args.txt, relative to
// the server directory.
const FileName = "user_jvm_args.txt"

// maxSize caps what Parse will read. The real file is a few hundred bytes;
// anything larger is not an argument file and is not worth loading into
// memory to find that out.
const maxSize = 1 << 20

// Flag names the panel knows how to edit. Everything else in the file is
// carried through untouched.
const (
	flagXmx = "-Xmx"
	flagXms = "-Xms"
)

// File is a parsed argument file that remembers its original layout.
type File struct {
	lines []line
}

// line is either one JVM flag or a verbatim line (comment or blank).
type line struct {
	raw string // used when arg == ""
	arg string
}

// Parse reads an argument file from r.
func Parse(r io.Reader) (*File, error) {
	f := &File{}
	scanner := bufio.NewScanner(io.LimitReader(r, maxSize))
	scanner.Buffer(make([]byte, 0, 64*1024), maxSize)

	for scanner.Scan() {
		raw := strings.TrimRight(scanner.Text(), "\r")
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			f.lines = append(f.lines, line{raw: raw})
			continue
		}
		f.lines = append(f.lines, line{raw: raw, arg: trimmed})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return f, nil
}

// Load reads the argument file at path.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(bytes.NewReader(data))
}

// Args returns the flags in file order, comments dropped.
func (f *File) Args() []string {
	out := make([]string, 0, len(f.lines))
	for _, l := range f.lines {
		if l.arg != "" {
			out = append(out, l.arg)
		}
	}
	return out
}

// MaxMemoryMB is the -Xmx in the file, 0 when there is none.
func (f *File) MaxMemoryMB() int { return f.memory(flagXmx) }

// MinMemoryMB is the -Xms in the file, 0 when there is none.
func (f *File) MinMemoryMB() int { return f.memory(flagXms) }

// memory reads the last occurrence of a heap flag, which is the one the JVM
// itself honours.
func (f *File) memory(flag string) int {
	found := 0
	for _, l := range f.lines {
		if l.arg == "" || !strings.HasPrefix(l.arg, flag) {
			continue
		}
		if mb, ok := parseSize(strings.TrimPrefix(l.arg, flag)); ok {
			found = mb
		}
	}
	return found
}

// SetMaxMemoryMB writes -Xmx. Zero removes it, which is the JVM's own default
// rather than "no limit".
func (f *File) SetMaxMemoryMB(mb int) { f.setMemory(flagXmx, mb) }

// SetMinMemoryMB writes -Xms. Zero removes it.
func (f *File) SetMinMemoryMB(mb int) { f.setMemory(flagXms, mb) }

func (f *File) setMemory(flag string, mb int) {
	if mb < 0 {
		mb = 0
	}
	value := fmt.Sprintf("%s%dM", flag, mb)

	// Rewrite the last occurrence and drop the earlier ones, so a file that
	// already disagreed with itself comes out saying one thing.
	last := -1
	for i, l := range f.lines {
		if l.arg != "" && strings.HasPrefix(l.arg, flag) {
			last = i
		}
	}
	if last >= 0 {
		kept := f.lines[:0]
		for i, l := range f.lines {
			switch {
			case i == last && mb > 0:
				kept = append(kept, line{raw: value, arg: value})
			case i == last, l.arg != "" && strings.HasPrefix(l.arg, flag):
				// Dropped: the one being cleared, and every stale duplicate.
			default:
				kept = append(kept, l)
			}
		}
		f.lines = kept
		return
	}
	if mb == 0 {
		return
	}

	// Forge ships the flag commented out under a paragraph that ends "Uncomment
	// the next line to set it", so uncommenting is what the file asks for and
	// it keeps the heap where a hand-editing operator expects to find it. Only
	// a comment whose entire content is this flag qualifies — anything else is
	// prose, and rewriting prose is not an edit anyone asked for.
	for i, l := range f.lines {
		if l.arg != "" {
			continue
		}
		body := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l.raw), "#"))
		if strings.HasPrefix(body, flag) {
			if _, ok := parseSize(strings.TrimPrefix(body, flag)); ok {
				f.lines[i] = line{raw: value, arg: value}
				return
			}
		}
	}
	f.lines = append(f.lines, line{raw: value, arg: value})
}

// Render writes the file back out.
func (f *File) Render() []byte {
	var buf bytes.Buffer
	for _, l := range f.lines {
		if l.arg != "" {
			buf.WriteString(l.arg)
		} else {
			buf.WriteString(l.raw)
		}
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// parseSize reads a JVM memory size — 4G, 4096M, 4096m, 4194304K, or a plain
// byte count — and returns it in whole megabytes.
//
// Anything smaller than a megabyte rounds up to one rather than to zero: zero
// is how the panel says "unset", and a -Xmx512K reported as unset would make
// the UI show no ceiling for a server that very much has one.
func parseSize(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	unit := byte(0)
	if last := raw[len(raw)-1]; last < '0' || last > '9' {
		unit = last
		raw = raw[:len(raw)-1]
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, false
	}

	var bytesTotal int64
	switch unit {
	case 0:
		bytesTotal = value
	case 'k', 'K':
		bytesTotal = value << 10
	case 'm', 'M':
		bytesTotal = value << 20
	case 'g', 'G':
		bytesTotal = value << 30
	case 't', 'T':
		bytesTotal = value << 40
	default:
		return 0, false
	}
	if bytesTotal == 0 {
		return 0, false
	}
	if mb := bytesTotal >> 20; mb > 0 {
		return int(mb), true
	}
	return 1, true
}
