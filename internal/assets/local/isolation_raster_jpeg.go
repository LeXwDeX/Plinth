package local

import (
	"bytes"
	"encoding/binary"
	"errors"
)

type rasterJPEGComponent struct {
	id   byte
	h, v int
}

type rasterJPEGHuffman struct {
	minimum, maximum, base [16]int
	values                 []byte
}

type rasterJPEGFrame struct {
	width, height, count int
	maxH, maxV, restart  int
	baseline             bool
	progressive          bool
	components           [4]rasterJPEGComponent
	huffman              [2][4]rasterJPEGHuffman
	// Refinement scans need only the positions of nonzero coefficients.
	// One uint64 per block replaces a second copy of all int32 coefficients.
	nonzero   [4][]uint64
	precision [4][64]int8
	// structureOnly skips entropy decoding and the standard decoder for an
	// image over the decode budget.
	structureOnly bool
}

func rasterJPEGError(reason string) error {
	return rejectInspection("jpeg_"+reason, errors.New("JPEG 结构或像素数据无效"))
}

// Descriptor offsets refer to the immutable delivered bytes. Replay only
// state-changing tables, restart intervals and entropy scans after the full
// frame/color profile has been budgeted. The bounded descriptor list avoids
// a second wire parser and preserves each scan's original Huffman state.
type rasterJPEGCheck struct {
	marker                 byte
	start, end, entropyEnd int
}

func validateJPEGBytes(data []byte, work *inspectionWorkBudget) (inspectionPlan, error) {
	if len(data) < 4 || !bytes.Equal(data[:2], []byte{0xff, 0xd8}) {
		return inspectionPlan{}, rasterJPEGError("invalid_structure")
	}
	var plan inspectionPlan
	var profile jpegRasterProfile
	frame := &profile.frame
	var checks []rasterJPEGCheck
	for component := range frame.precision {
		for coefficient := range frame.precision[component] {
			frame.precision[component][coefficient] = -1
		}
	}
	sections, scans := 0, 0
	for offset := 2; offset < len(data); {
		sections++
		if sections > maxRasterSections || data[offset] != 0xff {
			return inspectionPlan{}, rasterJPEGError("invalid_structure")
		}
		for offset < len(data) && data[offset] == 0xff {
			offset++
		}
		if offset == len(data) {
			return inspectionPlan{}, rasterJPEGError("invalid_structure")
		}
		marker := data[offset]
		offset++
		if marker == 0xd9 {
			if offset != len(data) || scans == 0 || frame.count == 0 {
				return inspectionPlan{}, rasterJPEGError("invalid_structure")
			}
			if frame.structureOnly {
				// Over the decode budget: every segment and the scan
				// framing were walked; no entropy data is decoded.
				return plan, nil
			}
			cost, err := profile.work()
			if err != nil {
				return inspectionPlan{}, err
			}
			decode, err := reserveRasterDecodeWork(len(data), cost, work)
			if err != nil {
				return inspectionPlan{}, err
			}
			if !decode {
				// The shared budget cannot cover a decode; the walk above
				// already checked every segment and the scan framing.
				return plan, nil
			}
			// Tables can change between scans. Replay the original ordered
			// state transitions after prepaying all entropy/decoder work.
			for _, check := range checks {
				payload := data[check.start:check.end]
				switch check.marker {
				case 0xc4:
					err = frame.readHuffman(payload)
				case 0xdd:
					frame.restart = int(binary.BigEndian.Uint16(payload))
				case 0xda:
					err = frame.checkScan(payload, data[check.end:check.entropyEnd])
				}
				if err != nil {
					return inspectionPlan{}, err
				}
			}
			for component := 0; component < frame.count; component++ {
				if frame.precision[component][0] < 0 {
					return inspectionPlan{}, rasterJPEGError("invalid_pixels")
				}
			}
			if err := decodeValidatedJPEG(data, frame.width, frame.height); err != nil {
				return inspectionPlan{}, err
			}
			return plan, nil
		}
		if marker == 0 || marker == 0xd8 || marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7) || len(data)-offset < 2 {
			return inspectionPlan{}, rasterJPEGError("invalid_structure")
		}
		length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if length < 2 || length > len(data)-offset {
			return inspectionPlan{}, rasterJPEGError("invalid_structure")
		}
		start, end := offset+2, offset+length
		payload := data[start:end]
		numeric := false
		switch marker {
		case 0xc0, 0xc1, 0xc2:
			if frame.count != 0 {
				return inspectionPlan{}, rasterJPEGError("invalid_structure")
			}
			frame.baseline, frame.progressive = marker == 0xc0, marker == 0xc2
			if err := frame.readFrame(payload, len(data)); err != nil {
				return inspectionPlan{}, err
			}
			numeric = true
		case 0xc4:
			checks = append(checks, rasterJPEGCheck{marker: marker, start: start, end: end})
			numeric = true
		case 0xdb:
			if !validRasterJPEGQuantization(payload) {
				return inspectionPlan{}, rasterJPEGError("invalid_structure")
			}
			numeric = true
		case 0xdd:
			if len(payload) != 2 {
				return inspectionPlan{}, rasterJPEGError("invalid_structure")
			}
			checks = append(checks, rasterJPEGCheck{marker: marker, start: start, end: end})
			numeric = true
		case 0xda:
			if frame.count == 0 {
				return inspectionPlan{}, rasterJPEGError("invalid_structure")
			}
			entropyEnd, ok := rasterJPEGEntropyEnd(data, end)
			if !ok {
				return inspectionPlan{}, rasterJPEGError("invalid_pixels")
			}
			if err := profile.addScan(payload); err != nil {
				return inspectionPlan{}, err
			}
			checks = append(checks, rasterJPEGCheck{marker: marker, start: start, end: end, entropyEnd: entropyEnd})
			if entropyEnd > end {
				plan.numeric = append(plan.numeric, inspectionSpan{int64(end), int64(entropyEnd)})
			}
			scans++
			numeric = true
			// The segment ends before its entropy stream, so add its header
			// spans here and continue at the terminating marker.
			plan.numeric = append(plan.numeric, inspectionSpan{int64(offset), int64(offset + 2)}, inspectionSpan{int64(start), int64(end)})
			offset = entropyEnd
			continue
		default:
			if marker != 0xfe && (marker < 0xe0 || marker > 0xef) {
				return inspectionPlan{}, rasterJPEGError("invalid_structure")
			}
			// APP and COM payloads remain text/metadata regardless of their
			// use by a decoder. An unknown private marker cannot get a waiver.
			// Read the complete ordered color state, including late markers,
			// before selecting the standard decoder's allocation shape.
			if marker == 0xe0 && len(payload) >= 5 && bytes.Equal(payload[:5], []byte("JFIF\x00")) {
				profile.jfif = true
			}
			if marker == 0xee && len(payload) >= 12 && bytes.Equal(payload[:5], []byte("Adobe")) {
				profile.adobe, profile.transform = true, payload[11]
			}
		}
		plan.numeric = append(plan.numeric, inspectionSpan{int64(offset), int64(offset + 2)})
		if numeric && end > start {
			plan.numeric = append(plan.numeric, inspectionSpan{int64(start), int64(end)})
		}
		offset = end
	}
	return inspectionPlan{}, rasterJPEGError("invalid_structure")
}

func (frame *rasterJPEGFrame) readFrame(payload []byte, encodedBytes int) error {
	if len(payload) < 6 || payload[0] != 8 {
		return rasterJPEGError("invalid_structure")
	}
	frame.height, frame.width = int(binary.BigEndian.Uint16(payload[1:3])), int(binary.BigEndian.Uint16(payload[3:5]))
	frame.count = int(payload[5])
	if (frame.count != 1 && frame.count != 3 && frame.count != 4) || len(payload) != 6+3*frame.count {
		return rasterJPEGError("invalid_structure")
	}
	full, err := rasterFullDecode(frame.width, frame.height, encodedBytes, "jpeg")
	if err != nil {
		return err
	}
	frame.structureOnly = !full
	for index := 0; index < frame.count; index++ {
		entry := payload[6+3*index : 9+3*index]
		h, v := int(entry[1]>>4), int(entry[1]&15)
		if (h != 1 && h != 2 && h != 4) || (v != 1 && v != 2 && v != 4) || entry[2] > 3 {
			return rasterJPEGError("invalid_structure")
		}
		for earlier := 0; earlier < index; earlier++ {
			if frame.components[earlier].id == entry[0] {
				return rasterJPEGError("invalid_structure")
			}
		}
		if frame.count == 1 {
			h, v = 1, 1
		}
		frame.components[index] = rasterJPEGComponent{entry[0], h, v}
		frame.maxH, frame.maxV = max(frame.maxH, h), max(frame.maxV, v)
	}
	for index := 0; index < frame.count; index++ {
		component := frame.components[index]
		if frame.maxH%component.h != 0 || frame.maxV%component.v != 0 {
			return rasterJPEGError("invalid_structure")
		}
	}
	return nil
}

func validRasterJPEGQuantization(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
	for len(payload) != 0 {
		precision, table := payload[0]>>4, payload[0]&15
		if precision > 1 || table > 3 {
			return false
		}
		step := 1 + int(precision)
		if len(payload) < 1+64*step {
			return false
		}
		for offset := 1; offset < 1+64*step; offset += step {
			if step == 1 && payload[offset] == 0 || step == 2 && binary.BigEndian.Uint16(payload[offset:offset+2]) == 0 {
				return false
			}
		}
		payload = payload[1+64*step:]
	}
	return true
}

func (frame *rasterJPEGFrame) readHuffman(payload []byte) error {
	if len(payload) == 0 {
		return rasterJPEGError("invalid_structure")
	}
	for len(payload) != 0 {
		if len(payload) < 17 || payload[0]>>4 > 1 || payload[0]&15 > 3 {
			return rasterJPEGError("invalid_structure")
		}
		class, table := int(payload[0]>>4), int(payload[0]&15)
		var huffman rasterJPEGHuffman
		code, count := 0, 0
		for index, lengthCount := range payload[1:17] {
			n := int(lengthCount)
			huffman.minimum[index], huffman.maximum[index], huffman.base[index] = -1, -1, count
			// JPEG reserves the all-one code for alignment padding.
			if n != 0 {
				if code+n >= 1<<(index+1) {
					return rasterJPEGError("invalid_structure")
				}
				huffman.minimum[index], huffman.maximum[index] = code, code+n-1
			}
			count += n
			code = (code + n) << 1
		}
		if count == 0 || count > 256 || len(payload) < 17+count {
			return rasterJPEGError("invalid_structure")
		}
		huffman.values = payload[17 : 17+count]
		for _, value := range huffman.values {
			if class == 0 && value > 11 || class == 1 && value&15 > 10 {
				return rasterJPEGError("invalid_structure")
			}
		}
		frame.huffman[class][table] = huffman
		payload = payload[17+count:]
	}
	return nil
}

// Locate the scan's framing without treating COM/APP data as entropy. The
// subsequent Huffman pass must consume every entropy byte, unlike jpeg.Decode,
// whose compatibility reader silently skips garbage before the next marker.
func rasterJPEGEntropyEnd(data []byte, offset int) (int, bool) {
	for offset < len(data) {
		if data[offset] != 0xff {
			offset++
			continue
		}
		start := offset
		for offset < len(data) && data[offset] == 0xff {
			offset++
		}
		if offset == len(data) {
			return 0, false
		}
		marker := data[offset]
		if marker == 0 || marker >= 0xd0 && marker <= 0xd7 {
			offset++
			continue
		}
		return start, true
	}
	return 0, false
}

type rasterJPEGScanComponent struct {
	index, dc, ac int
}

func (frame *rasterJPEGFrame) checkScan(payload, entropy []byte) error {
	if len(payload) < 6 || payload[0] == 0 || int(payload[0]) > frame.count || len(payload) != 4+2*int(payload[0]) {
		return rasterJPEGError("invalid_structure")
	}
	n := int(payload[0])
	var scan [4]rasterJPEGScanComponent
	totalHV := 0
	for index := 0; index < n; index++ {
		selector, tables := payload[1+2*index], payload[2+2*index]
		componentIndex := -1
		for candidate := 0; candidate < frame.count; candidate++ {
			if frame.components[candidate].id == selector {
				componentIndex = candidate
			}
		}
		if componentIndex < 0 || tables>>4 > 3 || tables&15 > 3 || frame.baseline && (tables>>4 > 1 || tables&15 > 1) {
			return rasterJPEGError("invalid_structure")
		}
		for earlier := 0; earlier < index; earlier++ {
			if scan[earlier].index == componentIndex {
				return rasterJPEGError("invalid_structure")
			}
		}
		scan[index] = rasterJPEGScanComponent{componentIndex, int(tables >> 4), int(tables & 15)}
		component := frame.components[componentIndex]
		totalHV += component.h * component.v
	}
	if frame.count > 1 && totalHV > 10 {
		return rasterJPEGError("invalid_structure")
	}
	ss, se, ah, al := int(payload[1+2*n]), int(payload[2+2*n]), int(payload[3+2*n]>>4), int(payload[3+2*n]&15)
	if !frame.progressive {
		if ss != 0 || se != 63 || ah != 0 || al != 0 {
			return rasterJPEGError("invalid_structure")
		}
	} else if ss > se || se > 63 || ss == 0 && se != 0 || ss != 0 && n != 1 || al > 13 || ah > 13 || ah != 0 && ah != al+1 {
		return rasterJPEGError("invalid_structure")
	}
	for _, component := range scan[:n] {
		for coefficient := ss; coefficient <= se; coefficient++ {
			previous := frame.precision[component.index][coefficient]
			if ah == 0 && previous != -1 || ah != 0 && previous != int8(ah) {
				return rasterJPEGError("invalid_pixels")
			}
			frame.precision[component.index][coefficient] = int8(al)
		}
	}
	mxx, myy := (frame.width+8*frame.maxH-1)/(8*frame.maxH), (frame.height+8*frame.maxV-1)/(8*frame.maxV)
	if frame.progressive {
		for _, selected := range scan[:n] {
			component := frame.components[selected.index]
			if frame.nonzero[selected.index] == nil {
				frame.nonzero[selected.index] = make([]uint64, mxx*myy*component.h*component.v)
			}
		}
	}
	reader := rasterJPEGBitReader{data: entropy}
	mcu, blockCount, expectedRestart, eobRun := 0, 0, byte(0xd0), 0
	for my := 0; my < myy; my++ {
		for mx := 0; mx < mxx; mx++ {
			for _, selected := range scan[:n] {
				component := frame.components[selected.index]
				for block := 0; block < component.h*component.v; block++ {
					bx, by := component.h*mx+block%component.h, component.v*my+block/component.h
					if n == 1 {
						bx, by = blockCount%(mxx*component.h), blockCount/(mxx*component.h)
						blockCount++
						if bx*8 >= frame.width || by*8 >= frame.height {
							continue
						}
					}
					var nonzero uint64
					position := by*mxx*component.h + bx
					if frame.progressive {
						nonzero = frame.nonzero[selected.index][position]
					}
					if err := reader.checkBlock(&nonzero, &frame.huffman[0][selected.dc], &frame.huffman[1][selected.ac], ss, se, ah, frame.progressive, &eobRun); err != nil {
						return err
					}
					if frame.progressive {
						frame.nonzero[selected.index][position] = nonzero
					}
				}
			}
			mcu++
			if frame.restart > 0 && mcu%frame.restart == 0 && mcu < mxx*myy {
				if eobRun != 0 || !reader.restart(expectedRestart) {
					return rasterJPEGError("invalid_pixels")
				}
				expectedRestart = 0xd0 + (expectedRestart-0xd0+1)%8
			}
		}
	}
	if eobRun != 0 || !reader.aligned() || reader.offset != len(entropy) {
		return rasterJPEGError("invalid_pixels")
	}
	return nil
}

type rasterJPEGBitReader struct {
	data   []byte
	offset int
	bits   uint32
	count  uint
}

func (reader *rasterJPEGBitReader) readBits(count uint) (uint32, error) {
	for reader.count < count {
		if reader.offset == len(reader.data) {
			return 0, rasterJPEGError("invalid_pixels")
		}
		value := reader.data[reader.offset]
		reader.offset++
		if value == 0xff {
			if reader.offset == len(reader.data) || reader.data[reader.offset] != 0 {
				return 0, rasterJPEGError("invalid_pixels")
			}
			reader.offset++
		}
		reader.bits = reader.bits<<8 | uint32(value)
		reader.count += 8
	}
	reader.count -= count
	return reader.bits >> reader.count & (1<<count - 1), nil
}

func (reader *rasterJPEGBitReader) symbol(table *rasterJPEGHuffman) (byte, error) {
	if len(table.values) == 0 {
		return 0, rasterJPEGError("invalid_pixels")
	}
	code := 0
	for index := 0; index < 16; index++ {
		bit, err := reader.readBits(1)
		if err != nil {
			return 0, err
		}
		code = code<<1 | int(bit)
		if table.maximum[index] >= 0 && code >= table.minimum[index] && code <= table.maximum[index] {
			return table.values[table.base[index]+code-table.minimum[index]], nil
		}
	}
	return 0, rasterJPEGError("invalid_pixels")
}

func (reader *rasterJPEGBitReader) aligned() bool {
	return reader.count <= 7 && reader.bits&(1<<reader.count-1) == 1<<reader.count-1
}

func (reader *rasterJPEGBitReader) restart(expected byte) bool {
	if !reader.aligned() || reader.offset >= len(reader.data) || reader.data[reader.offset] != 0xff {
		return false
	}
	for reader.offset < len(reader.data) && reader.data[reader.offset] == 0xff {
		reader.offset++
	}
	if reader.offset == len(reader.data) || reader.data[reader.offset] != expected {
		return false
	}
	reader.offset++
	reader.bits, reader.count = 0, 0
	return true
}

func (reader *rasterJPEGBitReader) checkBlock(nonzero *uint64, dc, ac *rasterJPEGHuffman, ss, se, ah int, progressive bool, eobRun *int) error {
	if ah != 0 {
		if ss == 0 {
			_, err := reader.readBits(1)
			return err
		}
		return reader.refineBlock(nonzero, ac, ss, se, eobRun)
	}
	zig := ss
	if zig == 0 {
		value, err := reader.symbol(dc)
		if err != nil {
			return err
		}
		if _, err := reader.readBits(uint(value)); err != nil {
			return err
		}
		zig++
	}
	if zig > se {
		return nil
	}
	if *eobRun > 0 {
		*eobRun--
		return nil
	}
	for ; zig <= se; zig++ {
		value, err := reader.symbol(ac)
		if err != nil {
			return err
		}
		run, size := int(value>>4), uint(value&15)
		if size != 0 {
			zig += run
			if zig > se {
				return rasterJPEGError("invalid_pixels")
			}
			if _, err := reader.readBits(size); err != nil {
				return err
			}
			*nonzero |= uint64(1) << zig
		} else if run == 15 {
			if zig+15 > se {
				return rasterJPEGError("invalid_pixels")
			}
			zig += 15
		} else {
			if !progressive && run != 0 {
				return rasterJPEGError("invalid_pixels")
			}
			bits, err := reader.readBits(uint(run))
			if err != nil {
				return err
			}
			*eobRun = (1 << run) + int(bits) - 1
			return nil
		}
	}
	return nil
}

func (reader *rasterJPEGBitReader) refineNonzero(nonzero uint64, zig, end, zeros int) (int, error) {
	for ; zig <= end; zig++ {
		if nonzero&(uint64(1)<<zig) == 0 {
			if zeros == 0 {
				return zig, nil
			}
			zeros--
		} else if _, err := reader.readBits(1); err != nil {
			return 0, err
		}
	}
	return zig, nil
}

func (reader *rasterJPEGBitReader) refineBlock(nonzero *uint64, table *rasterJPEGHuffman, ss, se int, eobRun *int) error {
	zig := ss
	if *eobRun == 0 {
		for ; zig <= se; zig++ {
			value, err := reader.symbol(table)
			if err != nil {
				return err
			}
			run, size := int(value>>4), value&15
			if size == 0 && run != 15 {
				bits, err := reader.readBits(uint(run))
				if err != nil {
					return err
				}
				*eobRun = 1<<run + int(bits)
				break
			}
			if size > 1 {
				return rasterJPEGError("invalid_pixels")
			}
			if size == 1 {
				if _, err := reader.readBits(1); err != nil {
					return err
				}
			}
			zig, err = reader.refineNonzero(*nonzero, zig, se, run)
			if err != nil || zig > se {
				return rasterJPEGError("invalid_pixels")
			}
			if size == 1 {
				*nonzero |= uint64(1) << zig
			}
		}
	}
	if *eobRun > 0 {
		*eobRun--
		_, err := reader.refineNonzero(*nonzero, zig, se, -1)
		return err
	}
	return nil
}
