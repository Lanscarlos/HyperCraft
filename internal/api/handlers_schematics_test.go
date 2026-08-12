package api

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/lanscarlos/hypercraft/internal/schemlib"
)

/* --------------------------------------------------------------- fixture

   A Sponge v2 schematic assembled by hand, the same way internal/schematic and
   internal/schemlib build theirs: there is no writer in the tree, and adding
   one for the tests is the single thing that could make a round trip pass with
   both halves wrong. */

func testSchematic(t *testing.T, name string) []byte {
	t.Helper()

	pString := func(s string) []byte {
		out := make([]byte, 2, 2+len(s))
		binary.BigEndian.PutUint16(out, uint16(len(s)))
		return append(out, s...)
	}
	pInt := func(v int) []byte {
		out := make([]byte, 4)
		binary.BigEndian.PutUint32(out, uint32(v))
		return out
	}
	pShort := func(v int) []byte {
		out := make([]byte, 2)
		binary.BigEndian.PutUint16(out, uint16(v))
		return out
	}
	tag := func(kind byte, tagName string, payload []byte) []byte {
		out := append([]byte{kind}, pString(tagName)...)
		return append(out, payload...)
	}
	compound := func(children ...[]byte) []byte {
		var out bytes.Buffer
		for _, child := range children {
			out.Write(child)
		}
		out.WriteByte(0) // TAG_End
		return out.Bytes()
	}

	blocks := []byte{1, 1, 1, 0, 2, 2, 2, 2}
	root := tag(10, "Schematic", compound(
		tag(3, "Version", pInt(2)),
		tag(3, "DataVersion", pInt(3465)),
		tag(2, "Width", pShort(2)),
		tag(2, "Height", pShort(2)),
		tag(2, "Length", pShort(2)),
		tag(10, "Palette", compound(
			tag(3, "minecraft:air", pInt(0)),
			tag(3, "minecraft:stone", pInt(1)),
			tag(3, "minecraft:oak_planks", pInt(2)),
		)),
		tag(7, "BlockData", append(pInt(len(blocks)), blocks...)),
		tag(10, "Metadata", compound(tag(8, "Name", pString(name)))),
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

// uploadSchematic posts one build through the library's multipart endpoint.
func (e *testEnv) uploadSchematic(fileName string, content []byte) *http.Response {
	e.t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		e.t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		e.t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		e.t.Fatalf("close writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, e.server.URL+"/api/schematics/upload", &body)
	if err != nil {
		e.t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set(csrfHeader, "1")

	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("upload: %v", err)
	}
	return resp
}

/* ------------------------------------------------------------------ tests */

func TestSchematicUploadListAndInstall(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("builds")

	resp := env.uploadSchematic("castle.schem", testSchematic(t, "城堡"))
	if resp.StatusCode != http.StatusCreated {
		resp.Body.Close()
		t.Fatalf("upload = %d, want 201", resp.StatusCode)
	}
	var uploaded struct {
		Results []schematicImportResult `json:"results"`
	}
	decodeBody(t, resp, &uploaded)
	if len(uploaded.Results) != 1 || uploaded.Results[0].Entry == nil {
		t.Fatalf("results = %+v", uploaded.Results)
	}
	entry := *uploaded.Results[0].Entry
	// The parse happens on the way in, so the row can describe the build
	// without anything opening it.
	if entry.Facts.NonAir != 7 || entry.Facts.Width != 2 {
		t.Errorf("facts = %+v", entry.Facts)
	}

	listed := env.do(http.MethodGet, "/api/schematics", nil)
	var library schematicLibraryResponse
	decodeBody(t, listed, &library)
	if len(library.Entries) != 1 || library.Entries[0].ID != entry.ID {
		t.Fatalf("library = %+v", library.Entries)
	}
	// The install menu is built from the listing, so the servers and the
	// directories their editors read travel with it.
	if len(library.Targets) != 1 || library.Targets[0].ID != created.ID {
		t.Fatalf("targets = %+v", library.Targets)
	}
	if len(library.Targets[0].Dirs) == 0 || library.Targets[0].Dirs[0].Dir == "" {
		t.Errorf("target dirs = %+v", library.Targets[0].Dirs)
	}

	install := env.do(http.MethodPost, "/api/schematics/"+entry.ID+"/install",
		installSchematicRequest{InstanceID: created.ID})
	var landed installSchematicResponse
	decodeBody(t, install, &landed)
	if landed.Path != schemlib.DirFastAsync+"/"+entry.FileName {
		t.Errorf("path = %q", landed.Path)
	}
	// The next thing that happens after an install is somebody typing it.
	if landed.Command != "//schem load "+"castle" {
		t.Errorf("command = %q", landed.Command)
	}

	onDisk := filepath.Join(created.Directory, filepath.FromSlash(landed.Path))
	if _, err := os.Stat(onDisk); err != nil {
		t.Fatalf("stat installed file: %v", err)
	}

	// A second install of the same build must not silently replace what is
	// there: it is how you lose the copy somebody edited in-game.
	again := env.do(http.MethodPost, "/api/schematics/"+entry.ID+"/install",
		installSchematicRequest{InstanceID: created.ID})
	again.Body.Close()
	if again.StatusCode != http.StatusConflict {
		t.Errorf("second install = %d, want 409", again.StatusCode)
	}

	overwrite := env.do(http.MethodPost, "/api/schematics/"+entry.ID+"/install",
		installSchematicRequest{InstanceID: created.ID, Overwrite: true})
	overwrite.Body.Close()
	if overwrite.StatusCode != http.StatusOK {
		t.Errorf("overwrite = %d, want 200", overwrite.StatusCode)
	}
}

func TestSchematicPreviewCarriesTheRenderPayload(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.uploadSchematic("castle.schem", testSchematic(t, "城堡"))
	var uploaded struct {
		Results []schematicImportResult `json:"results"`
	}
	decodeBody(t, resp, &uploaded)
	entry := *uploaded.Results[0].Entry

	// The library's preview is the file manager's preview pointed somewhere
	// else, so the browser draws it with the renderer it already has.
	preview := env.do(http.MethodGet, "/api/schematics/"+entry.ID+"/preview", nil)
	var body schematicResponse
	decodeBody(t, preview, &body)
	if body.Preview == nil || body.Blocks == "" || body.Runs == 0 {
		t.Fatalf("preview = %+v", body)
	}
	if body.Width != 2 || body.NonAir != 7 {
		t.Errorf("preview = %+v", body.Preview)
	}
}

func TestSchematicUploadRefusesSomethingThatIsNotOne(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	// Per file rather than per request: dragging in thirty builds where two
	// are corrupt should store twenty-eight and say which two.
	resp := env.uploadSchematic("broken.schem", []byte("not nbt at all"))
	if resp.StatusCode != http.StatusCreated {
		resp.Body.Close()
		t.Fatalf("upload = %d, want 201 with a per-file error", resp.StatusCode)
	}
	var uploaded struct {
		Results []schematicImportResult `json:"results"`
	}
	decodeBody(t, resp, &uploaded)
	if len(uploaded.Results) != 1 || uploaded.Results[0].Entry != nil || uploaded.Results[0].Error == "" {
		t.Fatalf("results = %+v", uploaded.Results)
	}

	listed := env.do(http.MethodGet, "/api/schematics", nil)
	var library schematicLibraryResponse
	decodeBody(t, listed, &library)
	if len(library.Entries) != 0 {
		t.Errorf("library = %+v, want nothing kept", library.Entries)
	}
}

func TestSchematicImportedFromAnInstance(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("builds")

	// A build saved in-game with //schem save, sitting where WorldEdit put it.
	dir := filepath.Join(created.Directory, "plugins", "WorldEdit", "schematics")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spawn.schem"), testSchematic(t, "主城"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	resp := env.do(http.MethodPost, "/api/instances/"+created.ID+"/schematics",
		importSchematicRequest{Path: "plugins/WorldEdit/schematics/spawn.schem"})
	var entry schemlib.Entry
	decodeBody(t, resp, &entry)
	if entry.ID == "" || entry.Facts.NonAir != 7 {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.Origin.Kind != schemlib.OriginInstance || entry.Origin.From != "builds" {
		t.Errorf("origin = %+v, want the server it came from", entry.Origin)
	}
	// The file on the server is left exactly where it is.
	if _, err := os.Stat(filepath.Join(dir, "spawn.schem")); err != nil {
		t.Errorf("import moved the original: %v", err)
	}

	// And the other direction is refused for a file that is not a schematic.
	bad := env.do(http.MethodPost, "/api/instances/"+created.ID+"/schematics",
		importSchematicRequest{Path: "server.properties"})
	bad.Body.Close()
	if bad.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("import(.properties) = %d, want 415", bad.StatusCode)
	}
}

func TestSchematicEditAndDelete(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.uploadSchematic("castle.schem", testSchematic(t, "城堡"))
	var uploaded struct {
		Results []schematicImportResult `json:"results"`
	}
	decodeBody(t, resp, &uploaded)
	entry := *uploaded.Results[0].Entry

	edited := env.do(http.MethodPut, "/api/schematics/"+entry.ID,
		editSchematicRequest{Name: "中世纪城堡", Note: "带护城河", Tags: []string{"中世纪"}})
	var updated schemlib.Entry
	decodeBody(t, edited, &updated)
	if updated.Name != "中世纪城堡" || updated.Note != "带护城河" || len(updated.Tags) != 1 {
		t.Errorf("updated = %+v", updated)
	}
	// Renaming an entry must not rename the file: //schem load takes that name.
	if updated.FileName != entry.FileName {
		t.Errorf("file renamed from %q to %q", entry.FileName, updated.FileName)
	}

	removed := env.do(http.MethodDelete, "/api/schematics/"+entry.ID, nil)
	removed.Body.Close()
	if removed.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", removed.StatusCode)
	}
	missing := env.do(http.MethodGet, "/api/schematics/"+entry.ID+"/preview", nil)
	missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Errorf("preview after delete = %d, want 404", missing.StatusCode)
	}
}

func TestSchematicMarketSourcesAreManaged(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	added := env.do(http.MethodPost, "/api/schematics/market/sources",
		schematicSourceRequest{Kind: schemlib.SourceGitHub, URL: "https://github.com/someone/builds"})
	var source schemlib.Source
	decodeBody(t, added, &source)
	if source.URL != "someone/builds" || source.Kind != schemlib.SourceGitHub {
		t.Fatalf("source = %+v", source)
	}

	off := true
	toggled := env.do(http.MethodPut, "/api/schematics/market/sources/"+source.ID,
		schematicSourceRequest{Disabled: &off})
	var updated schemlib.Source
	decodeBody(t, toggled, &updated)
	if !updated.Disabled {
		t.Errorf("source = %+v, want it switched off", updated)
	}

	gone := env.do(http.MethodDelete, "/api/schematics/market/sources/"+source.ID, nil)
	gone.Body.Close()
	if gone.StatusCode != http.StatusNoContent {
		t.Errorf("delete = %d, want 204", gone.StatusCode)
	}

	// The panel's own index ships with the build and can only be switched off.
	builtin := env.do(http.MethodDelete, "/api/schematics/market/sources/hypercraft", nil)
	builtin.Body.Close()
	if builtin.StatusCode != http.StatusConflict {
		t.Errorf("delete(builtin) = %d, want 409", builtin.StatusCode)
	}
}
