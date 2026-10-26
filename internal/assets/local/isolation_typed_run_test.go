package local

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func TestTypedRunValuesAndShortReadBoundaries(t *testing.T) {
	for _, item := range []struct {
		name   string
		kind   typedRunKind
		data   []byte
		accept bool
	}{
		{"integers", typedRunBytes, []byte(" //numeric.example/path "), true},
		{"absolute_integer", typedRunBytes, []byte("https://private.example/path"), false},
		{"bool", typedRunBoolean, []byte{0, 1, 1, 0}, true},
		{"invalid_bool", typedRunBoolean, []byte{0, 2}, false},
		{"finite32", typedRunFloat32, typedRunTestWord(4, uint64(math.Float32bits(1.5))), true},
		{"infinite32", typedRunFloat32, typedRunTestWord(4, uint64(math.Float32bits(float32(math.Inf(-1))))), false},
		{"nan32", typedRunFloat32, typedRunTestWord(4, 0x7fc00000), false},
		{"finite64_low_word_nan32", typedRunFloat64, typedRunTestWord(8, 0x3ff000007fc00000), true},
		{"infinite64", typedRunFloat64, typedRunTestWord(8, math.Float64bits(math.Inf(1))), false},
		{"nan64", typedRunFloat64, typedRunTestWord(8, math.Float64bits(math.NaN())), false},
	} {
		for _, step := range []int{1, 3, 0} {
			t.Run(item.name+string(rune('0'+step)), func(t *testing.T) {
				inspector, err := newTypedRunInspector(int64(len(item.data)), item.kind, newInspectionWorkBudget())
				if err == nil {
					chunk := step
					if chunk == 0 {
						chunk = len(item.data)
					}
					for offset := 0; offset < len(item.data) && err == nil; offset += chunk {
						err = inspector.inspect(item.data[offset:min(offset+chunk, len(item.data))])
					}
					if err == nil {
						err = inspector.validator.finish()
					}
				}
				if (err == nil) != item.accept {
					t.Fatalf("typed value acceptance=%t expected=%t", err == nil, item.accept)
				}
			})
		}
	}
}

func typedRunTestWord(width int, value uint64) []byte {
	result := make([]byte, width)
	if width == 8 {
		binary.LittleEndian.PutUint64(result, value)
	} else {
		binary.LittleEndian.PutUint32(result, uint32(value))
	}
	return result
}

func TestTypedRunWorkIsPrepaidAndShared(t *testing.T) {
	data := bytes.Repeat(typedRunTestWord(8, 0x3ff000007fc00000), 8193)
	work := newInspectionWorkBudget()
	inspector, err := newTypedRunInspector(int64(len(data)), typedRunFloat64, work)
	if err != nil {
		t.Fatal(err)
	}
	used := maxInspectionWork - work.remaining
	if used != int64(len(data))/8*2+(inspectionCeilChunks(int64(len(data)))+1)*8 {
		t.Fatal("validator work must count exponent bytes and fixed window visits")
	}
	if err := inspector.inspect(data); err != nil {
		t.Fatal(err)
	}
	if err := inspector.validator.finish(); err != nil {
		t.Fatal(err)
	}
	shared := &inspectionWorkBudget{remaining: 2*used - 1}
	if _, err := newTypedRunValidator(int64(len(data)), typedRunFloat64, shared); err != nil {
		t.Fatal(err)
	}
	validator, err := newTypedRunValidator(int64(len(data)), typedRunFloat64, shared)
	fbxTestRequireWorkRejection(t, err)
	if validator != nil {
		t.Fatal("exhausted budget must reject before constructing the next validator")
	}
	for _, size := range []int64{-1, math.MaxInt64, 3} {
		if _, err := newTypedRunValidator(size, typedRunFloat64, newInspectionWorkBudget()); err == nil {
			t.Fatal("overflow/misaligned declaration accepted")
		}
	}
}

func TestTypedRunBoundaryRepresentationPreservesAddressInterpretation(t *testing.T) {
	for _, size := range []int64{0, 1, 2, 4095, 4096, 4097, 65536, 65537} {
		boundary := typedRunBoundaryBytes(size)
		if len(boundary) > isolationOverlap+1 || int64(len(boundary))&1 != size&1 {
			t.Fatal("numeric boundary must preserve parity and bounded context")
		}
		for _, prefix := range [][]byte{[]byte("http"), []byte("%2"), []byte("\\u002"), {0, 'h', 0, 't'}} {
			for _, suffix := range [][]byte{[]byte("//private.example/path "), []byte("%2f%2fprivate.example/path"), []byte("\\u002f\\u002fprivate.example/path"), {0, '/', 0, '/', 0, 'x', 0, '/', 0}} {
				full := addressScanner{initial: externalAddress, continuation: continuedExternalAddress}
				compact := addressScanner{initial: externalAddress, continuation: continuedExternalAddress}
				full.contains(prefix)
				compact.contains(prefix)
				full.contains(bytes.Repeat([]byte{' '}, int(size)))
				compact.contains(boundary)
				if full.contains(suffix) != compact.contains(suffix) {
					t.Fatalf("numeric representation changed the neighboring text interpretation size=%d", size)
				}
			}
		}
	}
}
