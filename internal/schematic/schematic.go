// Package schematic reads WorldEdit schematics well enough for the panel to
// preview one without downloading it.
//
// Three on-disk shapes reach a server's plugins/WorldEdit/schematics folder:
// Sponge v1/v2 (.schem, the WorldEdit 7 default), Sponge v3 (.schem, 1.20+),
// and the pre-1.13 MCEdit format (.schematic), which stores numeric block ids
// instead of block states. All three are gzipped NBT, and all three are
// normalised here into one shape — a palette of modern block-state strings
// plus per-entry counts — so the UI never has to know which it is looking at.
//
// Nothing here writes a schematic, and nothing places blocks in a world. The
// panel's claim is "here is what is inside this file", not "here is a world
// editor".
package schematic

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

var (
	// ErrUnsupported marks a file that is readable but is not a schematic.
	ErrUnsupported = errors.New("not a schematic file")
	// ErrTooLarge marks a file past one of the size caps below.
	ErrTooLarge = errors.New("schematic is too large to preview")
	// ErrTooComplex marks a file whose tag count is past the parser's budget.
	ErrTooComplex = errors.New("schematic is too complex to preview")
)

const (
	// MaxFileBytes is the largest compressed file the preview will open. Real
	// schematics are kilobytes to a few megabytes; anything past this is either
	// a world in disguise or an attempt to make the daemon do work.
	MaxFileBytes = 64 << 20
	// maxDecompressed bounds the gzip expansion. A schematic compresses well —
	// most of it is air — so the ratio between this and MaxFileBytes is where a
	// decompression bomb would live if it were not checked.
	maxDecompressed = 96 << 20
	// maxVolume is the largest region the parser will walk at all.
	maxVolume = 64_000_000
	// maxVoxelVolume is the largest region whose blocks are sent to the browser
	// for rendering. Past it the response still carries the full statistics —
	// dimensions, palette, counts — and the UI says why there is no picture.
	maxVoxelVolume = 8_000_000
	// maxRuns caps the run-length encoded payload at roughly 1.8 MB. A build
	// dense enough to exceed it would not be legible at preview scale anyway.
	maxRuns = 300_000
)

// Preview is everything the panel shows about one schematic.
type Preview struct {
	Format      string `json:"format"` // "sponge" | "mcedit"
	Version     int    `json:"version"`
	DataVersion int    `json:"dataVersion"`

	Width  int `json:"width"`
	Height int `json:"height"`
	Length int `json:"length"`
	Volume int `json:"volume"`

	// Offset is where the region sits relative to the copy's origin, and
	// WEOffset where the player stood when it was copied. Both are what makes a
	// paste land in the same place twice, so both are worth showing.
	Offset   []int `json:"offset,omitempty"`
	WEOffset []int `json:"weOffset,omitempty"`

	Name    string `json:"name,omitempty"`
	Author  string `json:"author,omitempty"`
	Created string `json:"created,omitempty"` // RFC3339, empty when absent

	// Palette holds block-state strings ("minecraft:oak_stairs[facing=east]")
	// and Counts how many blocks each accounts for. The two are index-aligned
	// and are also the indices the block payload refers to.
	Palette []string `json:"palette"`
	Counts  []int    `json:"counts"`

	NonAir        int  `json:"nonAir"`
	BlockEntities int  `json:"blockEntities"`
	Entities      int  `json:"entities"`
	HasBiomes     bool `json:"hasBiomes"`

	// Blocks is the whole region as run-length encoded palette indices, in the
	// Y→Z→X order the formats themselves use, base64'd. Each run is six bytes:
	// a big-endian uint16 palette index followed by a big-endian uint32 length.
	//
	// RLE rather than a flat array because a schematic is mostly air: a 128³
	// region is two megabytes flat and a few kilobytes like this.
	Blocks string `json:"blocks,omitempty"`
	Runs   int    `json:"runs"`
	// Omitted names the cap that cost this file its picture — "volume",
	// "runs" or "palette" — and is empty when Blocks is present. The UI turns
	// it into a sentence; the daemon does not guess at the wording.
	Omitted string `json:"omitted,omitempty"`
}

// Parse reads one schematic. The reader is consumed whole, under the size caps
// above.
func Parse(r io.Reader) (*Preview, error) {
	raw, err := decompress(r)
	if err != nil {
		return nil, err
	}
	root, err := decodeNBT(raw)
	if err != nil {
		return nil, err
	}

	// Sponge v3 moved everything one level down, under an explicit "Schematic"
	// compound in an unnamed root. v1 and v2 put the same fields at the root.
	body := root
	if sub, ok := root.compound("Schematic"); ok {
		body = sub
	}

	switch {
	case hasSpongeBlocks(body):
		return parseSponge(body)
	case isMCEdit(body):
		return parseMCEdit(body)
	default:
		return nil, fmt.Errorf("%w: no block data in the file", ErrUnsupported)
	}
}

func hasSpongeBlocks(body compound) bool {
	if _, ok := body.bytes("BlockData"); ok { // v1, v2
		return true
	}
	if blocks, ok := body.compound("Blocks"); ok { // v3
		_, ok := blocks.bytes("Data")
		return ok
	}
	return false
}

func isMCEdit(body compound) bool {
	_, ok := body.bytes("Blocks")
	return ok
}

// decompress unwraps gzip or zlib, or passes an uncompressed file through.
// WorldEdit writes gzip, but schematics get handed around and re-saved by
// other tools, and a plain NBT file is still a readable schematic.
func decompress(r io.Reader) ([]byte, error) {
	head := bufio.NewReader(io.LimitReader(r, MaxFileBytes+1))
	magic, err := head.Peek(2)
	if err != nil {
		return nil, fmt.Errorf("%w: file is too short to be NBT", ErrUnsupported)
	}

	var body io.Reader = head
	switch {
	case magic[0] == 0x1f && magic[1] == 0x8b:
		zr, err := gzip.NewReader(head)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
		}
		defer zr.Close()
		body = zr
	case magic[0] == 0x78:
		zr, err := zlib.NewReader(head)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
		}
		defer zr.Close()
		body = zr
	case magic[0] == byte(tagCompound):
		// Uncompressed NBT starts with the root's TAG_Compound byte.
	default:
		return nil, fmt.Errorf("%w: unrecognised file header", ErrUnsupported)
	}

	raw, err := io.ReadAll(io.LimitReader(body, maxDecompressed+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	if len(raw) > maxDecompressed {
		return nil, fmt.Errorf("%w: unpacks to more than %d bytes", ErrTooLarge, maxDecompressed)
	}
	return raw, nil
}

/* ---------------------------------------------------------------- sponge */

func parseSponge(body compound) (*Preview, error) {
	p := &Preview{
		Format:      "sponge",
		Version:     body.int("Version", 1),
		DataVersion: body.int("DataVersion", 0),
		// Written as TAG_Short, which NBT has no unsigned form of: a 300-block
		// wide copy round-trips as -32236 unless the sign is undone here.
		Width:  unsigned16(body.int("Width", 0)),
		Height: unsigned16(body.int("Height", 0)),
		Length: unsigned16(body.int("Length", 0)),
	}
	if offset, ok := body.ints("Offset"); ok && len(offset) >= 3 {
		p.Offset = []int{int(offset[0]), int(offset[1]), int(offset[2])}
	}
	readMetadata(body, p)

	// v3 nests the palette and the block data inside a Blocks compound; v1 and
	// v2 keep them beside the dimensions.
	blocks := body
	if sub, ok := body.compound("Blocks"); ok {
		blocks = sub
	}

	dataKey := "BlockData"
	if _, ok := blocks.bytes(dataKey); !ok {
		dataKey = "Data"
	}
	data, ok := blocks.bytes(dataKey)
	if !ok {
		return nil, fmt.Errorf("%w: no block data", ErrUnsupported)
	}

	palette, err := spongePalette(blocks)
	if err != nil {
		return nil, err
	}
	p.Palette = palette

	// v2 and v3 call them BlockEntities, v1 inherited MCEdit's TileEntities.
	// `blocks` is `body` on v1/v2, so the first lookup covers both there.
	p.BlockEntities = blocks.listLen("BlockEntities")
	if p.BlockEntities == 0 {
		p.BlockEntities = body.listLen("TileEntities")
	}
	p.Entities = body.listLen("Entities")
	_, hasBiomePalette := body.compound("Biomes")
	_, hasBiomeArray := body.bytes("BiomeData")
	p.HasBiomes = hasBiomePalette || hasBiomeArray

	if err := p.fill(func(emit func(int) error) error {
		return walkVarints(data, p.Volume, len(palette), emit)
	}); err != nil {
		return nil, err
	}
	return p, nil
}

// spongePalette turns the {name: index} compound into an index-ordered slice.
//
// The indices are authoritative, not the map order, and the spec does not
// promise they are dense — a palette that skips 7 is legal and must not shift
// every block after it onto the wrong entry.
func spongePalette(blocks compound) ([]string, error) {
	raw, ok := blocks.compound("Palette")
	if !ok {
		return nil, fmt.Errorf("%w: no palette", ErrUnsupported)
	}

	highest := -1
	for _, v := range raw {
		switch v.kind {
		case tagByte, tagShort, tagInt, tagLong:
			if int(v.num) > highest {
				highest = int(v.num)
			}
		}
	}
	if highest < 0 {
		return nil, fmt.Errorf("%w: empty palette", ErrUnsupported)
	}
	if highest >= 1<<16 {
		return nil, fmt.Errorf("%w: palette has more than 65536 entries", ErrTooComplex)
	}

	palette := make([]string, highest+1)
	for name, v := range raw {
		switch v.kind {
		case tagByte, tagShort, tagInt, tagLong:
			if index := int(v.num); index >= 0 && index < len(palette) {
				palette[index] = name
			}
		}
	}
	for i, name := range palette {
		if name == "" {
			// A hole in the palette: keep the slot so indices still line up,
			// but do not let it read as air and vanish from the render.
			palette[i] = "minecraft:unknown"
		}
	}
	return palette, nil
}

// walkVarints decodes the LEB128 palette indices Sponge stores block data as,
// handing each one to emit in Y→Z→X order.
func walkVarints(data []byte, volume, paletteLen int, emit func(int) error) error {
	pos := 0
	for i := 0; i < volume; i++ {
		index, read, err := readVarint(data[pos:])
		if err != nil {
			return fmt.Errorf("%w: block data ends after %d of %d blocks", ErrUnsupported, i, volume)
		}
		pos += read
		if index < 0 || index >= paletteLen {
			return fmt.Errorf("%w: block %d refers to palette entry %d of %d", ErrUnsupported, i, index, paletteLen)
		}
		if err := emit(index); err != nil {
			return err
		}
	}
	return nil
}

func readVarint(data []byte) (int, int, error) {
	value, shift := 0, 0
	for i := 0; i < len(data); i++ {
		b := data[i]
		value |= int(b&0x7f) << shift
		if b&0x80 == 0 {
			return value, i + 1, nil
		}
		shift += 7
		if shift > 28 {
			return 0, 0, errors.New("varint is too long")
		}
	}
	return 0, 0, io.ErrUnexpectedEOF
}

/* ---------------------------------------------------------------- mcedit */

func parseMCEdit(body compound) (*Preview, error) {
	p := &Preview{
		Format: "mcedit",
		Width:  unsigned16(body.int("Width", 0)),
		Height: unsigned16(body.int("Height", 0)),
		Length: unsigned16(body.int("Length", 0)),
	}
	p.Offset = []int{
		body.int("WEOriginX", 0), body.int("WEOriginY", 0), body.int("WEOriginZ", 0),
	}
	p.WEOffset = []int{
		body.int("WEOffsetX", 0), body.int("WEOffsetY", 0), body.int("WEOffsetZ", 0),
	}
	p.BlockEntities = body.listLen("TileEntities")
	p.Entities = body.listLen("Entities")
	_, p.HasBiomes = body.bytes("Biomes")

	ids, _ := body.bytes("Blocks")
	meta, _ := body.bytes("Data")
	// AddBlocks packs the 9th–12th id bits two blocks to a byte, which is how
	// pre-1.13 stored modded ids above 255. Ignoring it turns every modded
	// block into whichever vanilla block shares its low byte.
	add, _ := body.bytes("AddBlocks")

	if err := p.checkVolume(); err != nil {
		return nil, err
	}
	if len(ids) < p.Volume {
		return nil, fmt.Errorf("%w: block array holds %d of %d blocks", ErrUnsupported, len(ids), p.Volume)
	}

	// The palette is built as the scan runs: legacy files have no palette of
	// their own, and a build usually touches a few dozen of the 4096 possible
	// id/meta pairs.
	index := make(map[int]int, 64)
	if err := p.fill(func(emit func(int) error) error {
		for i := 0; i < p.Volume; i++ {
			id := int(ids[i])
			if half := i >> 1; half < len(add) {
				if i&1 == 0 {
					id |= int(add[half]&0xf0) << 4
				} else {
					id |= int(add[half]&0x0f) << 8
				}
			}
			data := 0
			if i < len(meta) {
				data = int(meta[i] & 0x0f)
			}
			key := id<<4 | data
			slot, seen := index[key]
			if !seen {
				slot = len(p.Palette)
				if slot >= 1<<16 {
					return fmt.Errorf("%w: more than 65536 distinct blocks", ErrTooComplex)
				}
				index[key] = slot
				p.Palette = append(p.Palette, legacyName(id, data))
			}
			if err := emit(slot); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return p, nil
}

/* ------------------------------------------------------------- the scan */

// checkVolume settles the region's size and rejects the impossible ones.
func (p *Preview) checkVolume() error {
	if p.Width <= 0 || p.Height <= 0 || p.Length <= 0 {
		return fmt.Errorf("%w: region is %d×%d×%d", ErrUnsupported, p.Width, p.Height, p.Length)
	}
	// int64 on purpose: three legal shorts multiply to more than an int32.
	volume := int64(p.Width) * int64(p.Height) * int64(p.Length)
	if volume > maxVolume {
		return fmt.Errorf("%w: %d blocks, the reader caps at %d", ErrTooLarge, volume, maxVolume)
	}
	p.Volume = int(volume)
	return nil
}

// fill runs one pass over the region, counting each palette entry and building
// the run-length payload as it goes.
//
// One pass, not two, because the block data is the expensive part of the file
// and the counts and the picture are the same walk. The walker is passed in
// because the two formats decode a block differently but agree on everything
// after it.
func (p *Preview) fill(walk func(emit func(int) error) error) error {
	if err := p.checkVolume(); err != nil {
		return err
	}

	// Counts grow with the palette for MCEdit, where entries are discovered
	// during the walk; for Sponge the palette is already known.
	counts := make([]int, len(p.Palette))
	wantVoxels := p.Volume <= maxVoxelVolume
	if !wantVoxels {
		p.Omitted = "volume"
	}

	var runs bytes.Buffer
	if wantVoxels {
		runs.Grow(4096)
	}
	record := make([]byte, 6)
	current, length := -1, 0
	overflowed := false

	flush := func() {
		if length == 0 || !wantVoxels || overflowed {
			return
		}
		if p.Runs >= maxRuns {
			overflowed = true
			return
		}
		binary.BigEndian.PutUint16(record[0:2], uint16(current))
		binary.BigEndian.PutUint32(record[2:6], uint32(length))
		runs.Write(record)
		p.Runs++
	}

	err := walk(func(index int) error {
		for index >= len(counts) {
			counts = append(counts, 0)
		}
		counts[index]++

		// No overflow guard on length: maxVolume is well under 2³², so a single
		// run cannot outgrow the uint32 it is written into.
		if index != current {
			flush()
			current, length = index, 0
		}
		length++
		return nil
	})
	if err != nil {
		return err
	}
	flush()

	p.Counts = counts
	for i, count := range counts {
		if i < len(p.Palette) && !IsAir(p.Palette[i]) {
			p.NonAir += count
		}
	}

	switch {
	case !wantVoxels:
		// p.Omitted already says why.
	case overflowed:
		p.Blocks, p.Runs, p.Omitted = "", 0, "runs"
	case len(p.Palette) > 1<<16:
		p.Blocks, p.Runs, p.Omitted = "", 0, "palette"
	default:
		p.Blocks = base64.StdEncoding.EncodeToString(runs.Bytes())
	}
	return nil
}

func readMetadata(body compound, p *Preview) {
	meta, ok := body.compound("Metadata")
	if !ok {
		return
	}
	p.Name = meta.str("Name")
	p.Author = meta.str("Author")
	if ms, ok := meta.long("Date"); ok && ms > 0 {
		p.Created = time.UnixMilli(ms).UTC().Format(time.RFC3339)
	}
	if _, ok := meta["WEOffsetX"]; ok {
		p.WEOffset = []int{
			meta.int("WEOffsetX", 0), meta.int("WEOffsetY", 0), meta.int("WEOffsetZ", 0),
		}
	}
}

// unsigned16 undoes NBT's signed shorts. Dimensions are written as TAG_Short
// but are counts, so 40000 comes back negative and has to be folded round.
func unsigned16(n int) int {
	if n < 0 && n >= -32768 {
		return n + 65536
	}
	return n
}

// IsAir reports whether a block state contributes nothing to the build. The
// four air-like blocks are what separates "this schematic is 3 million blocks"
// from "this schematic is a 40-block statue in a big selection".
func IsAir(state string) bool {
	name := state
	if i := strings.IndexByte(name, '['); i >= 0 {
		name = name[:i]
	}
	if i := strings.IndexByte(name, ':'); i >= 0 {
		name = name[i+1:]
	}
	switch name {
	case "air", "cave_air", "void_air", "structure_void":
		return true
	}
	return false
}

// TopBlocks returns the palette entries by descending count, air excluded. Not
// used by the HTTP layer — the browser sorts what it renders — but it is what
// the tests assert against and what makes a Preview readable in a log line.
func (p *Preview) TopBlocks(limit int) []string {
	type row struct {
		name  string
		count int
	}
	rows := make([]row, 0, len(p.Palette))
	for i, name := range p.Palette {
		if i >= len(p.Counts) || p.Counts[i] == 0 || IsAir(name) {
			continue
		}
		rows = append(rows, row{name, p.Counts[i]})
	}
	sort.Slice(rows, func(a, b int) bool {
		if rows[a].count != rows[b].count {
			return rows[a].count > rows[b].count
		}
		return rows[a].name < rows[b].name
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = fmt.Sprintf("%s×%d", r.name, r.count)
	}
	return out
}
