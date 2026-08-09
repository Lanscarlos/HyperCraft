package instance

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

// EncodingAuto keeps the console bytes untouched when they are already UTF-8
// and falls back to the host's own code page when they are not. It is the
// default because it is right for both a Linux panel (UTF-8 everywhere) and a
// Chinese Windows one, where a JVM we did not launch still writes GBK.
const EncodingAuto = "auto"

// EncodingUTF8 is the charset the panel, the websocket and the browser all
// speak, so it is also the one we ask the JVM for.
const EncodingUTF8 = "utf-8"

// charsets are the console encodings an instance can be pinned to. UTF-8 maps
// to nil: it needs no conversion, only validation.
var charsets = map[string]encoding.Encoding{
	EncodingUTF8:   nil,
	"gbk":          simplifiedchinese.GBK,
	"gb18030":      simplifiedchinese.GB18030,
	"big5":         traditionalchinese.Big5,
	"shift_jis":    japanese.ShiftJIS,
	"euc-jp":       japanese.EUCJP,
	"euc-kr":       korean.EUCKR,
	"windows-1252": charmap.Windows1252,
	"iso-8859-1":   charmap.ISO8859_1,
}

// charsetAliases maps the spellings people actually type — code page numbers,
// hyphen-free forms — onto the canonical names above. Keys are squashed by
// squashCharsetName, so "Shift-JIS", "shift_jis" and "sjis" all land here.
var charsetAliases = map[string]string{
	"utf8":        EncodingUTF8,
	"cp65001":     EncodingUTF8,
	"gbk":         "gbk",
	"cp936":       "gbk",
	"ms936":       "gbk",
	"gb2312":      "gbk", // GBK is a superset; decoding GB2312 with it is safe.
	"gb18030":     "gb18030",
	"cp54936":     "gb18030",
	"big5":        "big5",
	"cp950":       "big5",
	"shiftjis":    "shift_jis",
	"sjis":        "shift_jis",
	"cp932":       "shift_jis",
	"mskanji":     "shift_jis",
	"eucjp":       "euc-jp",
	"euckr":       "euc-kr",
	"cp949":       "euc-kr",
	"windows1252": "windows-1252",
	"cp1252":      "windows-1252",
	"iso88591":    "iso-8859-1",
	"latin1":      "iso-8859-1",
}

// squashCharsetName reduces a charset spelling to its letters and digits so
// the alias table does not need an entry per punctuation variant.
func squashCharsetName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// canonicalEncoding resolves a user-supplied encoding name. An empty name
// means auto.
func canonicalEncoding(name string) (string, bool) {
	squashed := squashCharsetName(name)
	switch squashed {
	case "", "auto", "default", "system":
		return EncodingAuto, true
	}
	if canonical, ok := charsetAliases[squashed]; ok {
		return canonical, true
	}
	return "", false
}

// codec converts between the panel's UTF-8 world and whatever bytes the server
// process actually speaks.
//
// Output and input are deliberately allowed to differ. In auto mode we launch
// the JVM with UTF-8 stdio flags, so UTF-8 is what it expects on stdin, while
// its *output* may still arrive in the host code page — a start.sh wrapper, a
// custom command or a JVM whose flags the operator overrode never saw our
// flags. Sniffing each line handles that without mangling the common case.
type codec struct {
	// out decodes server output; nil means "already UTF-8".
	out encoding.Encoding
	// in encodes what we write to stdin; nil means UTF-8.
	in encoding.Encoding
	// sniff keeps valid UTF-8 lines untouched and only applies out to the
	// rest. Only auto mode sets it.
	sniff bool
}

// newCodec builds the converter for a configured encoding name. Unknown names
// are rejected by Config.validate long before this, so anything unexpected
// here degrades to UTF-8 rather than failing a start.
func newCodec(name string) *codec {
	canonical, ok := canonicalEncoding(name)
	if !ok {
		canonical = EncodingAuto
	}
	if canonical == EncodingAuto {
		return &codec{out: systemCharset(), sniff: true}
	}
	enc := charsets[canonical]
	return &codec{out: enc, in: enc}
}

// decode turns one raw console line into a UTF-8 string. The result is always
// valid UTF-8: invalid bytes become U+FFFD instead of travelling through JSON
// as something the browser renders as garbage.
func (c *codec) decode(b []byte) string {
	if c.out == nil {
		return sanitizeUTF8(b)
	}
	if c.sniff && utf8.Valid(b) {
		return string(b)
	}
	decoded, err := c.out.NewDecoder().Bytes(b)
	if err != nil {
		return sanitizeUTF8(b)
	}
	return sanitizeUTF8(decoded)
}

// encode turns a command typed in the browser into the bytes the server reads.
func (c *codec) encode(s string) []byte {
	if c.in == nil {
		return []byte(s)
	}
	encoded, err := c.in.NewEncoder().Bytes([]byte(s))
	if err != nil {
		// A rune the target charset cannot represent. Sending the UTF-8 form
		// is more useful than sending nothing at all.
		return []byte(s)
	}
	return encoded
}

// sanitizeUTF8 replaces invalid byte sequences with U+FFFD, leaving valid
// input allocation-free.
func sanitizeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return strings.ToValidUTF8(string(b), "�")
}
