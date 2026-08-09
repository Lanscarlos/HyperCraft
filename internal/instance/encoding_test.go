package instance

import (
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// gbk is what a JVM on a Chinese Windows writes when nobody told it to use
// UTF-8 — the case this whole file exists for.
func gbk(t *testing.T, s string) []byte {
	t.Helper()

	out, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatalf("encode %q as GBK: %v", s, err)
	}
	return out
}

func TestCodecDecodesConfiguredCharset(t *testing.T) {
	const line = "[12:00:00] [Server thread/INFO]: 服务器启动完成"

	cd := newCodec("GBK")
	if got := cd.decode(gbk(t, line)); got != line {
		t.Errorf("GBK line decoded to %q, want %q", got, line)
	}
}

func TestCodecEncodesCommandsForTheServer(t *testing.T) {
	const cmd = "say 你好\n"

	cd := newCodec("gbk")
	got := cd.encode(cmd)
	if want := gbk(t, cmd); string(got) != string(want) {
		t.Errorf("command encoded to % x, want % x", got, want)
	}

	// Auto mode goes out as UTF-8, matching the stdin encoding we ask the JVM
	// for at launch.
	if got := newCodec("").encode(cmd); string(got) != cmd {
		t.Errorf("auto mode encoded %q to % x, want it left alone", cmd, got)
	}
}

// Auto mode is what almost every instance runs, so it has to get both a
// well-behaved UTF-8 server and a legacy one right without being told which
// is which.
func TestAutoModeSniffsPerLine(t *testing.T) {
	cd := &codec{out: simplifiedchinese.GBK, sniff: true}

	const utf8Line = "[12:00:00] [Server thread/INFO]: 完成 ✔ ─ ▶"
	if got := cd.decode([]byte(utf8Line)); got != utf8Line {
		t.Errorf("valid UTF-8 was re-decoded: got %q, want %q", got, utf8Line)
	}

	const legacyLine = "[12:00:00] [Server thread/WARN]: 未知的命令"
	if got := cd.decode(gbk(t, legacyLine)); got != legacyLine {
		t.Errorf("GBK line decoded to %q, want %q", got, legacyLine)
	}
}

// Whatever a server throws at us, the console line has to survive JSON and
// reach the browser as text rather than as replacement soup.
func TestDecodedLinesAreAlwaysValidUTF8(t *testing.T) {
	lines := [][]byte{
		{0xff, 0xfe, 0x00},
		append([]byte("half a rune: "), 0xe4, 0xbd),
		gbk(t, "中文"),
	}

	for _, encodingName := range []string{"auto", "utf-8", "gbk"} {
		cd := newCodec(encodingName)
		for _, raw := range lines {
			if got := cd.decode(raw); !utf8.ValidString(got) {
				t.Errorf("%s: decode(% x) produced invalid UTF-8 %q", encodingName, raw, got)
			}
		}
	}
}

func TestCanonicalEncodingAcceptsTheSpellingsPeopleType(t *testing.T) {
	cases := map[string]string{
		"":           EncodingAuto,
		"Auto":       EncodingAuto,
		"system":     EncodingAuto,
		"UTF8":       EncodingUTF8,
		"utf-8":      EncodingUTF8,
		"GBK":        "gbk",
		"cp936":      "gbk",
		"GB2312":     "gbk",
		"Shift-JIS":  "shift_jis",
		"sjis":       "shift_jis",
		"Big5":       "big5",
		"latin1":     "iso-8859-1",
		"ISO-8859-1": "iso-8859-1",
	}

	for input, want := range cases {
		got, ok := canonicalEncoding(input)
		if !ok || got != want {
			t.Errorf("canonicalEncoding(%q) = %q, %v; want %q, true", input, got, ok, want)
		}
	}

	if _, ok := canonicalEncoding("klingon"); ok {
		t.Error("an unknown charset should be rejected, not silently accepted")
	}
}

func TestConfigRejectsAnUnknownEncoding(t *testing.T) {
	cfg := Config{Name: "n", Directory: "/srv/mc", Jar: "server.jar", Encoding: "klingon"}
	cfg.applyDefaults()
	if err := cfg.validate(); err == nil {
		t.Fatal("expected an unknown encoding to be rejected")
	}
}

// The two flags that put colour back are the whole reason the web console
// looks like cmd instead of a log file.
func TestCommandLineForcesColourAndUTF8(t *testing.T) {
	cfg := Config{Name: "n", Directory: "/srv/mc", Jar: "server.jar", JVMArgs: []string{"-XX:+UseG1GC"}}
	cfg.applyDefaults()

	_, args, err := cfg.commandLine()
	if err != nil {
		t.Fatalf("commandLine: %v", err)
	}
	line := strings.Join(args, " ")

	for _, want := range []string{"-Dterminal.ansi=true", "-Dterminal.jline=false", "-Dfile.encoding=UTF-8", "-Dstdout.encoding=UTF-8"} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %s in %q", want, line)
		}
	}

	// The operator's own args have to win, which means coming last.
	if strings.Index(line, "-XX:+UseG1GC") < strings.Index(line, "-Dterminal.ansi=true") {
		t.Errorf("panel flags must precede the operator's JVM args: %q", line)
	}
	if strings.Index(line, "-Dterminal.ansi=true") > strings.Index(line, "-jar") {
		t.Errorf("panel flags must come before -jar: %q", line)
	}
}

func TestColourAndEncodingFlagsCanBeTurnedOff(t *testing.T) {
	off := false
	cfg := Config{Name: "n", Directory: "/srv/mc", Jar: "server.jar", ForceColor: &off, Encoding: "gbk"}
	cfg.applyDefaults()

	_, args, err := cfg.commandLine()
	if err != nil {
		t.Fatalf("commandLine: %v", err)
	}
	line := strings.Join(args, " ")

	if strings.Contains(line, "terminal.ansi") {
		t.Errorf("colour was disabled but the flag is still there: %q", line)
	}
	// Pinning the console to GBK means we decode it ourselves; forcing the JVM
	// to UTF-8 as well would leave the two disagreeing.
	if strings.Contains(line, "file.encoding") {
		t.Errorf("a non-UTF-8 console must not get UTF-8 flags: %q", line)
	}
}
