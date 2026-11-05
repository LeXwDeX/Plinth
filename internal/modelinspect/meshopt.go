package modelinspect

import (
	"encoding/binary"
	"math"
)

// This file decodes EXT_meshopt_compression bufferViews in pure Go. It
// implements the three bitstreams the extension specification defines:
// attributes (vertex codec version 0), triangles (index codec versions 0
// and 1) and index sequences. The control flow mirrors meshoptimizer's
// reference decoder (MIT License, Copyright (c) 2016-2024 Arseny
// Kapoulkine). Unlike the reference decoder every read is bounds-checked and
// a stream must end exactly where the format places its tail, which is the
// acceptance rule of meshoptimizer's C++ decoder, so a malformed range is
// rejected instead of read past.

type meshoptMode uint8

const (
	meshoptAttributes meshoptMode = iota + 1
	meshoptTriangles
	meshoptIndices
)

type meshoptFilter uint8

const (
	meshoptFilterNone meshoptFilter = iota
	meshoptFilterOctahedral
	meshoptFilterQuaternion
	meshoptFilterExponential
)

const (
	meshoptVertexHeader    = 0xa0
	meshoptIndexHeader     = 0xe0
	meshoptSequenceHeader  = 0xd0
	meshoptByteGroup       = 16
	meshoptVertexBlockSize = 8192
	meshoptVertexBlockMax  = 256
	meshoptVertexTailMin   = 32
	meshoptIndexTail       = 16
	meshoptSequenceTail    = 4
)

// meshoptSource is the compressed copy of an EXT_meshopt_compression view:
// count elements of stride bytes, stored at offset in the ReaderAt.
type meshoptSource struct {
	offset int64
	length int64
	stride int
	count  int
	mode   meshoptMode
	filter meshoptFilter
}

// decodedBytes is the size of the decoded view, count times stride. A source
// without a problem guarantees it fits an int.
func (s *meshoptSource) decodedBytes() int64 {
	return int64(s.count) * int64(s.stride)
}

func parseMeshoptMode(value string) (meshoptMode, bool) {
	switch value {
	case "ATTRIBUTES":
		return meshoptAttributes, true
	case "TRIANGLES":
		return meshoptTriangles, true
	case "INDICES":
		return meshoptIndices, true
	default:
		return 0, false
	}
}

func parseMeshoptFilter(value string) (meshoptFilter, bool) {
	switch value {
	case "", "NONE":
		return meshoptFilterNone, true
	case "OCTAHEDRAL":
		return meshoptFilterOctahedral, true
	case "QUATERNION":
		return meshoptFilterQuaternion, true
	case "EXPONENTIAL":
		return meshoptFilterExponential, true
	default:
		return 0, false
	}
}

// problem applies the extension's stride, count and filter rules and returns
// the first violation, or "" when the source is valid.
func (s *meshoptSource) problem() string {
	if s.count < 0 {
		return "negative element count"
	}
	switch s.mode {
	case meshoptAttributes:
		if s.stride <= 0 || s.stride > 256 || s.stride%4 != 0 {
			return "attribute stride is not a multiple of 4 up to 256"
		}
	case meshoptTriangles, meshoptIndices:
		if s.stride != 2 && s.stride != 4 {
			return "index stride is neither 2 nor 4"
		}
		if s.mode == meshoptTriangles && s.count%3 != 0 {
			return "triangle index count is not a multiple of three"
		}
		if s.filter != meshoptFilterNone {
			return "index data cannot use a filter"
		}
	default:
		return "unknown mode"
	}
	switch s.filter {
	case meshoptFilterOctahedral:
		if s.stride != 4 && s.stride != 8 {
			return "OCTAHEDRAL filter needs a stride of 4 or 8"
		}
	case meshoptFilterQuaternion:
		if s.stride != 8 {
			return "QUATERNION filter needs a stride of 8"
		}
	}
	if s.count > maxIntValue/s.stride {
		return "decoded size overflows"
	}
	return ""
}

// decodeMeshopt decodes one compressed view into count*stride bytes.
func decodeMeshopt(source *meshoptSource, compressed []byte) ([]byte, error) {
	out := make([]byte, int(source.decodedBytes()))
	var err error
	switch source.mode {
	case meshoptAttributes:
		err = decodeMeshoptVertices(out, source.count, source.stride, compressed)
		if err == nil {
			applyMeshoptFilter(out, source.count, source.stride, source.filter)
		}
	case meshoptTriangles:
		err = decodeMeshoptTriangles(out, source.count, source.stride, compressed)
	case meshoptIndices:
		err = decodeMeshoptSequence(out, source.count, source.stride, compressed)
	default:
		err = invalidf("unknown meshopt mode")
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func meshoptTruncated() error { return invalidf("meshopt stream is truncated") }

func meshoptTrailing() error { return invalidf("meshopt stream does not end at its tail") }

func unzigzag8(v byte) byte { return (v >> 1) ^ -(v & 1) }

// decodeMeshoptVertices decodes the attribute codec. Elements are split into
// blocks; every byte position of the element is delta-coded against the
// previous element in groups of 16, starting from the baseline stored in
// the tail.
func decodeMeshoptVertices(dst []byte, count, stride int, src []byte) error {
	tail := max(stride, meshoptVertexTailMin)
	if len(src) < 1+tail {
		return meshoptTruncated()
	}
	if src[0] != meshoptVertexHeader {
		if src[0]&0xf0 == meshoptVertexHeader {
			return unsupportedf("meshopt vertex codec version %d", src[0]&0x0f)
		}
		return invalidf("meshopt vertex stream has a bad header")
	}
	// Payload reads stop at the tail; the last stride bytes of the tail are
	// the baseline element.
	data := src[:len(src)-tail]
	last := make([]byte, stride)
	copy(last, src[len(src)-stride:])
	blockElements := min((meshoptVertexBlockSize/stride)&^(meshoptByteGroup-1), meshoptVertexBlockMax)
	position := 1
	var deltas [meshoptByteGroup]byte
	for base := 0; base < count; base += blockElements {
		elements := min(count-base, blockElements)
		groups := (elements + meshoptByteGroup - 1) / meshoptByteGroup
		headerBytes := (groups + 3) / 4
		for k := 0; k < stride; k++ {
			if len(data)-position < headerBytes {
				return meshoptTruncated()
			}
			header := data[position : position+headerBytes]
			position += headerBytes
			value := last[k]
			for group := 0; group < groups; group++ {
				mode := (header[group/4] >> ((group % 4) * 2)) & 3
				next, ok := decodeMeshoptGroup(data, position, mode, &deltas)
				if !ok {
					return meshoptTruncated()
				}
				position = next
				first := base + group*meshoptByteGroup
				for i := 0; i < meshoptByteGroup && first+i < base+elements; i++ {
					value += unzigzag8(deltas[i])
					dst[(first+i)*stride+k] = value
				}
			}
			last[k] = value
		}
	}
	if position != len(data) {
		return meshoptTrailing()
	}
	return nil
}

// decodeMeshoptGroup decodes 16 zigzag deltas. Mode 0 stores none, modes 1
// and 2 pack 2- or 4-bit values whose all-ones value is a sentinel for a
// following full byte, and mode 3 stores 16 bytes verbatim.
func decodeMeshoptGroup(data []byte, position int, mode byte, out *[meshoptByteGroup]byte) (int, bool) {
	switch mode {
	case 0:
		*out = [meshoptByteGroup]byte{}
		return position, true
	case 1, 2:
		bits := 2 * int(mode)
		packed := meshoptByteGroup * bits / 8
		if len(data)-position < packed {
			return position, false
		}
		extra := position + packed
		sentinel := byte(1<<bits - 1)
		for i := 0; i < meshoptByteGroup; i++ {
			bit := i * bits
			value := (data[position+bit/8] >> (8 - bits - bit%8)) & sentinel
			if value == sentinel {
				if extra >= len(data) {
					return position, false
				}
				value = data[extra]
				extra++
			}
			out[i] = value
		}
		return extra, true
	default:
		if len(data)-position < meshoptByteGroup {
			return position, false
		}
		copy(out[:], data[position:position+meshoptByteGroup])
		return position + meshoptByteGroup, true
	}
}

// meshoptVByte reads a little-endian base-128 value of at most five bytes.
func meshoptVByte(data []byte, position int) (uint32, int, bool) {
	if position >= len(data) {
		return 0, position, false
	}
	lead := data[position]
	position++
	if lead < 128 {
		return uint32(lead), position, true
	}
	result := uint32(lead & 127)
	shift := uint(7)
	for i := 0; i < 4; i++ {
		if position >= len(data) {
			return 0, position, false
		}
		group := data[position]
		position++
		result |= uint32(group&127) << shift
		shift += 7
		if group < 128 {
			break
		}
	}
	return result, position, true
}

func meshoptDeltaIndex(data []byte, position int, last uint32) (uint32, int, bool) {
	v, position, ok := meshoptVByte(data, position)
	if !ok {
		return 0, position, false
	}
	return last + ((v >> 1) ^ -(v & 1)), position, true
}

func writeMeshoptIndex(dst []byte, index int, stride int, value uint32) {
	if stride == 2 {
		binary.LittleEndian.PutUint16(dst[index*2:], uint16(value))
		return
	}
	binary.LittleEndian.PutUint32(dst[index*4:], value)
}

// decodeMeshoptTriangles decodes the triangle index codec: one code byte per
// triangle, variable-length data for new indices, and a 16-byte auxiliary
// code table at the end. Edge and vertex FIFOs reproduce the encoder's
// state exactly.
func decodeMeshoptTriangles(dst []byte, count, stride int, src []byte) error {
	triangles := count / 3
	if len(src) < 1+triangles+meshoptIndexTail {
		return meshoptTruncated()
	}
	if src[0]&0xf0 != meshoptIndexHeader {
		return invalidf("meshopt index stream has a bad header")
	}
	version := src[0] & 0x0f
	if version > 1 {
		return unsupportedf("meshopt index codec version %d", version)
	}
	fecLimit := 15
	if version >= 1 {
		fecLimit = 13
	}
	var edges [16][2]uint32
	var vertices [16]uint32
	for i := range edges {
		edges[i] = [2]uint32{^uint32(0), ^uint32(0)}
		vertices[i] = ^uint32(0)
	}
	edgeOffset, vertexOffset := 0, 0
	pushEdge := func(a, b uint32) {
		edges[edgeOffset] = [2]uint32{a, b}
		edgeOffset = (edgeOffset + 1) & 15
	}
	pushVertex := func(v uint32, advance bool) {
		vertices[vertexOffset] = v
		if advance {
			vertexOffset = (vertexOffset + 1) & 15
		}
	}
	next, last := uint32(0), uint32(0)
	codes := src[1 : 1+triangles]
	data := src[:len(src)-meshoptIndexTail]
	auxiliary := src[len(src)-meshoptIndexTail:]
	position := 1 + triangles
	for i, code := range codes {
		var a, b, c uint32
		if code < 0xf0 {
			edge := edges[(edgeOffset-1-int(code>>4))&15]
			a, b = edge[0], edge[1]
			fec := int(code & 15)
			if fec < fecLimit {
				if fec == 0 {
					c = next
					next++
				} else {
					c = vertices[(vertexOffset-1-fec)&15]
				}
				pushVertex(c, fec == 0)
			} else {
				if fec != 15 {
					// 13 and 14 encode last-1 and last+1.
					c = last + uint32(fec-(fec^3))
				} else {
					var ok bool
					c, position, ok = meshoptDeltaIndex(data, position, last)
					if !ok {
						return meshoptTruncated()
					}
				}
				last = c
				pushVertex(c, true)
			}
			pushEdge(c, b)
			pushEdge(a, c)
		} else if code < 0xfe {
			aux := auxiliary[code&15]
			feb, fec := int(aux>>4), int(aux&15)
			a = next
			next++
			if feb == 0 {
				b = next
				next++
			} else {
				b = vertices[(vertexOffset-feb)&15]
			}
			if fec == 0 {
				c = next
				next++
			} else {
				c = vertices[(vertexOffset-fec)&15]
			}
			pushVertex(a, true)
			pushVertex(b, feb == 0)
			pushVertex(c, fec == 0)
			pushEdge(b, a)
			pushEdge(c, b)
			pushEdge(a, c)
		} else {
			if position >= len(data) {
				return meshoptTruncated()
			}
			aux := data[position]
			position++
			if aux == 0 {
				next = 0
			}
			fea := 15
			if code == 0xfe {
				fea = 0
			}
			feb, fec := int(aux>>4), int(aux&15)
			if fea == 0 {
				a = next
				next++
			}
			if feb == 0 {
				b = next
				next++
			} else {
				b = vertices[(vertexOffset-feb)&15]
			}
			if fec == 0 {
				c = next
				next++
			} else {
				c = vertices[(vertexOffset-fec)&15]
			}
			var ok bool
			if fea == 15 {
				if a, position, ok = meshoptDeltaIndex(data, position, last); !ok {
					return meshoptTruncated()
				}
				last = a
			}
			if feb == 15 {
				if b, position, ok = meshoptDeltaIndex(data, position, last); !ok {
					return meshoptTruncated()
				}
				last = b
			}
			if fec == 15 {
				if c, position, ok = meshoptDeltaIndex(data, position, last); !ok {
					return meshoptTruncated()
				}
				last = c
			}
			pushVertex(a, true)
			pushVertex(b, feb == 0 || feb == 15)
			pushVertex(c, fec == 0 || fec == 15)
			pushEdge(b, a)
			pushEdge(c, b)
			pushEdge(a, c)
		}
		writeMeshoptIndex(dst, 3*i, stride, a)
		writeMeshoptIndex(dst, 3*i+1, stride, b)
		writeMeshoptIndex(dst, 3*i+2, stride, c)
	}
	if position != len(data) {
		return meshoptTrailing()
	}
	return nil
}

// decodeMeshoptSequence decodes the index sequence codec: each index is a
// zigzag delta against one of two running baselines, selected by the low
// bit of its variable-length code.
func decodeMeshoptSequence(dst []byte, count, stride int, src []byte) error {
	if len(src) < 1+count+meshoptSequenceTail {
		return meshoptTruncated()
	}
	if src[0]&0xf0 != meshoptSequenceHeader {
		return invalidf("meshopt sequence stream has a bad header")
	}
	if version := src[0] & 0x0f; version > 1 {
		return unsupportedf("meshopt sequence codec version %d", version)
	}
	data := src[:len(src)-meshoptSequenceTail]
	position := 1
	var last [2]uint32
	for i := 0; i < count; i++ {
		v, next, ok := meshoptVByte(data, position)
		if !ok {
			return meshoptTruncated()
		}
		position = next
		baseline := v & 1
		v >>= 1
		last[baseline] += (v >> 1) ^ -(v & 1)
		writeMeshoptIndex(dst, i, stride, last[baseline])
	}
	if position != len(data) {
		return meshoptTrailing()
	}
	return nil
}

// applyMeshoptFilter reverses the encoder's lossy filter in place. Results
// use round-half-away-from-zero like the reference decoder; a degenerate
// octahedral input decodes to zero instead of NaN.
func applyMeshoptFilter(data []byte, count, stride int, filter meshoptFilter) {
	switch filter {
	case meshoptFilterOctahedral:
		if stride == 4 {
			for i := 0; i < count; i++ {
				element := data[i*4 : i*4+4]
				x, y, z := octahedralNormal(float64(int8(element[0])), float64(int8(element[1])),
					float64(int8(element[2])), 127)
				element[0], element[1], element[2] = byte(int8(x)), byte(int8(y)), byte(int8(z))
			}
			return
		}
		for i := 0; i < count; i++ {
			element := data[i*8 : i*8+8]
			x, y, z := octahedralNormal(
				float64(int16(binary.LittleEndian.Uint16(element[0:]))),
				float64(int16(binary.LittleEndian.Uint16(element[2:]))),
				float64(int16(binary.LittleEndian.Uint16(element[4:]))), 32767)
			binary.LittleEndian.PutUint16(element[0:], uint16(int16(x)))
			binary.LittleEndian.PutUint16(element[2:], uint16(int16(y)))
			binary.LittleEndian.PutUint16(element[4:], uint16(int16(z)))
		}
	case meshoptFilterQuaternion:
		for i := 0; i < count; i++ {
			element := data[i*8 : i*8+8]
			var raw [4]int16
			for c := range raw {
				raw[c] = int16(binary.LittleEndian.Uint16(element[c*2:]))
			}
			scale := math.Sqrt2 / 2 / float64(int(raw[3])|3)
			x, y, z := float64(raw[0])*scale, float64(raw[1])*scale, float64(raw[2])*scale
			w := math.Sqrt(math.Max(0, 1-x*x-y*y-z*z))
			largest := int(raw[3]) & 3
			out := [4]int16{}
			out[(largest+1)&3] = int16(roundAway(x * 32767))
			out[(largest+2)&3] = int16(roundAway(y * 32767))
			out[(largest+3)&3] = int16(roundAway(z * 32767))
			out[largest] = int16(roundAway(w * 32767))
			for c := range out {
				binary.LittleEndian.PutUint16(element[c*2:], uint16(out[c]))
			}
		}
	case meshoptFilterExponential:
		for i := 0; i+4 <= count*stride; i += 4 {
			v := int32(binary.LittleEndian.Uint32(data[i:]))
			mantissa := (v << 8) >> 8
			exponent := int(v >> 24)
			value := float32(math.Ldexp(float64(mantissa), exponent))
			binary.LittleEndian.PutUint32(data[i:], math.Float32bits(value))
		}
	}
}

// octahedralNormal reconstructs a unit vector from octahedral x, y and the
// stored scale one, scaled to limit.
func octahedralNormal(x, y, one, limit float64) (int, int, int) {
	z := one - math.Abs(x) - math.Abs(y)
	t := math.Min(z, 0)
	if x >= 0 {
		x += t
	} else {
		x -= t
	}
	if y >= 0 {
		y += t
	} else {
		y -= t
	}
	length := math.Sqrt(x*x + y*y + z*z)
	if length == 0 || !finite(length) {
		return 0, 0, 0
	}
	scale := limit / length
	return roundAway(x * scale), roundAway(y * scale), roundAway(z * scale)
}

func roundAway(value float64) int {
	if value >= 0 {
		return int(value + 0.5)
	}
	return int(value - 0.5)
}
