package fakeupstream

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"strconv"
	"strings"
	"sync"
)

// MaxPicturePixels bounds one fake picture. The largest official output an
// image Offering can ask for is a 4K Gemini 21:9 picture (6336x2688, about 17
// million pixels); Seedream's reference limit is 6000x6000.
const MaxPicturePixels = 36_000_000

// MaxPictureEdge bounds one side of a fake picture: no official output or
// reference is wider than 8K, so a longer side is a caller mistake, and the
// bound keeps every dimension far inside the PNG header's 32-bit fields.
const MaxPictureEdge = 16_384

// MaxPaddedJPEGBytes bounds what PadJPEG will allocate: the heaviest real
// answer the fake stands in for is a few megabytes.
const MaxPaddedJPEGBytes = 64 << 20

// PictureSpec describes one fake picture at its real pixel size (issue I17).
//
// The fake used to answer every request with the same 64x64 PNG (or a 1x1
// JPEG), so a request for 2048x2048 or a 16:9 4K picture came back at another
// size and nothing downstream could notice. Picture draws the same subject as
// ImageFile("png"), proportionally, at the requested size: a plain background
// and one dark rectangle centered with margins, so the 3d_input checklist and
// the consistency checks see a usable picture at any size.
//
// The bytes are deterministic and small. A PNG is written as a flat deflate
// stream of two repeating scanlines and a JPEG is built from flat planes, so a
// 4096x4096 picture costs a few hundred kilobytes and no pixel buffer.
type PictureSpec struct {
	// Format is "png", "jpeg" or "webp". WebP is not drawn: no encoder exists
	// in the repository, so it answers the fixed 1x1 WebP of ImageFile whatever
	// the size (a documented gap; Plinth only ever asks for PNG).
	Format        string
	Width, Height int
	// Transparent gives a PNG a fully transparent background with an opaque
	// subject (an RGBA picture), the shape of a background=transparent answer.
	Transparent bool
	// Variant shades the subject so n>1 candidates differ byte for byte.
	Variant int
}

var pictureCache sync.Map // PictureSpec -> File

// Picture returns the fake picture of spec. The returned bytes are shared:
// callers must copy before changing them (Corrupt does).
func Picture(spec PictureSpec) (File, error) {
	if spec.Width <= 0 || spec.Height <= 0 || spec.Width > MaxPictureEdge || spec.Height > MaxPictureEdge ||
		int64(spec.Width)*int64(spec.Height) > MaxPicturePixels {
		return File{}, fmt.Errorf("fakeupstream: picture size %dx%d is outside 1..%d pixels and 1..%d per side", spec.Width, spec.Height, MaxPicturePixels, MaxPictureEdge)
	}
	if cached, ok := pictureCache.Load(spec); ok {
		return cached.(File), nil
	}
	var file File
	switch spec.Format {
	case "png":
		data, err := subjectPNGAt(spec)
		if err != nil {
			return File{}, err
		}
		file = File{MediaType: "image/png", Data: data}
	case "jpeg":
		if spec.Transparent {
			return File{}, fmt.Errorf("fakeupstream: a JPEG cannot be transparent")
		}
		data, err := subjectJPEGAt(spec)
		if err != nil {
			return File{}, err
		}
		file = File{MediaType: "image/jpeg", Data: data}
	case "webp":
		fixture, err := ImageFile("webp")
		if err != nil {
			return File{}, err
		}
		file = fixture
	default:
		return File{}, fmt.Errorf("fakeupstream: no picture for image format %q", spec.Format)
	}
	pictureCache.Store(spec, file)
	return file, nil
}

// ParseSize reads a "WIDTHxHEIGHT" size (the form every 2D Offering sends).
func ParseSize(value string) (width, height int, ok bool) {
	left, right, found := strings.Cut(strings.TrimSpace(value), "x")
	if !found {
		return 0, 0, false
	}
	parsedWidth, widthErr := strconv.Atoi(left)
	parsedHeight, heightErr := strconv.Atoi(right)
	if widthErr != nil || heightErr != nil || parsedWidth <= 0 || parsedHeight <= 0 {
		return 0, 0, false
	}
	return parsedWidth, parsedHeight, true
}

// subjectBox is the subject rectangle: 5/16..11/16 of the width and 1/8..7/8 of
// the height, which is exactly x 20..44, y 8..56 on the 64x64 ImageFile picture.
func subjectBox(width, height int) (x0, x1, y0, y1 int) {
	return width * 5 / 16, width * 11 / 16, height / 8, height * 7 / 8
}

// subjectShade is the subject colour of a variant. Variant 0 is the colour of
// ImageFile("png"); every further variant is a little lighter and still dark
// enough to stay far from the white background.
func subjectShade(variant int) (red, green, blue uint8) {
	if variant < 0 {
		variant = -variant
	}
	shift := (variant % 12) * 9
	return uint8(52 + shift), uint8(58 + shift/2), uint8(70 + shift/3)
}

func subjectPNGAt(spec PictureSpec) ([]byte, error) {
	width, height := spec.Width, spec.Height
	if width < 1 || height < 1 || width > MaxPictureEdge || height > MaxPictureEdge {
		return nil, fmt.Errorf("fakeupstream: picture size %dx%d is outside 1..%d per side", width, height, MaxPictureEdge)
	}
	x0, x1, y0, y1 := subjectBox(width, height)
	red, green, blue := subjectShade(spec.Variant)
	pixel := func(subject bool) []byte {
		switch {
		case spec.Transparent && subject:
			return []byte{red, green, blue, 255}
		case spec.Transparent:
			return []byte{255, 255, 255, 0}
		case subject:
			return []byte{red, green, blue}
		default:
			return []byte{255, 255, 255}
		}
	}
	background, subject := pixel(false), pixel(true)
	bytesPerPixel := len(background)
	colorType := byte(2) // truecolor
	if spec.Transparent {
		colorType = 6 // truecolor with alpha
	}
	plainRow := make([]byte, 1+width*bytesPerPixel) // filter byte 0 (None) + pixels
	subjectRow := make([]byte, 1+width*bytesPerPixel)
	for x := 0; x < width; x++ {
		copy(plainRow[1+x*bytesPerPixel:], background)
		if x >= x0 && x < x1 {
			copy(subjectRow[1+x*bytesPerPixel:], subject)
		} else {
			copy(subjectRow[1+x*bytesPerPixel:], background)
		}
	}
	var compressed bytes.Buffer
	deflater, err := zlib.NewWriterLevel(&compressed, zlib.BestSpeed)
	if err != nil {
		return nil, err
	}
	for y := 0; y < height; y++ {
		row := plainRow
		if y >= y0 && y < y1 {
			row = subjectRow
		}
		if _, err := deflater.Write(row); err != nil {
			return nil, err
		}
	}
	if err := deflater.Close(); err != nil {
		return nil, err
	}
	header := make([]byte, 13)
	binary.BigEndian.PutUint32(header[0:], uint32(width))
	binary.BigEndian.PutUint32(header[4:], uint32(height))
	header[8], header[9] = 8, colorType // 8 bits per sample; compression, filter, interlace stay 0
	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	writePNGChunk(&out, "IHDR", header)
	writePNGChunk(&out, "IDAT", compressed.Bytes())
	writePNGChunk(&out, "IEND", nil)
	return out.Bytes(), nil
}

// subjectJPEGAt draws the picture as flat YCbCr planes, so the standard
// encoder takes its direct path and the picture needs no RGB pixel buffer.
func subjectJPEGAt(spec PictureSpec) ([]byte, error) {
	width, height := spec.Width, spec.Height
	if width < 1 || height < 1 || width > MaxPictureEdge || height > MaxPictureEdge {
		return nil, fmt.Errorf("fakeupstream: picture size %dx%d is outside 1..%d per side", width, height, MaxPictureEdge)
	}
	picture := image.NewYCbCr(image.Rect(0, 0, width, height), image.YCbCrSubsampleRatio420)
	x0, x1, y0, y1 := subjectBox(width, height)
	red, green, blue := subjectShade(spec.Variant)
	subjectY, subjectCb, subjectCr := color.RGBToYCbCr(red, green, blue)
	for y := 0; y < height; y++ {
		row := picture.Y[y*picture.YStride : y*picture.YStride+width]
		for x := range row {
			if y >= y0 && y < y1 && x >= x0 && x < x1 {
				row[x] = subjectY
			} else {
				row[x] = 255
			}
		}
	}
	chromaWidth, chromaHeight := (width+1)/2, (height+1)/2
	for y := 0; y < chromaHeight; y++ {
		cb := picture.Cb[y*picture.CStride : y*picture.CStride+chromaWidth]
		cr := picture.Cr[y*picture.CStride : y*picture.CStride+chromaWidth]
		for x := range cb {
			if y*2 >= y0 && y*2 < y1 && x*2 >= x0 && x*2 < x1 {
				cb[x], cr[x] = subjectCb, subjectCr
			} else {
				cb[x], cr[x] = 128, 128
			}
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, picture, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// PadJPEG grows a JPEG to at least size bytes with COM (comment) segments,
// which every decoder skips. It stands in for a real 4K answer, whose JPEG is
// several megabytes: the fake's own flat pictures are far smaller than a real
// photograph, so a test of response limits and timeouts asks for the weight
// explicitly. The filler is a fixed pattern.
func PadJPEG(data []byte, size int) []byte {
	if size <= len(data) || len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		return data
	}
	if size > MaxPaddedJPEGBytes {
		size = MaxPaddedJPEGBytes
	}
	const segment = 65533             // a COM segment holds at most 65533 bytes of text
	out := make([]byte, 0, len(data)) // grows by append up to the bounded size; no allocation by caller value
	out = append(out, data[:2]...)
	filler := bytes.Repeat([]byte("fake-upstream-padding "), segment/22+1)[:segment]
	for len(out)+4+segment+len(data)-2 <= size {
		out = append(out, 0xFF, 0xFE, 0xFF, 0xFF)
		out = append(out, filler...)
	}
	if rest := size - len(out) - (len(data) - 2); rest > 4 {
		length := rest - 2 // the length field counts itself
		out = append(out, 0xFF, 0xFE, byte(length>>8), byte(length))
		out = append(out, filler[:length-2]...)
	}
	return append(out, data[2:]...)
}

// PadPNG grows a PNG to at least size bytes with one ancillary tEXt chunk
// before IEND, which every decoder skips (see PadJPEG).
func PadPNG(data []byte, size int) []byte {
	const trailer = 12 // the IEND chunk: length, type, CRC
	if size <= len(data) || len(data) < trailer || !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return data
	}
	if size > MaxPaddedJPEGBytes {
		size = MaxPaddedJPEGBytes
	}
	const overhead = 12 + len("Comment\x00") // chunk framing plus the keyword
	text := size - len(data) - overhead
	if text < 1 {
		text = 1
	}
	filler := bytes.Repeat([]byte("fake-upstream-padding "), text/22+1)[:text]
	var chunk bytes.Buffer
	writePNGChunk(&chunk, "tEXt", append([]byte("Comment\x00"), filler...))
	out := make([]byte, 0, len(data)+chunk.Len())
	out = append(out, data[:len(data)-trailer]...)
	out = append(out, chunk.Bytes()...)
	return append(out, data[len(data)-trailer:]...)
}

// PadFile pads a fake picture to at least size bytes (JPEG and PNG only; other
// formats are returned as they are).
func PadFile(file File, size int) File {
	switch file.MediaType {
	case "image/jpeg":
		file.Data = PadJPEG(file.Data, size)
	case "image/png":
		file.Data = PadPNG(file.Data, size)
	}
	return file
}
