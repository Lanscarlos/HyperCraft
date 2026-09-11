package launchscript

import "strings"

// Lexing, quoting and variable expansion.
//
// This is not a shell and does not try to become one. It handles the four
// things a start script actually does — quote a path, hold a value in a
// variable, continue a line, comment a line — and refuses everything else
// rather than approximating it. An approximation here would be invisible: the
// wrong command line parses fine and only tells you on the first start.

// part is one piece of a token. Single-quoted pieces never expand, which is
// the whole reason a token is a list rather than a string.
type part struct {
	text   string
	expand bool
	sub    bool // a command substitution; refused wherever it would matter
}

// token is one word or one operator from a line.
type token struct {
	raw   string
	parts []part
	op    string
}

func (t token) isOp() bool { return t.op != "" }

// logicalLine is one command line after comments are dropped and continuations
// are joined, carrying the number of the physical line it started on.
type logicalLine struct {
	text string
	num  int
}

// logicalLines splits a script into the lines worth looking at.
func logicalLines(text string, dialect Dialect) []logicalLine {
	raw := strings.Split(text, "\n")
	out := make([]logicalLine, 0, len(raw))

	continuation := "\\"
	if dialect == Batch {
		continuation = "^"
	}

	var pending string
	var pendingNum int
	for i, line := range raw {
		line = strings.TrimRight(line, "\r")
		num := i + 1

		if pending != "" {
			joined := pending + " " + strings.TrimSpace(line)
			if strings.HasSuffix(strings.TrimSpace(line), continuation) {
				pending = strings.TrimSuffix(strings.TrimSpace(joined), continuation)
				continue
			}
			out = append(out, logicalLine{text: strings.TrimSpace(joined), num: pendingNum})
			pending = ""
			continue
		}

		trimmed := strings.TrimSpace(line)
		if dialect == Batch {
			trimmed = strings.TrimPrefix(trimmed, "@")
			trimmed = strings.TrimSpace(trimmed)
		}
		if trimmed == "" || isComment(trimmed, dialect) {
			continue
		}
		if strings.HasSuffix(trimmed, continuation) {
			pending = strings.TrimSuffix(trimmed, continuation)
			pendingNum = num
			continue
		}
		out = append(out, logicalLine{text: trimmed, num: num})
	}
	if pending != "" {
		out = append(out, logicalLine{text: strings.TrimSpace(pending), num: pendingNum})
	}
	return out
}

func isComment(line string, dialect Dialect) bool {
	if dialect == Batch {
		lower := strings.ToLower(line)
		return strings.HasPrefix(line, "::") || lower == "rem" || strings.HasPrefix(lower, "rem ")
	}
	return strings.HasPrefix(line, "#")
}

// operators are recognised longest first, so "&&" never reads as two "&".
var operators = []string{"&&", "||", ">>", ";", "|", "&", ">", "<"}

// tokenize splits one logical line into words and operators.
func tokenize(line string, dialect Dialect) []token {
	var out []token
	runes := []rune(line)
	i := 0

	for i < len(runes) {
		if runes[i] == ' ' || runes[i] == '\t' {
			i++
			continue
		}
		// A comment can start mid-line, but only where a word could.
		if dialect == Shell && runes[i] == '#' {
			break
		}
		if op, width := matchOperator(runes, i); op != "" {
			out = append(out, token{raw: op, op: op})
			i += width
			continue
		}

		start := i
		var parts []part
		var plain strings.Builder

		flush := func(expand bool) {
			if plain.Len() > 0 {
				parts = append(parts, part{text: plain.String(), expand: expand})
				plain.Reset()
			}
		}

		for i < len(runes) {
			c := runes[i]
			if c == ' ' || c == '\t' {
				break
			}
			if op, _ := matchOperator(runes, i); op != "" {
				break
			}
			switch {
			case dialect == Shell && c == '\'':
				flush(true)
				i++
				var lit strings.Builder
				for i < len(runes) && runes[i] != '\'' {
					lit.WriteRune(runes[i])
					i++
				}
				i++ // closing quote
				parts = append(parts, part{text: lit.String(), expand: false})
			case c == '"':
				i++
				for i < len(runes) && runes[i] != '"' {
					if dialect == Shell && runes[i] == '\\' && i+1 < len(runes) {
						i++
						plain.WriteRune(runes[i])
						i++
						continue
					}
					if dialect == Shell && runes[i] == '$' && i+1 < len(runes) && runes[i+1] == '(' {
						flush(true)
						sub, width := captureSubstitution(runes, i)
						parts = append(parts, part{text: sub, sub: true})
						i += width
						continue
					}
					plain.WriteRune(runes[i])
					i++
				}
				i++ // closing quote
				flush(true)
			case dialect == Shell && c == '\\' && i+1 < len(runes):
				i++
				plain.WriteRune(runes[i])
				i++
			case dialect == Shell && c == '$' && i+1 < len(runes) && runes[i+1] == '(':
				flush(true)
				sub, width := captureSubstitution(runes, i)
				parts = append(parts, part{text: sub, sub: true})
				i += width
			case dialect == Shell && c == '`':
				flush(true)
				i++
				var sub strings.Builder
				for i < len(runes) && runes[i] != '`' {
					sub.WriteRune(runes[i])
					i++
				}
				i++
				parts = append(parts, part{text: sub.String(), sub: true})
			default:
				plain.WriteRune(c)
				i++
			}
		}
		flush(true)
		out = append(out, token{raw: string(runes[start:i]), parts: parts})
	}
	return out
}

func matchOperator(runes []rune, i int) (string, int) {
	rest := string(runes[i:])
	for _, op := range operators {
		if strings.HasPrefix(rest, op) {
			return op, len([]rune(op))
		}
	}
	return "", 0
}

// captureSubstitution reads a $( … ) group whole, counting nesting so that the
// idiom `$(cd "$(dirname "$0")" && pwd)` comes back in one piece.
func captureSubstitution(runes []rune, i int) (string, int) {
	start := i
	i += 2 // $(
	depth := 1
	var body strings.Builder
	for i < len(runes) && depth > 0 {
		switch runes[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				i++
				return body.String(), i - start
			}
		}
		body.WriteRune(runes[i])
		i++
	}
	return body.String(), i - start
}

// expansion is what one token turned into, and everything the caller needs to
// decide whether to trust it.
type expansion struct {
	text       string
	unresolved []string
	sub        bool
	passthru   bool // "$@" or "$*": the caller's own arguments
	scriptPath bool // $0 or ${0…}: where the script itself lives
}

// expand resolves a token against the variables seen so far.
func expand(tk token, vars map[string]string, dialect Dialect) expansion {
	var out expansion
	var b strings.Builder
	for _, p := range tk.parts {
		if p.sub {
			out.sub = true
			continue
		}
		if !p.expand {
			b.WriteString(p.text)
			continue
		}
		expandInto(&b, p.text, vars, dialect, &out, 0)
	}
	out.text = b.String()
	return out
}

// maxExpansionDepth stops a variable that refers to itself from hanging the
// import dialog.
const maxExpansionDepth = 8

func expandInto(b *strings.Builder, text string, vars map[string]string, dialect Dialect, out *expansion, depth int) {
	if depth > maxExpansionDepth {
		return
	}
	runes := []rune(text)
	for i := 0; i < len(runes); {
		if dialect == Batch {
			if runes[i] == '%' {
				if name, width, ok := readPercentName(runes, i); ok {
					resolve(b, name, "", vars, dialect, out, depth)
					i += width
					continue
				}
			}
			b.WriteRune(runes[i])
			i++
			continue
		}

		if runes[i] != '$' || i+1 >= len(runes) {
			b.WriteRune(runes[i])
			i++
			continue
		}

		switch next := runes[i+1]; {
		case next == '@' || next == '*':
			out.passthru = true
			i += 2
		case next == '0':
			out.scriptPath = true
			i += 2
		case next == '{':
			name, fallback, width, ok := readBracedName(runes, i)
			if !ok {
				b.WriteRune(runes[i])
				i++
				continue
			}
			if name == "0" || strings.HasPrefix(name, "0") {
				out.scriptPath = true
				i += width
				continue
			}
			resolve(b, name, fallback, vars, dialect, out, depth)
			i += width
		default:
			name, width := readBareName(runes, i)
			if name == "" {
				b.WriteRune(runes[i])
				i++
				continue
			}
			resolve(b, name, "", vars, dialect, out, depth)
			i += width
		}
	}
}

// resolve writes one variable's value, or records that nobody knows it.
func resolve(b *strings.Builder, name, fallback string, vars map[string]string, dialect Dialect, out *expansion, depth int) {
	if value, ok := vars[name]; ok {
		expandInto(b, value, vars, dialect, out, depth+1)
		return
	}
	if fallback != "" {
		expandInto(b, fallback, vars, dialect, out, depth+1)
		return
	}
	out.unresolved = append(out.unresolved, name)
}

func readBareName(runes []rune, i int) (string, int) {
	j := i + 1
	for j < len(runes) && isNameRune(runes[j]) {
		j++
	}
	if j == i+1 {
		return "", 0
	}
	return string(runes[i+1 : j]), j - i
}

// readBracedName reads ${NAME} and ${NAME:-默认值}. Any other modifier is left
// unread, which makes the caller treat the variable as unknown rather than
// silently dropping whatever the modifier meant.
func readBracedName(runes []rune, i int) (name, fallback string, width int, ok bool) {
	j := i + 2
	depth := 1
	var body strings.Builder
	for j < len(runes) && depth > 0 {
		switch runes[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				j++
				inner := body.String()
				if n, f, found := strings.Cut(inner, ":-"); found {
					return n, f, j - i, true
				}
				return inner, "", j - i, true
			}
		}
		body.WriteRune(runes[j])
		j++
	}
	return "", "", 0, false
}

func readPercentName(runes []rune, i int) (string, int, bool) {
	j := i + 1
	for j < len(runes) && runes[j] != '%' {
		if !isNameRune(runes[j]) {
			return "", 0, false
		}
		j++
	}
	if j >= len(runes) || j == i+1 {
		return "", 0, false
	}
	return string(runes[i+1 : j]), j - i + 1, true
}

func isNameRune(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}
