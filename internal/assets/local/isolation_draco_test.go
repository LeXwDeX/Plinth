package local

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"io"
	"math"
	"os"
	"testing"
	"time"
)

func dracoFixtureBytes(t *testing.T) []byte {
	t.Helper()
	b, e := hex.DecodeString("445241434f0202010100000018080008000003ffffffff02884001ff000001000903000002010101000f03550dad022b0130057352a4fbacff3f00f0ff0300ffffffefff0300ffbff0ff0300ff3f00f0ffffff0200faffffffff0700fefff4ff00c0ff0f00f8ffffbfffdfff0300fd3f00e0ff0300ff7fff0f00e0fff7ffff7f00a0ffc7ff0f00f8ff0080ffdfff0300e8fffd7fff1f00e8ff0100000000ff3f0000000080bf000080bf000080bf000000400e")
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func dracoFixtureDoc(size int) map[string]any {
	return map[string]any{"asset": map[string]any{"version": "2.0"}, "buffers": []any{map[string]any{"byteLength": size}}, "bufferViews": []any{map[string]any{"buffer": 0, "byteLength": size}}, "accessors": []any{map[string]any{"componentType": 5126, "count": 24, "type": "VEC3", "min": []any{-1, -1, -1}, "max": []any{1, 1, 1}}, map[string]any{"componentType": 5125, "count": 24, "type": "SCALAR"}}, "extensionsUsed": []any{"KHR_draco_mesh_compression"}, "extensionsRequired": []any{"KHR_draco_mesh_compression"}, "meshes": []any{map[string]any{"primitives": []any{map[string]any{"attributes": map[string]any{"POSITION": 0}, "indices": 1, "extensions": map[string]any{"KHR_draco_mesh_compression": map[string]any{"bufferView": 0, "attributes": map[string]any{"POSITION": 0}}}}}}}}
}
func dracoGLB(t *testing.T, b []byte, edit func(map[string]any)) []byte {
	t.Helper()
	doc := dracoFixtureDoc(len(b))
	if edit != nil {
		edit(doc)
	}
	j, e := json.Marshal(doc)
	if e != nil {
		t.Fatal(e)
	}
	return glbFixtureWithBIN(string(j), b)
}
func TestDracoOriginalBytesAcrossDeliveryBoundaries(t *testing.T) {
	body := dracoGLB(t, dracoFixtureBytes(t), nil)
	assertIsolationBoundary(t, body, "glb", "model/gltf-binary", true)
	assertIsolationBoundary(t, zipModel(t, map[string]string{"scene.glb": string(body)}), "zip", "application/zip", true)
}
func TestDracoRejectsInternalUnconsumedAndOuterBytes(t *testing.T) {
	b := dracoFixtureBytes(t)
	for _, tc := range []struct {
		name           string
		offset, length int
	}{{"rans_bits", 24, 2}, {"rans_symbols", 51, 5}} {
		t.Run(tc.name, func(t *testing.T) {
			tail := []byte("//hidden.example/")
			modified := append([]byte{}, b[:tc.offset]...)
			if int(modified[tc.offset-1]) != tc.length {
				t.Fatal("fixture entropy boundary changed")
			}
			modified[tc.offset-1] += byte(len(tail))
			modified = append(modified, tail...)
			modified = append(modified, b[tc.offset:]...)
			assertIsolationBoundary(t, dracoGLB(t, modified, nil), "glb", "model/gltf-binary", false)
		})
	}
	for _, modified := range [][]byte{append(append([]byte{}, b...), 0), b[:len(b)-1]} {
		assertIsolationBoundary(t, dracoGLB(t, modified, nil), "glb", "model/gltf-binary", false)
	}
}
func TestDracoRejectsUnsupportedHeadersAndBindings(t *testing.T) {
	b := dracoFixtureBytes(t)
	for _, offset := range []int{5, 6, 7, 8, 9, 10, 11} {
		copyB := append([]byte{}, b...)
		copyB[offset] = 127
		assertIsolationBoundary(t, dracoGLB(t, copyB, nil), "glb", "model/gltf-binary", false)
	}
	edits := map[string]func(map[string]any){"count": func(d map[string]any) { d["accessors"].([]any)[0].(map[string]any)["count"] = 25 }, "bounds": func(d map[string]any) { d["accessors"].([]any)[0].(map[string]any)["max"] = []any{2, 1, 1} }, "index_count": func(d map[string]any) { d["accessors"].([]any)[1].(map[string]any)["count"] = 27 }, "component": func(d map[string]any) { d["accessors"].([]any)[0].(map[string]any)["componentType"] = 5125 }, "missing_required": func(d map[string]any) { delete(d, "extensionsRequired") }, "external_json": func(d map[string]any) { d["extras"] = map[string]any{"uri": "https://private.example/model"} }, "image_overlap": func(d map[string]any) { d["images"] = []any{map[string]any{"bufferView": 0, "mimeType": "image/png"}} }, "id": func(d map[string]any) {
		p := d["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)[0].(map[string]any)
		p["extensions"].(map[string]any)["KHR_draco_mesh_compression"].(map[string]any)["attributes"].(map[string]any)["POSITION"] = 1
	}}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			assertIsolationBoundary(t, dracoGLB(t, b, edit), "glb", "model/gltf-binary", false)
		})
	}
}
func TestDracoSharedWorkAndDeadline(t *testing.T) {
	b := dracoFixtureBytes(t)
	body := dracoGLB(t, b, nil)
	budget := newInlineInspectionBudgetForSize(int64(len(body)))
	budget.work.remaining = 10
	if inspectGLBStreamBounded(bytes.NewReader(body), int64(len(body)), nil, "", budget) == nil {
		t.Fatal("work budget bypass")
	}
	budget = newInlineInspectionBudgetForSize(int64(len(body)))
	budget.work.dracoDeadline = time.Now().Add(-time.Second)
	if inspectGLBStreamBounded(bytes.NewReader(body), int64(len(body)), nil, "", budget) == nil {
		t.Fatal("shared expired deadline bypass")
	}
}
func TestDracoWASMMemoryLimitAndCancelledExecution(t *testing.T) {
	ctx := context.Background()
	runtime := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithMemoryLimitPages(1).WithCloseOnContextDone(true))
	defer runtime.Close(ctx)
	wasi_snapshot_preview1.MustInstantiate(ctx, runtime)
	_, err := runtime.CompileModule(ctx, dracoModule)
	if err == nil {
		t.Fatal("module minimum memory exceeds the runtime cap but compilation succeeded")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if validateDracoContext(cancelled, dracoFixtureBytes(t), dracoRange{}) == nil {
		t.Fatal("cancelled execution succeeded")
	}
}
func TestDracoActualRodinOriginalArtifact(t *testing.T) {
	path := os.Getenv("ART_AI_DRACO_SAMPLE")
	if path == "" {
		t.Skip("local unchanged real artifact is opt-in")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != "28e2084ff9dfdaf26e00707945cef69a0689f5634aa991c8dce10fd4f3c9b010" {
		t.Fatal("sample identity mismatch")
	}
	assertIsolationBoundary(t, body, "glb", "model/gltf-binary", true)
	jsonLength := int(binary.LittleEndian.Uint32(body[12:16]))
	decoder := json.NewDecoder(bytes.NewReader(body[20 : 20+jsonLength]))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	bin := body[28+jsonLength:]
	ranges, err := glbDracoRanges(document, int64(len(bin)), nil, nil)
	if err != nil || len(ranges) != 1 {
		t.Fatalf("sample binding: %v", err)
	}
	r := ranges[0]
	compressed := bin[r.start:r.end]
	deadline, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	started := time.Now()
	if validateDracoContext(deadline, compressed, r) == nil || time.Since(started) > 5*time.Second {
		t.Fatal("running decoder was not terminated within its deadline")
	}
	ctx := context.Background()
	limited := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithMemoryLimitPages(512).WithCloseOnContextDone(true))
	defer limited.Close(ctx)
	wasi_snapshot_preview1.MustInstantiate(ctx, limited)
	if err := instantiateDracoScanner(ctx, limited); err != nil {
		t.Fatal(err)
	}
	ctx = context.WithValue(ctx, dracoScanContextKey{}, newDracoNumericScan(r.scanWork))
	module, err := limited.CompileModule(ctx, dracoModule)
	if err != nil {
		t.Fatal(err)
	}
	var receipt dracoReceipt
	_, err = limited.InstantiateModule(ctx, module, wazero.NewModuleConfig().WithStdin(io.MultiReader(bytes.NewReader(dracoRequest(compressed, r)), bytes.NewReader(compressed))).WithStdout(&receipt))
	if err == nil {
		t.Fatal("real mesh decoded past 32MiB cap")
	}
}

func TestDracoFiniteValuesAndUnclaimedBytes(t *testing.T) {
	b := dracoFixtureBytes(t)
	for _, value := range []uint32{math.Float32bits(float32(math.Inf(1))), 0x7fc00000} {
		bad := append([]byte{}, b...)
		binary.LittleEndian.PutUint32(bad[len(bad)-17:], value)
		assertIsolationBoundary(t, dracoGLB(t, bad, nil), "glb", "model/gltf-binary", false)
	}
	doc := dracoFixtureDoc(len(b) + 20)
	j, _ := json.Marshal(doc)
	unknown := append(append([]byte{}, b...), []byte(" //hidden.example/x ")...)
	assertIsolationBoundary(t, glbFixtureWithBIN(string(j), unknown), "glb", "model/gltf-binary", false)
	j, _ = json.Marshal(dracoFixtureDoc(len(b)))
	assertIsolationBoundary(t, glbWithJSON(string(j)), "glb", "model/gltf-binary", false)
}

func TestDracoDecodedAbsoluteAddressesRemainRejected(t *testing.T) {
	b := dracoFixtureBytes(t)
	copy(b[len(b)-17:], []byte("http://x"))
	r := dracoRange{scanWork: 1 << 20, work: 1 << 20, points: 24, indices: 24, indexType: 5125, attributes: []dracoAttribute{{id: 0, component: 5126, components: 3}}}
	if err := validateDracoContext(context.Background(), b, r); err == nil {
		t.Fatal("decoded FLOAT words containing an absolute address were accepted")
	}
}
func TestDracoIndexBoundsAndDuplicateRangeBinding(t *testing.T) {
	b := dracoFixtureBytes(t)
	for _, bad := range []bool{false, true} {
		body := dracoGLB(t, b, func(d map[string]any) {
			index := d["accessors"].([]any)[1].(map[string]any)
			index["min"] = []any{0}
			index["max"] = []any{23}
			if bad {
				index["max"] = []any{24}
			}
		})
		assertIsolationBoundary(t, body, "glb", "model/gltf-binary", !bad)
	}
	body := dracoGLB(t, b, func(d map[string]any) {
		mesh := d["meshes"].([]any)[0].(map[string]any)
		p := mesh["primitives"].([]any)[0]
		mesh["primitives"] = []any{p, p}
	})
	assertIsolationBoundary(t, body, "glb", "model/gltf-binary", true)
}

func TestDracoBuildManifestMatchesSourcesAndBinary(t *testing.T) {
	raw, err := os.ReadFile("draco/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"inspect.cc", "strict.patch", "inspect.wasm"} {
		data, err := os.ReadFile("draco/" + name)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != manifest.Files[name] {
			t.Fatalf("%s differs from the rebuilt manifest", name)
		}
	}
}
