package local

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"
)

// These after-only tests exercise the shared inspection implementation. They
// are excluded from the exact pre-fix public delivery-boundary receipt.
func TestFBXNodeLimitFixtureUsesValidEmptyScopes(t *testing.T) {
	for _, version := range []uint32{7400, 7500} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			body := fbxTestFile(t, version, fbxTestNode{name: []byte("N"), children: []fbxTestNode{}})
			if err := inspectBinaryFBX(bytes.NewReader(body), int64(len(body)), newInspectionWorkBudget()); err != nil {
				t.Fatal("the node-limit fixture must use a valid empty scope")
			}
		})
	}
}

func TestFBXWorkBudgetRejectsBeforeExpensivePayload(t *testing.T) {
	data := make([]byte, 1<<20)
	array := fbxTestArray(t, 'i', data, -2)
	oversizedArray := bytes.Clone(array)
	binary.LittleEndian.PutUint32(oversizedArray[1:5], math.MaxUint32)
	compressed := fbxTestArray(t, 'i', data, zlib.BestCompression)
	oversizedContent := fbxTestBytes('R', data)
	binary.LittleEndian.PutUint32(oversizedContent[1:5], math.MaxUint32)
	for _, item := range []struct {
		name        string
		property    []byte
		budget      int64
		workFailure bool
		noReads     bool
	}{
		{"no_initial_buffer_budget", array, 1, true, true},
		{"raw_array_work", array, fbxParserWindowWork + (300 << 10), true, false},
		{"compressed_array_work", compressed, fbxParserWindowWork + (400 << 10), true, false},
		{"decoded_array_bound", oversizedArray, maxInspectionWork, false, false},
		{"content_work", fbxTestBytes('R', data), fbxParserWindowWork + (300 << 10), true, false},
		{"content_length_bound", oversizedContent, maxInspectionWork, false, false},
	} {
		for _, step := range []int{1, 0} {
			t.Run(fmt.Sprintf("%s/read_%d", item.name, step), func(t *testing.T) {
				body := fbxTestFile(t, 7400, fbxTestNode{name: []byte("Values"), properties: [][]byte{item.property}})
				reader := &fbxTestReadCounter{reader: bytes.NewReader(body), step: step}
				err := inspectBinaryFBX(reader, int64(len(body)), &inspectionWorkBudget{remaining: item.budget})
				if err == nil {
					t.Fatal("declared limit or work exhaustion must reject")
				}
				if item.workFailure {
					fbxTestRequireWorkRejection(t, err)
				}
				if item.noReads {
					if reader.readBytes != 0 {
						t.Fatal("initial work must be reserved before allocating or reading the input buffer")
					}
					return
				}
				if step == 1 {
					// Only framing and declarations may be consumed. A one-byte
					// reader prevents the fixed input buffer from prefetching.
					payloadStart := 27 + 13 + len("Values") + 13
					if item.property[0] == 'R' {
						payloadStart -= 8
					}
					if reader.readBytes != payloadStart {
						t.Fatalf("payload was touched before rejection: read=%d declaration_end=%d", reader.readBytes, payloadStart)
					}
				} else if reader.readBytes > isolationChunkBytes {
					t.Fatal("rejection must not read beyond one fixed input-buffer prefetch")
				}
			})
		}
	}
	t.Run("small_scalar_work_before_name_or_body", func(t *testing.T) {
		body := fbxTestFile(t, 7400, fbxTestNode{name: []byte("Values"), properties: [][]byte{fbxTestScalar('C', []byte{0})}})
		reader := &fbxTestReadCounter{reader: bytes.NewReader(body), step: 1}
		// Pay for the input buffer and node header, but not its properties.
		work := &inspectionWorkBudget{remaining: fbxParserWindowWork + fbxNodeWork}
		fbxTestRequireWorkRejection(t, inspectBinaryFBX(reader, int64(len(body)), work))
		if reader.readBytes != 27+13 {
			t.Fatal("small property and name work must be prepaid before reading their bytes")
		}
	})
}

func TestFBXWorkBudgetAccumulatesAcrossFilesImagesAndArchiveMembers(t *testing.T) {
	for _, item := range []struct {
		name     string
		property []byte
	}{
		{"raw_integer_array", fbxTestArray(t, 'i', make([]byte, 1<<16), -2)},
		{"compressed_double_array", fbxTestArray(t, 'd', bytes.Repeat(fbxTestNumber(8, 0x3ff000007fc00000), 8193), zlib.BestCompression)},
		{"png_image", fbxTestBytes('R', inlineTestPNG(t, ""))},
		{"jpeg_image", fbxTestBytes('R', rasterNoiseFixture(t, "jpeg"))},
	} {
		t.Run(item.name, func(t *testing.T) {
			body := fbxTestFile(t, 7400, fbxTestNode{name: []byte("Values"), properties: [][]byte{item.property}})
			used, decode := fbxTestMeasuredWork(t, body)
			structural := used - decode
			t.Run("same_shared_work", func(t *testing.T) {
				work := &inspectionWorkBudget{remaining: structural}
				if err := inspectBinaryFBX(bytes.NewReader(body), int64(len(body)), work); err != nil {
					t.Fatal("first valid resource must fit the shared work budget")
				}
				remaining := work.remaining
				fbxTestRequireWorkRejection(t, inspectBinaryFBX(bytes.NewReader(body), int64(len(body)), work))
				if work.remaining > remaining {
					t.Fatal("second file must share the budget, not reset it")
				}
			})
			t.Run("two_properties", func(t *testing.T) {
				two := fbxTestFile(t, 7400, fbxTestNode{name: []byte("Values"), properties: [][]byte{item.property, item.property}})
				fbxTestRequireWorkRejection(t, inspectBinaryFBX(bytes.NewReader(two), int64(len(two)), &inspectionWorkBudget{remaining: structural}))
			})
			for _, method := range []uint16{zip.Store, zip.Deflate} {
				t.Run(fmt.Sprintf("archive_%d", method), func(t *testing.T) {
					wrapped := archiveGLTFTestZIP(t, []archiveGLTFTestMember{{"first.fbx", body}, {"second.fbx", body}}, method)
					archive, err := zip.NewReader(bytes.NewReader(wrapped), int64(len(wrapped)))
					if err != nil {
						t.Fatal("valid archive fixture must parse")
					}
					budget := newInlineInspectionBudget()
					budget.work = &inspectionWorkBudget{remaining: structural}
					readWork := &archiveInspectionWork{remaining: int64(len(body))*6 + 1024}
					names := map[string]struct{}{"first.fbx": {}, "second.fbx": {}}
					if err := inspectArchiveBufferMember(archive.File[0], names, budget, readWork, nil, false); err != nil {
						t.Fatal("first valid FBX archive member must fit")
					}
					fbxTestRequireWorkRejection(t, inspectArchiveBufferMember(archive.File[1], names, budget, readWork, nil, false))
				})
			}
		})
	}
}

// A valid model must not be rejected because an earlier optional image decode
// spent work that a later mandatory structure walk needs. The observed failure
// was a Meshy multiview FBX with four embedded PNGs: decoding the first images
// consumed most of the shared budget, and the fourth image's framing/CRC walk
// was then rejected even though the structural work alone fit.
func TestFBXOptionalDecodeCannotStarveLaterStructuralWork(t *testing.T) {
	image := fbxTestBytes('R', inlineTestPNG(t, ""))
	body := fbxTestFile(t, 7400, fbxTestNode{
		name:       []byte("Texture"),
		properties: [][]byte{image, image},
	})
	used, decode := fbxTestMeasuredWork(t, body)
	if decode <= 0 {
		t.Fatal("fixture must run an optional image decode to exercise reclaiming")
	}
	structural := used - decode
	// Exactly the structural bound: the first image decodes while work remains,
	// and the second image's walk reclaims that optional spend.
	if err := inspectBinaryFBX(bytes.NewReader(body), int64(len(body)), &inspectionWorkBudget{remaining: structural}); err != nil {
		t.Fatalf("optional decode must not starve a mandatory structure walk: %v", err)
	}
	// One unit short of the structural bound is still a hard rejection.
	fbxTestRequireWorkRejection(t, inspectBinaryFBX(bytes.NewReader(body), int64(len(body)), &inspectionWorkBudget{remaining: structural - 1}))
}

func TestFBXRasterWorkIsReservedBeforeCompletePixelDecode(t *testing.T) {
	// PNG DecodeConfig can read this intact signature/IHDR. The full image
	// decode cannot succeed because IDAT and IEND are missing.
	partialPNG := bytes.Clone(inlineTestPNG(t, "")[:33])
	body := fbxTestFile(t, 7400, fbxTestNode{name: []byte("Texture"), properties: [][]byte{fbxTestBytes('R', partialPNG)}})
	err := inspectBinaryFBX(bytes.NewReader(body), int64(len(body)), newInspectionWorkBudget())
	if err == nil || strings.Contains(err.Error(), "工作量超出限制") {
		t.Fatal("unconstrained fixture must fail raster structure, not its work budget")
	}
	reader := &fbxTestReadCounter{reader: bytes.NewReader(body), step: 1}
	fbxTestRequireWorkRejection(t, inspectBinaryFBX(reader, int64(len(body)), &inspectionWorkBudget{remaining: fbxParserWindowWork + (32 << 10)}))
	if reader.readBytes > 27+13+len("Texture")+5+len(partialPNG) {
		t.Fatal("pixel-decode work must reject before reading subsequent model framing")
	}
}

func TestFBXStreamingAggregatesShortReadsAndChecksExactLength(t *testing.T) {
	for _, version := range []uint32{7400, 7500} {
		for _, kind := range []byte{'i', 'f', 'd'} {
			word := fbxTestNumber(fbxTestArrayWidth(kind), 1)
			if kind == 'f' {
				word = fbxTestNumber(4, uint64(math.Float32bits(1.25)))
			} else if kind == 'd' {
				word = fbxTestNumber(8, 0x3ff000007fc00000)
			}
			data := bytes.Repeat(word, (2*isolationChunkBytes)/len(word)+3)
			for _, level := range []int{-2, zlib.BestCompression} {
				body := fbxTestFile(t, version, fbxTestNode{name: []byte("Values"), properties: [][]byte{fbxTestArray(t, kind, data, level)}})
				for _, step := range []int{1, 7, 257} {
					t.Run(fmt.Sprintf("%d/%c/level_%d/read_%d", version, kind, level, step), func(t *testing.T) {
						reader := &fbxTestReadCounter{reader: bytes.NewReader(body), step: step}
						if err := inspectBinaryFBX(reader, int64(len(body)), newInspectionWorkBudget()); err != nil {
							t.Fatalf("finite typed values must survive bounded short reads: %v", err)
						}
						if reader.readBytes != len(body) {
							t.Fatal("valid parse must consume the exact declared file")
						}
					})
				}
				for _, truncate := range []int{27, len(body) / 2, len(body) - 1} {
					t.Run(fmt.Sprintf("%d/%c/level_%d/truncate_%d", version, kind, level, truncate), func(t *testing.T) {
						reader := &fbxTestReadCounter{reader: bytes.NewReader(body[:truncate]), step: 7}
						if err := inspectBinaryFBX(reader, int64(len(body)), newInspectionWorkBudget()); err == nil {
							t.Fatal("short source must not satisfy the original declared file length")
						}
					})
				}
			}
		}
	}
}

func TestFBXSharedFloatInspectionCarriesEverySplitWord(t *testing.T) {
	for _, width := range []int{4, 8} {
		for split := 1; split < width; split++ {
			for _, item := range []struct {
				name string
				bits uint64
				bad  bool
			}{
				{"finite", 0x3ff000007fc00000, false},
				{"nan", 0x7ff8000000000000, true},
				{"positive_inf", 0x7ff0000000000000, true},
				{"negative_inf", 0xfff0000000000000, true},
			} {
				for _, step := range []int{1, 11} {
					t.Run(fmt.Sprintf("width_%d/split_%d/%s/read_%d", width, split, item.name, step), func(t *testing.T) {
						start := width - split
						count := 2*isolationChunkBytes/width + 4
						bits := item.bits
						if width == 4 {
							bits = uint64(math.Float32bits(1.25))
							if item.bad {
								bits = uint64(math.Float32bits(float32(math.Float64frombits(item.bits))))
							}
						}
						body := make([]byte, start+count*width)
						finite := fbxTestNumber(width, 0x3ff000007fc00000)
						if width == 4 {
							finite = fbxTestNumber(width, uint64(math.Float32bits(1.25)))
						}
						for position := start; position < len(body); position += width {
							copy(body[position:position+width], finite)
						}
						position := isolationChunkBytes - split
						copy(body[position:position+width], fbxTestNumber(width, bits))
						plan := inspectionPlan{fields: []inspectionField{{int64(start), int64(width), int64(width), int64(count), true}}}
						if width == 8 {
							plan.floatBytes = 8
						} // The existing default remains float32.
						reader := &fbxTestReadCounter{reader: bytes.NewReader(body), step: step}
						err := inspectPlannedBytesWithWork(reader, plan, newInspectionWorkBudget())
						if item.bad {
							if err == nil || !strings.Contains(err.Error(), "非有限浮点数") {
								t.Fatal("split non-finite word must be detected as a complete float")
							}
						} else if err != nil {
							t.Fatalf("finite complete word must pass every window split: %v", err)
						}
					})
				}
			}
		}
	}
}

type fbxTestReadCounter struct {
	reader    *bytes.Reader
	step      int
	readBytes int
}

func (reader *fbxTestReadCounter) Read(body []byte) (int, error) {
	if reader.step > 0 && len(body) > reader.step {
		body = body[:reader.step]
	}
	count, err := reader.reader.Read(body)
	reader.readBytes += count
	return count, err
}

func (reader *fbxTestReadCounter) Len() int { return reader.reader.Len() }

var _ io.Reader = (*fbxTestReadCounter)(nil)

// fbxTestMeasuredWork reports the total work a fixture spent and how much of
// it was optional image decoding. Structural work is the difference: with an
// unconstrained budget nothing is reclaimed, so every decode unit stays marked
// reclaimable. Structural work alone must exhaust the budget for a rejection;
// decode work is best-effort and may be yielded to a later structural check.
func fbxTestMeasuredWork(t *testing.T, body []byte) (used, decode int64) {
	t.Helper()
	work := newInspectionWorkBudget()
	initial := work.remaining
	if err := inspectBinaryFBX(bytes.NewReader(body), int64(len(body)), work); err != nil {
		t.Fatalf("valid work measurement fixture must parse: %v", err)
	}
	if work.remaining >= initial {
		t.Fatal("valid FBX must reserve positive shared work")
	}
	used = initial - work.remaining
	if work.reclaimable > used {
		t.Fatal("optional decode work cannot exceed total work")
	}
	return used, work.reclaimable
}

func fbxTestRequireWorkRejection(t *testing.T, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "工作量超出限制") {
		t.Fatal("resource must reject specifically when shared work is exhausted")
	}
}
