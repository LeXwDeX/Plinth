package local

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"io"
)

const (
	fbxBinaryMagic      = "Kaydara FBX Binary  \x00\x1a\x00"
	maxFBXDepth         = 64
	maxFBXNodes         = 100000
	maxFBXProperties    = 100000
	maxFBXPropertyBytes = 256 << 20
	maxFBXArrayBytes    = 256 << 20
	// Node/property framing has a fixed number of bounded descriptor visits.
	// Body scans and scalar plans reserve their own work. The old repeated
	// 4KiB tiny-fragment carry scans no longer exist in the batched wire path.
	fbxNodeWork          = 128
	fbxPropertyWork      = 64
	fbxParserWindowWork  = 5 * isolationChunkBytes
	fbxInflateWindowWork = 2 * isolationChunkBytes
)

// These constants describe the binary wire framing, independently of any
// exporter implementation. The first 16 footer bytes are exporter-dependent;
// only the closing identifier is fixed.
var fbxFooterEnd = [...]byte{0xf8, 0x5a, 0x8c, 0x6a, 0xde, 0xf5, 0xd9, 0x7e, 0xec, 0xe9, 0x0c, 0xe3, 0x75, 0x8f, 0x29, 0x0b}

func isBinaryFBXMagic(magic []byte) bool {
	return len(magic) >= 8 && bytes.Equal(magic[:8], []byte(fbxBinaryMagic[:8]))
}

// inspectBinaryFBX grants numeric interpretation only after its wire lengths,
// types and compression boundaries have been verified. The continuous scanner
// checks absolute addresses in both delivered and decoded numeric bytes. Each
// metadata body is also checked from its own origin, across streaming windows.
func inspectBinaryFBX(reader io.Reader, size int64, work *inspectionWorkBudget) error {
	return inspectBinaryFBXFidelity(reader, size, work, nil)
}

type fbxFidelity struct{ skin, animation bool }

func inspectBinaryFBXFidelity(reader io.Reader, size int64, work *inspectionWorkBudget, fidelity *fbxFidelity) error {
	if size < 27+13+161 || size > maxArchiveBytes {
		return fbxStructureError()
	}
	if work == nil {
		work = newInspectionWorkBudget()
	}
	// Account for source/payload buffers and the three wire-batching buffers.
	if err := work.reserve(fbxParserWindowWork); err != nil {
		return err
	}
	limited := &io.LimitedReader{R: reader, N: size}
	parser := fbxInspector{
		input:    bufio.NewReaderSize(limited, isolationChunkBytes),
		wire:     newFBXWireInspector(work),
		work:     work,
		size:     size,
		buffer:   make([]byte, isolationChunkBytes),
		headerN:  13,
		fidelity: fidelity,
	}
	var header [27]byte
	if err := parser.readChecked(header[:], inspectionPlan{numeric: []inspectionSpan{{23, 27}}}); err != nil {
		return err
	}
	if !bytes.Equal(header[:23], []byte(fbxBinaryMagic)) {
		return fbxStructureError()
	}
	parser.version = binary.LittleEndian.Uint32(header[23:])
	// The supported 7.x profiles share this property wire grammar. Version
	// 7500 changes node lengths and offsets from 32 bits to 64 bits.
	if parser.version < 7100 || parser.version > 7700 || parser.version%100 != 0 {
		return fbxStructureError()
	}
	if parser.version >= 7500 {
		parser.headerN = 25
	}
	for {
		end, properties, propertyBytes, nameBytes, sentinel, err := parser.nodeHeader(size-161, 1)
		if err != nil {
			return err
		}
		if sentinel {
			break
		}
		if err := parser.nodeBody(end, properties, propertyBytes, nameBytes, 1); err != nil {
			return err
		}
	}
	if err := parser.footer(); err != nil {
		return err
	}
	if parser.offset != size || limited.N != 0 || parser.input.Buffered() != 0 {
		return fbxStructureError()
	}
	var extra [1]byte
	if count, err := reader.Read(extra[:]); count != 0 || err != io.EOF {
		return fbxStructureError()
	}
	return parser.wire.flush()
}

type fbxInspector struct {
	input             *bufio.Reader
	wire              *fbxWireInspector
	work              *inspectionWorkBudget
	buffer            []byte
	size, offset      int64
	headerN           int64
	version           uint32
	nodes, properties int
	fidelity          *fbxFidelity
	nodeNames         [maxFBXDepth + 1]string
}

// Batch small headers, names and scalar properties into the same delivered-
// byte windows. Re-running two encoded-address scans over a 4KiB carry for
// every tiny fragment would multiply work despite a small physical file.
// Finite values and local plans are validated immediately; only address scans
// wait for a complete window or the mandatory successful-EOF flush.
type fbxWireInspector struct {
	finite             *plannedInspector
	absolute, external addressScanner
	absoluteBytes      []byte
	externalBytes      []byte
	maskedScratch      []byte
	absoluteUsed       int
	externalUsed       int
	failure            error
}

func newFBXWireInspector(work *inspectionWorkBudget) *fbxWireInspector {
	return &fbxWireInspector{
		finite:        newPlannedInspectorWithWork(work),
		absolute:      addressScanner{initial: absoluteAddress, continuation: absoluteAddress},
		external:      addressScanner{initial: externalAddress, continuation: continuedExternalAddress},
		absoluteBytes: make([]byte, isolationChunkBytes),
		externalBytes: make([]byte, isolationChunkBytes),
		maskedScratch: make([]byte, isolationChunkBytes),
	}
}

func (inspector *fbxWireInspector) prepare(plan *inspectionPlan, size int64) error {
	if inspector.failure != nil {
		return inspector.failure
	}
	return inspector.finite.prepare(plan, size)
}

func (inspector *fbxWireInspector) inspect(data []byte, plan inspectionPlan) error {
	if err := inspector.prepare(&plan, int64(len(data))); err != nil {
		return err
	}
	return inspector.inspectPart(data, plan, 0)
}

func (inspector *fbxWireInspector) inspectPart(data []byte, plan inspectionPlan, offset int64) error {
	if inspector.failure != nil {
		return inspector.failure
	}
	for len(data) > 0 {
		chunk := data[:min(len(data), isolationChunkBytes)]
		if err := inspector.finite.inspectFinitePart(chunk, &plan, offset); err != nil {
			return err
		}
		masked := inspector.maskedScratch[:len(chunk)]
		copy(masked, chunk)
		maskInspectionSpans(masked, offset, plan.allowed)
		maskInspectionSpans(masked, offset, plan.numeric)
		for _, index := range inspector.finite.active {
			maskInspectionField(masked, offset, plan.fields[index])
		}
		// Mask every typed field once per source window, before splitting it
		// over queued windows. This avoids extra field/index visits at a batch
		// boundary; the already reserved numeric-work estimate stays valid.
		if err := inspector.queueAbsolute(chunk, plan.allowed, offset); err != nil {
			return err
		}
		if err := inspector.queueExternal(masked); err != nil {
			return err
		}
		offset += int64(len(chunk))
		data = data[len(chunk):]
	}
	return nil
}

func (inspector *fbxWireInspector) queueAbsolute(data []byte, allowed []inspectionSpan, offset int64) error {
	for len(data) != 0 {
		count := min(len(data), isolationChunkBytes-inspector.absoluteUsed)
		dst := inspector.absoluteBytes[inspector.absoluteUsed : inspector.absoluteUsed+count]
		copy(dst, data[:count])
		maskInspectionSpans(dst, offset, allowed)
		inspector.absoluteUsed += count
		offset += int64(count)
		data = data[count:]
		if inspector.absoluteUsed == isolationChunkBytes {
			if err := inspector.flushAbsolute(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (inspector *fbxWireInspector) queueExternal(data []byte) error {
	for len(data) != 0 {
		count := copy(inspector.externalBytes[inspector.externalUsed:], data)
		inspector.externalUsed += count
		data = data[count:]
		if inspector.externalUsed == isolationChunkBytes {
			if err := inspector.flushExternal(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (inspector *fbxWireInspector) beginNumeric(size int64, work *inspectionWorkBudget) error {
	boundary := typedRunBoundaryBytes(size)
	if err := work.reserve(int64(len(boundary)) + 8*(inspectionCeilChunks(size)+1)); err != nil {
		return err
	}
	return inspector.queueExternal(boundary)
}

func (inspector *fbxWireInspector) numericPart(data []byte) error {
	if inspector.failure != nil {
		return inspector.failure
	}
	return inspector.queueAbsolute(data, nil, 0)
}

func (inspector *fbxWireInspector) flushAbsolute() error {
	if inspector.failure != nil {
		return inspector.failure
	}
	if inspector.absoluteUsed == 0 {
		return nil
	}
	if inspector.absolute.contains(inspector.absoluteBytes[:inspector.absoluteUsed]) {
		// A compressed Reader can return bytes together with this error. Keep
		// it sticky even if an intermediate codec consumes only those bytes.
		inspector.failure = rejectInspection("external_address", errors.New("FBX 内容含外部地址"))
		return inspector.failure
	}
	inspector.absoluteUsed = 0
	return nil
}

func (inspector *fbxWireInspector) flushExternal() error {
	if inspector.failure != nil {
		return inspector.failure
	}
	if inspector.externalUsed != 0 && inspector.external.contains(inspector.externalBytes[:inspector.externalUsed]) {
		inspector.failure = rejectInspection("external_address", errors.New("FBX 内容含外部地址"))
		return inspector.failure
	}
	inspector.externalUsed = 0
	return nil
}

func (inspector *fbxWireInspector) flush() error {
	if err := inspector.flushAbsolute(); err != nil {
		return err
	}
	return inspector.flushExternal()
}

func fbxStructureError() error {
	return rejectInspection("fbx_invalid_structure", errors.New("FBX 字节结构无效"))
}

func fbxArrayError() error {
	return rejectInspection("fbx_invalid_array", errors.New("FBX 数值数组结构无效"))
}

func (parser *fbxInspector) readSource(data []byte) error {
	if int64(len(data)) > parser.size-parser.offset {
		return fbxStructureError()
	}
	count, err := io.ReadFull(parser.input, data)
	parser.offset += int64(count)
	if err != nil {
		return fbxStructureError()
	}
	return nil
}

func (parser *fbxInspector) readChecked(data []byte, plan inspectionPlan) error {
	if err := parser.readSource(data); err != nil {
		return err
	}
	return parser.wire.inspect(data, plan)
}

func (parser *fbxInspector) readNumeric(data []byte) error {
	return parser.readChecked(data, inspectionPlan{numeric: []inspectionSpan{{0, int64(len(data))}}})
}

func (parser *fbxInspector) nodeHeader(limit int64, depth int) (end, properties, propertyBytes, nameBytes int64, sentinel bool, err error) {
	if depth > maxFBXDepth || parser.offset > limit || parser.headerN > limit-parser.offset {
		return 0, 0, 0, 0, false, fbxStructureError()
	}
	if err := parser.work.reserve(fbxNodeWork); err != nil {
		return 0, 0, 0, 0, false, err
	}
	var header [25]byte
	if err := parser.readNumeric(header[:parser.headerN]); err != nil {
		return 0, 0, 0, 0, false, err
	}
	if allFBXZero(header[:parser.headerN]) {
		return 0, 0, 0, 0, true, nil
	}
	var endWord, propertyWord, lengthWord uint64
	if parser.headerN == 25 {
		endWord = binary.LittleEndian.Uint64(header[:8])
		propertyWord = binary.LittleEndian.Uint64(header[8:16])
		lengthWord = binary.LittleEndian.Uint64(header[16:24])
		nameBytes = int64(header[24])
	} else {
		endWord = uint64(binary.LittleEndian.Uint32(header[:4]))
		propertyWord = uint64(binary.LittleEndian.Uint32(header[4:8]))
		lengthWord = uint64(binary.LittleEndian.Uint32(header[8:12]))
		nameBytes = int64(header[12])
	}
	if endWord > uint64(limit) || endWord < uint64(parser.offset) || nameBytes == 0 ||
		propertyWord > uint64(maxFBXProperties-parser.properties) || lengthWord > uint64(limit-parser.offset) {
		return 0, 0, 0, 0, false, fbxStructureError()
	}
	end, properties, propertyBytes = int64(endWord), int64(propertyWord), int64(lengthWord)
	if nameBytes > end-parser.offset || propertyBytes > end-parser.offset-nameBytes || properties > propertyBytes/2 {
		return 0, 0, 0, 0, false, fbxStructureError()
	}
	parser.nodes++
	if parser.nodes > maxFBXNodes {
		return 0, 0, 0, 0, false, fbxStructureError()
	}
	if err := parser.work.reserve(properties*fbxPropertyWork + nameBytes*2); err != nil {
		return 0, 0, 0, 0, false, err
	}
	parser.properties += int(properties)
	return end, properties, propertyBytes, nameBytes, false, nil
}

func (parser *fbxInspector) nodeBody(end, properties, propertyBytes, nameBytes int64, depth int) error {
	if parser.fidelity != nil {
		name, err := parser.input.Peek(int(nameBytes))
		if err != nil {
			return err
		}
		parser.nodeNames[depth] = string(name)
	}
	if err := parser.consume(nameBytes, inspectionPlan{}, true, false); err != nil {
		return err
	}
	propertyEnd := parser.offset + propertyBytes // Header validation bounded the addition.
	object := parser.fidelity != nil && depth == 2 && parser.nodeNames[1] == "Objects" && properties >= 3
	validObject := object
	for index := int64(0); index < properties; index++ {
		if object && index < 3 {
			if err := parser.work.reserve(16); err != nil {
				return err
			}
			code, err := parser.input.Peek(1)
			if err != nil {
				return err
			}
			if index == 0 {
				validObject = validObject && code[0] == 'L'
			}
			if index == 1 {
				validObject = validObject && code[0] == 'S'
			}
			if index == 2 && validObject && code[0] == 'S' {
				// Only typed object declarations under Objects count. Metadata and
				// binary arrays containing these words cannot create fidelity facts.
				if parser.nodeNames[depth] == "AnimationStack" {
					parser.fidelity.animation = true
				}
				if parser.nodeNames[depth] == "Deformer" && propertyEnd-parser.offset >= 9 {
					prefix, err := parser.input.Peek(9)
					if err != nil {
						return err
					}
					if binary.LittleEndian.Uint32(prefix[1:5]) == 4 && string(prefix[5:9]) == "Skin" {
						parser.fidelity.skin = true
					}
				}
			}
		}
		if err := parser.property(propertyEnd); err != nil {
			return err
		}
	}
	if parser.offset != propertyEnd {
		return fbxStructureError()
	}
	if parser.offset == end {
		// A leaf can omit its own null record. Blender's exporter (used by
		// Tripo's converter) also omits it for a last child without properties.
		return nil
	}
	if parser.headerN > end-parser.offset {
		return fbxStructureError()
	}
	childLimit := end - parser.headerN
	for parser.offset < childLimit {
		childEnd, count, length, name, sentinel, err := parser.nodeHeader(childLimit, depth+1)
		if err != nil {
			return err
		}
		if sentinel {
			return fbxStructureError() // A terminator must occupy the exact final record.
		}
		if err := parser.nodeBody(childEnd, count, length, name, depth+1); err != nil {
			return err
		}
	}
	if parser.offset != childLimit {
		return fbxStructureError()
	}
	var sentinel [25]byte
	if err := parser.readNumeric(sentinel[:parser.headerN]); err != nil {
		return err
	}
	if !allFBXZero(sentinel[:parser.headerN]) || parser.offset != end {
		return fbxStructureError()
	}
	return nil
}

func (parser *fbxInspector) property(limit int64) error {
	if parser.offset >= limit {
		return fbxStructureError()
	}
	var code [1]byte
	if err := parser.readChecked(code[:], inspectionPlan{}); err != nil {
		return err
	}
	var width int64
	var floating, boolean bool
	switch code[0] {
	case 'Z', 'C':
		width = 1
	case 'B':
		width, boolean = 1, true
	case 'Y':
		width = 2
	case 'I':
		width = 4
	case 'L':
		width = 8
	case 'F':
		width, floating = 4, true
	case 'D':
		width, floating = 8, true
	case 'b', 'c', 'i', 'l', 'f', 'd':
		return parser.array(code[0], limit)
	case 'S', 'R':
		return parser.content(code[0], limit)
	default:
		return fbxStructureError()
	}
	if width > limit-parser.offset {
		return fbxStructureError()
	}
	plan := fbxNumericPlan(width, width, floating)
	return parser.consume(width, plan, false, boolean)
}

func fbxNumericPlan(size, width int64, floating bool) inspectionPlan {
	if size == 0 {
		return inspectionPlan{}
	}
	plan := inspectionPlan{fields: []inspectionField{{0, width, width, size / width, floating}}}
	if floating {
		plan.floatBytes = width
	}
	return plan
}

// consume aggregates short source reads into bounded 64KiB windows. A plan is
// preflighted once for the complete field, so its work/index reservation covers
// every window without a reset or per-byte descriptor traversal.
func (parser *fbxInspector) consume(length int64, plan inspectionPlan, text, boolean bool) error {
	if length < 0 || length > parser.size-parser.offset {
		return fbxStructureError()
	}
	if err := parser.wire.prepare(&plan, length); err != nil {
		return err
	}
	scanner := addressScanner{initial: externalAddress, continuation: continuedExternalAddress}
	for offset := int64(0); offset < length; {
		chunk := parser.buffer[:min(int64(len(parser.buffer)), length-offset)]
		if err := parser.readSource(chunk); err != nil {
			return err
		}
		if boolean && !fbxBooleans(chunk) {
			return fbxArrayError()
		}
		if text && scanner.contains(chunk) {
			return rejectInspection("external_address", errors.New("FBX 字段含外部地址"))
		}
		if err := parser.wire.inspectPart(chunk, plan, offset); err != nil {
			return err
		}
		offset += int64(len(chunk))
	}
	return nil
}

func (parser *fbxInspector) array(kind byte, limit int64) error {
	if limit-parser.offset < 12 {
		return fbxArrayError()
	}
	var header [12]byte
	if err := parser.readNumeric(header[:]); err != nil {
		return err
	}
	count := int64(binary.LittleEndian.Uint32(header[:4]))
	encoding := binary.LittleEndian.Uint32(header[4:8])
	encoded := int64(binary.LittleEndian.Uint32(header[8:12]))
	width := int64(1)
	if kind == 'i' || kind == 'f' {
		width = 4
	} else if kind == 'l' || kind == 'd' {
		width = 8
	}
	if encoding > 1 || encoded > limit-parser.offset || count > maxFBXArrayBytes/width {
		return fbxArrayError()
	}
	decoded := count * width
	typeKind := typedRunBytes
	if kind == 'f' {
		typeKind = typedRunFloat32
	}
	if kind == 'd' {
		typeKind = typedRunFloat64
	}
	if kind == 'b' {
		typeKind = typedRunBoolean
	}
	if encoding == 0 {
		if encoded != decoded {
			return fbxArrayError()
		}
		return parser.consumeTyped(decoded, typeKind)
	}
	// Prepay the single produced decoded stream and fixed codec buffers.
	// Encoded bytes enter the already required continuous delivered-wire
	// scanner once. Their separate numeric copy/mask has been removed; only
	// its bounded boundary/queue visits remain and are reserved below.
	if err := parser.work.reserve(decoded + fbxInflateWindowWork); err != nil {
		return err
	}
	decodedInspector, err := newTypedRunInspector(decoded, typeKind, parser.work)
	if err != nil {
		return err
	}
	if err := parser.wire.beginNumeric(encoded, parser.work); err != nil {
		return err
	}
	raw := &fbxEncodedReader{parser: parser, remaining: encoded}
	// zlib and the tail check share this exact reader. Bytes read ahead into
	// its buffer are still unconsumed compressed tail and must be rejected.
	compressed := bufio.NewReaderSize(raw, isolationChunkBytes)
	stream, err := zlib.NewReader(compressed)
	if err != nil {
		return fbxArrayError()
	}
	defer stream.Close()
	for offset := int64(0); offset < decoded; {
		chunk := parser.buffer[:min(int64(len(parser.buffer)), decoded-offset)]
		if _, err := io.ReadFull(stream, chunk); err != nil {
			return fbxArrayError()
		}
		if err := decodedInspector.inspect(chunk); err != nil {
			return err
		}
		offset += int64(len(chunk))
	}
	var extra [1]byte
	if count, err := stream.Read(extra[:]); count != 0 || err != io.EOF {
		return fbxArrayError() // Also verifies the checksum after the exact count.
	}
	if err := stream.Close(); err != nil {
		return fbxArrayError()
	}
	if _, err := compressed.ReadByte(); err != io.EOF || raw.remaining != 0 {
		return fbxArrayError()
	}
	return decodedInspector.validator.finish()
}

func (parser *fbxInspector) consumeTyped(size int64, kind typedRunKind) error {
	validator, err := newTypedRunValidator(size, kind, parser.work)
	if err != nil {
		return err
	}
	// Reading a raw run produces the same checked window as inflation does.
	// No second copy is made for finite checks or relative-address masking.
	if err := parser.work.reserve(size); err != nil {
		return err
	}
	if err := parser.wire.beginNumeric(size, parser.work); err != nil {
		return err
	}
	for remaining := size; remaining > 0; {
		chunk := parser.buffer[:min(int64(len(parser.buffer)), remaining)]
		if err := parser.readSource(chunk); err != nil {
			return err
		}
		if err := validator.inspect(chunk); err != nil {
			return err
		}
		if err := parser.wire.numericPart(chunk); err != nil {
			return err
		}
		remaining -= int64(len(chunk))
	}
	return validator.finish()
}

// This reader exposes exactly one declared encoded payload while preserving
// the model's continuous delivered-byte scan. Its caller buffers ReadByte;
// no compressed byte causes an individual disk read.
type fbxEncodedReader struct {
	parser    *fbxInspector
	remaining int64
}

func (reader *fbxEncodedReader) Read(data []byte) (int, error) {
	if reader.remaining == 0 {
		return 0, io.EOF
	}
	data = data[:min(int64(len(data)), reader.remaining)]
	count, err := reader.parser.input.Read(data)
	reader.parser.offset += int64(count)
	reader.remaining -= int64(count)
	if count > 0 {
		if inspectErr := reader.parser.wire.numericPart(data[:count]); inspectErr != nil {
			return count, inspectErr
		}
	}
	if err == io.EOF && reader.remaining != 0 {
		return count, fbxArrayError()
	}
	return count, err
}

func (parser *fbxInspector) content(kind byte, limit int64) error {
	if limit-parser.offset < 4 {
		return fbxStructureError()
	}
	var header [4]byte
	if err := parser.readNumeric(header[:]); err != nil {
		return err
	}
	length := int64(binary.LittleEndian.Uint32(header[:]))
	if length > maxFBXPropertyBytes || length > limit-parser.offset {
		return fbxStructureError()
	}
	// Cover the body read, its independent origin scan and both continuous
	// wire scanners before allocating or consuming a declared payload.
	if err := parser.work.reserve(length * 4); err != nil {
		return err
	}
	if kind == 'S' || length == 0 {
		return parser.consume(length, inspectionPlan{}, true, false)
	}
	var prefix [8]byte
	peek := prefix[:min(int64(len(prefix)), length)]
	if err := parser.readSource(peek); err != nil {
		return err
	}
	format := rasterFormat(peek, "", "")
	if format != "" {
		if length > maxRasterInspect {
			return fbxStructureError()
		}
		data := make([]byte, int(length))
		copy(data, peek)
		if err := parser.readSource(data[len(peek):]); err != nil {
			return err
		}
		plan, err := validateRasterBytesWithWork(data, format, parser.work)
		if err != nil {
			return err
		}
		return parser.wire.inspect(data, plan)
	}
	scanner := addressScanner{initial: externalAddress, continuation: continuedExternalAddress}
	if scanner.contains(peek) {
		return rejectInspection("external_address", errors.New("FBX 字段含外部地址"))
	}
	if err := parser.wire.inspect(peek, inspectionPlan{}); err != nil {
		return err
	}
	for remaining := length - int64(len(peek)); remaining > 0; {
		chunk := parser.buffer[:min(int64(len(parser.buffer)), remaining)]
		if err := parser.readSource(chunk); err != nil {
			return err
		}
		if scanner.contains(chunk) {
			return rejectInspection("external_address", errors.New("FBX 字段含外部地址"))
		}
		if err := parser.wire.inspect(chunk, inspectionPlan{}); err != nil {
			return err
		}
		remaining -= int64(len(chunk))
	}
	return nil
}

func (parser *fbxInspector) footer() error {
	if err := parser.work.reserve(256); err != nil {
		return err
	}
	var identifier [16]byte
	if err := parser.readChecked(identifier[:], inspectionPlan{}); err != nil {
		return err
	}
	if containsExternalAddress(identifier[:]) {
		return rejectInspection("external_address", errors.New("FBX 页脚含外部地址"))
	}
	// Blender writes its reserved zero word before alignment; Assimp aligns
	// first, then writes the zero word. Select only those two exact lengths
	// from the remaining file size; never search arbitrary padding for a
	// plausible version. Both retain their full 16-byte aligned padding block.
	const tailBytes = 4 + 120 + 16 // version, reserved zeros, closing identifier
	padding := parser.size - parser.offset - tailBytes
	blenderPadding := 4 + 16 - (parser.offset+4)%16
	assimpPadding := 16 - parser.offset%16 + 4
	if padding != blenderPadding && padding != assimpPadding {
		return fbxStructureError()
	}
	var zeros [140]byte
	if err := parser.readNumeric(zeros[:padding]); err != nil {
		return err
	}
	if !allFBXZero(zeros[:padding]) {
		return fbxStructureError()
	}
	var version [4]byte
	if err := parser.readNumeric(version[:]); err != nil {
		return err
	}
	if binary.LittleEndian.Uint32(version[:]) != parser.version {
		return fbxStructureError()
	}
	if err := parser.readNumeric(zeros[:120]); err != nil {
		return err
	}
	if !allFBXZero(zeros[:120]) {
		return fbxStructureError()
	}
	var closing [16]byte
	if err := parser.readChecked(closing[:], inspectionPlan{}); err != nil {
		return err
	}
	if !bytes.Equal(closing[:], fbxFooterEnd[:]) {
		return fbxStructureError()
	}
	return nil
}

func allFBXZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}

func fbxBooleans(data []byte) bool {
	for _, value := range data {
		if value > 1 {
			return false
		}
	}
	return true
}
