package local

import (
	"errors"
)

// A typed run is one contiguous, length-checked little-endian wire field.
// Unlike a general accessor plan it has no gaps, overlapping descriptors or
// per-byte protocol-relative mask. Absolute addresses remain fully checked.
type typedRunKind uint8

const (
	typedRunBytes typedRunKind = iota
	typedRunBoolean
	typedRunFloat32
	typedRunFloat64
)

type typedRunValidator struct {
	kind       typedRunKind
	size, read int64
	word       [8]byte
	used       int
}

func newTypedRunValidator(size int64, kind typedRunKind, work *inspectionWorkBudget) (*typedRunValidator, error) {
	width := int64(1)
	if kind == typedRunFloat32 {
		width = 4
	}
	if kind == typedRunFloat64 {
		width = 8
	}
	if size < 0 || size > maxArchiveBytes || kind > typedRunFloat64 || size%width != 0 {
		return nil, errors.New("内容连续数值字段无效")
	}
	if work == nil {
		work = newInspectionWorkBudget()
	}
	// Each finite check touches the two exponent bytes of a word. Boolean
	// validation touches every byte. Fixed window visits cover carry/dispatch
	// work; no whole-run copy, clearing or interval-index walk is performed.
	var cost int64
	if kind == typedRunBoolean {
		if err := addInspectionWork(&cost, size, 1); err != nil {
			return nil, err
		}
	} else if width > 1 {
		if err := addInspectionWork(&cost, size/width, 2); err != nil {
			return nil, err
		}
	}
	if err := addInspectionWork(&cost, inspectionCeilChunks(size)+1, 8); err != nil {
		return nil, err
	}
	if err := work.reserve(cost); err != nil {
		return nil, err
	}
	return &typedRunValidator{kind: kind, size: size}, nil
}

func (validator *typedRunValidator) inspect(data []byte) error {
	if int64(len(data)) > validator.size-validator.read {
		return errors.New("内容连续数值字段长度无效")
	}
	validator.read += int64(len(data))
	if validator.kind == typedRunBytes {
		return nil
	}
	if validator.kind == typedRunBoolean {
		for _, value := range data {
			if value > 1 {
				return errors.New("内容数值字段含无效布尔值")
			}
		}
		return nil
	}
	width := 4
	if validator.kind == typedRunFloat64 {
		width = 8
	}
	if validator.used != 0 {
		count := copy(validator.word[validator.used:width], data)
		validator.used += count
		data = data[count:]
		if validator.used == width {
			if !finiteTypedWord(validator.word[:width]) {
				return errors.New("内容数值字段含非有限浮点数")
			}
			validator.used = 0
		}
	}
	for len(data) >= width {
		if !finiteTypedWord(data[:width]) {
			return errors.New("内容数值字段含非有限浮点数")
		}
		data = data[width:]
	}
	if len(data) != 0 {
		validator.used = copy(validator.word[:], data)
	}
	return nil
}

func finiteTypedWord(word []byte) bool {
	if len(word) == 8 {
		return word[7]&0x7f != 0x7f || word[6]&0xf0 != 0xf0
	}
	return word[3]&0x7f != 0x7f || word[2]&0x80 == 0
}

func (validator *typedRunValidator) finish() error {
	if validator.read != validator.size || validator.used != 0 {
		return errors.New("内容连续数值字段长度无效")
	}
	return nil
}

// The external-address scanner keeps only this much context. A long verified
// numeric run is equivalent to at most one full carry of spaces: all previous
// text context is gone. Preserve byte parity for the UTF-16 interpretations.
// Short runs retain every space and therefore every adjacent-field boundary.
var typedRunBoundary = func() [isolationOverlap + 2]byte {
	var boundary [isolationOverlap + 2]byte
	for index := range boundary {
		boundary[index] = ' '
	}
	return boundary
}()

func typedRunBoundaryBytes(size int64) []byte {
	count := min(size, int64(isolationOverlap)+(size&1))
	return typedRunBoundary[:int(count)]
}

type typedRunInspector struct {
	validator *typedRunValidator
	absolute  addressScanner
}

func newTypedRunInspector(size int64, kind typedRunKind, work *inspectionWorkBudget) (*typedRunInspector, error) {
	validator, err := newTypedRunValidator(size, kind, work)
	if err != nil {
		return nil, err
	}
	return &typedRunInspector{validator: validator, absolute: addressScanner{initial: absoluteAddress, continuation: absoluteAddress}}, nil
}

func (inspector *typedRunInspector) inspect(data []byte) error {
	if err := inspector.validator.inspect(data); err != nil {
		return err
	}
	if inspector.absolute.contains(data) {
		return rejectInspection("external_address", errors.New("内容数值字段含绝对外部地址"))
	}
	return nil
}
