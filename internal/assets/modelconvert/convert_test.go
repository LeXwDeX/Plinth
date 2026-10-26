package modelconvert

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
)

func testPolicy() modelinput.Policy {
	return modelinput.Policy{ProfileID: modelinput.ProfileID, Revision: modelinput.ProfileRevision, Limits: modelinput.Limits{MaxBytes: 10 << 20, MaxJSONBytes: 2 << 20, MaxDepth: 32, MaxElements: 100000, MaxVertices: 1000000, MaxTriangles: 1000000, MaxNodes: 10000, MaxWork: 64 << 20, MaxImagePixels: 1000000, MaxTotalImagePixels: 2000000}}
}
func testLimits() Limits {
	return Limits{MaxInputBytes: 32 << 20, MaxOutputBytes: 32 << 20, MaxWork: 128 << 20}
}
func parts(raw []byte) ([]byte, []byte) {
	j := int(binary.LittleEndian.Uint32(raw[12:]))
	return bytes.TrimRight(raw[20:20+j], " "), raw[28+j:]
}
func document(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var d map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&d); err != nil {
		t.Fatal(err)
	}
	return d
}
func logical(t *testing.T, j, b []byte) []byte {
	d := document(t, j)
	n, err := d["buffers"].([]any)[0].(map[string]any)["byteLength"].(json.Number).Int64()
	if err != nil {
		t.Fatal(err)
	}
	return b[:n]
}
func selfContained(t *testing.T, raw []byte) []byte {
	t.Helper()
	j, b := parts(raw)
	d := document(t, j)
	d["buffers"].([]any)[0].(map[string]any)["uri"] = "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(logical(t, j, b))
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func assertFailure(t *testing.T, fn func(context.Context, []byte, modelinput.Policy, Limits) ([]byte, error), raw []byte, policy modelinput.Policy, limits Limits, kind modelinput.Kind) {
	t.Helper()
	original := bytes.Clone(raw)
	result, err := fn(context.Background(), raw, policy, limits)
	var failure *modelinput.Error
	if result != nil || !errors.As(err, &failure) || failure.Kind != kind {
		t.Fatalf("got bytes=%d err=%v, want no partial output and %s", len(result), err, kind)
	}
	if !bytes.Equal(raw, original) {
		t.Fatal("input mutated on failure")
	}
}

func TestRoundTripPreservesJSONLexemesGeometryUVAndTexture(t *testing.T) {
	for name, opts := range map[string]modelinputtest.Options{"nonindexed": {}, "indexed": {Indexed: true}, "interleaved": {Indexed: true, Interleaved: true}, "multi-textured": {Indexed: true, Interleaved: true, Textured: true, MultiplePrimitives: true}} {
		t.Run(name, func(t *testing.T) {
			d, b := modelinputtest.Document(opts)
			d["extras"] = map[string]any{"precise": json.Number("9007199254740993"), "decimal": json.Number("1.2300e-3"), "label": "原始 model"}
			d["nodes"] = []any{
				map[string]any{"children": []any{1}, "translation": []float64{2, 3, 4}, "extras": map[string]any{"source": "hierarchy"}},
				map[string]any{"mesh": 0, "matrix": []float64{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}},
			}
			raw := modelinputtest.GLB(d, b)
			original := bytes.Clone(raw)
			j, bin := parts(raw)
			wantBin := logical(t, j, bin)
			wantHash := sha256.Sum256(wantBin)
			gltf, err := GLBToGLTF(context.Background(), raw, testPolicy(), testLimits())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(raw, original) {
				t.Fatal("forward mutated source")
			}
			gd := document(t, gltf)
			buffer := gd["buffers"].([]any)[0].(map[string]any)
			uri := buffer["uri"].(string)
			decoded, err := base64.StdEncoding.Strict().DecodeString(strings.TrimPrefix(uri, "data:application/octet-stream;base64,"))
			if err != nil {
				t.Fatal(err)
			}
			if sha256.Sum256(decoded) != wantHash {
				t.Fatal("logical BIN, including UV/image bytes, changed")
			}
			delete(buffer, "uri")
			if !reflect.DeepEqual(document(t, j), gd) {
				t.Fatal("forward changed document semantics")
			}
			for _, literal := range []string{"9007199254740993", "1.2300e-3"} {
				if !bytes.Contains(gltf, []byte(literal)) {
					t.Fatalf("numeric lexeme %s changed", literal)
				}
			}
			gltfOriginal := bytes.Clone(gltf)
			back, err := GLTFToGLB(context.Background(), gltf, testPolicy(), testLimits())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(gltf, gltfOriginal) {
				t.Fatal("reverse mutated source")
			}
			bj, bb := parts(back)
			if !bytes.Equal(j, bj) {
				t.Fatal("round trip changed non-container JSON bytes")
			}
			if sha256.Sum256(logical(t, bj, bb)) != wantHash {
				t.Fatal("round trip changed logical buffer")
			}
			// Named UV/image ranges independently guard against payload relocation.
			if opts.Textured {
				views := d["bufferViews"].([]any)
				for _, index := range []int{len(views) - 2, len(views) - 1} {
					v := views[index].(map[string]any)
					off, n := v["byteOffset"].(int), v["byteLength"].(int)
					if sha256.Sum256(bb[off:off+n]) != sha256.Sum256(b[off:off+n]) {
						t.Fatal("UV/image hash changed")
					}
				}
			}
		})
	}
}

func TestReverseRejectsDuplicateKeysBeforeRemovingURI(t *testing.T) {
	good := selfContained(t, modelinputtest.Triangle(modelinputtest.Options{}))
	var d map[string]json.RawMessage
	if err := json.Unmarshal(good, &d); err != nil {
		t.Fatal(err)
	}
	uriStart := bytes.Index(good, []byte(`"uri":`))
	uriEnd := uriStart + len(`"uri":`)
	for good[uriEnd] != '"' {
		uriEnd++
	}
	uriEnd++
	for good[uriEnd] != '"' {
		uriEnd++
	}
	uriEnd++
	dupURI := append(bytes.Clone(good[:uriEnd]), append([]byte(","), good[uriStart:]...)...)
	for name, raw := range map[string][]byte{"duplicate-uri": dupURI, "duplicate-root": append([]byte(`{"scene":0,`), good[1:]...), "escaped-key": bytes.Replace(good, []byte(`"byteLength":36`), []byte(`"byteLength":36,"byte\u004cength":36`), 1), "metadata": bytes.Replace(good, []byte(`"asset":{`), []byte(`"asset":{"extras":{"x":1,"x":2},`), 1)} {
		t.Run(name, func(t *testing.T) { assertFailure(t, GLTFToGLB, raw, testPolicy(), testLimits(), modelinput.Invalid) })
	}
}

func TestRejectsMalformedDependenciesAndUnsupportedModels(t *testing.T) {
	good := modelinputtest.Triangle(modelinputtest.Options{Indexed: true, Textured: true})
	j, b := parts(good)
	for name, change := range map[string]func(map[string]any){"animation": func(d map[string]any) { d["animations"] = []any{} }, "skin": func(d map[string]any) { d["skins"] = []any{} }, "extension": func(d map[string]any) { d["extensionsUsed"] = []any{"EXT_test"} }, "external-image": func(d map[string]any) { d["images"].([]any)[0].(map[string]any)["uri"] = "texture.png" }, "multiple-buffers": func(d map[string]any) { d["buffers"] = append(d["buffers"].([]any), map[string]any{"byteLength": 4}) }, "metadata-data-uri": func(d map[string]any) {
		d["extras"] = map[string]any{"dependency": "data:application/octet-stream;base64,AAAA"}
	}} {
		t.Run(name, func(t *testing.T) {
			d := document(t, j)
			change(d)
			raw := modelinputtest.GLB(d, b)
			assertFailure(t, GLBToGLTF, raw, testPolicy(), testLimits(), modelinput.Unsupported)
			assertFailure(t, GLTFToGLB, selfContained(t, raw), testPolicy(), testLimits(), modelinput.Unsupported)
		})
	}
	assertFailure(t, GLBToGLTF, []byte("not GLB"), testPolicy(), testLimits(), modelinput.Invalid)
	for name, uri := range map[string]string{"external": "model.bin", "network": "https://example.invalid/model.bin", "mime": "data:text/plain;base64,AAAA", "base64": "data:application/octet-stream;base64,!!!!", "noncanonical": "data:application/octet-stream;base64,AB=="} {
		t.Run(name, func(t *testing.T) {
			raw := selfContained(t, good)
			d := document(t, raw)
			d["buffers"].([]any)[0].(map[string]any)["uri"] = uri
			raw, _ = json.Marshal(d)
			kind := modelinput.Invalid
			if name == "external" || name == "network" || name == "mime" {
				kind = modelinput.Unsupported
			}
			assertFailure(t, GLTFToGLB, raw, testPolicy(), testLimits(), kind)
		})
	}
}

func TestBoundsAndCancellationHaveNoPartialOutput(t *testing.T) {
	glb := modelinputtest.Triangle(modelinputtest.Options{})
	gltf := selfContained(t, glb)
	for name, fn := range map[string]func(context.Context, []byte, modelinput.Policy, Limits) ([]byte, error){"forward": GLBToGLTF, "reverse": GLTFToGLB} {
		t.Run(name, func(t *testing.T) {
			raw := glb
			if name == "reverse" {
				raw = gltf
			}
			limits := testLimits()
			limits.MaxInputBytes = int64(len(raw) - 1)
			assertFailure(t, fn, raw, testPolicy(), limits, modelinput.Limit)
			limits = testLimits()
			limits.MaxOutputBytes = 16
			assertFailure(t, fn, raw, testPolicy(), limits, modelinput.Limit)
			limits = testLimits()
			limits.MaxWork = 10
			assertFailure(t, fn, raw, testPolicy(), limits, modelinput.Limit)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			out, err := fn(ctx, raw, testPolicy(), testLimits())
			var failure *modelinput.Error
			if out != nil || !errors.As(err, &failure) || failure.Kind != modelinput.Canceled {
				t.Fatalf("cancellation got %d %v", len(out), err)
			}
			limits = testLimits()
			limits.MaxInputBytes = 0
			assertFailure(t, fn, raw, testPolicy(), limits, modelinput.InvalidPolicy)
		})
	}
	// Encoded URI fits the wire limit but its decoded bytes exceed the profile;
	// rejection must happen before decoding or allocating the claimed payload.
	p := testPolicy()
	p.Limits.MaxBytes = 1024
	p.Limits.MaxJSONBytes = 1024
	d := document(t, gltf)
	d["buffers"].([]any)[0].(map[string]any)["uri"] = "data:application/octet-stream;base64," + strings.Repeat("AAAA", 400)
	bomb, _ := json.Marshal(d)
	assertFailure(t, GLTFToGLB, bomb, p, testLimits(), modelinput.Limit)
}
