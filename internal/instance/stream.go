package instance

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/transform"
)

// A pseudo-terminal hands us arbitrary byte chunks rather than whole lines, so
// the two things the pipe path got for free — a decode boundary that never
// splits a character, and a notion of "line" — have to be rebuilt here.

// maxPending bounds the bytes held back waiting for the rest of a character.
// Every charset the panel speaks completes within four bytes; anything longer
// is broken input that must not be allowed to accumulate.
const maxPending = 16

// maxLine caps a single extracted line, matching the pipe path's scanner limit.
// Crash reports and mod loaders do emit lines this long.
const maxLine = 1024 * 1024

// streamDecoder turns a stream of raw console bytes into UTF-8, holding back a
// trailing partial character until the chunk that completes it arrives.
//
// It is stateful and must be used for exactly one process: charset decoders
// carry state of their own, and rewinding one mid-stream is not possible.
type streamDecoder struct {
	// tr is nil when the stream is already UTF-8, which is the common case.
	tr      transform.Transformer
	pending []byte
}

// newStreamDecoder builds the stream counterpart of codec.decode.
//
// It deliberately does not carry over auto mode's per-line "valid UTF-8 wins"
// sniff. On a stream there are no line boundaries to re-guess at, and a
// decoder that changed its mind halfway through would corrupt the characters
// it had already half-consumed. Auto therefore resolves once, to whatever the
// host locale says (UTF-8 on any modern unix) — which is also what the panel
// asks the JVM for. A server that insists on some other charset needs the
// instance's encoding pinned, and then gets a fully stateful decode of it.
func newStreamDecoder(c *codec) *streamDecoder {
	if c == nil || c.out == nil {
		return &streamDecoder{}
	}
	return &streamDecoder{tr: c.out.NewDecoder()}
}

// decode converts one chunk. The result is always valid UTF-8 and always ends
// on a character boundary, so it can be handed straight to a browser.
func (d *streamDecoder) decode(chunk []byte) []byte {
	if len(chunk) == 0 {
		return nil
	}
	src := chunk
	if len(d.pending) > 0 {
		src = append(d.pending, chunk...)
		d.pending = nil
	}
	if d.tr == nil {
		return d.decodeUTF8(src)
	}
	return d.decodeCharset(src, false)
}

// flush releases anything still held back, once no more bytes are coming. The
// leftover is by definition an incomplete character, so it surfaces as U+FFFD
// rather than disappearing.
func (d *streamDecoder) flush() []byte {
	if len(d.pending) == 0 {
		return nil
	}
	src := d.pending
	d.pending = nil
	if d.tr == nil {
		return []byte(sanitizeUTF8(src))
	}
	return d.decodeCharset(src, true)
}

// decodeUTF8 passes bytes through, holding back a trailing partial rune and
// replacing anything that is not valid UTF-8.
func (d *streamDecoder) decodeUTF8(src []byte) []byte {
	if cut := incompleteTail(src); cut < len(src) {
		if len(src)-cut <= maxPending {
			d.pending = append(d.pending[:0], src[cut:]...)
			src = src[:cut]
		}
	}
	if utf8.Valid(src) {
		return src
	}
	return []byte(strings.ToValidUTF8(string(src), "�"))
}

// decodeCharset runs the stream through a stateful charset decoder. A tail the
// decoder reports as incomplete is held back for the next chunk.
func (d *streamDecoder) decodeCharset(src []byte, atEOF bool) []byte {
	dst := make([]byte, 0, len(src)*2+utf8.UTFMax)
	buf := make([]byte, len(src)*2+utf8.UTFMax)

	for len(src) > 0 {
		nDst, nSrc, err := d.tr.Transform(buf, src, atEOF)
		dst = append(dst, buf[:nDst]...)
		src = src[nSrc:]

		switch {
		case err == transform.ErrShortDst:
			// Output did not fit; go round again with what is left.
			if nDst == 0 && nSrc == 0 {
				buf = make([]byte, len(buf)*2)
			}
			continue
		case err == transform.ErrShortSrc && !atEOF:
			// A character straddling the chunk boundary. Hold it unless the
			// decoder is stuck on input it will never be able to finish.
			if len(src) <= maxPending {
				d.pending = append(d.pending[:0], src...)
				return dst
			}
			// Broken beyond hope: drain it as replacement characters.
			atEOF = true
			continue
		case err != nil:
			// Not a boundary problem. Salvage the rest rather than dropping it.
			return append(dst, []byte(sanitizeUTF8(src))...)
		}
	}
	return dst
}

// incompleteTail reports where a trailing partial UTF-8 sequence begins, or
// len(b) when the input ends on a character boundary.
func incompleteTail(b []byte) int {
	for back := 1; back <= utf8.UTFMax-1 && back <= len(b); back++ {
		start := len(b) - back
		c := b[start]
		if c < utf8.RuneSelf {
			return len(b) // ASCII cannot be the start of a partial sequence
		}
		if !utf8.RuneStart(c) {
			continue // continuation byte; keep walking back to its lead
		}
		if runeSize(c) > back {
			return start
		}
		return len(b)
	}
	return len(b)
}

// runeSize is how many bytes the sequence opened by this lead byte occupies.
func runeSize(lead byte) int {
	switch {
	case lead&0xE0 == 0xC0:
		return 2
	case lead&0xF0 == 0xE0:
		return 3
	case lead&0xF8 == 0xF0:
		return 4
	default:
		return 1 // not a lead byte at all; it stands alone as U+FFFD
	}
}

// lineSplitter reassembles lines from a terminal byte stream, so the panel can
// keep a line-oriented log of a stream that no longer has any.
//
// It is a log view, not a terminal emulator: the only redraw it understands is
// a carriage return, which it resolves the way a real terminal would — what is
// left on screen is whatever was written after the last one. That is enough for
// the progress bars pregenerators and mod loaders draw, and those are what
// would otherwise fill the log with half-finished percentages.
type lineSplitter struct {
	buf []byte
}

// push feeds a decoded chunk in and returns whatever complete lines it finished.
func (s *lineSplitter) push(chunk []byte) []string {
	if len(chunk) == 0 {
		return nil
	}
	s.buf = append(s.buf, chunk...)

	var lines []string
	for {
		idx := bytes.IndexByte(s.buf, '\n')
		if idx < 0 {
			break
		}
		lines = append(lines, collapseCR(string(s.buf[:idx])))
		s.buf = s.buf[idx+1:]
	}

	// A line longer than the cap is emitted as-is rather than buffered forever.
	if len(s.buf) > maxLine {
		lines = append(lines, collapseCR(string(s.buf)))
		s.buf = s.buf[:0]
	}
	// Reclaim the head of the buffer once it has been consumed.
	if len(s.buf) == 0 && cap(s.buf) > 64*1024 {
		s.buf = nil
	}
	return lines
}

// flush returns a trailing partial line, if the stream ended on one.
func (s *lineSplitter) flush() (string, bool) {
	if len(s.buf) == 0 {
		return "", false
	}
	line := collapseCR(string(s.buf))
	s.buf = s.buf[:0]
	if strings.TrimSpace(stripANSI(line)) == "" {
		return "", false
	}
	return line, true
}

// collapseCR reduces a run of carriage-return redraws to what a terminal would
// still be showing: the text written after the last one.
func collapseCR(line string) string {
	line = strings.TrimSuffix(line, "\r")
	if idx := strings.LastIndexByte(line, '\r'); idx >= 0 {
		return line[idx+1:]
	}
	return line
}
