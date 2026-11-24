package mattingapiv1

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"sync"
)

// The picture the download answers (I21). The service's result is the source
// picture with its background removed: an RGBA PNG of the source's own pixel
// size, transparent where the background was. The committed live captures keep
// no artifact bytes (only that the answer was image/png), so the size and the
// alpha channel are constructed, see Constructed().
//
// The fake draws the same deterministic matte for every source of one size: an
// opaque subject in the middle, a one pixel half-transparent edge around it and
// a fully transparent background.
const (
	// fallbackSide is the picture of a file whose header the fake cannot read.
	fallbackSide = 64
	// maxEchoSide bounds the memory the fake spends on one answer: a source
	// larger than this is answered scaled down to it on the long side, keeping the
	// ratio. The service keeps the size; this is the fake's own deviation.
	maxEchoSide     = 4096
	maxEchoSideText = "4096"
)

// scaledToCap returns the size the fake answers for a source of width x height.
func scaledToCap(width, height int) (int, int) {
	longest := max(width, height)
	if longest <= maxEchoSide {
		return width, height
	}
	scale := func(side int) int { return max(1, int(int64(side)*maxEchoSide/int64(longest))) }
	return scale(width), scale(height)
}

var matteCache sync.Map // [2]int -> []byte

// matteFor returns the PNG answered for a source of the given size; 0x0 (an
// unreadable file) gets the fallback size.
func matteFor(width, height int) ([]byte, error) {
	if width <= 0 || height <= 0 {
		width, height = fallbackSide, fallbackSide
	}
	width, height = scaledToCap(width, height)
	key := [2]int{width, height}
	if cached, ok := matteCache.Load(key); ok {
		return cached.([]byte), nil
	}
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	left, top := width/4, height/4
	right, bottom := max(width-width/4, left+1), max(height-height/4, top+1)
	subject := color.NRGBA{R: 52, G: 58, B: 70, A: 255}
	edge := color.NRGBA{R: 52, G: 58, B: 70, A: 128}
	for y := max(top-1, 0); y < min(bottom+1, height); y++ {
		for x := max(left-1, 0); x < min(right+1, width); x++ {
			if x >= left && x < right && y >= top && y < bottom {
				picture.SetNRGBA(x, y, subject)
			} else {
				picture.SetNRGBA(x, y, edge)
			}
		}
	}
	var encoded bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&encoded, picture); err != nil {
		return nil, err
	}
	data := encoded.Bytes()
	matteCache.Store(key, data)
	return data, nil
}
