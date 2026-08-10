package instance

import (
	"strings"
	"testing"
)

// A pseudo-terminal hands over whatever happened to be in the buffer, so a
// character straddling two reads is the normal case, not an edge case. Getting
// this wrong turns every chunk boundary into a replacement character.
func TestStreamDecoderRejoinsCharactersSplitAcrossChunks(t *testing.T) {
	// Sample text per charset, since none of them can spell everything: an em
	// dash is not representable in Shift_JIS, and a sample the encoder rejects
	// falls back to UTF-8 and would leave this testing mojibake against itself.
	samples := map[string]string{
		EncodingUTF8: "你好，世界 — Done (1.0s)!",
		"gbk":        "你好，世界 Done (1.0s)!",
		"gb18030":    "你好，世界 Done (1.0s)!",
		"big5":       "你好，世界 Done (1.0s)!",
		"shift_jis":  "こんにちは世界 Done (1.0s)!",
		"euc-kr":     "안녕하세요 Done (1.0s)!",
	}

	for name, text := range samples {
		t.Run(name, func(t *testing.T) {
			cd := newCodec(name)
			raw := cd.encode(text)
			if got := cd.decode(raw); got != text {
				t.Fatalf("sample is not representable in %s: round-trips to %q", name, got)
			}

			// Every possible split point, one byte at a time in the worst case.
			for size := 1; size <= len(raw); size++ {
				dec := newStreamDecoder(cd)
				var got strings.Builder
				for start := 0; start < len(raw); start += size {
					end := min(start+size, len(raw))
					got.Write(dec.decode(raw[start:end]))
				}
				got.Write(dec.flush())

				if got.String() != text {
					t.Fatalf("chunk size %d: got %q, want %q", size, got.String(), text)
				}
			}
		})
	}
}

// Auto resolves once for a stream rather than sniffing each line; on a UTF-8
// host that means bytes pass through untouched.
func TestStreamDecoderPassesUTF8Through(t *testing.T) {
	dec := newStreamDecoder(newCodec(EncodingUTF8))
	if got := string(dec.decode([]byte("plain ascii"))); got != "plain ascii" {
		t.Errorf("got %q", got)
	}
}

// Bytes that are not valid in the declared charset must not be able to stall
// the stream: they surface as replacement characters and everything after them
// keeps flowing.
func TestStreamDecoderDoesNotStallOnBrokenInput(t *testing.T) {
	dec := newStreamDecoder(newCodec(EncodingUTF8))

	var got strings.Builder
	got.Write(dec.decode([]byte{'a', 0xff, 0xfe, 'b'}))
	got.Write(dec.decode([]byte("c")))
	got.Write(dec.flush())

	out := got.String()
	if !strings.HasPrefix(out, "a") || !strings.HasSuffix(out, "bc") {
		t.Errorf("broken bytes swallowed their neighbours: %q", out)
	}
}

// A partial character left over when the server exits has to surface as
// something, rather than being silently dropped along with the rest.
func TestStreamDecoderFlushesAPartialCharacter(t *testing.T) {
	cd := newCodec(EncodingUTF8)
	raw := []byte("你")

	dec := newStreamDecoder(cd)
	if got := dec.decode(raw[:2]); len(got) != 0 {
		t.Errorf("an incomplete character should be held back, got %q", got)
	}
	if got := dec.flush(); len(got) == 0 {
		t.Error("flush dropped the incomplete character instead of surfacing it")
	}
}

func TestLineSplitter(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
		want   []string
		tail   string
	}{{
		name:   "splits on newlines",
		chunks: []string{"one\ntwo\n"},
		want:   []string{"one", "two"},
	}, {
		name:   "joins a line split across chunks",
		chunks: []string{"hel", "lo wor", "ld\n"},
		want:   []string{"hello world"},
	}, {
		name:   "strips the CR of a CRLF ending",
		chunks: []string{"windows\r\n"},
		want:   []string{"windows"},
	}, {
		name:   "keeps only what a redraw left on screen",
		chunks: []string{"10%\r60%\r100%\n"},
		want:   []string{"100%"},
	}, {
		name:   "holds a line that has no newline yet",
		chunks: []string{"Loading: 42%"},
		want:   nil,
		tail:   "Loading: 42%",
	}, {
		name:   "drops a trailing fragment that is only whitespace",
		chunks: []string{"done\n  "},
		want:   []string{"done"},
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var s lineSplitter
			var got []string
			for _, chunk := range tc.chunks {
				got = append(got, s.push([]byte(chunk))...)
			}

			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("lines: got %q, want %q", got, tc.want)
			}
			tail, ok := s.flush()
			if tc.tail == "" && ok {
				t.Errorf("unexpected trailing fragment %q", tail)
			}
			if tc.tail != "" && tail != tc.tail {
				t.Errorf("tail: got %q, want %q", tail, tc.tail)
			}
		})
	}
}
