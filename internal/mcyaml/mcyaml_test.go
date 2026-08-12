package mcyaml

import (
	"strings"
	"testing"
)

const spigotSample = `# This is the main configuration file for Spigot.
config-version: 12
settings:
  debug: false
  bungeecord: false # 走代理时打开
  save-user-cache-on-stop-only: false
messages:
  whitelist: You are not whitelisted on this server!
  unknown-command: Unknown command. Type "/help" for help.
world-settings:
  default:
    verbose: true
    mob-spawn-range: 6
    entity-activation-range:
      animals: 32
      monsters: 32
`

func parse(t *testing.T, text string) *File {
	t.Helper()
	file, err := Parse(strings.NewReader(text))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return file
}

func TestParseReadsNestedPaths(t *testing.T) {
	file := parse(t, spigotSample)

	cases := map[string]string{
		"config-version":                                         "12",
		"settings.bungeecord":                                    "false",
		"messages.whitelist":                                     "You are not whitelisted on this server!",
		"messages.unknown-command":                               `Unknown command. Type "/help" for help.`,
		"world-settings.default.mob-spawn-range":                 "6",
		"world-settings.default.entity-activation-range.animals": "32",
	}
	for path, want := range cases {
		got, ok := file.Get(path)
		if !ok {
			t.Fatalf("Get(%q): not found", path)
		}
		if got != want {
			t.Fatalf("Get(%q) = %q, want %q", path, got, want)
		}
	}

	if _, ok := file.Get("settings"); ok {
		t.Fatal("a map should not read as a scalar")
	}
	if !file.Has("settings") {
		t.Fatal("Has(settings) = false")
	}
}

// The whole reason this is not a YAML library: an untouched file has to come
// back byte for byte, comments and all.
func TestRenderPreservesUntouchedFile(t *testing.T) {
	file := parse(t, spigotSample)
	if got := file.Render(); got != spigotSample {
		t.Fatalf("Render changed the file:\n%s", got)
	}
}

func TestSetExistingKeepsCommentAndLayout(t *testing.T) {
	file := parse(t, spigotSample)
	if err := file.SetBool("settings.bungeecord", true); err != nil {
		t.Fatalf("SetBool: %v", err)
	}

	got := file.Render()
	if !strings.Contains(got, "  bungeecord: true # 走代理时打开") {
		t.Fatalf("value or trailing comment lost:\n%s", got)
	}
	if strings.Count(got, "bungeecord") != 1 {
		t.Fatalf("key written twice:\n%s", got)
	}
}

func TestSetCreatesMissingParents(t *testing.T) {
	file := parse(t, "# Paper global config\nverbose: false\n")
	if err := file.SetBool("proxies.velocity.enabled", true); err != nil {
		t.Fatalf("SetBool: %v", err)
	}
	if err := file.SetString("proxies.velocity.secret", "s3cret"); err != nil {
		t.Fatalf("SetString: %v", err)
	}

	want := "# Paper global config\nverbose: false\nproxies:\n  velocity:\n    enabled: true\n    secret: s3cret\n"
	if got := file.Render(); got != want {
		t.Fatalf("Render =\n%s\nwant\n%s", got, want)
	}

	// And the file it produced has to read back the same way.
	again := parse(t, file.Render())
	if value, ok := again.Get("proxies.velocity.secret"); !ok || value != "s3cret" {
		t.Fatalf("round trip lost the secret: %q %v", value, ok)
	}
}

func TestSetInsertsIntoExistingBlock(t *testing.T) {
	file := parse(t, "proxies:\n  velocity:\n    enabled: false\n\n# next section\nmisc:\n  x: 1\n")
	if err := file.SetString("proxies.velocity.secret", "abc"); err != nil {
		t.Fatalf("SetString: %v", err)
	}

	want := "proxies:\n  velocity:\n    enabled: false\n    secret: abc\n\n# next section\nmisc:\n  x: 1\n"
	if got := file.Render(); got != want {
		t.Fatalf("Render =\n%s\nwant\n%s", got, want)
	}
}

// A four-space file gets four-space children. Two would still parse, but the
// operator's next hand-edit would be to fix it back.
func TestSetFollowsTheFilesOwnIndent(t *testing.T) {
	file := parse(t, "settings:\n    debug: false\n")
	if err := file.SetBool("settings.extra.deep", true); err != nil {
		t.Fatalf("SetBool: %v", err)
	}
	want := "settings:\n    debug: false\n    extra:\n        deep: true\n"
	if got := file.Render(); got != want {
		t.Fatalf("Render =\n%s\nwant\n%s", got, want)
	}
}

func TestSetOnEmptyFile(t *testing.T) {
	file := parse(t, "")
	if err := file.SetBool("settings.velocity-support.enabled", true); err != nil {
		t.Fatalf("SetBool: %v", err)
	}
	want := "settings:\n  velocity-support:\n    enabled: true\n"
	if got := file.Render(); got != want {
		t.Fatalf("Render =\n%s\nwant\n%s", got, want)
	}
	if file.Empty() {
		t.Fatal("Empty() = true after a write")
	}
}

func TestListsAndBlockScalarsAreNotRewritten(t *testing.T) {
	source := "worlds:\n  - world\n  - world_nether\nmotd: |\n  line one\n  line two\nflow: [a, b]\n"
	file := parse(t, source)

	for _, path := range []string{"worlds", "motd", "flow"} {
		if err := file.SetString(path, "nope"); err == nil {
			t.Fatalf("SetString(%q) should have refused", path)
		}
	}
	if got := file.Render(); got != source {
		t.Fatalf("Render changed the file:\n%s", got)
	}
	// The block scalar's own lines must not have been read as settings.
	if file.Has("line one") {
		t.Fatal("a block scalar's body was parsed as a key")
	}
}

func TestSetOverAMapIsRefused(t *testing.T) {
	file := parse(t, "settings:\n  debug: false\n")
	if err := file.SetString("settings", "x"); err == nil {
		t.Fatal("writing a scalar over a map should have been refused")
	}
}

func TestQuotingAndDecoding(t *testing.T) {
	cases := []struct{ value, encoded string }{
		{"lobby", "lobby"},
		{"", `""`},
		{"true", `"true"`},
		{"Unknown command: try /help", `"Unknown command: try /help"`},
		{"你好，世界", "你好，世界"},
		{`say "hi"`, `"say \"hi\""`},
		{"-dash", `"-dash"`},
	}
	for _, test := range cases {
		if got := EncodeString(test.value); got != test.encoded {
			t.Fatalf("EncodeString(%q) = %q, want %q", test.value, got, test.encoded)
		}
		if got := DecodeScalar(test.encoded); got != test.value {
			t.Fatalf("DecodeScalar(%q) = %q, want %q", test.encoded, got, test.value)
		}
	}

	if got := DecodeScalar("'it''s here'"); got != "it's here" {
		t.Fatalf("single quoted decode = %q", got)
	}
}

// A '#' with no space before it is part of the value — a hex colour in a MOTD
// is the case that shows up in the wild.
func TestHashInsideValueIsNotAComment(t *testing.T) {
	file := parse(t, "motd: <#09add3>Hello # 真注释\n")
	value, ok := file.Get("motd")
	if !ok || value != "<#09add3>Hello" {
		t.Fatalf("Get(motd) = %q %v", value, ok)
	}
	if err := file.SetString("motd", "<#123456>Hi"); err != nil {
		t.Fatalf("SetString: %v", err)
	}
	if got := file.Render(); got != "motd: <#123456>Hi # 真注释\n" {
		t.Fatalf("Render = %q", got)
	}
}

func TestBoolFallback(t *testing.T) {
	file := parse(t, "a: true\nb: no\nc: maybe\n")
	if !file.Bool("a", false) {
		t.Fatal("a should read true")
	}
	if file.Bool("b", true) {
		t.Fatal("b should read false")
	}
	if !file.Bool("c", true) || file.Bool("missing", false) {
		t.Fatal("unparseable and missing keys should fall back")
	}
}
