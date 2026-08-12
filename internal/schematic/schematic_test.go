package schematic

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

/* ------------------------------------------------------- NBT test writer

   The parser has no writer to test against, so the fixtures are assembled by
   hand here. Keeping the builder in the test file is deliberate: a writer in
   the package would be dead code in production and the one thing that could
   make a round-trip test pass while both halves are wrong. */

func tag(kind tagType, name string, payload []byte) []byte {
	var out bytes.Buffer
	out.WriteByte(byte(kind))
	out.Write(pString(name))
	out.Write(payload)
	return out.Bytes()
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

func pLong(v int64) []byte {
	out := make([]byte, 8)
	binary.BigEndian.PutUint64(out, uint64(v))
	return out
}

func pByteArray(data []byte) []byte {
	return append(pInt(len(data)), data...)
}

func pIntArray(values ...int) []byte {
	out := pInt(len(values))
	for _, v := range values {
		out = append(out, pInt(v)...)
	}
	return out
}

func pCompound(children ...[]byte) []byte {
	var out bytes.Buffer
	for _, child := range children {
		out.Write(child)
	}
	out.WriteByte(byte(tagEnd))
	return out.Bytes()
}

func pList(elem tagType, items ...[]byte) []byte {
	var out bytes.Buffer
	out.WriteByte(byte(elem))
	out.Write(pInt(len(items)))
	for _, item := range items {
		out.Write(item)
	}
	return out.Bytes()
}

func gzipped(t *testing.T, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	if _, err := zw.Write(data); err != nil {
		t.Fatalf("gzip: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return out.Bytes()
}

func varints(indices ...int) []byte {
	var out []byte
	for _, index := range indices {
		for index >= 0x80 {
			out = append(out, byte(index)|0x80)
			index >>= 7
		}
		out = append(out, byte(index))
	}
	return out
}

/* --------------------------------------------------------------- fixtures */

// spongeV2 is a 2×2×2 region: a bottom layer of stone with one air corner and
// a top layer of oak planks.
func spongeV2(t *testing.T) []byte {
	t.Helper()
	blocks := varints(
		1, 1, 1, 0, // y=0
		2, 2, 2, 2, // y=1
	)
	root := tag(tagCompound, "Schematic", pCompound(
		tag(tagInt, "Version", pInt(2)),
		tag(tagInt, "DataVersion", pInt(2586)),
		tag(tagShort, "Width", pShort(2)),
		tag(tagShort, "Height", pShort(2)),
		tag(tagShort, "Length", pShort(2)),
		tag(tagIntArray, "Offset", pIntArray(-1, 0, 3)),
		tag(tagInt, "PaletteMax", pInt(3)),
		tag(tagCompound, "Palette", pCompound(
			tag(tagInt, "minecraft:air", pInt(0)),
			tag(tagInt, "minecraft:stone", pInt(1)),
			tag(tagInt, "minecraft:oak_planks", pInt(2)),
		)),
		tag(tagByteArray, "BlockData", pByteArray(blocks)),
		tag(tagCompound, "Metadata", pCompound(
			tag(tagString, "Author", pString("notch")),
			tag(tagString, "Name", pString("小屋")),
			tag(tagLong, "Date", pLong(1700000000000)),
			tag(tagInt, "WEOffsetX", pInt(4)),
			tag(tagInt, "WEOffsetY", pInt(5)),
			tag(tagInt, "WEOffsetZ", pInt(6)),
		)),
		tag(tagList, "BlockEntities", pList(tagCompound,
			pCompound(tag(tagString, "Id", pString("minecraft:chest"))),
		)),
	))
	return gzipped(t, root)
}

/* ------------------------------------------------------------------ tests */

func TestParseSpongeV2(t *testing.T) {
	preview, err := Parse(bytes.NewReader(spongeV2(t)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if preview.Format != "sponge" || preview.Version != 2 {
		t.Errorf("format %q version %d, want sponge 2", preview.Format, preview.Version)
	}
	if preview.Width != 2 || preview.Height != 2 || preview.Length != 2 || preview.Volume != 8 {
		t.Errorf("region %d×%d×%d volume %d, want 2×2×2 volume 8",
			preview.Width, preview.Height, preview.Length, preview.Volume)
	}
	if preview.DataVersion != 2586 {
		t.Errorf("DataVersion = %d, want 2586", preview.DataVersion)
	}
	if got := preview.Counts; len(got) != 3 || got[0] != 1 || got[1] != 3 || got[2] != 4 {
		t.Errorf("counts = %v, want [1 3 4]", got)
	}
	if preview.NonAir != 7 {
		t.Errorf("NonAir = %d, want 7", preview.NonAir)
	}
	if preview.Author != "notch" || preview.Name != "小屋" {
		t.Errorf("metadata = %q by %q, want 小屋 by notch", preview.Name, preview.Author)
	}
	if !strings.HasPrefix(preview.Created, "2023-11-14") {
		t.Errorf("Created = %q, want a 2023-11-14 timestamp", preview.Created)
	}
	if len(preview.Offset) != 3 || preview.Offset[0] != -1 || preview.Offset[2] != 3 {
		t.Errorf("Offset = %v, want [-1 0 3]", preview.Offset)
	}
	if len(preview.WEOffset) != 3 || preview.WEOffset[1] != 5 {
		t.Errorf("WEOffset = %v, want [4 5 6]", preview.WEOffset)
	}
	if preview.BlockEntities != 1 {
		t.Errorf("BlockEntities = %d, want 1", preview.BlockEntities)
	}

	// The runs are the render, so they are checked against the same order the
	// block data was written in rather than only for length.
	want := []run{{1, 3}, {0, 1}, {2, 4}}
	if got := decodeRuns(t, preview.Blocks); !equalRuns(got, want) {
		t.Errorf("runs = %v, want %v", got, want)
	}
	if preview.Runs != 3 {
		t.Errorf("Runs = %d, want 3", preview.Runs)
	}
	if preview.Omitted != "" {
		t.Errorf("Omitted = %q, want empty", preview.Omitted)
	}
}

func TestParseSpongeV3(t *testing.T) {
	// v3 keeps the same fields but moves the palette and the data into a
	// Blocks compound, under a Schematic compound in an unnamed root.
	blocks := varints(0, 1, 1, 1)
	root := tag(tagCompound, "", pCompound(
		tag(tagCompound, "Schematic", pCompound(
			tag(tagInt, "Version", pInt(3)),
			tag(tagInt, "DataVersion", pInt(3465)),
			tag(tagShort, "Width", pShort(2)),
			tag(tagShort, "Height", pShort(2)),
			tag(tagShort, "Length", pShort(1)),
			tag(tagCompound, "Blocks", pCompound(
				tag(tagCompound, "Palette", pCompound(
					tag(tagInt, "minecraft:air", pInt(0)),
					tag(tagInt, "minecraft:glass", pInt(1)),
				)),
				tag(tagByteArray, "Data", pByteArray(blocks)),
				tag(tagList, "BlockEntities", pList(tagCompound)),
			)),
			tag(tagCompound, "Biomes", pCompound(
				tag(tagCompound, "Palette", pCompound(
					tag(tagInt, "minecraft:plains", pInt(0)),
				)),
			)),
		)),
	))

	preview, err := Parse(bytes.NewReader(gzipped(t, root)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if preview.Version != 3 || preview.DataVersion != 3465 {
		t.Errorf("version %d data %d, want 3 / 3465", preview.Version, preview.DataVersion)
	}
	if preview.Volume != 4 || preview.NonAir != 3 {
		t.Errorf("volume %d nonAir %d, want 4 / 3", preview.Volume, preview.NonAir)
	}
	if !preview.HasBiomes {
		t.Error("HasBiomes = false, want true")
	}
	if got := decodeRuns(t, preview.Blocks); !equalRuns(got, []run{{0, 1}, {1, 3}}) {
		t.Errorf("runs = %v, want [{0 1} {1 3}]", got)
	}
}

func TestParseMCEdit(t *testing.T) {
	// Four blocks: air, red wool (35:14), spruce planks (5:1) and a modded id
	// 4096 that only AddBlocks can express.
	ids := []byte{0, 35, 5, 0}
	meta := []byte{0, 14, 1, 0}
	// AddBlocks packs two blocks per byte, high nibble first. Block 3 is the
	// odd index of the second byte, so its extra bits go in the low nibble.
	add := []byte{0x00, 0x01}

	root := tag(tagCompound, "Schematic", pCompound(
		tag(tagString, "Materials", pString("Alpha")),
		tag(tagShort, "Width", pShort(4)),
		tag(tagShort, "Height", pShort(1)),
		tag(tagShort, "Length", pShort(1)),
		tag(tagByteArray, "Blocks", pByteArray(ids)),
		tag(tagByteArray, "Data", pByteArray(meta)),
		tag(tagByteArray, "AddBlocks", pByteArray(add)),
		tag(tagInt, "WEOffsetX", pInt(2)),
		tag(tagList, "Entities", pList(tagCompound,
			pCompound(tag(tagString, "id", pString("minecraft:armor_stand"))),
			pCompound(tag(tagString, "id", pString("minecraft:item_frame"))),
		)),
	))

	preview, err := Parse(bytes.NewReader(gzipped(t, root)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if preview.Format != "mcedit" {
		t.Errorf("format = %q, want mcedit", preview.Format)
	}
	want := []string{"minecraft:air", "minecraft:red_wool", "minecraft:spruce_planks", "minecraft:legacy_256"}
	if len(preview.Palette) != len(want) {
		t.Fatalf("palette = %v, want %v", preview.Palette, want)
	}
	for i, name := range want {
		if preview.Palette[i] != name {
			t.Errorf("palette[%d] = %q, want %q", i, preview.Palette[i], name)
		}
	}
	if preview.NonAir != 3 {
		t.Errorf("NonAir = %d, want 3", preview.NonAir)
	}
	if preview.Entities != 2 {
		t.Errorf("Entities = %d, want 2", preview.Entities)
	}
	if len(preview.WEOffset) != 3 || preview.WEOffset[0] != 2 {
		t.Errorf("WEOffset = %v, want [2 0 0]", preview.WEOffset)
	}
}

// A palette with a gap must not shift every block after it onto the wrong
// entry — the indices in the file are authoritative, not the map order.
func TestSparsePalette(t *testing.T) {
	root := tag(tagCompound, "Schematic", pCompound(
		tag(tagInt, "Version", pInt(2)),
		tag(tagShort, "Width", pShort(2)),
		tag(tagShort, "Height", pShort(1)),
		tag(tagShort, "Length", pShort(1)),
		tag(tagCompound, "Palette", pCompound(
			tag(tagInt, "minecraft:air", pInt(0)),
			tag(tagInt, "minecraft:dirt", pInt(2)),
		)),
		tag(tagByteArray, "BlockData", pByteArray(varints(2, 2))),
	))

	preview, err := Parse(bytes.NewReader(gzipped(t, root)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(preview.Palette) != 3 || preview.Palette[2] != "minecraft:dirt" {
		t.Fatalf("palette = %v, want a hole at 1 and dirt at 2", preview.Palette)
	}
	if preview.Palette[1] != "minecraft:unknown" {
		t.Errorf("palette[1] = %q, want a placeholder that does not read as air", preview.Palette[1])
	}
	if preview.NonAir != 2 {
		t.Errorf("NonAir = %d, want 2", preview.NonAir)
	}
}

// Dimensions are TAG_Short but are counts, so anything past 32767 arrives
// negative and has to be folded back rather than rejected as impossible.
func TestUnsignedDimensions(t *testing.T) {
	if got := unsigned16(-32236); got != 33300 {
		t.Errorf("unsigned16(-32236) = %d, want 33300", got)
	}
	if got := unsigned16(120); got != 120 {
		t.Errorf("unsigned16(120) = %d, want 120", got)
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want error
	}{
		{
			name: "not NBT at all",
			data: []byte("PK\x03\x04 this is a zip"),
			want: ErrUnsupported,
		},
		{
			name: "NBT without block data",
			data: gzipped(t, tag(tagCompound, "Schematic", pCompound(
				tag(tagInt, "Version", pInt(2)),
			))),
			want: ErrUnsupported,
		},
		{
			name: "block data ends early",
			data: gzipped(t, tag(tagCompound, "Schematic", pCompound(
				tag(tagShort, "Width", pShort(4)),
				tag(tagShort, "Height", pShort(4)),
				tag(tagShort, "Length", pShort(4)),
				tag(tagCompound, "Palette", pCompound(
					tag(tagInt, "minecraft:stone", pInt(0)),
				)),
				tag(tagByteArray, "BlockData", pByteArray(varints(0, 0))),
			))),
			want: ErrUnsupported,
		},
		{
			name: "palette index past the end",
			data: gzipped(t, tag(tagCompound, "Schematic", pCompound(
				tag(tagShort, "Width", pShort(1)),
				tag(tagShort, "Height", pShort(1)),
				tag(tagShort, "Length", pShort(1)),
				tag(tagCompound, "Palette", pCompound(
					tag(tagInt, "minecraft:stone", pInt(0)),
				)),
				tag(tagByteArray, "BlockData", pByteArray(varints(9))),
			))),
			want: ErrUnsupported,
		},
		{
			name: "region larger than the parser walks",
			data: gzipped(t, tag(tagCompound, "Schematic", pCompound(
				tag(tagShort, "Width", pShort(-1)),  // 65535
				tag(tagShort, "Height", pShort(-1)), // 65535
				tag(tagShort, "Length", pShort(-1)), // 65535
				tag(tagCompound, "Palette", pCompound(
					tag(tagInt, "minecraft:stone", pInt(0)),
				)),
				tag(tagByteArray, "BlockData", pByteArray(varints(0))),
			))),
			want: ErrTooLarge,
		},
		{
			name: "truncated NBT",
			data: gzipped(t, tag(tagCompound, "Schematic", pCompound(
				tag(tagShort, "Width", pShort(2)),
			))[:12]),
			want: ErrMalformed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(bytes.NewReader(tc.data))
			if !errors.Is(err, tc.want) {
				t.Fatalf("Parse error = %v, want %v", err, tc.want)
			}
		})
	}
}

// Uncompressed NBT is still a schematic: files get re-saved by other tools and
// refusing one because it is not gzipped would be a bad reason to fail.
func TestParseUncompressed(t *testing.T) {
	root := tag(tagCompound, "Schematic", pCompound(
		tag(tagShort, "Width", pShort(1)),
		tag(tagShort, "Height", pShort(1)),
		tag(tagShort, "Length", pShort(1)),
		tag(tagCompound, "Palette", pCompound(
			tag(tagInt, "minecraft:bedrock", pInt(0)),
		)),
		tag(tagByteArray, "BlockData", pByteArray(varints(0))),
	))
	preview, err := Parse(bytes.NewReader(root))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if preview.NonAir != 1 {
		t.Errorf("NonAir = %d, want 1", preview.NonAir)
	}
}

func TestIsAir(t *testing.T) {
	for _, name := range []string{"minecraft:air", "air", "minecraft:cave_air", "minecraft:structure_void"} {
		if !IsAir(name) {
			t.Errorf("IsAir(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"minecraft:stone", "minecraft:oak_stairs[facing=east]", "minecraft:airship"} {
		if IsAir(name) {
			t.Errorf("IsAir(%q) = true, want false", name)
		}
	}
}

func TestLegacyName(t *testing.T) {
	cases := []struct {
		id, data int
		want     string
	}{
		{35, 0, "minecraft:white_wool"},
		{35, 15, "minecraft:black_wool"},
		// The high bits carry the axis on a log and "no decay" on leaves; only
		// the low two select the wood.
		{17, 5, "minecraft:spruce_log"},
		{18, 9, "minecraft:spruce_leaves"},
		{5, 5, "minecraft:dark_oak_planks"},
		{1, 0, "minecraft:stone"},
		{0, 0, "minecraft:air"},
		{4095, 0, "minecraft:legacy_4095"},
		{4095, 3, "minecraft:legacy_4095_3"},
	}
	for _, tc := range cases {
		if got := legacyName(tc.id, tc.data); got != tc.want {
			t.Errorf("legacyName(%d, %d) = %q, want %q", tc.id, tc.data, got, tc.want)
		}
	}
}

func TestTopBlocks(t *testing.T) {
	preview, err := Parse(bytes.NewReader(spongeV2(t)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	top := preview.TopBlocks(5)
	if len(top) != 2 || top[0] != "minecraft:oak_planks×4" || top[1] != "minecraft:stone×3" {
		t.Errorf("TopBlocks = %v, want planks then stone, air excluded", top)
	}
}

/* ----------------------------------------------------------- run helpers */

type run struct {
	index  int
	length int
}

func decodeRuns(t *testing.T, encoded string) []run {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("base64: %v", err)
	}
	if len(raw)%6 != 0 {
		t.Fatalf("payload is %d bytes, not a whole number of 6-byte runs", len(raw))
	}
	out := make([]run, 0, len(raw)/6)
	for i := 0; i < len(raw); i += 6 {
		out = append(out, run{
			index:  int(binary.BigEndian.Uint16(raw[i:])),
			length: int(binary.BigEndian.Uint32(raw[i+2:])),
		})
	}
	return out
}

func equalRuns(got, want []run) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
