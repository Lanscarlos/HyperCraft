package api

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// A 2×1×1 Sponge v2 schematic: one stone block and one air block. Written out
// by hand rather than checked in as a binary fixture — the whole file is forty
// bytes, and a byte array in the repo would be a thing nobody can review.
func tinySchematic(t *testing.T) []byte {
	t.Helper()

	str := func(s string) []byte {
		out := make([]byte, 2, 2+len(s))
		binary.BigEndian.PutUint16(out, uint16(len(s)))
		return append(out, s...)
	}
	i32 := func(v int) []byte {
		out := make([]byte, 4)
		binary.BigEndian.PutUint32(out, uint32(v))
		return out
	}
	i16 := func(v int) []byte {
		out := make([]byte, 2)
		binary.BigEndian.PutUint16(out, uint16(v))
		return out
	}
	tag := func(kind byte, name string, payload []byte) []byte {
		return append(append([]byte{kind}, str(name)...), payload...)
	}

	const (
		tagShort     = 2
		tagInt       = 3
		tagString    = 8
		tagCompound  = 10
		tagByteArray = 7
		tagEnd       = 0
	)

	var body bytes.Buffer
	body.Write(tag(tagInt, "Version", i32(2)))
	body.Write(tag(tagShort, "Width", i16(2)))
	body.Write(tag(tagShort, "Height", i16(1)))
	body.Write(tag(tagShort, "Length", i16(1)))

	var palette bytes.Buffer
	palette.Write(tag(tagInt, "minecraft:air", i32(0)))
	palette.Write(tag(tagInt, "minecraft:stone", i32(1)))
	palette.WriteByte(tagEnd)
	body.Write(tag(tagCompound, "Palette", palette.Bytes()))

	// One varint per block, in Y→Z→X order.
	body.Write(tag(tagByteArray, "BlockData", append(i32(2), 0x00, 0x01)))

	var meta bytes.Buffer
	meta.Write(tag(tagString, "Author", str("steve")))
	meta.WriteByte(tagEnd)
	body.Write(tag(tagCompound, "Metadata", meta.Bytes()))
	body.WriteByte(tagEnd)

	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	if _, err := zw.Write(tag(tagCompound, "Schematic", body.Bytes())); err != nil {
		t.Fatalf("gzip: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return out.Bytes()
}

func TestSchematicPreview(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("schem")

	dir := filepath.Join(created.Directory, "plugins", "WorldEdit", "schematics")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "castle.schem"), tinySchematic(t), 0o644); err != nil {
		t.Fatalf("write schematic: %v", err)
	}

	resp := env.do(http.MethodGet, "/api/instances/"+created.ID+
		"/files/schematic?path=plugins/WorldEdit/schematics/castle.schem", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var got struct {
		File    string   `json:"file"`
		Format  string   `json:"format"`
		Width   int      `json:"width"`
		Volume  int      `json:"volume"`
		NonAir  int      `json:"nonAir"`
		Author  string   `json:"author"`
		Palette []string `json:"palette"`
		Counts  []int    `json:"counts"`
		Blocks  string   `json:"blocks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.File != "castle.schem" || got.Format != "sponge" {
		t.Errorf("file %q format %q, want castle.schem / sponge", got.File, got.Format)
	}
	if got.Width != 2 || got.Volume != 2 || got.NonAir != 1 {
		t.Errorf("width %d volume %d nonAir %d, want 2 / 2 / 1", got.Width, got.Volume, got.NonAir)
	}
	if got.Author != "steve" {
		t.Errorf("author = %q, want steve", got.Author)
	}
	if len(got.Palette) != 2 || got.Palette[1] != "minecraft:stone" {
		t.Errorf("palette = %v", got.Palette)
	}
	if len(got.Counts) != 2 || got.Counts[0] != 1 || got.Counts[1] != 1 {
		t.Errorf("counts = %v, want [1 1]", got.Counts)
	}
	if got.Blocks == "" {
		t.Error("blocks payload is empty, so the browser has nothing to draw")
	}
}

// The endpoint only reads schematics. Pointing it at a jar has to fail on the
// extension, before anything tries to gunzip a hundred megabytes of zip.
func TestSchematicPreviewRejectsOtherFiles(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("schem-reject")

	if err := os.WriteFile(filepath.Join(created.Directory, "server.jar"), []byte("PK\x03\x04"), 0o644); err != nil {
		t.Fatalf("write jar: %v", err)
	}

	resp := env.do(http.MethodGet, "/api/instances/"+created.ID+
		"/files/schematic?path=server.jar", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", resp.StatusCode)
	}
}

// A file with the right extension and the wrong contents is the common case
// after a failed download, and it must not read as a server fault.
func TestSchematicPreviewRejectsGarbage(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("schem-garbage")

	if err := os.WriteFile(filepath.Join(created.Directory, "broken.schem"),
		[]byte("<html>404 not found</html>"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	resp := env.do(http.MethodGet, "/api/instances/"+created.ID+
		"/files/schematic?path=broken.schem", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", resp.StatusCode)
	}
}
