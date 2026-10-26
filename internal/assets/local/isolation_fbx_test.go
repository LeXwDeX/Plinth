package local

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"testing"
)

// The fixtures are independent wire writers, not copies of the importer.
// Header, node widths, array parameters and footer layout were checked against
// Blender's official FBX encoder/parser. No real provider bytes are fixtures.
// https://github.com/blender/blender/blob/main/scripts/addons_core/io_scene_fbx/encode_bin.py
// https://github.com/blender/blender/blob/main/scripts/addons_core/io_scene_fbx/parse_fbx.py

func TestFBXBinaryNumericTypesUseAllDeliveryBoundaries(t *testing.T) {
	t.Parallel()
	for _, version := range []uint32{7400, 7500} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			properties := [][]byte{
				fbxTestScalar('Z', []byte{0xf9}), fbxTestScalar('B', []byte{1}), fbxTestScalar('C', []byte{'Q'}),
				fbxTestScalar('Y', fbxTestNumber(2, 0xfffe)), fbxTestScalar('I', fbxTestNumber(4, 0xfffffff4)),
				fbxTestScalar('L', fbxTestNumber(8, 42)), fbxTestScalar('F', fbxTestNumber(4, uint64(math.Float32bits(1.25)))),
				// The low word is a float32 NaN, but the complete double is finite.
				fbxTestScalar('D', fbxTestNumber(8, 0x3ff000007fc00000)),
				fbxTestBytes('S', []byte("synthetic metadata")), fbxTestBytes('R', []byte{0, 1, 2, 3}),
			}
			t.Run("standard_scalars", func(t *testing.T) {
				fbxTestWrappers(t, fbxTestFile(t, version, fbxTestNode{name: []byte("Properties"), properties: properties}), true)
			})
			for _, kind := range []byte{'i', 'l', 'f', 'd', 'c', 'b'} {
				width := fbxTestArrayWidth(kind)
				data := fbxTestProtocolNumbers(t, kind)
				for _, mode := range []struct {
					name  string
					level int
				}{
					{"raw", -2}, {"zlib_stored", zlib.NoCompression}, {"zlib_deflate", zlib.BestCompression},
				} {
					t.Run(fmt.Sprintf("%c_%s", kind, mode.name), func(t *testing.T) {
						property := fbxTestArray(t, kind, data, mode.level)
						if kind != 'b' && mode.level == zlib.NoCompression && !containsExternalAddress(property[13:]) {
							t.Fatal("stored zlib fixture must retain the coincidental protocol-relative bytes")
						}
						if len(data)%width != 0 {
							t.Fatal("numeric fixture must contain complete components")
						}
						fbxTestWrappers(t, fbxTestFile(t, version, fbxTestNode{name: []byte("Values"), properties: [][]byte{property}}), true)
					})
				}
				t.Run(fmt.Sprintf("%c_empty_array", kind), func(t *testing.T) {
					property := fbxTestArray(t, kind, nil, -2)
					fbxTestWrappers(t, fbxTestFile(t, version, fbxTestNode{name: []byte("Values"), properties: [][]byte{property}}), true)
				})
			}
			t.Run("nested_geometry", func(t *testing.T) {
				leaf := fbxTestNode{name: []byte("PolygonVertexIndex"), properties: [][]byte{fbxTestArray(t, 'i', fbxTestProtocolNumbers(t, 'i'), zlib.BestCompression)}}
				geometry := fbxTestNode{name: []byte("Geometry"), properties: [][]byte{fbxTestScalar('L', fbxTestNumber(8, 17))}, children: []fbxTestNode{leaf}}
				fbxTestWrappers(t, fbxTestFile(t, version, fbxTestNode{name: []byte("Objects"), children: []fbxTestNode{geometry}}), true)
			})
			t.Run("empty_child_scope", func(t *testing.T) {
				fbxTestWrappers(t, fbxTestFile(t, version, fbxTestNode{name: []byte("AnimationLayer"), properties: [][]byte{fbxTestScalar('I', fbxTestNumber(4, 1))}, children: []fbxTestNode{}}), true)
			})
			t.Run("variable_footer_identifier", func(t *testing.T) {
				body := fbxTestFile(t, version, fbxTestNode{name: []byte("Values"), properties: [][]byte{fbxTestScalar('I', fbxTestNumber(4, 1))}})
				footer := fbxTestNodeEnd(body, version, 27) + fbxTestHeaderBytes(version)
				body[footer] ^= 1 // Timestamp-derived exporter identifiers vary.
				fbxTestWrappers(t, body, true)
			})
		})
	}
}

func TestFBXAbsoluteAddressesRemainPrivateInNumericArrays(t *testing.T) {
	t.Parallel()
	for _, version := range []uint32{7400, 7500} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			for _, kind := range []byte{'i', 'l', 'f', 'd'} {
				data := []byte(strings.Repeat("https://provider.example/private?token=synth ", 16))
				for len(data)%fbxTestArrayWidth(kind) != 0 {
					data = append(data, 0)
				}
				fbxTestFinite(t, kind, data)
				for _, mode := range []struct {
					name  string
					level int
				}{
					{"raw", -2}, {"zlib_wire_literal", zlib.NoCompression}, {"zlib_decoded_private", zlib.BestCompression},
				} {
					t.Run(fmt.Sprintf("%c_%s", kind, mode.name), func(t *testing.T) {
						property := fbxTestArray(t, kind, data, mode.level)
						if mode.level == zlib.BestCompression && absoluteAddress.Match(property[13:]) {
							t.Fatal("compressed fixture must conceal the absolute address before decoding")
						}
						fbxTestWrappers(t, fbxTestFile(t, version, fbxTestNode{name: []byte("Values"), properties: [][]byte{property}}), false)
					})
				}
			}
		})
	}
}

func TestFBXMetadataStartsAtItsOwnBodyBoundary(t *testing.T) {
	t.Parallel()
	for _, version := range []uint32{7400, 7500} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			for _, item := range []struct {
				name string
				data []byte
			}{
				{"raw", []byte("//provider.example/private?token=synth")},
				{"absolute", []byte("https://provider.example/private?token=synth")},
				{"slash_escaped", []byte(`\/\/provider.example/private?token=synth`)},
				{"unicode_escaped", []byte(`\u002f\u002fprovider.example/private?token=synth`)},
				{"percent_encoded", []byte(`%2F%2Fprovider.example%2Fprivate%3Ftoken%3Dsynth`)},
				{"html_encoded", []byte(`&#47;&#47;provider.example/private?token=synth`)},
				{"utf16_le", inlineTestUTF16([]byte("//provider.example/private?token=synth"), true)},
				{"utf16_be", inlineTestUTF16([]byte("//provider.example/private?token=synth"), false)},
			} {
				for _, origin := range []string{"name", "string", "unknown_raw"} {
					t.Run(origin+"_"+item.name, func(t *testing.T) {
						node := fbxTestNode{name: []byte("Metadata")}
						data := append([]byte(nil), item.data...)
						if origin == "name" {
							// A length byte of 97 ('a') is not a URI boundary in
							// an outer text scan. It must not hide this body's start.
							for len(data) < 97 {
								data = append(data, ' ')
							}
							node.name = data
						} else {
							kind := byte('S')
							if origin == "unknown_raw" {
								kind = 'R'
							}
							node.properties = [][]byte{fbxTestBytes(kind, data)}
						}
						fbxTestWrappers(t, fbxTestFile(t, version, node), false)
					})
				}
			}
			t.Run("footer_identifier_body", func(t *testing.T) {
				body := fbxTestFile(t, version, fbxTestNode{name: []byte("Values"), properties: [][]byte{fbxTestScalar('I', fbxTestNumber(4, 1))}})
				footer := fbxTestNodeEnd(body, version, 27) + fbxTestHeaderBytes(version)
				copy(body[footer:footer+16], append([]byte("//a.example/x"), make([]byte, 3)...))
				fbxTestWrappers(t, body, false)
			})
		})
	}
}

func TestFBXEmbeddedRasterUsesItsCompleteContentBoundary(t *testing.T) {
	t.Parallel()
	privatePNG := inlineTestCompressedPNG(t, strings.Repeat("https://provider.example/private?token=synth ", 64))
	if containsExternalAddress(privatePNG) {
		t.Fatal("compressed PNG fixture must conceal the private metadata from the outer scan")
	}
	jpeg := rasterNoiseFixture(t, "jpeg")
	privateJPEG := rasterJPEGMetadata(jpeg, 0xfe, []byte("//provider.example/private?token=synth"))
	for _, item := range []struct {
		name, variant, mime string
		body                []byte
		accepted            bool
	}{
		{"noise_png", "png", "image/png", rasterNoiseFixture(t, "png"), true},
		{"noise_jpeg", "jpeg", "image/jpeg", jpeg, true},
		{"public_compressed_png_metadata", "png", "image/png", inlineTestCompressedPNG(t, "synth metadata"), true},
		{"private_compressed_png_metadata", "png", "image/png", privatePNG, true},
		{"private_jpeg_metadata", "jpeg", "image/jpeg", privateJPEG, true},
		{"truncated_png", "png", "image/png", privatePNG[:len(privatePNG)-1], false},
		{"png_with_unframed_tail", "png", "image/png", append(inlineTestPNG(t, "synth"), 'x'), false},
		// Alignment padding after IEND is tolerated (#330).
		{"png_with_alignment_padding", "png", "image/png", append(inlineTestPNG(t, "synth"), ' '), true},
	} {
		t.Run(item.name, func(t *testing.T) {
			t.Run("standalone_image", func(t *testing.T) {
				inlineTestBoundary(t, item.body, item.variant, item.mime, "image", item.accepted)
			})
			for _, version := range []uint32{7400, 7500} {
				t.Run(fmt.Sprint(version), func(t *testing.T) {
					body := fbxTestFile(t, version, fbxTestNode{name: []byte("Content"), properties: [][]byte{fbxTestBytes('R', item.body)}})
					fbxTestWrappers(t, body, item.accepted)
				})
			}
		})
	}
	t.Run("native_fbx_with_numeric_view_admits_embedded_image_whole", func(t *testing.T) {
		body := fbxTestFile(t, 7400, fbxTestNode{name: []byte("Content"), properties: [][]byte{fbxTestBytes('R', privatePNG)}})
		doc := archiveGLTFTestNumericView("private.fbx", len(body), 0, len(body))
		archiveGLTFTestWrappers(t, []archiveGLTFTestModel{{"scene", doc}}, []archiveGLTFTestMember{{"private.fbx", body}}, false, true)
	})
}

func TestFBXInvalidWireFramingCannotBeDelivered(t *testing.T) {
	t.Parallel()
	for _, version := range []uint32{7400, 7500} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			for _, item := range []struct {
				name string
				edit func([]byte) []byte
			}{
				{"header_magic_corrupt", func(body []byte) []byte { body[22] = 1; return body }},
				{"header_version_zero", func(body []byte) []byte { binary.LittleEndian.PutUint32(body[23:27], 0); return body }},
				{"header_version_unknown", func(body []byte) []byte { binary.LittleEndian.PutUint32(body[23:27], math.MaxUint32); return body }},
				{"node_end_zero_with_nonzero_record", func(body []byte) []byte { fbxTestSetNodeWord(body, version, 27, 0, 0); return body }},
				{"node_end_before_header", func(body []byte) []byte { fbxTestSetNodeWord(body, version, 27, 0, 28); return body }},
				{"node_end_outside_file", func(body []byte) []byte { fbxTestSetNodeWord(body, version, 27, 0, uint64(len(body))+1); return body }},
				{"property_count_short", func(body []byte) []byte { fbxTestSetNodeWord(body, version, 27, 1, 0); return body }},
				{"property_count_long", func(body []byte) []byte { fbxTestSetNodeWord(body, version, 27, 1, 2); return body }},
				{"property_count_oversized", func(body []byte) []byte { fbxTestSetNodeWord(body, version, 27, 1, math.MaxUint64); return body }},
				{"property_list_short", func(body []byte) []byte { fbxTestSetNodeWord(body, version, 27, 2, 4); return body }},
				{"property_list_long", func(body []byte) []byte { fbxTestSetNodeWord(body, version, 27, 2, 6); return body }},
				{"property_list_oversized", func(body []byte) []byte { fbxTestSetNodeWord(body, version, 27, 2, math.MaxUint64); return body }},
				{"name_exceeds_node", func(body []byte) []byte { body[27+fbxTestHeaderBytes(version)-1] = 255; return body }},
				{"root_sentinel_nonzero", func(body []byte) []byte { body[fbxTestNodeEnd(body, version, 27)+4] = 1; return body }},
				{"footer_zero_prefix_corrupt", func(body []byte) []byte {
					body[fbxTestNodeEnd(body, version, 27)+fbxTestHeaderBytes(version)+16] = 1
					return body
				}},
				{"footer_padding_nonzero", func(body []byte) []byte {
					body[fbxTestNodeEnd(body, version, 27)+fbxTestHeaderBytes(version)+20] = 1
					return body
				}},
				{"footer_version_mismatch", func(body []byte) []byte {
					binary.LittleEndian.PutUint32(body[len(body)-140:len(body)-136], version+100)
					return body
				}},
				{"footer_reserved_bytes_nonzero", func(body []byte) []byte { body[len(body)-20] = 1; return body }},
				{"footer_final_magic_corrupt", func(body []byte) []byte { body[len(body)-1] ^= 1; return body }},
				{"footer_truncated", func(body []byte) []byte { return body[:len(body)-1] }},
				{"unframed_trailer", func(body []byte) []byte { return append(body, 0) }},
			} {
				t.Run(item.name, func(t *testing.T) {
					body := fbxTestFile(t, version, fbxTestNode{name: []byte("Values"), properties: [][]byte{fbxTestScalar('I', fbxTestNumber(4, 1))}})
					fbxTestWrappers(t, item.edit(body), false)
				})
			}
			t.Run("nested_sentinel_nonzero", func(t *testing.T) {
				body := fbxTestFile(t, version, fbxTestNode{name: []byte("Objects"), children: []fbxTestNode{{name: []byte("Value"), properties: [][]byte{fbxTestScalar('I', fbxTestNumber(4, 1))}}}})
				body[fbxTestNodeEnd(body, version, 27)-fbxTestHeaderBytes(version)] = 1
				fbxTestWrappers(t, body, false)
			})
			t.Run("unknown_property_type", func(t *testing.T) {
				fbxTestWrappers(t, fbxTestFile(t, version, fbxTestNode{name: []byte("Values"), properties: [][]byte{{'X', 0}}}), false)
			})
			t.Run("noncanonical_boolean", func(t *testing.T) {
				fbxTestWrappers(t, fbxTestFile(t, version, fbxTestNode{name: []byte("Values"), properties: [][]byte{fbxTestScalar('B', []byte{2})}}), false)
			})
		})
	}
}

func TestFBXArraysRejectInvalidContentAndCompression(t *testing.T) {
	t.Parallel()
	for _, version := range []uint32{7400, 7500} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			for _, item := range []struct {
				name string
				edit func([]byte) []byte
			}{
				{"count_length_mismatch", func(prop []byte) []byte { binary.LittleEndian.PutUint32(prop[1:5], 99); return prop }},
				{"decoded_count_oversized", func(prop []byte) []byte { binary.LittleEndian.PutUint32(prop[1:5], math.MaxUint32); return prop }},
				{"unknown_encoding", func(prop []byte) []byte { binary.LittleEndian.PutUint32(prop[5:9], 2); return prop }},
				{"payload_length_oversized", func(prop []byte) []byte { binary.LittleEndian.PutUint32(prop[9:13], math.MaxUint32); return prop }},
				{"zlib_bad_checksum", func(prop []byte) []byte { prop[len(prop)-1] ^= 1; return prop }},
				{"zlib_truncated", func(prop []byte) []byte { return fbxTestResizeArrayPayload(prop[:len(prop)-1]) }},
				{"zlib_trailing_byte", func(prop []byte) []byte { return fbxTestResizeArrayPayload(append(prop, 0)) }},
				{"zlib_concatenated_stream", func(prop []byte) []byte {
					return fbxTestResizeArrayPayload(append(prop, fbxTestZlib(t, make([]byte, 4), zlib.BestCompression)...))
				}},
				{"zlib_expanded_short", func(prop []byte) []byte { binary.LittleEndian.PutUint32(prop[1:5], 1); return prop }},
			} {
				t.Run(item.name, func(t *testing.T) {
					prop := fbxTestArray(t, 'i', make([]byte, 8), zlib.BestCompression)
					fbxTestWrappers(t, fbxTestFile(t, version, fbxTestNode{name: []byte("Values"), properties: [][]byte{item.edit(prop)}}), false)
				})
			}
			t.Run("raw_declared_length_short", func(t *testing.T) {
				prop := fbxTestArray(t, 'i', make([]byte, 8), -2)
				binary.LittleEndian.PutUint32(prop[9:13], 4)
				fbxTestWrappers(t, fbxTestFile(t, version, fbxTestNode{name: []byte("Values"), properties: [][]byte{prop}}), false)
			})
			t.Run("noncanonical_boolean_array", func(t *testing.T) {
				fbxTestWrappers(t, fbxTestFile(t, version, fbxTestNode{name: []byte("Values"), properties: [][]byte{fbxTestArray(t, 'b', []byte{0, 2}, zlib.BestCompression)}}), false)
			})
			for _, kind := range []byte{'f', 'd'} {
				for _, value := range []struct {
					name string
					f32  uint32
					f64  uint64
				}{
					{"nan", math.Float32bits(float32(math.NaN())), math.Float64bits(math.NaN())},
					{"positive_infinity", math.Float32bits(float32(math.Inf(1))), math.Float64bits(math.Inf(1))},
					{"negative_infinity", math.Float32bits(float32(math.Inf(-1))), math.Float64bits(math.Inf(-1))},
				} {
					for _, level := range []int{-2, zlib.BestCompression} {
						t.Run(fmt.Sprintf("%c_%s_%d", kind, value.name, level), func(t *testing.T) {
							bits := value.f64
							if kind == 'f' {
								bits = uint64(value.f32)
							}
							data := fbxTestNumber(fbxTestArrayWidth(kind), bits)
							fbxTestWrappers(t, fbxTestFile(t, version, fbxTestNode{name: []byte("Values"), properties: [][]byte{fbxTestArray(t, kind, data, level)}}), false)
						})
					}
				}
			}
		})
	}
}

func TestFBXAggregateStructureLimitsCannotResetPerNode(t *testing.T) {
	t.Parallel()
	t.Run("excessive_depth", func(t *testing.T) {
		root := fbxTestNode{name: []byte("Leaf"), properties: [][]byte{fbxTestScalar('I', fbxTestNumber(4, 1))}}
		for range 65 {
			root = fbxTestNode{name: []byte("Scope"), children: []fbxTestNode{root}}
		}
		fbxTestWrappers(t, fbxTestFile(t, 7400, root), false)
	})
	t.Run("excessive_nodes", func(t *testing.T) {
		nodes := make([]fbxTestNode, 100001)
		for index := range nodes {
			nodes[index] = fbxTestNode{name: []byte("N"), children: []fbxTestNode{}}
		}
		// Real empty scopes keep property work below its separate bound.
		// Exercise this costly node-count limit once at each delivery entry;
		// the other framing cases cover Stored and Deflate archive wrappers.
		t.Run("fbx", func(t *testing.T) {
			inlineTestBoundary(t, fbxTestFile(t, 7500, nodes...), "fbx", "application/octet-stream", "model", false)
		})
	})
	t.Run("aggregate_properties", func(t *testing.T) {
		properties := make([][]byte, 100000)
		for index := range properties {
			properties[index] = fbxTestScalar('C', []byte{0})
		}
		fbxTestWrappers(t, fbxTestFile(t, 7400,
			fbxTestNode{name: []byte("First"), properties: properties[:1]},
			fbxTestNode{name: []byte("Second"), properties: properties}), false)
	})
}

func TestFBXScalarFloatKindsKeepIndependentWordWidths(t *testing.T) {
	t.Parallel()
	for _, version := range []uint32{7400, 7500} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			t.Run("mixed_finite_words", func(t *testing.T) {
				properties := [][]byte{
					fbxTestScalar('F', fbxTestNumber(4, uint64(math.Float32bits(1.25)))),
					fbxTestScalar('D', fbxTestNumber(8, 0x3ff000007fc00000)),
					fbxTestScalar('F', fbxTestNumber(4, uint64(math.Float32bits(-2.5)))),
					fbxTestScalar('D', fbxTestNumber(8, math.Float64bits(-3.75))),
				}
				fbxTestWrappers(t, fbxTestFile(t, version, fbxTestNode{name: []byte("Mixed"), properties: properties}), true)
			})
			for _, kind := range []byte{'F', 'D'} {
				for _, value := range []struct {
					name string
					bits uint64
				}{
					{"nan", 0x7ff8000000000000}, {"positive_inf", 0x7ff0000000000000}, {"negative_inf", 0xfff0000000000000},
				} {
					t.Run(fmt.Sprintf("%c_%s", kind, value.name), func(t *testing.T) {
						width, bits := 8, value.bits
						if kind == 'F' {
							width, bits = 4, uint64(math.Float32bits(float32(math.Float64frombits(bits))))
						}
						// Surround the invalid scalar with the other float kind.
						// A batched byte scan must not share one finite-word width.
						properties := [][]byte{
							fbxTestScalar('D', fbxTestNumber(8, 0x3ff000007fc00000)),
							fbxTestScalar(kind, fbxTestNumber(width, bits)),
							fbxTestScalar('F', fbxTestNumber(4, uint64(math.Float32bits(1.25)))),
						}
						fbxTestWrappers(t, fbxTestFile(t, version, fbxTestNode{name: []byte("Mixed"), properties: properties}), false)
					})
				}
			}
		})
	}
}

func TestFBXAbsoluteNumericBytesCrossEveryStreamWindowSplit(t *testing.T) {
	t.Parallel()
	address := []byte("https://provider.example/private?token=synth")
	for _, version := range []uint32{7400, 7500} {
		for _, kind := range []byte{'f', 'd'} {
			for _, level := range []int{-2, zlib.BestCompression} {
				for split := 1; split < len("https://"); split++ {
					t.Run(fmt.Sprintf("%d/%c/level_%d/split_%d", version, kind, level, split), func(t *testing.T) {
						width := fbxTestArrayWidth(kind)
						data := make([]byte, 2*(64<<10)+2*width)
						position := (64 << 10) - split
						if level == -2 {
							// Align the literal scheme to the physical wire stream;
							// compressed arrays instead cross the decoded window.
							position -= 27 + fbxTestHeaderBytes(version) + len("Values") + 13
						}
						copy(data[position:], address)
						fbxTestFinite(t, kind, data)
						property := fbxTestArray(t, kind, data, level)
						if level != -2 && absoluteAddress.Match(property[13:]) {
							t.Fatal("compressed case must contain the absolute address only after decoding")
						}
						fbxTestWrappers(t, fbxTestFile(t, version, fbxTestNode{name: []byte("Values"), properties: [][]byte{property}}), false)
					})
				}
			}
		}
	}
}

func TestFBXAbsoluteDeliveredPrefixCrossesTinyWireFragments(t *testing.T) {
	t.Parallel()
	for _, version := range []uint32{7400, 7500} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			// This valid S length has on-wire bytes ':', '/', '/', zero.
			// Together with name "http" and property type S it forms the
			// absolute prefix across three separately parsed tiny fragments.
			data := make([]byte, 0x002f2f3a)
			copy(data, "provider.example/private?token=synth")
			property := fbxTestBytes('S', data)
			if absoluteAddress.Match(data) || absoluteAddress.Match([]byte("http")) || absoluteAddress.Match(property) {
				t.Fatal("individual wire fragments must not already contain the absolute prefix")
			}
			body := fbxTestFile(t, version, fbxTestNode{name: []byte("http"), properties: [][]byte{property}})
			if !absoluteAddress.Match(body) {
				t.Fatal("delivered bytes must contain the absolute prefix across fragments")
			}
			fbxTestWrappers(t, body, false)
		})
	}
}

func TestFBXEmbeddedPublicMetadataUsesExactSpansAcrossWindows(t *testing.T) {
	t.Parallel()
	known := c2paFixturePNG(t, "cbor", c2paFixtureString(3, []byte(c2paVocabularyFixture)))
	uriOffset := bytes.Index(known, []byte(c2paVocabularyFixture))
	if uriOffset < 0 {
		t.Fatal("public metadata fixture must retain its complete vocabulary URI")
	}
	for _, version := range []uint32{7400, 7500} {
		for split := 1; split < len("https://"); split++ {
			t.Run(fmt.Sprintf("%d/public_split_%d", version, split), func(t *testing.T) {
				// The S property pads the wire only. Its harmless body and the
				// complete public C2PA text are independent metadata fields.
				padding := (64 << 10) - split - (27 + fbxTestHeaderBytes(version) + len("Texture") + 5 + 5 + uriOffset)
				properties := [][]byte{
					fbxTestBytes('S', bytes.Repeat([]byte{'x'}, padding)), fbxTestBytes('R', known),
				}
				body := fbxTestFile(t, version, fbxTestNode{name: []byte("Texture"), properties: properties})
				if bytes.Index(body, []byte(c2paVocabularyFixture)) != (64<<10)-split {
					t.Fatal("public URI must cross the intended physical window boundary")
				}
				fbxTestWrappers(t, body, true)
			})
		}
		t.Run(fmt.Sprintf("%d/private_neighbor_is_cached_whole", version), func(t *testing.T) {
			content := append([]byte{0x82}, c2paFixtureString(3, []byte(c2paVocabularyFixture))...)
			content = append(content, c2paFixtureString(3, []byte("https://provider.example/private?token=synth"))...)
			png := c2paFixturePNG(t, "cbor", content)
			// A structurally valid embedded image is admitted whole, metadata
			// included (Lex, 2026-10-09).
			fbxTestWrappers(t, fbxTestFile(t, version, fbxTestNode{name: []byte("Texture"), properties: [][]byte{fbxTestBytes('R', png)}}), true)
		})
	}
}

type fbxTestNode struct {
	name       []byte
	properties [][]byte
	children   []fbxTestNode // A non-nil empty slice encodes an empty child scope.
}

func fbxTestFile(t *testing.T, version uint32, nodes ...fbxTestNode) []byte {
	t.Helper()
	var body bytes.Buffer
	body.WriteString("Kaydara FBX Binary  \x00\x1a\x00")
	body.Write(fbxTestNumber(4, uint64(version)))
	for _, node := range nodes {
		fbxTestWriteNode(t, &body, version, node)
	}
	body.Write(make([]byte, fbxTestHeaderBytes(version)))
	body.Write([]byte{0xfa, 0xbc, 0xab, 0x09, 0xd0, 0xc8, 0xd4, 0x66, 0xb1, 0x76, 0xfb, 0x83, 0x1c, 0xf7, 0x26, 0x7e})
	body.Write(make([]byte, 4))
	body.Write(make([]byte, 16-body.Len()%16))
	body.Write(fbxTestNumber(4, uint64(version)))
	body.Write(make([]byte, 120))
	body.Write([]byte{0xf8, 0x5a, 0x8c, 0x6a, 0xde, 0xf5, 0xd9, 0x7e, 0xec, 0xe9, 0x0c, 0xe3, 0x75, 0x8f, 0x29, 0x0b})
	return body.Bytes()
}

func fbxTestWriteNode(t *testing.T, body *bytes.Buffer, version uint32, node fbxTestNode) {
	t.Helper()
	if len(node.name) > 255 {
		t.Fatal("FBX fixture node name exceeds its one-byte length")
	}
	start := body.Len()
	header := fbxTestHeaderBytes(version)
	body.Write(make([]byte, header))
	body.Bytes()[start+header-1] = byte(len(node.name))
	body.Write(node.name)
	propertyBytes := 0
	for _, property := range node.properties {
		body.Write(property)
		propertyBytes += len(property)
	}
	for _, child := range node.children {
		fbxTestWriteNode(t, body, version, child)
	}
	if node.children != nil {
		body.Write(make([]byte, header))
	}
	for index, value := range []uint64{uint64(body.Len()), uint64(len(node.properties)), uint64(propertyBytes)} {
		fbxTestSetNodeWord(body.Bytes(), version, start, index, value)
	}
}

func fbxTestHeaderBytes(version uint32) int {
	if version >= 7500 {
		return 25
	}
	return 13
}

func fbxTestSetNodeWord(body []byte, version uint32, start, index int, value uint64) {
	if version >= 7500 {
		binary.LittleEndian.PutUint64(body[start+index*8:start+(index+1)*8], value)
	} else {
		binary.LittleEndian.PutUint32(body[start+index*4:start+(index+1)*4], uint32(value))
	}
}

func fbxTestNodeEnd(body []byte, version uint32, start int) int {
	if version >= 7500 {
		return int(binary.LittleEndian.Uint64(body[start : start+8]))
	}
	return int(binary.LittleEndian.Uint32(body[start : start+4]))
}

func fbxTestNumber(width int, value uint64) []byte {
	data := make([]byte, width)
	for index := range data {
		data[index] = byte(value >> (8 * index))
	}
	return data
}

func fbxTestScalar(kind byte, data []byte) []byte {
	return append([]byte{kind}, data...)
}

func fbxTestBytes(kind byte, data []byte) []byte {
	property := append([]byte{kind}, fbxTestNumber(4, uint64(len(data)))...)
	return append(property, data...)
}

func fbxTestArrayWidth(kind byte) int {
	switch kind {
	case 'd', 'l':
		return 8
	case 'f', 'i':
		return 4
	default:
		return 1
	}
}

func fbxTestProtocolNumbers(t *testing.T, kind byte) []byte {
	t.Helper()
	if kind == 'b' {
		return []byte{0, 1, 0, 1}
	}
	data := []byte(" //provider.example/private?token=synth")
	for len(data)%fbxTestArrayWidth(kind) != 0 {
		data = append(data, 0)
	}
	fbxTestFinite(t, kind, data)
	return data
}

func fbxTestFinite(t *testing.T, kind byte, data []byte) {
	t.Helper()
	if kind != 'f' && kind != 'd' {
		return
	}
	for offset := 0; offset < len(data); offset += fbxTestArrayWidth(kind) {
		var value float64
		if kind == 'f' {
			value = float64(math.Float32frombits(binary.LittleEndian.Uint32(data[offset : offset+4])))
		} else {
			value = math.Float64frombits(binary.LittleEndian.Uint64(data[offset : offset+8]))
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			t.Fatal("positive numeric fixture contains a non-finite component")
		}
	}
}

func fbxTestArray(t *testing.T, kind byte, data []byte, level int) []byte {
	t.Helper()
	width := fbxTestArrayWidth(kind)
	if len(data)%width != 0 {
		t.Fatal("FBX fixture array must contain complete components")
	}
	payload := append([]byte(nil), data...)
	encoding := uint64(0)
	if level != -2 {
		payload, encoding = fbxTestZlib(t, data, level), 1
	}
	property := []byte{kind}
	property = append(property, fbxTestNumber(4, uint64(len(data)/width))...)
	property = append(property, fbxTestNumber(4, encoding)...)
	property = append(property, fbxTestNumber(4, uint64(len(payload)))...)
	return append(property, payload...)
}

func fbxTestZlib(t *testing.T, data []byte, level int) []byte {
	t.Helper()
	var output bytes.Buffer
	writer, err := zlib.NewWriterLevel(&output, level)
	if err != nil {
		t.Fatal("failed to initialize fixture zlib writer")
	}
	if _, err := writer.Write(data); err != nil {
		t.Fatal("failed to write fixture zlib array")
	}
	if err := writer.Close(); err != nil {
		t.Fatal("failed to finish fixture zlib array")
	}
	return output.Bytes()
}

func fbxTestResizeArrayPayload(property []byte) []byte {
	binary.LittleEndian.PutUint32(property[9:13], uint32(len(property)-13))
	return property
}

func fbxTestWrappers(t *testing.T, body []byte, accepted bool) {
	t.Helper()
	for _, wrapper := range []struct {
		name, variant, mime string
		data                []byte
	}{
		{"fbx", "fbx", "application/octet-stream", body},
		{"zip_stored", "zip", "application/zip", archiveGLTFTestZIP(t, []archiveGLTFTestMember{{"scene.fbx", body}}, zip.Store)},
		{"zip_deflate", "zip", "application/zip", archiveGLTFTestZIP(t, []archiveGLTFTestMember{{"scene.fbx", body}}, zip.Deflate)},
	} {
		t.Run(wrapper.name, func(t *testing.T) {
			inlineTestBoundary(t, wrapper.data, wrapper.variant, wrapper.mime, "model", accepted)
		})
	}
}

// Blender's exporter (behind Tripo's converter) omits the null record of a
// last child that has neither properties nor children.
func TestFBXLastEmptyChildMayOmitItsNullRecord(t *testing.T) {
	t.Parallel()
	for _, version := range []uint32{7400, 7500} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			body := fbxTestFile(t, version, fbxTestNode{name: []byte("Objects"), children: []fbxTestNode{
				{name: []byte("Value"), properties: [][]byte{fbxTestScalar('I', fbxTestNumber(4, 1))}},
				{name: []byte("Empty")},
			}})
			fbxTestWrappers(t, body, true)
		})
	}
}
