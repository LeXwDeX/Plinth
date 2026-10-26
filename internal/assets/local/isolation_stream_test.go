package local

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestAddressStreamPreservesOBJFaceContext(t *testing.T) {
	for _, encoding := range []string{"raw", "percent", "slash escaped", "Unicode escaped", "UTF16LE", "UTF16BE"} {
		for _, window := range []int{0, 1, 3} {
			for _, negative := range []bool{false, true} {
				name := fmt.Sprintf("%s/window_%d/negative_%t", encoding, window, negative)
				t.Run(name, func(t *testing.T) {
					boundary := isolationChunkBytes - isolationOverlap + window*isolationChunkBytes
					prefix, suffix := "f 12", "//3 13//3 14//3\n"
					if negative {
						prefix, suffix = "f -12", "//-3 -13//-3 -14//-3\n"
					}
					switch encoding {
					case "percent":
						suffix = strings.ReplaceAll(suffix, "/", `%2F`)
					case "slash escaped":
						suffix = strings.ReplaceAll(suffix, "/", `\/`)
					case "Unicode escaped":
						suffix = strings.ReplaceAll(suffix, "/", `\u002f`)
					}
					var body []byte
					if strings.HasPrefix(encoding, "UTF16") {
						body = utf16LE("#" + strings.Repeat("x", boundary/2-len(prefix)-2) + "\n" + prefix + suffix)
						body = append(body, utf16LE("#"+strings.Repeat("x", isolationChunkBytes)+"\n")...)
						if encoding == "UTF16BE" {
							for offset := 0; offset+1 < len(body); offset += 2 {
								body[offset], body[offset+1] = body[offset+1], body[offset]
							}
						}
					} else {
						body = []byte("#" + strings.Repeat("x", boundary-len(prefix)-2) + "\n" + prefix + suffix)
						body = append(body, []byte("#"+strings.Repeat("x", isolationChunkBytes)+"\n")...)
					}
					if err := scanExternalAddresses(bytes.NewReader(body)); err != nil {
						t.Fatalf("valid face index at a later window boundary rejected: %v", err)
					}
				})
			}
		}
	}
}

type fragmentAddressReader struct {
	reader io.Reader
	limit  int
}

func (r fragmentAddressReader) Read(body []byte) (int, error) {
	return r.reader.Read(body[:min(len(body), r.limit)])
}

func TestAddressStreamRejectsReferencesAcrossReadBoundaries(t *testing.T) {
	for _, content := range [][]byte{
		[]byte("mtllib //192.0.2.1/material.mtl\n"),
		[]byte("mtllib //localhost/material.mtl\n"),
		[]byte("mtllib //[::1]/material.mtl\n"),
		[]byte("mtllib //3232235777/material.mtl\n"),
		[]byte(`mtllib %2F%2Flocalhost%2Fmaterial.mtl`),
		[]byte(`mtllib \/\/localhost\/material.mtl`),
		[]byte(`mtllib \u002f\u002flocalhost/material.mtl`),
		utf16LE("Texture: //localhost/material.png"),
		utf16LE(`Texture: %2F%2Flocalhost%2Fmaterial.png`),
		utf16LE(`Texture: \u002f\u002flocalhost/material.png`),
	} {
		for _, position := range []int{0, isolationChunkBytes - isolationOverlap, isolationChunkBytes - 1, isolationChunkBytes + 1} {
			body := append(bytes.Repeat([]byte(" "), position), content...)
			if err := scanExternalAddresses(bytes.NewReader(body)); err == nil {
				t.Fatalf("reference at byte %d escaped the stream check", position)
			}
		}
		if err := scanExternalAddresses(fragmentAddressReader{reader: bytes.NewReader(content), limit: 3}); err == nil {
			t.Fatal("reference split into short reads escaped the stream check")
		}
	}
	if err := scanExternalAddresses(fragmentAddressReader{reader: strings.NewReader("f 12//3 13//3 14//3\n"), limit: 3}); err != nil {
		t.Fatalf("short reads changed a valid face index: %v", err)
	}
}
