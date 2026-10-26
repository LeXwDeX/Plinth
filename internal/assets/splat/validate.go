// Package splat validates the application's bounded 32-byte Gaussian profile.
// It never rewrites geometry, reduces point count or follows external resources.
package splat

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"math"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

var ErrInvalid = errors.New("invalid Gaussian Splat byte profile")

// ValidateReader consumes exactly size bytes with constant memory and bounded
// work. Records are LE xyz/scale float32, RGBA uint8, then quaternion uint8.
// Quantized quaternion components decode as (byte-128)/128 and normalize in the
// renderer. An exact unit-length constraint would reject valid quantization.
func ValidateReader(input io.Reader, size int64) error {
	if input == nil || size <= 0 || size > schema.GaussianSplatMaxBytes || size%schema.GaussianSplatRecordBytes != 0 {
		return ErrInvalid
	}
	reader := bufio.NewReaderSize(input, 64<<10)
	var record [schema.GaussianSplatRecordBytes]byte
	visible := false
	for offset := int64(0); offset < size; offset += schema.GaussianSplatRecordBytes {
		if _, err := io.ReadFull(reader, record[:]); err != nil {
			return ErrInvalid
		}
		for axis := 0; axis < 3; axis++ {
			position := float64(math.Float32frombits(binary.LittleEndian.Uint32(record[axis*4:])))
			scale := float64(math.Float32frombits(binary.LittleEndian.Uint32(record[12+axis*4:])))
			if math.IsNaN(position) || math.Abs(position) > schema.GaussianSplatMaxPosition || math.IsNaN(scale) || scale < schema.GaussianSplatMinScale || scale > schema.GaussianSplatMaxScale {
				return ErrInvalid
			}
		}
		if record[28] == 128 && record[29] == 128 && record[30] == 128 && record[31] == 128 {
			return ErrInvalid
		}
		visible = visible || record[27] != 0
	}
	if _, err := reader.ReadByte(); err != io.EOF || !visible {
		return ErrInvalid
	}
	return nil
}
