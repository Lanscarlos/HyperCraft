package velocitycfg

import (
	"strings"
	"testing"
)

const sample = `# Config version. Do not change this
config-version = "2.7"

bind = "0.0.0.0:25577"
motd = "<#09add3>A Velocity Server" # what players see
show-max-players = 500
online-mode = true

[servers]
# Configure your servers here.
lobby = "127.0.0.1:30066"
survival = "127.0.0.1:30067"

try = [
    "lobby"
]

[advanced]
compression-threshold = 256
`

func parse(t *testing.T, text string) *File {
	t.Helper()
	file, err := Parse(strings.NewReader(text))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return file
}

func TestReadsScalarsAndTables(t *testing.T) {
	file := parse(t, sample)

	for _, want := range []struct {
		section, key, value string
	}{
		{"", "bind", "0.0.0.0:25577"},
		{"", "motd", "<#09add3>A Velocity Server"},
		{"", "show-max-players", "500"},
		{"", "online-mode", "true"},
		{"servers", "lobby", "127.0.0.1:30066"},
		{"advanced", "compression-threshold", "256"},
	} {
		got, ok := file.Value(want.section, want.key)
		if !ok {
			t.Errorf("%s.%s is missing", want.section, want.key)
			continue
		}
		if got != want.value {
			t.Errorf("%s.%s = %q, want %q", want.section, want.key, got, want.value)
		}
	}

	// The same key name in two tables must not collide: compression-threshold
	// is only in [advanced], and nothing at the top level answers to it.
	if _, ok := file.Value("", "compression-threshold"); ok {
		t.Error("a table's key was also readable at the top level")
	}
}

// The try list is written across three lines by Velocity itself. Read one line
// at a time it would lose the entry and leave a stray "]" behind.
func TestReadsAMultiLineArray(t *testing.T) {
	file := parse(t, sample)

	try, ok := file.List("servers", "try")
	if !ok {
		t.Fatal("try is missing")
	}
	if len(try) != 1 || try[0] != "lobby" {
		t.Fatalf("try = %#v, want [lobby]", try)
	}
}

// Everything the panel does not touch has to come back out unchanged: this is a
// file operators also edit by hand, and their comments are the documentation.
func TestSavingKeepsCommentsAndOrder(t *testing.T) {
	file := parse(t, sample)
	file.SetString("", "bind", "0.0.0.0:25565")

	out := file.Render()
	if !strings.Contains(out, "# Config version. Do not change this") {
		t.Error("a leading comment was lost")
	}
	if !strings.Contains(out, "# Configure your servers here.") {
		t.Error("a comment inside a table was lost")
	}
	if !strings.Contains(out, `motd = "<#09add3>A Velocity Server" # what players see`) {
		t.Error("a trailing comment was lost")
	}
	if !strings.Contains(out, `bind = "0.0.0.0:25565"`) {
		t.Error("the new value was not written")
	}
	if strings.Contains(out, "25577") {
		t.Error("the old value is still there")
	}
	if strings.Index(out, "[servers]") > strings.Index(out, "[advanced]") {
		t.Error("the tables came back in the wrong order")
	}

	// Re-reading what we wrote has to produce the same file again — the panel
	// saves this file over and over.
	if again := parse(t, out).Render(); again != out {
		t.Errorf("a second save changed the file:\n%s", again)
	}
}

func TestWritesTypedValues(t *testing.T) {
	file := parse(t, sample)
	file.SetRaw("", "show-max-players", "42")
	file.SetRaw("", "online-mode", "false")
	file.SetString("", "motd", `一个"中文"标语`)

	out := file.Render()
	if !strings.Contains(out, "show-max-players = 42") {
		t.Error("a number was not written bare")
	}
	if !strings.Contains(out, "online-mode = false") {
		t.Error("a boolean was not written bare")
	}
	// TOML is UTF-8: unlike server.properties, Chinese goes in literally.
	if !strings.Contains(out, `motd = "一个\"中文\"标语"`) {
		t.Errorf("the string was not quoted and escaped:\n%s", out)
	}
	if got, _ := parse(t, out).Value("", "motd"); got != `一个"中文"标语` {
		t.Errorf("re-read motd = %q", got)
	}
}

func TestAddsMissingKeysInTheRightTable(t *testing.T) {
	file := parse(t, sample)
	file.SetRaw("advanced", "read-timeout", "30000")
	file.SetRaw("query", "enabled", "true")
	file.SetString("", "ping-passthrough", "ALL")

	out := file.Render()
	reread := parse(t, out)

	if got, ok := reread.Value("advanced", "read-timeout"); !ok || got != "30000" {
		t.Errorf("read-timeout = %q (%v), want 30000", got, ok)
	}
	// A table that did not exist is created rather than the key being dropped.
	if got, ok := reread.Value("query", "enabled"); !ok || got != "true" {
		t.Errorf("query.enabled = %q (%v), want true", got, ok)
	}
	// A new top-level key must land above the first table header, or it would
	// silently become a member of that table.
	if strings.Index(out, "ping-passthrough") > strings.Index(out, "[servers]") {
		t.Error("a top-level key was written inside a table")
	}
}

func TestSetEntriesRewritesTheServerList(t *testing.T) {
	file := parse(t, sample)
	file.SetEntries("servers", []Entry{
		{Key: "lobby", Value: "10.0.0.2:25565"},
		{Key: "creative", Value: "10.0.0.3:25565"},
	}, "try")

	out := file.Render()
	reread := parse(t, out)

	servers := reread.Entries("servers", "try")
	if len(servers) != 2 {
		t.Fatalf("servers = %#v, want two", servers)
	}
	if servers[0].Key != "lobby" || servers[0].Value != "10.0.0.2:25565" {
		t.Errorf("the kept server was not updated in place: %#v", servers[0])
	}
	if servers[1].Key != "creative" {
		t.Errorf("the new server is missing: %#v", servers)
	}
	if strings.Contains(out, "survival") {
		t.Error("a removed server is still in the file")
	}
	// A new server belongs above the try list: try is the order servers are
	// tried in, and it reads as the end of the table.
	if strings.Index(out, "creative") > strings.Index(out, "try = ") {
		t.Error("a new server was written below the try list")
	}
	if try, _ := reread.List("servers", "try"); len(try) != 1 || try[0] != "lobby" {
		t.Errorf("the try list was disturbed: %#v", try)
	}
}

func TestSetListWritesOneLine(t *testing.T) {
	file := parse(t, sample)
	file.SetList("servers", "try", []string{"lobby", "survival"})

	if got, _ := parse(t, file.Render()).List("servers", "try"); len(got) != 2 || got[1] != "survival" {
		t.Errorf("try = %#v", got)
	}
}

// A key whose name is not a bare TOML key — a forced host — has to be quoted
// going out, or the file stops parsing as TOML at all.
func TestQuotesKeysThatNeedIt(t *testing.T) {
	file := parse(t, sample)
	file.SetList("forced-hosts", "lobby.example.com", []string{"lobby"})

	out := file.Render()
	if !strings.Contains(out, `"lobby.example.com" = ["lobby"]`) {
		t.Errorf("the key was not quoted:\n%s", out)
	}
	if got, ok := parse(t, out).List("forced-hosts", "lobby.example.com"); !ok || len(got) != 1 {
		t.Errorf("forced host = %#v (%v)", got, ok)
	}
}

func TestEmptyFileAndDefault(t *testing.T) {
	if !parse(t, "# nothing but a comment\n").Empty() {
		t.Error("a file with no keys is not empty")
	}

	stock := Default()
	if stock.Empty() {
		t.Fatal("the stock config parsed as empty")
	}
	if got, ok := stock.Value("", "bind"); !ok || got != "0.0.0.0:25577" {
		t.Errorf("stock bind = %q (%v)", got, ok)
	}
	if got, ok := stock.Value("", "config-version"); !ok || got == "" {
		t.Errorf("stock config-version = %q (%v)", got, ok)
	}
	// Velocity ships three example sub-servers that point at nothing. Handing
	// those to an operator as if they were their own servers is worse than an
	// empty list.
	if servers := stock.Entries("servers", "try"); len(servers) != 0 {
		t.Errorf("the stock config came with servers: %#v", servers)
	}
	if try, ok := stock.List("servers", "try"); !ok || len(try) != 0 {
		t.Errorf("stock try = %#v (%v)", try, ok)
	}
}
