package local

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"testing"
)

// Fixture builders for PNG images carrying a C2PA-style caBX JUMBF chunk.
// Production no longer parses the manifest (images are cached as delivered),
// but these remain useful to build realistic embedded images.

const c2paVocabularyFixture = "https://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia"

func c2paFixtureString(major byte, body []byte) []byte {
	var header []byte
	switch {
	case len(body) < 24:
		header = []byte{major<<5 | byte(len(body))}
	case len(body) < 256:
		header = []byte{major<<5 | 24, byte(len(body))}
	default:
		header = []byte{major<<5 | 25, byte(len(body) >> 8), byte(len(body))}
	}
	return append(header, body...)
}

func c2paFixtureBox(kind string, body []byte) []byte {
	result := make([]byte, 8, 8+len(body))
	binary.BigEndian.PutUint32(result[:4], uint32(len(body)+8))
	copy(result[4:8], kind)
	return append(result, body...)
}

func c2paFixturePNG(t *testing.T, kind string, body []byte) []byte {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	payload := c2paFixtureBox("jumb", c2paFixtureBox(kind, body))
	chunk := make([]byte, 8, len(payload)+12)
	binary.BigEndian.PutUint32(chunk[:4], uint32(len(payload)))
	copy(chunk[4:8], "caBX")
	chunk = append(chunk, payload...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
	result := append([]byte(nil), encoded.Bytes()[:len(encoded.Bytes())-12]...)
	result = append(result, chunk...)
	return append(result, encoded.Bytes()[len(encoded.Bytes())-12:]...)
}
