package assetdelivery

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestExifStamperAddsSchemaMetadataToSupportedContainers(
	t *testing.T,
) {
	metadata := proxySchemaFixture().Metadata
	clock := func() time.Time {
		return time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	}
	stamper := NewExifStamper(clock)
	for name, fixture := range map[string]struct {
		mediaType string
		content   []byte
		assert    func(*testing.T, []byte)
	}{
		"jpeg": {
			mediaType: "image/jpeg",
			content:   jpegFixture(t),
			assert: func(t *testing.T, content []byte) {
				t.Helper()
				if !bytes.Contains(content, []byte("Exif\x00\x00")) {
					t.Fatal("JPEG 缺少 APP1 Exif 标识")
				}
			},
		},
		"png": {
			mediaType: "image/png",
			content:   pngFixture(t),
			assert: func(t *testing.T, content []byte) {
				t.Helper()
				if bytes.Count(content, []byte("eXIf")) != 1 {
					t.Fatal("PNG 没有且仅有一个 eXIf chunk")
				}
			},
		},
		"webp": {
			mediaType: "image/webp",
			content:   webPFixture(),
			assert: func(t *testing.T, content []byte) {
				t.Helper()
				if string(content[:4]) != "RIFF" ||
					string(content[8:12]) != "WEBP" ||
					string(content[12:16]) != "VP8X" ||
					content[20]&0x08 == 0 ||
					bytes.Count(content, []byte("EXIF")) != 1 {
					t.Fatal("WebP 缺少合法 VP8X EXIF 声明")
				}
				if int(binary.LittleEndian.Uint32(content[4:8])) !=
					len(content)-8 {
					t.Fatal("WebP RIFF 文件长度未更新")
				}
			},
		},
		"tiff": {
			mediaType: "image/tiff",
			content:   tiffFixture(),
			assert: func(t *testing.T, content []byte) {
				t.Helper()
				offset := binary.LittleEndian.Uint32(content[4:8])
				if offset <= 8 ||
					int(offset)+2 > len(content) ||
					binary.LittleEndian.Uint16(
						content[offset:offset+2],
					) < 3 {
					t.Fatal("TIFF IFD0 未合并版权字段")
				}
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := stamper.Stamp(
				fixture.content,
				fixture.mediaType,
				metadata,
			)
			if err != nil {
				t.Fatal(err)
			}
			copyright := strings.ReplaceAll(
				metadata.CopyrightTemplate,
				"{year}",
				strconv.Itoa(clock().Year()),
			)
			for _, value := range []string{
				copyright,
				metadata.Artist,
				metadata.Software,
			} {
				if !bytes.Contains(result, []byte(value)) {
					t.Fatalf("容器缺少 Schema 元数据 %q", value)
				}
			}
			fixture.assert(t, result)
		})
	}
}

func TestExifStamperLeavesUnsupportedMediaUnchanged(t *testing.T) {
	content := []byte("gif-content")
	result, err := NewExifStamper(time.Now).Stamp(
		content,
		"image/gif",
		catalog.AssetProxyMetadataSchema{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result, content) {
		t.Fatalf("unsupported media changed to %q", result)
	}
}

func jpegFixture(t *testing.T) []byte {
	t.Helper()
	var encoded bytes.Buffer
	source := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	source.Set(0, 0, color.NRGBA{R: 255, A: 255})
	if err := jpeg.Encode(&encoded, source, nil); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func pngFixture(t *testing.T) []byte {
	t.Helper()
	var encoded bytes.Buffer
	source := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	source.Set(0, 0, color.NRGBA{G: 255, A: 255})
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func webPFixture() []byte {
	// VP8L 2x3 canvas header. The metadata stamper only rewrites the RIFF
	// container and does not need to decode the synthetic image payload.
	widthMinusOne := uint32(1)
	heightMinusOne := uint32(2)
	header := widthMinusOne | heightMinusOne<<14
	payload := []byte{
		0x2f,
		byte(header),
		byte(header >> 8),
		byte(header >> 16),
		byte(header >> 24),
	}
	var result bytes.Buffer
	result.WriteString("RIFF")
	_ = binary.Write(&result, binary.LittleEndian, uint32(4+8+6))
	result.WriteString("WEBP")
	result.WriteString("VP8L")
	_ = binary.Write(&result, binary.LittleEndian, uint32(len(payload)))
	result.Write(payload)
	result.WriteByte(0)
	return result.Bytes()
}

func tiffFixture() []byte {
	content := make([]byte, 14)
	copy(content[:2], "II")
	binary.LittleEndian.PutUint16(content[2:4], 42)
	binary.LittleEndian.PutUint32(content[4:8], 8)
	binary.LittleEndian.PutUint16(content[8:10], 0)
	binary.LittleEndian.PutUint32(content[10:14], 0)
	return content
}
