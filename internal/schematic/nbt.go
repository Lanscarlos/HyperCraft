package schematic

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// NBT is Minecraft's binary tag format: big-endian, length-prefixed, and
// self-describing. Only what a schematic needs is implemented here — there is
// no writer, and no attempt at Java's "modified UTF-8" surrogate handling,
// because every string a schematic carries that the panel reads (block state
// ids, an author name) is plain UTF-8 in practice.
type tagType byte

const (
	tagEnd tagType = iota
	tagByte
	tagShort
	tagInt
	tagLong
	tagFloat
	tagDouble
	tagByteArray
	tagString
	tagList
	tagCompound
	tagIntArray
	tagLongArray
)

// ErrMalformed marks a file that is not readable as NBT.
var ErrMalformed = errors.New("malformed NBT data")

// maxTags bounds how many tags one file may expand into.
//
// A schematic's block data lives in a single byte array, so a legitimate file
// needs a few thousand tags plus one per entity. A hostile file, on the other
// hand, is a few hundred kilobytes of gzip that unpacks into millions of empty
// compounds — each of which would cost a Go struct. The cap is what keeps a
// preview request from being a way to exhaust the daemon's memory.
const maxTags = 2_000_000

// value is one decoded tag. Which field carries the payload depends on kind;
// the accessors below are the only intended way to read it.
type value struct {
	kind tagType
	num  int64  // byte, short, int, long — and float/double as bits
	str  string // string
	// data holds []byte, []int32, []int64, []value (a list) or compound.
	data any
}

type compound map[string]value

type nbtReader struct {
	buf  []byte
	pos  int
	tags int
}

// decodeNBT reads one named root tag, which is what every schematic file is.
func decodeNBT(buf []byte) (compound, error) {
	r := &nbtReader{buf: buf}
	kind, err := r.readType()
	if err != nil {
		return nil, err
	}
	if kind != tagCompound {
		return nil, fmt.Errorf("%w: root tag is %d, not a compound", ErrMalformed, kind)
	}
	if _, err := r.readString(); err != nil { // the root's own name, unused
		return nil, err
	}
	return r.readCompound()
}

func (r *nbtReader) need(n int) error {
	if n < 0 || r.pos+n > len(r.buf) {
		return fmt.Errorf("%w: ran off the end of the data", ErrMalformed)
	}
	return nil
}

func (r *nbtReader) readType() (tagType, error) {
	if err := r.need(1); err != nil {
		return 0, err
	}
	kind := tagType(r.buf[r.pos])
	r.pos++
	if kind > tagLongArray {
		return 0, fmt.Errorf("%w: unknown tag type %d", ErrMalformed, kind)
	}
	return kind, nil
}

func (r *nbtReader) readU16() (int, error) {
	if err := r.need(2); err != nil {
		return 0, err
	}
	n := int(binary.BigEndian.Uint16(r.buf[r.pos:]))
	r.pos += 2
	return n, nil
}

func (r *nbtReader) readI32() (int32, error) {
	if err := r.need(4); err != nil {
		return 0, err
	}
	n := int32(binary.BigEndian.Uint32(r.buf[r.pos:]))
	r.pos += 4
	return n, nil
}

func (r *nbtReader) readI64() (int64, error) {
	if err := r.need(8); err != nil {
		return 0, err
	}
	n := int64(binary.BigEndian.Uint64(r.buf[r.pos:]))
	r.pos += 8
	return n, nil
}

func (r *nbtReader) readString() (string, error) {
	length, err := r.readU16()
	if err != nil {
		return "", err
	}
	if err := r.need(length); err != nil {
		return "", err
	}
	s := string(r.buf[r.pos : r.pos+length])
	r.pos += length
	return s, nil
}

// readLength reads an array or list length and rejects the negative values a
// crafted file uses to make a reader allocate wildly.
func (r *nbtReader) readLength() (int, error) {
	n, err := r.readI32()
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, fmt.Errorf("%w: negative length %d", ErrMalformed, n)
	}
	return int(n), nil
}

func (r *nbtReader) readCompound() (compound, error) {
	out := compound{}
	for {
		kind, err := r.readType()
		if err != nil {
			return nil, err
		}
		if kind == tagEnd {
			return out, nil
		}
		name, err := r.readString()
		if err != nil {
			return nil, err
		}
		v, err := r.readPayload(kind)
		if err != nil {
			return nil, err
		}
		out[name] = v
	}
}

func (r *nbtReader) readPayload(kind tagType) (value, error) {
	r.tags++
	if r.tags > maxTags {
		return value{}, fmt.Errorf("%w: file expands into more than %d tags", ErrTooComplex, maxTags)
	}

	switch kind {
	case tagByte:
		if err := r.need(1); err != nil {
			return value{}, err
		}
		n := int64(int8(r.buf[r.pos]))
		r.pos++
		return value{kind: kind, num: n}, nil
	case tagShort:
		n, err := r.readU16()
		if err != nil {
			return value{}, err
		}
		return value{kind: kind, num: int64(int16(n))}, nil
	case tagInt:
		n, err := r.readI32()
		if err != nil {
			return value{}, err
		}
		return value{kind: kind, num: int64(n)}, nil
	case tagLong:
		n, err := r.readI64()
		if err != nil {
			return value{}, err
		}
		return value{kind: kind, num: n}, nil
	case tagFloat:
		n, err := r.readI32()
		if err != nil {
			return value{}, err
		}
		// Kept as raw bits. Nothing the preview reads is a float, so decoding
		// one would only be a conversion no caller asks for.
		return value{kind: kind, num: int64(uint32(n))}, nil
	case tagDouble:
		n, err := r.readI64()
		if err != nil {
			return value{}, err
		}
		return value{kind: kind, num: n}, nil
	case tagString:
		s, err := r.readString()
		if err != nil {
			return value{}, err
		}
		return value{kind: kind, str: s}, nil
	case tagByteArray:
		length, err := r.readLength()
		if err != nil {
			return value{}, err
		}
		if err := r.need(length); err != nil {
			return value{}, err
		}
		// A slice of the decompressed buffer rather than a copy: block data is
		// the largest thing in the file and it is only ever read.
		data := r.buf[r.pos : r.pos+length : r.pos+length]
		r.pos += length
		return value{kind: kind, data: data}, nil
	case tagIntArray:
		length, err := r.readLength()
		if err != nil {
			return value{}, err
		}
		if err := r.need(length * 4); err != nil {
			return value{}, err
		}
		out := make([]int32, length)
		for i := range out {
			out[i] = int32(binary.BigEndian.Uint32(r.buf[r.pos+i*4:]))
		}
		r.pos += length * 4
		return value{kind: kind, data: out}, nil
	case tagLongArray:
		length, err := r.readLength()
		if err != nil {
			return value{}, err
		}
		if err := r.need(length * 8); err != nil {
			return value{}, err
		}
		out := make([]int64, length)
		for i := range out {
			out[i] = int64(binary.BigEndian.Uint64(r.buf[r.pos+i*8:]))
		}
		r.pos += length * 8
		return value{kind: kind, data: out}, nil
	case tagList:
		elem, err := r.readType()
		if err != nil {
			return value{}, err
		}
		length, err := r.readLength()
		if err != nil {
			return value{}, err
		}
		// An empty list is written with element type TAG_End; anything else
		// claiming TAG_End elements has no payload to read and would loop.
		if elem == tagEnd {
			if length != 0 {
				return value{}, fmt.Errorf("%w: list of TAG_End with %d entries", ErrMalformed, length)
			}
			return value{kind: kind, data: []value{}}, nil
		}
		// Every element carries at least one byte, so a length larger than the
		// bytes left is a lie — checking it here stops a 2-billion-entry header
		// from allocating before the first read fails.
		if err := r.need(length); err != nil {
			return value{}, err
		}
		out := make([]value, 0, min(length, 1024))
		for i := 0; i < length; i++ {
			v, err := r.readPayload(elem)
			if err != nil {
				return value{}, err
			}
			out = append(out, v)
		}
		return value{kind: kind, data: out}, nil
	case tagCompound:
		c, err := r.readCompound()
		if err != nil {
			return value{}, err
		}
		return value{kind: kind, data: c}, nil
	default:
		return value{}, fmt.Errorf("%w: cannot read tag type %d", ErrMalformed, kind)
	}
}

/* ------------------------------------------------------------- accessors */

// int returns an integral tag's value, or fallback for anything else. A
// schematic's Width is a short in one version and an int in another, so the
// callers here care about the number and not about which width it was written
// at.
func (c compound) int(name string, fallback int) int {
	v, ok := c[name]
	if !ok {
		return fallback
	}
	switch v.kind {
	case tagByte, tagShort, tagInt, tagLong:
		return int(v.num)
	default:
		return fallback
	}
}

func (c compound) long(name string) (int64, bool) {
	v, ok := c[name]
	if !ok {
		return 0, false
	}
	switch v.kind {
	case tagByte, tagShort, tagInt, tagLong:
		return v.num, true
	default:
		return 0, false
	}
}

func (c compound) str(name string) string {
	if v, ok := c[name]; ok && v.kind == tagString {
		return v.str
	}
	return ""
}

func (c compound) compound(name string) (compound, bool) {
	if v, ok := c[name]; ok && v.kind == tagCompound {
		if sub, ok := v.data.(compound); ok {
			return sub, true
		}
	}
	return nil, false
}

func (c compound) bytes(name string) ([]byte, bool) {
	if v, ok := c[name]; ok && v.kind == tagByteArray {
		if data, ok := v.data.([]byte); ok {
			return data, true
		}
	}
	return nil, false
}

func (c compound) ints(name string) ([]int32, bool) {
	if v, ok := c[name]; ok && v.kind == tagIntArray {
		if data, ok := v.data.([]int32); ok {
			return data, true
		}
	}
	return nil, false
}

// listLen counts a list's entries. Entities and block entities are only ever
// reported as totals, so their contents are never walked past the decode.
func (c compound) listLen(name string) int {
	if v, ok := c[name]; ok && v.kind == tagList {
		if data, ok := v.data.([]value); ok {
			return len(data)
		}
	}
	return 0
}
