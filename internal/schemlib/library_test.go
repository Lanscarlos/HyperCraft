package schemlib

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/* -------------------------------------------------------------- fixtures

   internal/schematic has no writer, so a schematic is assembled by hand here.
   Kept in the test file for the same reason it is over there: a writer in the
   package would be dead code, and the one thing that could make a round trip
   pass with both halves wrong. */

const (
	tagEnd       = 0
	tagByteArray = 7
	tagString    = 8
	tagList      = 9
	tagCompound  = 10
	tagInt       = 3
	tagShort     = 2
)

func tag(kind byte, name string, payload []byte) []byte {
	out := []byte{kind}
	out = append(out, pString(name)...)
	return append(out, payload...)
}

func pString(s string) []byte {
	out := make([]byte, 2, 2+len(s))
	binary.BigEndian.PutUint16(out, uint16(len(s)))
	return append(out, s...)
}

func pShort(v int) []byte {
	out := make([]byte, 2)
	binary.BigEndian.PutUint16(out, uint16(v))
	return out
}

func pInt(v int) []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, uint32(v))
	return out
}

func pCompound(children ...[]byte) []byte {
	var out bytes.Buffer
	for _, child := range children {
		out.Write(child)
	}
	out.WriteByte(tagEnd)
	return out.Bytes()
}

// schem is a gzipped Sponge v2 schematic: a 2×2×2 cube whose bottom layer is
// stone with one air corner and whose top layer is oak planks. `name` goes into
// the metadata, so a test can tell two otherwise identical files apart — which
// is also what makes their digests differ.
func schem(t *testing.T, name string) []byte {
	t.Helper()
	blocks := []byte{1, 1, 1, 0, 2, 2, 2, 2} // one varint per block, all < 0x80

	root := tag(tagCompound, "Schematic", pCompound(
		tag(tagInt, "Version", pInt(2)),
		tag(tagInt, "DataVersion", pInt(3465)),
		tag(tagShort, "Width", pShort(2)),
		tag(tagShort, "Height", pShort(2)),
		tag(tagShort, "Length", pShort(2)),
		tag(tagCompound, "Palette", pCompound(
			tag(tagInt, "minecraft:air", pInt(0)),
			tag(tagInt, "minecraft:stone", pInt(1)),
			tag(tagInt, "minecraft:oak_planks", pInt(2)),
		)),
		tag(tagByteArray, "BlockData", append(pInt(len(blocks)), blocks...)),
		tag(tagCompound, "Metadata", pCompound(
			tag(tagString, "Author", pString("notch")),
			tag(tagString, "Name", pString(name)),
		)),
		tag(tagList, "BlockEntities", append([]byte{tagCompound}, pInt(0)...)),
	))

	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	if _, err := zw.Write(root); err != nil {
		t.Fatalf("gzip: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return out.Bytes()
}

func newLibrary(t *testing.T) *Library {
	t.Helper()
	return NewLibrary(t.TempDir())
}

func add(t *testing.T, l *Library, name, file string) Entry {
	t.Helper()
	entry, err := l.Add(name, file, bytes.NewReader(schem(t, name)), Origin{Kind: OriginUpload}, 0)
	if err != nil {
		t.Fatalf("Add(%q): %v", file, err)
	}
	return entry
}

/* ----------------------------------------------------------------- tests */

func TestAddStoresTheFileAndWhatIsInIt(t *testing.T) {
	library := newLibrary(t)
	entry := add(t, library, "主城", "spawn.schem")

	if entry.Facts.Width != 2 || entry.Facts.Height != 2 || entry.Facts.Length != 2 {
		t.Errorf("region %d×%d×%d, want 2×2×2",
			entry.Facts.Width, entry.Facts.Height, entry.Facts.Length)
	}
	if entry.Facts.NonAir != 7 || entry.Facts.Volume != 8 {
		t.Errorf("blocks %d/%d, want 7/8", entry.Facts.NonAir, entry.Facts.Volume)
	}
	// Two distinct non-air states, most-used first: four planks then three
	// stone. The list is what the row is drawn from, so the order is the point.
	if entry.Facts.Kinds != 2 || len(entry.Facts.Top) != 2 {
		t.Fatalf("kinds = %d, top = %v", entry.Facts.Kinds, entry.Facts.Top)
	}
	if entry.Facts.Top[0].Name != "minecraft:oak_planks" || entry.Facts.Top[0].Count != 4 {
		t.Errorf("top block = %+v, want 4× oak_planks", entry.Facts.Top[0])
	}
	if entry.Facts.Author != "notch" || entry.Facts.SavedName != "主城" {
		t.Errorf("metadata = %q by %q", entry.Facts.SavedName, entry.Facts.Author)
	}
	if entry.SHA256 == "" || entry.Size == 0 {
		t.Errorf("entry = %+v, want a digest and a size", entry)
	}

	// The file has to be a real file under a real name: an install copies it
	// into a server directory and //load takes that name.
	if _, err := os.Stat(filepath.Join(library.Root(), entry.FileName)); err != nil {
		t.Errorf("stat %s: %v", entry.FileName, err)
	}
	if len(library.List()) != 1 {
		t.Errorf("List() = %d entries, want 1", len(library.List()))
	}
}

func TestAddRefusesTheSameFileTwice(t *testing.T) {
	library := newLibrary(t)
	add(t, library, "主城", "spawn.schem")

	// A different name, the same bytes. Uploading a file twice is never
	// deliberate, and two rows of it is how a library stops being browsable.
	_, err := library.Add("另一个", "other.schem",
		bytes.NewReader(schem(t, "主城")), Origin{Kind: OriginUpload}, 0)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("Add duplicate = %v, want ErrExists", err)
	}
	if got := len(library.List()); got != 1 {
		t.Errorf("List() = %d entries, want 1", got)
	}
	if names, _ := filepath.Glob(filepath.Join(library.Root(), "*"+partSuffix)); len(names) > 0 {
		t.Errorf("left staged files behind: %v", names)
	}
}

func TestAddRefusesSomethingThatIsNotASchematic(t *testing.T) {
	library := newLibrary(t)

	if _, err := library.Add("", "notes.txt", strings.NewReader("hello"), Origin{}, 0); !errors.Is(err, ErrNotSchematic) {
		t.Errorf("Add(.txt) = %v, want ErrNotSchematic", err)
	}
	// The right extension on the wrong bytes is the interesting case: it is
	// what a half-finished download looks like.
	_, err := library.Add("", "broken.schem", strings.NewReader("not nbt at all"), Origin{}, 0)
	if err == nil {
		t.Fatal("Add(garbage) succeeded, want a parse failure")
	}
	if entries, _ := os.ReadDir(library.Root()); len(entries) != 0 {
		t.Errorf("library root = %v, want nothing kept", entries)
	}
}

func TestAddKeepsNamesApartOnDisk(t *testing.T) {
	library := newLibrary(t)
	first := add(t, library, "主城", "spawn.schem")
	// Same name, different bytes: two builds an operator genuinely called the
	// same thing. Both are kept, and neither may overwrite the other's file.
	second, err := library.Add("主城", "spawn.schem",
		bytes.NewReader(schem(t, "主城 v2")), Origin{Kind: OriginUpload}, 0)
	if err != nil {
		t.Fatalf("Add second: %v", err)
	}

	if first.ID == second.ID {
		t.Errorf("both entries got id %q", first.ID)
	}
	if first.FileName == second.FileName {
		t.Errorf("both entries got file %q", first.FileName)
	}
	// The id is derived from the name rather than from a counter, and CJK
	// survives it: a library of 建筑-1, 建筑-2 is one nobody can search.
	if !strings.HasPrefix(first.ID, "主城") {
		t.Errorf("id = %q, want it to start from the name", first.ID)
	}
}

func TestRescanAdoptsLooseFilesAndForgetsMissingOnes(t *testing.T) {
	library := newLibrary(t)
	gone := add(t, library, "会消失的", "gone.schem")

	// A file dropped into the directory by hand — the migration path off
	// another panel, and the reason the library is a plain folder.
	if err := os.WriteFile(filepath.Join(library.Root(), "castle.schem"), schem(t, "城堡"), 0o644); err != nil {
		t.Fatalf("write loose file: %v", err)
	}
	// And a file that is not a schematic at all, which must be left alone.
	if err := os.WriteFile(filepath.Join(library.Root(), "readme.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.Remove(filepath.Join(library.Root(), gone.FileName)); err != nil {
		t.Fatalf("remove: %v", err)
	}

	added, dropped := library.Rescan()
	if added != 1 || dropped != 1 {
		t.Fatalf("Rescan() = %d added, %d dropped; want 1, 1", added, dropped)
	}

	entries := library.List()
	if len(entries) != 1 {
		t.Fatalf("List() = %+v, want one entry", entries)
	}
	if entries[0].FileName != "castle.schem" {
		t.Errorf("adopted %q, want castle.schem", entries[0].FileName)
	}
	// The name inside the file beats the file name: the builder named it.
	if entries[0].Name != "城堡" {
		t.Errorf("name = %q, want 城堡", entries[0].Name)
	}
	if entries[0].Origin.Kind != OriginFound {
		t.Errorf("origin = %q, want %q", entries[0].Origin.Kind, OriginFound)
	}
	if entries[0].Facts.NonAir != 7 {
		t.Errorf("adopted entry was not parsed: %+v", entries[0].Facts)
	}
}

func TestRescanKeepsAFileItCannotRead(t *testing.T) {
	library := newLibrary(t)
	if err := os.WriteFile(filepath.Join(library.Root(), "broken.schem"), []byte("nope"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Refusing it would mean the panel silently ignoring a file sitting in its
	// own directory, which looks exactly like the daemon being broken.
	if added, _ := library.Rescan(); added != 1 {
		t.Fatalf("Rescan() adopted %d, want 1", added)
	}
	entries := library.List()
	if len(entries) != 1 || entries[0].Facts.Unreadable == "" {
		t.Fatalf("entries = %+v, want one carrying a reason", entries)
	}
}

func TestEditKeepsTheFileName(t *testing.T) {
	library := newLibrary(t)
	entry := add(t, library, "主城", "spawn.schem")

	updated, err := library.Edit(entry.ID, "主城（2024）", "  第三季的出生点  ", []string{"出生点", " 出生点 ", ""})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if updated.Name != "主城（2024）" || updated.Note != "第三季的出生点" {
		t.Errorf("edited = %+v", updated)
	}
	// Tags are de-duplicated case- and whitespace-insensitively, and blanks are
	// dropped: they come out of a free-text box.
	if len(updated.Tags) != 1 || updated.Tags[0] != "出生点" {
		t.Errorf("tags = %v, want [出生点]", updated.Tags)
	}
	// Renaming an entry must not rename the file: the name on disk is what an
	// operator types into //schem load.
	if updated.FileName != entry.FileName {
		t.Errorf("file name changed from %q to %q", entry.FileName, updated.FileName)
	}
}

func TestRemoveDeletesTheFile(t *testing.T) {
	library := newLibrary(t)
	entry := add(t, library, "主城", "spawn.schem")

	if err := library.Remove(entry.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := library.Get(entry.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Remove = %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(filepath.Join(library.Root(), entry.FileName)); !os.IsNotExist(err) {
		t.Errorf("file survived Remove: %v", err)
	}
}

func TestPreviewReadsTheWholeRegion(t *testing.T) {
	library := newLibrary(t)
	entry := add(t, library, "主城", "spawn.schem")

	// The library's preview is the file manager's preview pointed somewhere
	// else — same parse, same payload, so the browser draws it with the
	// renderer it already has.
	preview, got, err := library.Preview(entry.ID)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if got.ID != entry.ID {
		t.Errorf("Preview returned entry %q, want %q", got.ID, entry.ID)
	}
	if preview.Blocks == "" || preview.Runs == 0 {
		t.Errorf("preview carries no block payload: %+v", preview)
	}
}

func TestIndexSurvivesARestart(t *testing.T) {
	root := t.TempDir()
	first := NewLibrary(root)
	entry, err := first.Add("主城", "spawn.schem",
		bytes.NewReader(schem(t, "主城")), Origin{Kind: OriginMarket, From: "某个源"}, 0)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	reopened := NewLibrary(root)
	got, err := reopened.Get(entry.ID)
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.Name != "主城" || got.Origin.From != "某个源" || got.Facts.NonAir != 7 {
		t.Errorf("reloaded = %+v", got)
	}
}

func TestInvalidIDsAreRefused(t *testing.T) {
	library := newLibrary(t)
	for _, id := range []string{"", ".", "..", "../etc", `a\b`, "x" + partSuffix} {
		if _, err := library.Get(id); !errors.Is(err, ErrInvalidID) && !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q) = %v, want a refusal", id, err)
		}
	}
}

func TestSlugKeepsCJKAndDropsPunctuation(t *testing.T) {
	cases := map[string]string{
		// Full-width punctuation separates like a space does rather than
		// travelling into a file name.
		"主城（旧）":         "主城-旧",
		"Spawn Point":   "spawn-point",
		"../etc/passwd": "etc-passwd",
		"  ":            "",
	}
	for in, want := range cases {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanFileNameStripsPaths(t *testing.T) {
	cases := map[string]string{
		`C:\builds\castle.schem`: "castle.schem",
		"../../etc/passwd":       "passwd",
		"plain.schem":            "plain.schem",
		indexName:                "",
		"x" + partSuffix:         "",
	}
	for in, want := range cases {
		if got := cleanFileName(in); got != want {
			t.Errorf("cleanFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTargetsPrefersTheEditorTheServerHas(t *testing.T) {
	// A server with nothing installed still gets an ordered list to offer, and
	// FastAsyncWorldEdit leads it — a server with both is running FAWE.
	bare := Targets(func(string) bool { return false })
	if len(bare) != 3 || bare[0].Dir != DirFastAsync || bare[0].Present {
		t.Fatalf("bare targets = %+v", bare)
	}

	modded := Targets(func(rel string) bool { return rel == "config/worldedit" })
	if modded[0].Dir != DirModded || !modded[0].Present {
		t.Errorf("modded targets = %+v, want the modded dir first", modded)
	}
	if DefaultTarget(func(rel string) bool { return rel == "plugins/WorldEdit" }) != DirWorldEdit {
		t.Error("a Bukkit server with WorldEdit should default to WorldEdit's own folder")
	}
}

func TestCleanTargetDirRefusesEscapes(t *testing.T) {
	for _, dir := range []string{"", "..", "../elsewhere", "plugins/../..", "/.."} {
		if _, err := CleanTargetDir(dir); err == nil {
			t.Errorf("CleanTargetDir(%q) was accepted", dir)
		}
	}
	// A leading slash is read as "from the instance root", the same way the
	// file manager and the plugin installer read it — not as an absolute path.
	if got, err := CleanTargetDir("/plugins/WorldEdit/schematics/"); err != nil || got != DirWorldEdit {
		t.Errorf("CleanTargetDir = %q, %v", got, err)
	}
}
