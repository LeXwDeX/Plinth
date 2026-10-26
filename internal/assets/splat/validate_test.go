package splat

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func validRecord() []byte {
	raw := make([]byte, 32)
	for i := 0; i < 3; i++ {
		binary.LittleEndian.PutUint32(raw[12+i*4:], math.Float32bits(0.01))
	}
	raw[24], raw[25], raw[26], raw[27] = 255, 80, 20, 255
	raw[28], raw[29], raw[30], raw[31] = 255, 128, 128, 128
	return raw
}

func TestSplatRejectsMalformedAndOutOfDomainRecords(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func([]byte) []byte
		valid  bool
	}{
		{"valid", func(b []byte) []byte { return b }, true},
		{"empty", func(b []byte) []byte { return nil }, false},
		{"truncated", func(b []byte) []byte { return b[:31] }, false},
		{"extra", func(b []byte) []byte { return append(b, 0) }, false},
		{"nan-position", func(b []byte) []byte {
			binary.LittleEndian.PutUint32(b, math.Float32bits(float32(math.NaN())))
			return b
		}, false},
		{"infinite-position", func(b []byte) []byte {
			binary.LittleEndian.PutUint32(b, math.Float32bits(float32(math.Inf(1))))
			return b
		}, false},
		{"finite-extreme-position", func(b []byte) []byte { binary.LittleEndian.PutUint32(b, math.Float32bits(math.MaxFloat32)); return b }, false},
		{"finite-extreme-scale", func(b []byte) []byte {
			binary.LittleEndian.PutUint32(b[12:], math.Float32bits(math.MaxFloat32))
			return b
		}, false},
		{"nan-scale", func(b []byte) []byte {
			binary.LittleEndian.PutUint32(b[12:], math.Float32bits(float32(math.NaN())))
			return b
		}, false},
		{"zero-scale", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[12:], 0); return b }, false},
		{"negative-scale", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[12:], math.Float32bits(-.01)); return b }, false},
		{"subnormal-scale", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[12:], 1); return b }, false},
		{"degenerate-quaternion", func(b []byte) []byte {
			for i := 28; i < 32; i++ {
				b[i] = 128
			}
			return b
		}, false},
		{"all-transparent", func(b []byte) []byte { b[27] = 0; return b }, false},
		{"quantized-small-quaternion", func(b []byte) []byte { b[28] = 129; return b }, true},
		{"exact-domain-boundary", func(b []byte) []byte {
			binary.LittleEndian.PutUint32(b, math.Float32bits(-1024))
			binary.LittleEndian.PutUint32(b[4:], math.Float32bits(1024))
			binary.LittleEndian.PutUint32(b[12:], math.Float32bits(schema.GaussianSplatMinScale))
			binary.LittleEndian.PutUint32(b[16:], math.Float32bits(schema.GaussianSplatMaxScale))
			return b
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := tc.mutate(validRecord())
			if err := ValidateReader(bytes.NewReader(raw), int64(len(raw))); (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
	if err := ValidateReader(bytes.NewReader(validRecord()), 64); err == nil {
		t.Fatal("declared longer stream accepted")
	}
	if err := ValidateReader(bytes.NewReader(append(validRecord(), validRecord()...)), 32); err == nil {
		t.Fatal("undeclared trailing record accepted")
	}
	if err := ValidateReader(bytes.NewReader(validRecord()), schema.GaussianSplatMaxBytes+32); err == nil {
		t.Fatal("unbounded record count accepted")
	}
}

func TestSplatMaximumRecordCountAndFloat32Safety(t *testing.T) {
	raw := bytes.Repeat(validRecord(), int(schema.GaussianSplatMaxBytes)/32)
	if err := ValidateReader(bytes.NewReader(raw), int64(len(raw))); err != nil {
		t.Fatal(err)
	}
	// For a normalized quaternion each rotation entry is <=1 in magnitude.
	// |R diag(s^2) R^T| <= 3 max(s)^2, all in finite normal float32 range.
	for _, value := range []float32{3 * schema.GaussianSplatMaxScale * schema.GaussianSplatMaxScale, schema.GaussianSplatMinScale * schema.GaussianSplatMinScale, 2 * (schema.GaussianSplatMaxPosition + 3*schema.GaussianSplatMaxScale), 3 * schema.GaussianSplatMinScale} {
		if value <= 0 || math.IsInf(float64(value), 0) || math.IsNaN(float64(value)) || value < float32(math.Float32frombits(0x00800000)) {
			t.Fatalf("unsafe derived bound %g", value)
		}
	}
}
