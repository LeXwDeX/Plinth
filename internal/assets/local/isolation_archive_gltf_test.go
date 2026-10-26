package local

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestArchiveGLTFBufferImagesUseAllDeliveryBoundaries(t *testing.T) {
	plainPNG := inlineTestPNG(t, "synth")
	privatePNG := inlineTestCompressedPNG(t, strings.Repeat("https://provider.example/private?token=synth ", 64))
	if containsExternalAddress(privatePNG) {
		t.Fatal("compressed fixture must conceal its address from the outer byte scan")
	}
	t.Run("standalone_private_compressed_png", func(t *testing.T) {
		// Addresses inside a valid image's bytes never block it.
		inlineTestBoundary(t, privatePNG, "png", "image/png", "image", true)
	})
	for _, item := range []struct {
		name, mime string
		body       []byte
		index      int
		offset     int
		shared     bool
		accepted   bool
	}{
		{"private_compressed_png", "image/png", privatePNG, 0, 4, false, true},
		{"private_compressed_png_buffer2", "image/png", privatePNG, 2, 8, true, true},
		{"png_offset", "image/png", plainPNG, 0, 4, false, true},
		{"png_buffer2_shared_view", "image/png", plainPNG, 2, 8, true, true},
		{"compressed_public_metadata", "image/png", inlineTestCompressedPNG(t, strings.Repeat("synth ", 64)), 1, 4, true, true},
		{"noise_png_pixels", "image/png", rasterNoiseFixture(t, "png"), 2, 8, true, true},
		{"noise_jpeg_pixels", "image/jpeg", rasterNoiseFixture(t, "jpeg"), 1, 4, false, true},
	} {
		t.Run(item.name, func(t *testing.T) {
			doc, members := archiveGLTFTestImage(t, item.body, item.mime, item.index, item.offset, item.shared)
			archiveGLTFTestWrappers(t, []archiveGLTFTestModel{{"scene", doc}}, members, false, item.accepted)
		})
	}
	t.Run("buffer_members_before_model", func(t *testing.T) {
		doc, members := archiveGLTFTestImage(t, plainPNG, "image/png", 1, 4, true)
		archiveGLTFTestWrappers(t, []archiveGLTFTestModel{{"scene", doc}}, members, true, true)
	})
	t.Run("nested_model_relative_member", func(t *testing.T) {
		doc, members := archiveGLTFTestImage(t, plainPNG, "image/png", 1, 4, false)
		for index := range members {
			doc["buffers"].([]any)[index].(map[string]any)["uri"] = "../cache/" + members[index].name
			members[index].name = "cache/" + members[index].name
		}
		archiveGLTFTestWrappers(t, []archiveGLTFTestModel{{"models/scene", doc}}, members, false, true)
	})
	t.Run("same_buffer_shared_across_models", func(t *testing.T) {
		doc, members := archiveGLTFTestImage(t, plainPNG, "image/png", 0, 4, true)
		archiveGLTFTestWrappers(t, []archiveGLTFTestModel{{"first", doc}, {"second", doc}}, members, true, true)
	})
	t.Run("disjoint_images_across_models", func(t *testing.T) {
		body := append(make([]byte, 4), plainPNG...)
		body = append(body, make([]byte, 4)...)
		offset := len(body)
		body = append(body, plainPNG...)
		first := archiveGLTFTestImageView("shared.bin", len(body), 4, len(plainPNG), "image/png")
		second := archiveGLTFTestImageView("shared.bin", len(body), offset, len(plainPNG), "image/png")
		archiveGLTFTestWrappers(t, []archiveGLTFTestModel{{"first", first}, {"second", second}}, []archiveGLTFTestMember{{"shared.bin", body}}, false, true)
	})
	t.Run("typed_positions_keep_numeric_boundary", func(t *testing.T) {
		body := []byte(" //provider.example/private")
		for len(body) < 36 {
			body = append(body, 0)
		}
		doc := inlineTestPointDocument(t, body)
		members := archiveGLTFTestExternalBuffers(t, doc)
		archiveGLTFTestWrappers(t, []archiveGLTFTestModel{{"scene", doc}}, members, false, true)
	})
}

func TestArchiveGLTFBuffersRejectConflictingDeclarations(t *testing.T) {
	plainPNG := inlineTestPNG(t, "synth")
	jpeg := rasterNoiseFixture(t, "jpeg")
	for _, item := range []struct {
		name, mime string
		body       []byte
	}{
		{"png_mime_jpeg", "image/jpeg", plainPNG},
		{"jpeg_mime_png", "image/png", jpeg},
		{"png_mime_unknown_codec", "image/webp", plainPNG},
		{"jpeg_mime_unknown_codec", "image/webp", jpeg},
	} {
		t.Run(item.name, func(t *testing.T) {
			doc, members := archiveGLTFTestImage(t, item.body, item.mime, 1, 4, false)
			archiveGLTFTestWrappers(t, []archiveGLTFTestModel{{"scene", doc}}, members, false, false)
		})
	}
	for _, item := range []struct {
		name string
		edit func(map[string]any, []archiveGLTFTestMember) []archiveGLTFTestMember
	}{
		{"declared_length_short", func(doc map[string]any, members []archiveGLTFTestMember) []archiveGLTFTestMember {
			doc["buffers"].([]any)[1].(map[string]any)["byteLength"] = len(members[1].body) - 1
			return members
		}},
		{"declared_length_long", func(doc map[string]any, members []archiveGLTFTestMember) []archiveGLTFTestMember {
			doc["buffers"].([]any)[1].(map[string]any)["byteLength"] = len(members[1].body) + 1
			return members
		}},
		{"declared_length_exhausts_integer_range", func(doc map[string]any, members []archiveGLTFTestMember) []archiveGLTFTestMember {
			doc["buffers"].([]any)[1].(map[string]any)["byteLength"] = int64(1<<63 - 1)
			return members
		}},
		{"image_view_outside_buffer", func(doc map[string]any, members []archiveGLTFTestMember) []archiveGLTFTestMember {
			doc["bufferViews"].([]any)[0].(map[string]any)["byteOffset"] = len(members[1].body)
			return members
		}},
		{"image_buffer_index_outside_document", func(doc map[string]any, members []archiveGLTFTestMember) []archiveGLTFTestMember {
			doc["bufferViews"].([]any)[0].(map[string]any)["buffer"] = len(members)
			return members
		}},
		{"image_missing_mime", func(doc map[string]any, members []archiveGLTFTestMember) []archiveGLTFTestMember {
			delete(doc["images"].([]any)[0].(map[string]any), "mimeType")
			return members
		}},
		{"image_source_conflict", func(doc map[string]any, members []archiveGLTFTestMember) []archiveGLTFTestMember {
			doc["images"].([]any)[0].(map[string]any)["uri"] = inlineTestURI(plainPNG, "image/png", false)
			return members
		}},
		{"image_numeric_overlap", func(doc map[string]any, members []archiveGLTFTestMember) []archiveGLTFTestMember {
			doc["accessors"] = []any{map[string]any{"bufferView": 0, "componentType": 5121, "count": len(plainPNG), "type": "SCALAR"}}
			return members
		}},
		{"image_partial_overlap", func(doc map[string]any, members []archiveGLTFTestMember) []archiveGLTFTestMember {
			doc["bufferViews"] = append(doc["bufferViews"].([]any), map[string]any{"buffer": 1, "byteOffset": 5, "byteLength": len(plainPNG) - 1})
			doc["images"] = append(doc["images"].([]any), map[string]any{"bufferView": 1, "mimeType": "image/png"})
			return members
		}},
		{"missing_buffer_member", func(_ map[string]any, members []archiveGLTFTestMember) []archiveGLTFTestMember { return members[:1] }},
		{"duplicate_exact_member", func(_ map[string]any, members []archiveGLTFTestMember) []archiveGLTFTestMember {
			return append(members, members[1])
		}},
		{"duplicate_dot_member", func(_ map[string]any, members []archiveGLTFTestMember) []archiveGLTFTestMember {
			return append(members, archiveGLTFTestMember{"./" + members[1].name, members[1].body})
		}},
		{"duplicate_parent_normalized_member", func(_ map[string]any, members []archiveGLTFTestMember) []archiveGLTFTestMember {
			return append(members, archiveGLTFTestMember{"folder/../" + members[1].name, members[1].body})
		}},
		{"unknown_gap_private_address", func(doc map[string]any, members []archiveGLTFTestMember) []archiveGLTFTestMember {
			prefix := []byte(" //provider.example/private?token=synth")
			for len(prefix)%4 != 0 {
				prefix = append(prefix, 0)
			}
			members[1].body = append(prefix, plainPNG...)
			doc["buffers"].([]any)[1].(map[string]any)["byteLength"] = len(members[1].body)
			doc["bufferViews"].([]any)[0].(map[string]any)["byteOffset"] = len(prefix)
			return members
		}},
		{"unknown_tail_private_address", func(doc map[string]any, members []archiveGLTFTestMember) []archiveGLTFTestMember {
			members[1].body = append(members[1].body, []byte(" //provider.example/private?token=synth")...)
			doc["buffers"].([]any)[1].(map[string]any)["byteLength"] = len(members[1].body)
			return members
		}},
	} {
		t.Run(item.name, func(t *testing.T) {
			doc, members := archiveGLTFTestImage(t, plainPNG, "image/png", 1, 4, false)
			members = item.edit(doc, members)
			archiveGLTFTestWrappers(t, []archiveGLTFTestModel{{"scene", doc}}, members, false, false)
		})
	}
	t.Run("disjoint_numeric_prefix", func(t *testing.T) {
		doc, members := archiveGLTFTestImage(t, plainPNG, "image/png", 1, 4, false)
		doc["bufferViews"] = append(doc["bufferViews"].([]any), map[string]any{"buffer": 1, "byteOffset": 0, "byteLength": 4})
		doc["accessors"] = []any{map[string]any{"bufferView": 1, "componentType": 5125, "count": 1, "type": "SCALAR"}}
		archiveGLTFTestWrappers(t, []archiveGLTFTestModel{{"scene", doc}}, members, false, true)
	})
	t.Run("native_png_with_numeric_view_is_admitted_whole", func(t *testing.T) {
		privatePNG := inlineTestCompressedPNG(t, strings.Repeat("https://provider.example/private?token=synth ", 64))
		doc := archiveGLTFTestNumericView("preview.png", len(privatePNG), 0, len(privatePNG))
		archiveGLTFTestWrappers(t, []archiveGLTFTestModel{{"scene", doc}}, []archiveGLTFTestMember{{"preview.png", privatePNG}}, false, true)
	})
	t.Run("cross_model_image_numeric_conflict", func(t *testing.T) {
		doc, members := archiveGLTFTestImage(t, plainPNG, "image/png", 0, 4, false)
		numeric := archiveGLTFTestNumericView(members[0].name, len(members[0].body), 4, len(plainPNG))
		archiveGLTFTestWrappers(t, []archiveGLTFTestModel{{"image", doc}, {"numeric", numeric}}, members, true, false)
	})
	t.Run("cross_model_image_mime_conflict", func(t *testing.T) {
		doc, members := archiveGLTFTestImage(t, plainPNG, "image/png", 0, 4, false)
		conflict := archiveGLTFTestImageView(members[0].name, len(members[0].body), 4, len(plainPNG), "image/jpeg")
		archiveGLTFTestWrappers(t, []archiveGLTFTestModel{{"png", doc}, {"jpeg", conflict}}, members, false, false)
	})
}

func TestArchiveGLTFBufferLimitsAndFraming(t *testing.T) {
	plainPNG := inlineTestPNG(t, "synth")
	for _, item := range []struct {
		name string
		edit func(*testing.T, []byte) []byte
	}{
		{"buffer_crc_failure_at_eof", func(t *testing.T, body []byte) []byte { return archiveGLTFTestWrongCRC(t, body, "buffer-0.bin") }},
		{"truncated_footer", func(_ *testing.T, body []byte) []byte { return body[:len(body)-1] }},
		{"unframed_trailer", func(_ *testing.T, body []byte) []byte { return append(body, []byte("synth")...) }},
	} {
		t.Run(item.name, func(t *testing.T) {
			doc, members := archiveGLTFTestImage(t, plainPNG, "image/png", 0, 4, false)
			archiveGLTFTestEachWrapper(t, []archiveGLTFTestModel{{"scene", doc}}, members, false, func(t *testing.T, body []byte) {
				inlineTestBoundary(t, item.edit(t, body), "zip", "application/zip", "model", false)
			})
		})
	}
	t.Run("shared_views_within_global_budget", func(t *testing.T) {
		first := archiveGLTFTestImageView("shared.bin", len(plainPNG), 0, len(plainPNG), "image/png")
		second := archiveGLTFTestImageView("shared.bin", len(plainPNG), 0, len(plainPNG), "image/png")
		for _, doc := range []map[string]any{first, second} {
			images := make([]any, 128)
			for index := range images {
				images[index] = map[string]any{"bufferView": 0, "mimeType": "image/png"}
			}
			doc["images"] = images
		}
		archiveGLTFTestWrappers(t, []archiveGLTFTestModel{{"first", first}, {"second", second}}, []archiveGLTFTestMember{{"shared.bin", plainPNG}}, false, true)
	})
	t.Run("models_share_global_range_budget", func(t *testing.T) {
		first := archiveGLTFTestImageView("shared.bin", len(plainPNG), 0, len(plainPNG), "image/png")
		second := archiveGLTFTestImageView("shared.bin", len(plainPNG), 0, len(plainPNG), "image/png")
		for _, doc := range []map[string]any{first, second} {
			// Each model stays below the 10,000-field bound. Deduplicating
			// their shared views must not reset the archive's total budget.
			images := make([]any, 6000)
			for index := range images {
				images[index] = map[string]any{"bufferView": 0, "mimeType": "image/png"}
			}
			doc["images"] = images
		}
		archiveGLTFTestWrappers(t, []archiveGLTFTestModel{{"first", first}, {"second", second}}, []archiveGLTFTestMember{{"shared.bin", plainPNG}}, false, false)
	})
	t.Run("models_share_global_binding_budget", func(t *testing.T) {
		models := make([]archiveGLTFTestModel, 2)
		for modelIndex := range models {
			buffers := make([]any, 6000)
			for index := range buffers {
				buffers[index] = map[string]any{"uri": "shared.bin", "byteLength": 4}
			}
			models[modelIndex] = archiveGLTFTestModel{fmt.Sprintf("model-%d", modelIndex), map[string]any{
				"asset": map[string]any{"version": "2.0"}, "buffers": buffers,
			}}
		}
		archiveGLTFTestWrappers(t, models, []archiveGLTFTestMember{{"shared.bin", make([]byte, 4)}}, false, false)
	})
}

type archiveGLTFTestMember struct {
	name string
	body []byte
}

type archiveGLTFTestModel struct {
	name     string
	document map[string]any
}

func archiveGLTFTestImage(t *testing.T, body []byte, mime string, index, offset int, shared bool) (map[string]any, []archiveGLTFTestMember) {
	t.Helper()
	doc := inlineTestBufferImageDocument(body, mime, index, offset, shared)
	return doc, archiveGLTFTestExternalBuffers(t, doc)
}

func archiveGLTFTestExternalBuffers(t *testing.T, doc map[string]any) []archiveGLTFTestMember {
	t.Helper()
	buffers := doc["buffers"].([]any)
	members := make([]archiveGLTFTestMember, len(buffers))
	for index, value := range buffers {
		buffer := value.(map[string]any)
		_, encoded, found := strings.Cut(buffer["uri"].(string), ",")
		if !found {
			t.Fatal("fixture buffer must have a base64 data URI")
		}
		body, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil {
			t.Fatal("failed to decode fixture buffer")
		}
		name := fmt.Sprintf("buffer-%d.bin", index)
		buffer["uri"] = name
		members[index] = archiveGLTFTestMember{name, body}
	}
	return members
}

func archiveGLTFTestImageView(name string, total, offset, length int, mime string) map[string]any {
	return map[string]any{
		"asset":       map[string]any{"version": "2.0"},
		"buffers":     []any{map[string]any{"uri": name, "byteLength": total}},
		"bufferViews": []any{map[string]any{"buffer": 0, "byteOffset": offset, "byteLength": length}},
		"images":      []any{map[string]any{"bufferView": 0, "mimeType": mime}},
	}
}

func archiveGLTFTestNumericView(name string, total, offset, length int) map[string]any {
	return map[string]any{
		"asset":       map[string]any{"version": "2.0"},
		"buffers":     []any{map[string]any{"uri": name, "byteLength": total}},
		"bufferViews": []any{map[string]any{"buffer": 0, "byteOffset": offset, "byteLength": length}},
		"accessors":   []any{map[string]any{"bufferView": 0, "componentType": 5121, "count": length, "type": "SCALAR"}},
	}
}

func archiveGLTFTestWrappers(t *testing.T, models []archiveGLTFTestModel, members []archiveGLTFTestMember, membersFirst, accepted bool) {
	t.Helper()
	archiveGLTFTestEachWrapper(t, models, members, membersFirst, func(t *testing.T, body []byte) {
		inlineTestBoundary(t, body, "zip", "application/zip", "model", accepted)
	})
}

func archiveGLTFTestEachWrapper(t *testing.T, models []archiveGLTFTestModel, members []archiveGLTFTestMember, membersFirst bool, check func(*testing.T, []byte)) {
	t.Helper()
	for _, wrapper := range []struct {
		name   string
		method uint16
		glb    bool
	}{
		{"gltf_stored", zip.Store, false},
		{"gltf_deflate", zip.Deflate, false},
		{"glb_stored", zip.Store, true},
		{"glb_deflate", zip.Deflate, true},
	} {
		t.Run(wrapper.name, func(t *testing.T) {
			entries := make([]archiveGLTFTestMember, 0, len(models)+len(members))
			if membersFirst {
				entries = append(entries, members...)
			}
			for _, model := range models {
				body := inlineTestJSON(t, model.document)
				name := model.name + ".gltf"
				if wrapper.glb {
					body, name = inlineTestGLB(body), model.name+".glb"
				}
				entries = append(entries, archiveGLTFTestMember{name, body})
			}
			if !membersFirst {
				entries = append(entries, members...)
			}
			check(t, archiveGLTFTestZIP(t, entries, wrapper.method))
		})
	}
}

func archiveGLTFTestZIP(t *testing.T, members []archiveGLTFTestMember, method uint16) []byte {
	t.Helper()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for _, member := range members {
		entry, err := archive.CreateHeader(&zip.FileHeader{Name: member.name, Method: method})
		if err != nil {
			t.Fatal("failed to create archive member fixture")
		}
		if _, err := entry.Write(member.body); err != nil {
			t.Fatal("failed to write archive member fixture")
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal("failed to finish archive fixture")
	}
	return output.Bytes()
}

func archiveGLTFTestWrongCRC(t *testing.T, body []byte, name string) []byte {
	t.Helper()
	result := append([]byte(nil), body...)
	footer := bytes.LastIndex(result, []byte("PK\x05\x06"))
	if footer < 0 || footer+22 > len(result) {
		t.Fatal("ZIP fixture must have a complete footer")
	}
	position := int(binary.LittleEndian.Uint32(result[footer+16 : footer+20]))
	for position+46 <= footer && bytes.Equal(result[position:position+4], []byte("PK\x01\x02")) {
		names := int(binary.LittleEndian.Uint16(result[position+28 : position+30]))
		extra := int(binary.LittleEndian.Uint16(result[position+30 : position+32]))
		comments := int(binary.LittleEndian.Uint16(result[position+32 : position+34]))
		if position+46+names+extra+comments > footer {
			t.Fatal("ZIP fixture must have a complete central entry")
		}
		if string(result[position+46:position+46+names]) == name {
			crc := binary.LittleEndian.Uint32(result[position+16:position+20]) ^ 1
			binary.LittleEndian.PutUint32(result[position+16:position+20], crc)
			local := int(binary.LittleEndian.Uint32(result[position+42 : position+46]))
			if local+30 > position {
				t.Fatal("ZIP fixture local header must be complete")
			}
			flags := binary.LittleEndian.Uint16(result[local+6 : local+8])
			if flags&8 != 0 {
				start := local + 30 + int(binary.LittleEndian.Uint16(result[local+26:local+28])) + int(binary.LittleEndian.Uint16(result[local+28:local+30]))
				descriptor := start + int(binary.LittleEndian.Uint32(result[position+20:position+24]))
				if descriptor+4 <= position && bytes.Equal(result[descriptor:descriptor+4], []byte("PK\x07\x08")) {
					descriptor += 4
				}
				if descriptor+12 > position {
					t.Fatal("ZIP fixture descriptor must be complete")
				}
				binary.LittleEndian.PutUint32(result[descriptor:descriptor+4], crc)
			} else {
				binary.LittleEndian.PutUint32(result[local+14:local+18], crc)
			}
			// Keep all declared CRC values consistent. Only reading the member
			// through EOF reveals its mismatch with the unchanged payload.
			archive, err := zip.NewReader(bytes.NewReader(result), int64(len(result)))
			if err != nil {
				t.Fatal("CRC fixture must still parse as ZIP")
			}
			for _, member := range archive.File {
				if member.Name != name {
					continue
				}
				reader, err := member.Open()
				if err != nil {
					t.Fatal("CRC fixture must open before reading its payload")
				}
				_, readErr := io.Copy(io.Discard, reader)
				_ = reader.Close()
				if !errors.Is(readErr, zip.ErrChecksum) {
					t.Fatal("CRC fixture must fail with the actual checksum mismatch")
				}
				return result
			}
			t.Fatal("CRC fixture member is missing")
		}
		position += 46 + names + extra + comments
	}
	t.Fatal("CRC fixture member is missing")
	return nil
}
