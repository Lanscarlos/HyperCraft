package confighist

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

// The line differ.
//
// Small on purpose: what it compares is YAML, properties files and JSON, where
// a change is a handful of lines in a file of a few hundred. It trims the
// common head and tail, runs Myers' greedy algorithm over what is left, and
// gives up past a bounded amount of difference — a config file that changed
// beyond recognition reads better as "replaced" than as a thousand interleaved
// ± lines nobody will scroll through.

// maxDiffScript bounds the edit distance the differ will trace. The trace costs
// O(D²) memory, so this is what keeps a pathological pair of files from eating
// the panel's heap.
const maxDiffScript = 600

// maxDiffLines is the largest file pair that is compared line by line at all.
const maxDiffLines = 20000

// LineKind is a diff line's role.
type LineKind string

const (
	LineContext LineKind = "context"
	LineAdd     LineKind = "add"
	LineDelete  LineKind = "delete"
)

// DiffLine is one rendered line.
type DiffLine struct {
	Kind LineKind `json:"kind"`
	// OldLine and NewLine are 1-based numbers, 0 where the line does not exist
	// on that side.
	OldLine int    `json:"oldLine"`
	NewLine int    `json:"newLine"`
	Text    string `json:"text"`
	// Sensitive marks a line whose value is a credential. The panel shows
	// Masked until the operator clicks it — see the design's §7. The real text
	// is still sent: the same session can open the file itself, so hiding it
	// from the response would buy nothing and break "点击才展开".
	Sensitive bool   `json:"sensitive,omitempty"`
	Masked    string `json:"masked,omitempty"`
}

// Hunk is a run of changed lines with its context.
type Hunk struct {
	OldStart int        `json:"oldStart"`
	OldCount int        `json:"oldCount"`
	NewStart int        `json:"newStart"`
	NewCount int        `json:"newCount"`
	Lines    []DiffLine `json:"lines"`
}

// FileDiff is one file's change, ready to render.
type FileDiff struct {
	Path   string `json:"path"`
	Status string `json:"status"` // added | modified | deleted
	Hunks  []Hunk `json:"hunks"`
	// Binary marks content the differ refused to line up: a file with NUL bytes
	// or invalid UTF-8. The rules should keep these out, but a plugin writing a
	// .cfg full of bytes is not a reason to render mojibake.
	Binary bool `json:"binary"`
	// Truncated marks a diff that gave up and reported the file as replaced
	// rather than tracing every edit.
	Truncated  bool `json:"truncated"`
	Insertions int  `json:"insertions"`
	Deletions  int  `json:"deletions"`
}

const diffContext = 3

// diffText compares two file contents.
func diffText(path string, old, next []byte, status string) FileDiff {
	out := FileDiff{Path: path, Status: status}

	if isBinary(old) || isBinary(next) {
		out.Binary = true
		return out
	}
	if bytes.Equal(old, next) {
		return out
	}

	oldLines := splitLines(old)
	newLines := splitLines(next)
	if len(oldLines) > maxDiffLines || len(newLines) > maxDiffLines {
		return replacedWhole(out, oldLines, newLines)
	}

	script, ok := diffLines(oldLines, newLines)
	if !ok {
		return replacedWhole(out, oldLines, newLines)
	}

	out.Hunks = assemble(script, oldLines, newLines)
	for _, hunk := range out.Hunks {
		for _, line := range hunk.Lines {
			switch line.Kind {
			case LineAdd:
				out.Insertions++
			case LineDelete:
				out.Deletions++
			}
		}
	}
	return out
}

// replacedWhole is the fallback shape: everything gone, everything new.
func replacedWhole(out FileDiff, oldLines, newLines []string) FileDiff {
	out.Truncated = true
	out.Insertions = len(newLines)
	out.Deletions = len(oldLines)

	hunk := Hunk{OldStart: 1, OldCount: len(oldLines), NewStart: 1, NewCount: len(newLines)}
	for i, line := range oldLines {
		hunk.Lines = append(hunk.Lines, decorate(DiffLine{Kind: LineDelete, OldLine: i + 1, Text: line}))
	}
	for i, line := range newLines {
		hunk.Lines = append(hunk.Lines, decorate(DiffLine{Kind: LineAdd, NewLine: i + 1, Text: line}))
	}
	if len(hunk.Lines) > 0 {
		out.Hunks = []Hunk{hunk}
	}
	return out
}

// op is one step of the edit script.
type op struct {
	kind LineKind
	old  int // index into oldLines, -1 when absent
	next int // index into newLines, -1 when absent
}

// diffLines produces the edit script, or false when it would cost too much.
func diffLines(a, b []string) ([]op, bool) {
	// Trim the common head and tail first. On a config file that is nearly all
	// of the work: a one-key edit leaves two short middles to actually compare.
	head := 0
	for head < len(a) && head < len(b) && a[head] == b[head] {
		head++
	}
	tail := 0
	for tail < len(a)-head && tail < len(b)-head && a[len(a)-1-tail] == b[len(b)-1-tail] {
		tail++
	}

	middle, ok := myers(a[head:len(a)-tail], b[head:len(b)-tail])
	if !ok {
		return nil, false
	}

	script := make([]op, 0, head+len(middle)+tail)
	for i := 0; i < head; i++ {
		script = append(script, op{kind: LineContext, old: i, next: i})
	}
	for _, step := range middle {
		if step.old >= 0 {
			step.old += head
		}
		if step.next >= 0 {
			step.next += head
		}
		script = append(script, step)
	}
	for i := 0; i < tail; i++ {
		script = append(script, op{
			kind: LineContext,
			old:  len(a) - tail + i,
			next: len(b) - tail + i,
		})
	}
	return script, true
}

// myers is the greedy algorithm from "An O(ND) Difference Algorithm and Its
// Variations", keeping the trace so the script can be walked back out.
func myers(a, b []string) ([]op, bool) {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil, true
	}
	// Nothing shorter than |n-m| edits is possible, so a pair this lopsided is
	// already past the budget and there is no point starting.
	if abs(n-m) > maxDiffScript {
		return nil, false
	}

	// The diagonal k never leaves [-D, D], and D never exceeds the budget, so
	// the vector is sized to the budget rather than to the files. Sizing it to
	// n+m instead is what would make each of the six hundred saved traces a
	// hundred kilobytes on a large file.
	bound := min(n+m, maxDiffScript)
	offset := bound + 1
	v := make([]int, 2*bound+3)
	var trace [][]int

	for d := 0; d <= bound; d++ {
		snapshot := make([]int, len(v))
		copy(snapshot, v)
		trace = append(trace, snapshot)

		for k := -d; k <= d; k += 2 {
			var x int
			switch {
			case k == -d:
				x = v[offset+k+1]
			case k != d && v[offset+k-1] < v[offset+k+1]:
				x = v[offset+k+1]
			default:
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[offset+k] = x
			if x >= n && y >= m {
				return backtrack(trace, a, b, offset), true
			}
		}
	}
	return nil, false
}

// backtrack walks the saved traces from the end point to the origin, emitting
// the script in reverse and then flipping it.
func backtrack(trace [][]int, a, b []string, offset int) []op {
	var reversed []op
	x, y := len(a), len(b)

	for d := len(trace) - 1; d >= 0 && (x > 0 || y > 0); d-- {
		v := trace[d]
		k := x - y

		var prevK int
		switch {
		case k == -d:
			prevK = k + 1
		case k != d && v[offset+k-1] < v[offset+k+1]:
			prevK = k + 1
		default:
			prevK = k - 1
		}
		prevX := v[offset+prevK]
		prevY := prevX - prevK

		for x > prevX && y > prevY {
			reversed = append(reversed, op{kind: LineContext, old: x - 1, next: y - 1})
			x--
			y--
		}
		if d == 0 {
			break
		}
		if x == prevX {
			reversed = append(reversed, op{kind: LineAdd, old: -1, next: y - 1})
		} else {
			reversed = append(reversed, op{kind: LineDelete, old: x - 1, next: -1})
		}
		x, y = prevX, prevY
	}

	script := make([]op, len(reversed))
	for i, step := range reversed {
		script[len(reversed)-1-i] = step
	}
	return script
}

// assemble turns the script into hunks with three lines of context, dropping
// the long unchanged stretches between them.
func assemble(script []op, oldLines, newLines []string) []Hunk {
	interesting := make([]bool, len(script))
	any := false
	for i, step := range script {
		if step.kind == LineContext {
			continue
		}
		any = true
		for j := max(0, i-diffContext); j <= min(len(script)-1, i+diffContext); j++ {
			interesting[j] = true
		}
	}
	if !any {
		return nil
	}

	var hunks []Hunk
	for i := 0; i < len(script); {
		if !interesting[i] {
			i++
			continue
		}
		end := i
		for end < len(script) && interesting[end] {
			end++
		}

		hunk := Hunk{}
		for _, step := range script[i:end] {
			line := DiffLine{Kind: step.kind}
			switch step.kind {
			case LineContext:
				line.OldLine, line.NewLine = step.old+1, step.next+1
				line.Text = oldLines[step.old]
				hunk.OldCount++
				hunk.NewCount++
			case LineDelete:
				line.OldLine = step.old + 1
				line.Text = oldLines[step.old]
				hunk.OldCount++
			case LineAdd:
				line.NewLine = step.next + 1
				line.Text = newLines[step.next]
				hunk.NewCount++
			}
			if hunk.OldStart == 0 && line.OldLine > 0 {
				hunk.OldStart = line.OldLine
			}
			if hunk.NewStart == 0 && line.NewLine > 0 {
				hunk.NewStart = line.NewLine
			}
			hunk.Lines = append(hunk.Lines, decorate(line))
		}
		hunks = append(hunks, hunk)
		i = end
	}
	return hunks
}

// splitLines breaks content into lines, keeping a trailing CR visible rather
// than silently normalising it — a file that switched to CRLF is a change the
// operator should be able to see.
func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	text := string(data)
	text = strings.TrimSuffix(text, "\n")
	return strings.Split(text, "\n")
}

// isBinary uses the same test Git does: a NUL byte in the first few kilobytes.
// Invalid UTF-8 counts too, because the panel renders these as text.
func isBinary(data []byte) bool {
	head := data
	if len(head) > 8000 {
		head = head[:8000]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return true
	}
	return !utf8.Valid(head)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
