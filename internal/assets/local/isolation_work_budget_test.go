package local

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"testing"
)

func TestInspectionWorkNormalizesDuplicateFields(t *testing.T) {
	field := inspectionField{start: 0, width: 4, stride: 4, count: 4096, floating: true}
	plan := inspectionPlan{fields: make([]inspectionField, maxArchiveFiles)}
	for index := range plan.fields {
		plan.fields[index] = field
	}
	if err := normalizeInspectionPlan(&plan, 16384); err != nil {
		t.Fatal(err)
	}
	if len(plan.fields) != 1 {
		t.Fatalf("duplicate work descriptors=%d, expected one", len(plan.fields))
	}
	if plan.fields[0].start != 0 || plan.fields[0].end() != 16384 || !plan.fields[0].floating {
		t.Fatal("normalization changed the numeric/finite-value contract")
	}
}

type inspectionUnreadableProbe struct{ reads int }

func (reader *inspectionUnreadableProbe) Read([]byte) (int, error) {
	reader.reads++
	return 0, errors.New("unexpected fixture read")
}

func inspectionOverlapFields(count int) []inspectionField {
	fields := make([]inspectionField, count)
	for index := range fields {
		fields[index] = inspectionField{start: int64(index * 4), width: 4, stride: 4, count: 1024, floating: true}
	}
	return fields
}

func TestInspectionWorkRejectsAmplificationBeforeReading(t *testing.T) {
	for _, item := range []struct {
		name   string
		fields []inspectionField
	}{
		{"sliding_overlaps", inspectionOverlapFields(256)},
		{"huge_width", []inspectionField{{0, math.MaxInt64, math.MaxInt64, 1, false}}},
		{"huge_count", []inspectionField{{0, 4, 4, math.MaxInt64 / 4, true}}},
		{"huge_descriptor_extent", []inspectionField{{math.MaxInt64 - 4, 4, 4, 1, true}}},
		{"overflowing_range", []inspectionField{{math.MaxInt64 - 4, 4, 4, 2, true}}},
	} {
		t.Run(item.name, func(t *testing.T) {
			reader := &inspectionUnreadableProbe{}
			if err := inspectPlannedBytes(reader, inspectionPlan{fields: item.fields}); err == nil {
				t.Fatal("excessive or invalid work accepted")
			}
			if reader.reads != 0 {
				t.Fatalf("work was checked after %d reads", reader.reads)
			}
		})
	}
}

func TestInspectionWorkAdmitsLargeSparsePlans(t *testing.T) {
	plan := inspectionPlan{fields: make([]inspectionField, maxArchiveFiles)}
	for index := range plan.fields {
		plan.fields[index] = inspectionField{start: int64(index) * isolationChunkBytes, width: 4, stride: 4, count: 1, floating: true}
	}
	if err := normalizeInspectionPlan(&plan, maxArchiveBytes); err != nil {
		t.Fatal("sparse valid descriptors rejected", err)
	}
	if len(plan.fields) != maxArchiveFiles {
		t.Fatal("independent sparse fields were lost")
	}
}

func inspectionWorkDocument(t *testing.T, count int, sliding bool, body []byte, uri string) []byte {
	t.Helper()
	buffer := map[string]any{"byteLength": len(body)}
	if uri != "" {
		buffer["uri"] = uri
	}
	accessors := make([]any, count)
	for index := range accessors {
		offset := 0
		if sliding {
			offset = index * 4
		}
		accessors[index] = map[string]any{
			"bufferView": 0, "byteOffset": offset, "componentType": 5126,
			"count": (len(body) - (count-1)*4) / 4, "type": "SCALAR",
		}
		if !sliding {
			accessors[index].(map[string]any)["count"] = len(body) / 4
		}
	}
	document, err := json.Marshal(map[string]any{
		"asset": map[string]any{"version": "2.0"}, "buffers": []any{buffer},
		"bufferViews": []any{map[string]any{"buffer": 0, "byteLength": len(body)}},
		"accessors":   accessors,
	})
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func inspectionWorkArchive(t *testing.T, method uint16, names []string, bodies [][]byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for index, name := range names {
		entry, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(bodies[index]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func inspectionWorkDeliveryWrappers(t *testing.T, fields int, sliding bool, bin []byte, accepted bool) {
	t.Helper()
	document := inspectionWorkDocument(t, fields, sliding, bin, "")
	glb := glbFixtureWithBIN(string(document), bin)
	inline := inspectionWorkDocument(t, fields, sliding, bin, "data:application/octet-stream;base64,"+base64.StdEncoding.EncodeToString(bin))
	sidecar := inspectionWorkDocument(t, fields, sliding, bin, "geometry.bin")
	for _, item := range []struct {
		name, variant, mime string
		body                []byte
	}{
		{"glb", "glb", "model/gltf-binary", glb},
		{"inline", "gltf", "model/gltf+json", inline},
	} {
		t.Run(item.name, func(t *testing.T) {
			inlineTestBoundary(t, item.body, item.variant, item.mime, "model", accepted)
		})
	}
	for _, method := range []uint16{zip.Store, zip.Deflate} {
		for _, source := range []string{"glb", "inline", "relative"} {
			names, bodies := []string{"scene.glb"}, [][]byte{glb}
			if source == "inline" {
				names, bodies = []string{"scene.gltf"}, [][]byte{inline}
			} else if source == "relative" {
				names, bodies = []string{"scene.gltf", "geometry.bin"}, [][]byte{sidecar, bin}
			}
			archive := inspectionWorkArchive(t, method, names, bodies)
			t.Run(fmt.Sprintf("zip-%d-%s", method, source), func(t *testing.T) {
				inlineTestBoundary(t, archive, "zip", "application/zip", "model", accepted)
			})
		}
	}
}

func TestInspectionWorkUsesAllDeliveryBoundaries(t *testing.T) {
	t.Run("duplicates", func(t *testing.T) {
		inspectionWorkDeliveryWrappers(t, 512, false, make([]byte, 64), true)
	})
	t.Run("sliding_overlaps", func(t *testing.T) {
		inspectionWorkDeliveryWrappers(t, 256, true, make([]byte, 4096+255*4), false)
	})
	for _, item := range []struct {
		name string
		bits uint32
	}{
		{"positive_infinity", 0x7f800000}, {"negative_infinity", 0xff800000}, {"nan", 0x7fc00000},
	} {
		t.Run(item.name, func(t *testing.T) {
			body := make([]byte, 64)
			binary.LittleEndian.PutUint32(body[60:], item.bits)
			inspectionWorkDeliveryWrappers(t, 16, false, body, false)
		})
	}
	t.Run("absolute_address", func(t *testing.T) {
		body := make([]byte, 64)
		copy(body, "https://provider.example/private")
		inspectionWorkDeliveryWrappers(t, 16, false, body, false)
	})
}

// Current-only helper tests below this line do not belong to the old-source
// overlay. The old-compatible public and preflight tests above are unchanged.

func TestInspectionWorkBudgetReservationIsOncePerExecution(t *testing.T) {
	body := make([]byte, 16384)
	plan := inspectionPlan{fields: make([]inspectionField, maxArchiveFiles)}
	for index := range plan.fields {
		plan.fields[index] = inspectionField{0, 4, 4, int64(len(body)) / 4, true}
	}
	budget := newInspectionWorkBudget()
	inspector := newPlannedInspectorWithWork(budget)
	if err := inspector.prepare(&plan, int64(len(body))); err != nil {
		t.Fatal(err)
	}
	work, remaining := plan.work.total, budget.remaining
	if work <= 2*int64(len(body)) || work > baseInspectionWork {
		t.Fatal("canonical work omitted sorting or repeatedly charged numeric bytes")
	}
	if err := inspector.prepare(&plan, int64(len(body))); err != nil || budget.remaining != remaining {
		t.Fatal("an unused plan was charged twice", err)
	}
	if err := normalizeInspectionPlan(&plan, int64(len(body))); err != nil || budget.remaining != remaining {
		t.Fatal("normalization spent the execution budget", err)
	}
	if err := inspector.inspectPart(body, plan, 0); err != nil || budget.remaining != remaining {
		t.Fatal("a streamed window re-reserved the plan", err)
	}
	if err := inspector.prepare(&plan, int64(len(body))); err != nil || budget.remaining != remaining-work {
		t.Fatal("a genuine repeated execution did not spend additional work", err)
	}
}

func TestInspectionWorkFiniteChecksSurviveCanonicalization(t *testing.T) {
	plan := inspectionPlan{fields: []inspectionField{{0, 4, 4, 8, false}, {0, 16, 16, 2, true}}}
	if err := normalizeInspectionPlan(&plan, 32); err != nil || len(plan.fields) != 1 || !plan.fields[0].floating {
		t.Fatal("equivalent numeric layouts lost the stricter float check", err)
	}
	body := make([]byte, 32)
	binary.LittleEndian.PutUint32(body[28:], math.Float32bits(float32(math.Inf(1))))
	if err := inspectPlannedBytes(bytes.NewReader(body), plan); err == nil {
		t.Fatal("deduplicated integer/float storage admitted infinity")
	}
	for _, invalid := range []bool{false, true} {
		body := make([]byte, 2+2*isolationChunkBytes)
		if invalid {
			binary.LittleEndian.PutUint32(body[isolationChunkBytes-2:], 0x7f800000)
		}
		plan := inspectionPlan{fields: []inspectionField{{2, 4, 4, 2 * isolationChunkBytes / 4, true}}}
		if err := inspectPlannedBytes(fieldShortReader{bytes.NewReader(body), 1}, plan); (err != nil) != invalid {
			t.Fatal("canonical float spanning aggregated input windows changed acceptance")
		}
	}
}

func TestInspectionWorkIntervalIndexCountsOnlyActiveDescriptors(t *testing.T) {
	plan := inspectionPlan{fields: make([]inspectionField, maxArchiveFiles)}
	for index := range plan.fields {
		plan.fields[index] = inspectionField{int64(index) * isolationChunkBytes, 4, 4, 1, true}
	}
	inspector := newPlannedInspector()
	if err := inspector.prepare(&plan, maxArchiveBytes); err != nil {
		t.Fatal(err)
	}
	if plan.index == nil || len(plan.index.ends) > 32768 {
		t.Fatal("field index exceeded its bounded workspace")
	}
	before := plan.reservation.nodes
	if err := inspector.inspectPart(make([]byte, 1024), plan, 5000*isolationChunkBytes); err != nil {
		t.Fatal(err)
	}
	visits := before - plan.reservation.nodes
	if len(inspector.active) != 1 || visits <= 0 || visits > 32 {
		t.Fatalf("active fields=%d descriptor visits=%d, expected one bounded index path", len(inspector.active), visits)
	}
	plan.reservation.nodes = 0
	if err := inspector.inspectPart(make([]byte, 4), plan, 5000*isolationChunkBytes); err == nil {
		t.Fatal("exhausted descriptor work was reset between windows")
	}
}

type inspectionSizedUnreadableProbe struct {
	inspectionUnreadableProbe
	size int
}

func (reader *inspectionSizedUnreadableProbe) Len() int { return reader.size }

func TestInspectionWorkKnownSizePrecedesExtent(t *testing.T) {
	reader := &inspectionSizedUnreadableProbe{size: 8}
	plan := inspectionPlan{fields: []inspectionField{{0, 4, 4, 4, true}}}
	budget := newInspectionWorkBudget()
	if err := inspectPlannedBytesWithWork(reader, plan, budget); err == nil || reader.reads != 0 || budget.remaining != maxInspectionWork {
		t.Fatal("known truncated length was ignored before reading or reserving work")
	}
	limited := &inspectionUnreadableProbe{}
	if err := inspectPlannedBytesWithWork(io.LimitReader(limited, 8), plan, budget); err == nil || limited.reads != 0 || budget.remaining != maxInspectionWork {
		t.Fatal("declared Reader limit was ignored before reading or reserving work")
	}
}

func TestInspectionWorkChunksAndIndependentInspectorsShareReservation(t *testing.T) {
	body := make([]byte, 4*isolationChunkBytes)
	plan := inspectionPlan{fields: []inspectionField{{0, 4, 8, int64(len(body)) / 8, true}}}
	if err := normalizeInspectionPlan(&plan, int64(len(body))); err != nil {
		t.Fatal(err)
	}
	budget := &inspectionWorkBudget{remaining: plan.work.total}
	inspector := newPlannedInspectorWithWork(budget)
	if err := inspector.prepare(&plan, int64(len(body))); err != nil || budget.remaining != 0 {
		t.Fatal("the resource did not reserve its complete work", err)
	}
	nodes := plan.reservation.nodes
	for offset := 0; offset < len(body); offset += isolationChunkBytes {
		if err := inspector.inspectPart(body[offset:offset+isolationChunkBytes], plan, int64(offset)); err != nil {
			t.Fatal(err)
		}
	}
	if plan.reservation.nodes != nodes-4 || budget.remaining != 0 {
		t.Fatal("descriptor or resource accounting reset between chunks")
	}
	reader := &inspectionSizedUnreadableProbe{size: len(body)}
	if err := inspectPlannedBytesWithWork(reader, inspectionPlan{fields: plan.fields}, budget); err == nil || reader.reads != 0 {
		t.Fatal("a fresh inspector reset the dependent-resource budget")
	}
	budget = &inspectionWorkBudget{remaining: plan.work.total}
	if err := inspectPlannedBytesWithWork(bytes.NewReader(body), inspectionPlan{fields: plan.fields}, budget); err != nil || budget.remaining != 0 {
		t.Fatal("EOF validation charged a completed resource again", err)
	}
}

func TestInspectionWorkDependentInlineAndArchiveBuffersAreCumulative(t *testing.T) {
	body := make([]byte, 64)
	field := inspectionField{0, 4, 4, 16, true}
	plan := inspectionPlan{fields: []inspectionField{field}}
	if err := normalizeInspectionPlan(&plan, 64); err != nil {
		t.Fatal(err)
	}
	resourceWork := plan.work.total
	uri := "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(body)
	document, err := json.Marshal(map[string]any{
		"asset":       map[string]any{"version": "2.0"},
		"buffers":     []any{map[string]any{"byteLength": 64, "uri": uri}, map[string]any{"byteLength": 64, "uri": uri}},
		"bufferViews": []any{map[string]any{"buffer": 0, "byteLength": 64}, map[string]any{"buffer": 1, "byteLength": 64}},
		"accessors": []any{
			map[string]any{"bufferView": 0, "componentType": 5126, "count": 16, "type": "SCALAR"},
			map[string]any{"bufferView": 1, "componentType": 5126, "count": 16, "type": "SCALAR"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, enough := range []bool{false, true} {
		t.Run(fmt.Sprintf("inline-enough-%v", enough), func(t *testing.T) {
			budget := newInlineInspectionBudget()
			budget.work.remaining = 2 * resourceWork
			if !enough {
				budget.work.remaining--
			}
			_, err := decodeGLTFDocumentBounded(document, nil, "", budget)
			if (err == nil) != enough {
				t.Fatal("dependent inline buffers did not consume one shared work budget", err)
			}
			if enough && (budget.work.remaining != 0 || budget.remaining != maxInlineResourceBytes-128) {
				t.Fatal("duplicate resource content was exempted or charged twice")
			}
		})
		for _, method := range []uint16{zip.Store, zip.Deflate} {
			t.Run(fmt.Sprintf("archive-%d-enough-%v", method, enough), func(t *testing.T) {
				wire := inspectionWorkArchive(t, method, []string{"first.bin", "second.bin"}, [][]byte{body, body})
				archive, err := zip.NewReader(bytes.NewReader(wire), int64(len(wire)))
				if err != nil {
					t.Fatal(err)
				}
				budget := newInlineInspectionBudget()
				budget.work.remaining = 2 * resourceWork
				if !enough {
					budget.work.remaining--
				}
				for index, entry := range archive.File {
					reader, err := entry.Open()
					if err != nil {
						t.Fatal(err)
					}
					err = inspectArchiveMemberReader(bufio.NewReader(reader), entry, nil, budget, &archiveBufferLayout{fields: []inspectionField{field}})
					_ = reader.Close()
					accepted := index == 0 || enough
					if (err == nil) != accepted {
						t.Fatal("dependent archive buffers reset their numeric work budget", err)
					}
				}
				if enough && budget.work.remaining != 0 {
					t.Fatal("ZIP EOF/CRC validation charged numeric work twice")
				}
			})
		}
	}
}

func TestInspectionWorkZeroFieldsKeepStreamingWithoutIndex(t *testing.T) {
	plan := inspectionPlan{}
	if err := normalizeInspectionPlan(&plan, math.MaxInt64); err != nil || plan.index != nil || plan.work.total != 0 {
		t.Fatal("untyped metadata constructed a numeric index or reserved work", err)
	}
	budget := &inspectionWorkBudget{}
	if err := inspectPlannedBytesWithWork(io.LimitReader(bytes.NewReader([]byte("local metadata")), 14), plan, budget); err != nil {
		t.Fatal("plain metadata depends on unused numeric budget", err)
	}
}
