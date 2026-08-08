package mcprops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `#Minecraft server properties
#Fri Aug 08 12:00:00 CST 2025
motd=A Minecraft Server
max-players=20

# a comment in the middle
online-mode=true
level-name=world
`

func parse(t *testing.T, s string) *File {
	t.Helper()
	f, err := Parse(strings.NewReader(s))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return f
}

func TestRoundTripPreservesLayout(t *testing.T) {
	f := parse(t, sample)

	if got := string(f.Bytes()); got != sample {
		t.Errorf("round trip changed the file.\n--- got ---\n%s\n--- want ---\n%s", got, sample)
	}
}

func TestSetUpdatesInPlaceAndAppends(t *testing.T) {
	f := parse(t, sample)
	f.Set("max-players", "64")
	f.Set("view-distance", "12")

	out := string(f.Bytes())
	if !strings.Contains(out, "max-players=64") {
		t.Errorf("max-players was not updated:\n%s", out)
	}
	if strings.Contains(out, "max-players=20") {
		t.Errorf("old max-players value survived:\n%s", out)
	}
	if !strings.Contains(out, "# a comment in the middle") {
		t.Errorf("comments were lost:\n%s", out)
	}

	// The updated key must stay where it was, and the new one go to the end.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if lines[3] != "max-players=64" {
		t.Errorf("max-players moved: line 4 is %q", lines[3])
	}
	if lines[len(lines)-1] != "view-distance=12" {
		t.Errorf("new key was not appended: last line is %q", lines[len(lines)-1])
	}
}

// Minecraft reads server.properties as ISO-8859-1, so a Chinese MOTD only
// survives if it is written as \uXXXX escapes.
func TestNonASCIIIsEscapedForMinecraft(t *testing.T) {
	f := parse(t, sample)
	f.Set("motd", "我的世界服务器 §a欢迎")

	out := string(f.Bytes())
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "motd=") {
			continue
		}
		if strings.ContainsAny(line, "我界欢") {
			t.Errorf("MOTD was written as raw UTF-8, Minecraft will mojibake it: %q", line)
		}
		if !strings.Contains(line, `\u6211`) { // 我
			t.Errorf("expected a \\uXXXX escape in %q", line)
		}
		if !strings.Contains(line, `\u00A7`) { // §
			t.Errorf("expected the section sign to be escaped in %q", line)
		}
	}

	// And reading it back must give the original string.
	reparsed := parse(t, out)
	if got, _ := reparsed.Get("motd"); got != "我的世界服务器 §a欢迎" {
		t.Errorf("MOTD did not survive the round trip: %q", got)
	}
}

func TestUnescapeHandlesSurrogatePairs(t *testing.T) {
	f := parse(t, "motd=hi \\uD83D\\uDE00\n")
	got, _ := f.Get("motd")
	if got != "hi 😀" {
		t.Errorf("surrogate pair not decoded: %q", got)
	}

	// And it must re-encode as a pair, not as a lone replacement char.
	if out := string(f.Bytes()); !strings.Contains(out, `\uD83D\uDE00`) {
		t.Errorf("surrogate pair not re-encoded: %q", out)
	}
}

func TestColonSeparatorAndEscapes(t *testing.T) {
	f := parse(t, "resource-pack:http\\://example.com/pack.zip\nrcon.password=s3cret\n")

	if got, ok := f.Get("resource-pack"); !ok || got != "http://example.com/pack.zip" {
		t.Errorf("colon-separated entry parsed as %q (ok=%v)", got, ok)
	}
	if got, ok := f.Get("rcon.password"); !ok || got != "s3cret" {
		t.Errorf("dotted key parsed as %q (ok=%v)", got, ok)
	}
}

func TestMissingFileLoadsEmpty(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "server.properties"))
	if err != nil {
		t.Fatalf("Load of a missing file should succeed, got %v", err)
	}
	if n := len(f.Entries()); n != 0 {
		t.Errorf("expected no entries, got %d", n)
	}
}

func TestSaveIsAtomicAndReadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.properties")

	f := parse(t, sample)
	f.Set("server-port", "25566")
	if err := f.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "server-port=25566") {
		t.Errorf("value not persisted:\n%s", data)
	}

	// The temp file used for the atomic rename must not be left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected only server.properties, found %v", names)
	}
}
