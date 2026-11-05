package modelinspect

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// meshoptVector is one case of testdata/meshopt_vectors.json: a stream
// compressed by meshoptimizer's encoder and the bytes its decoder produced.
type meshoptVector struct {
	Name       string `json:"name"`
	Mode       string `json:"mode"`
	Filter     string `json:"filter"`
	Count      int    `json:"count"`
	Stride     int    `json:"stride"`
	Compressed []byte `json:"compressed"`
	Decoded    []byte `json:"decoded"`
}

func loadMeshoptVectors(t testing.TB) map[string]meshoptVector {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "meshopt_vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []meshoptVector `json:"cases"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	vectors := make(map[string]meshoptVector, len(file.Cases))
	for _, vector := range file.Cases {
		vectors[vector.Name] = vector
	}
	return vectors
}

func (v meshoptVector) source(t testing.TB) *meshoptSource {
	t.Helper()
	mode, ok := parseMeshoptMode(v.Mode)
	if !ok {
		t.Fatalf("%s: mode %q", v.Name, v.Mode)
	}
	filter, ok := parseMeshoptFilter(v.Filter)
	if !ok {
		t.Fatalf("%s: filter %q", v.Name, v.Filter)
	}
	source := &meshoptSource{stride: v.Stride, count: v.Count, mode: mode, filter: filter}
	if problem := source.problem(); problem != "" {
		t.Fatalf("%s: %s", v.Name, problem)
	}
	return source
}

// The Go decoder reproduces meshoptimizer's own decoder byte for byte on
// every lossless stream. Octahedral and quaternion filters are recomputed in
// float64 instead of float32, so a component may round one step apart.
func TestMeshoptDecoderMatchesReferenceDecoder(t *testing.T) {
	vectors := loadMeshoptVectors(t)
	if len(vectors) < 10 {
		t.Fatalf("only %d vectors loaded", len(vectors))
	}
	for name, vector := range vectors {
		t.Run(name, func(t *testing.T) {
			decoded, err := decodeMeshopt(vector.source(t), vector.Compressed)
			if err != nil {
				t.Fatalf("decodeMeshopt: %v", err)
			}
			if len(decoded) != len(vector.Decoded) {
				t.Fatalf("decoded %d bytes, want %d", len(decoded), len(vector.Decoded))
			}
			switch vector.Filter {
			case "OCTAHEDRAL", "QUATERNION":
				width := 2
				if vector.Stride == 4 {
					width = 1
				}
				for i := 0; i < len(decoded); i += width {
					got, want := signedComponent(decoded[i:], width), signedComponent(vector.Decoded[i:], width)
					if math.Abs(float64(got-want)) > 1 {
						t.Fatalf("component %d = %d, want %d +/- 1", i/width, got, want)
					}
				}
			default:
				if !bytes.Equal(decoded, vector.Decoded) {
					t.Fatalf("decoded bytes differ from the reference decoder")
				}
			}
		})
	}
}

func signedComponent(data []byte, width int) int {
	if width == 1 {
		return int(int8(data[0]))
	}
	return int(int16(binary.LittleEndian.Uint16(data)))
}

// A stream must end exactly at its tail: a missing or extra byte, a foreign
// header or a codec version the extension does not define is rejected, and
// no corruption of a valid stream panics or reads past it.
func TestMeshoptDecoderRejectsMalformedStreams(t *testing.T) {
	vectors := loadMeshoptVectors(t)
	for _, name := range []string{"vertex_positions", "vertex_small_deltas", "triangles_grid_u16", "triangles_soup_u32", "sequence_u32"} {
		vector := vectors[name]
		source := vector.source(t)
		t.Run(name, func(t *testing.T) {
			truncated := vector.Compressed[:len(vector.Compressed)-1]
			if _, err := decodeMeshopt(source, truncated); !errors.Is(err, ErrInvalid) {
				t.Fatalf("truncated stream: err = %v, want ErrInvalid", err)
			}
			extended := append(append([]byte(nil), vector.Compressed...), 0)
			if _, err := decodeMeshopt(source, extended); !errors.Is(err, ErrInvalid) {
				t.Fatalf("stream with a trailing byte: err = %v, want ErrInvalid", err)
			}
			foreign := append([]byte(nil), vector.Compressed...)
			foreign[0] = 0x10
			if _, err := decodeMeshopt(source, foreign); !errors.Is(err, ErrInvalid) {
				t.Fatalf("foreign header: err = %v, want ErrInvalid", err)
			}
			newer := append([]byte(nil), vector.Compressed...)
			newer[0] = vector.Compressed[0]&0xf0 | 0x02
			if _, err := decodeMeshopt(source, newer); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("unknown codec version: err = %v, want ErrUnsupported", err)
			}
			for position := 1; position < len(vector.Compressed); position++ {
				for _, value := range []byte{0x00, 0x0f, 0x80, 0xff} {
					corrupted := append([]byte(nil), vector.Compressed...)
					corrupted[position] = value
					// Either outcome is acceptable; the decoder must not panic.
					_, _ = decodeMeshopt(source, corrupted)
				}
			}
		})
	}
	vertex := vectors["vertex_constant"]
	version1 := append([]byte(nil), vertex.Compressed...)
	version1[0] = 0xa1
	if _, err := decodeMeshopt(vertex.source(t), version1); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("vertex codec version 1: err = %v, want ErrUnsupported (KHR_meshopt_compression only)", err)
	}
}

// meshoptCubeDocument is cubeDocument with its positions and indices stored
// only as EXT_meshopt_compression streams: the views live in a fallback
// buffer without data, as gltfpack writes them.
func meshoptCubeDocument(t testing.TB) *testGLB {
	t.Helper()
	vectors := loadMeshoptVectors(t)
	positions, triangles := vectors["cube_positions"], vectors["cube_triangles"]
	b := newTestGLB()
	place := func(data []byte) int {
		b.align4()
		offset := b.bin.Len()
		b.bin.Write(data)
		return offset
	}
	positionsOffset := place(positions.Compressed)
	trianglesOffset := place(triangles.Compressed)
	positionView := b.appendDoc("bufferViews", map[string]any{
		"buffer": 1, "byteOffset": 0, "byteLength": 96, "byteStride": 12,
		"extensions": map[string]any{"EXT_meshopt_compression": map[string]any{
			"buffer": 0, "byteOffset": positionsOffset, "byteLength": len(positions.Compressed),
			"byteStride": 12, "count": 8, "mode": "ATTRIBUTES",
		}},
	})
	indexView := b.appendDoc("bufferViews", map[string]any{
		"buffer": 1, "byteOffset": 96, "byteLength": 72,
		"extensions": map[string]any{"EXT_meshopt_compression": map[string]any{
			"buffer": 0, "byteOffset": trianglesOffset, "byteLength": len(triangles.Compressed),
			"byteStride": 2, "count": 36, "mode": "TRIANGLES",
		}},
	})
	b.extraBuffers = []any{map[string]any{
		"byteLength": 168,
		"extensions": map[string]any{"EXT_meshopt_compression": map[string]any{"fallback": true}},
	}}
	position := b.addAccessor(positionView, 0, 5126, 8, "VEC3", false,
		[]float64{-0.5, -0.5, -0.5}, []float64{0.5, 0.5, 0.5})
	index := b.addAccessor(indexView, 0, 5123, 36, "SCALAR", false, nil, nil)
	mesh := b.addMesh([]any{map[string]any{
		"attributes": map[string]int{"POSITION": position},
		"indices":    index,
	}})
	b.addScene([]int{b.addNode(map[string]any{"name": "cube", "mesh": mesh})})
	b.doc["extensionsUsed"] = []string{"EXT_meshopt_compression"}
	b.doc["extensionsRequired"] = []string{"EXT_meshopt_compression"}
	return b
}

// A meshopt-compressed cube inspects and renders like the uncompressed one.
func TestMeshoptCompressedCubeMatchesUncompressedCube(t *testing.T) {
	compressed := mustParseModel(t, meshoptCubeDocument(t).finish(), DefaultLimits())
	plain := mustParseModel(t, cubeGLB(), DefaultLimits())
	got, want := Inspect(compressed, DefaultInspectOptions()), Inspect(plain, DefaultInspectOptions())
	if got.Triangles != want.Triangles || got.Vertices != want.Vertices || got.BBox != want.BBox ||
		got.Topology != want.Topology {
		t.Fatalf("compressed report = %+v / %+v, want %+v / %+v", got.BBox, got.Topology, want.BBox, want.Topology)
	}
	if !got.Topology.Watertight || containsWarning(got, "read_error") {
		t.Fatalf("compressed cube: topology %+v warnings %v", got.Topology, got.Warnings)
	}
	request := RenderRequest{Width: 48, Height: 48, View: ViewThreeQuarter, Mode: ModeClay, Workers: 1}
	gotImage, wantImage := renderOrFail(t, compressed, request), renderOrFail(t, plain, request)
	// The triangle codec may rotate each triangle's corners, which can move
	// an edge pixel; the pictures must still agree almost everywhere.
	different := 0
	for i := range gotImage.Pix {
		if absByte(gotImage.Pix[i], wantImage.Pix[i]) > 2 {
			different++
		}
	}
	if different > len(gotImage.Pix)/200 {
		t.Fatalf("%d of %d channel values differ from the uncompressed render", different, len(gotImage.Pix))
	}
}

// Decoded views stay cached, so they are bounded like the file: a stream
// that would unpack beyond MaxBytes is refused before anything is decoded.
func TestMeshoptDecodedSizeIsBoundedByMaxBytes(t *testing.T) {
	b := meshoptCubeDocument(t)
	views := b.doc["bufferViews"].([]any)
	view := views[0].(map[string]any)
	// 1 Mi elements of 12 bytes: 12 MiB once decoded.
	view["byteLength"] = 12 << 20
	view["extensions"].(map[string]any)["EXT_meshopt_compression"].(map[string]any)["count"] = 1 << 20
	views[1].(map[string]any)["byteOffset"] = 12 << 20
	b.extraBuffers[0].(map[string]any)["byteLength"] = 12<<20 + 72
	data := b.finish()
	limits := DefaultLimits()
	limits.MaxBytes = 1 << 20
	if _, err := Parse(bytes.NewReader(data), int64(len(data)), limits); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Parse with 12 MiB of declared meshopt data and MaxBytes 1 MiB: err = %v, want ErrTooLarge", err)
	}
	limits.MaxBytes = 16 << 20
	if _, err := Parse(bytes.NewReader(data), int64(len(data)), limits); err != nil {
		t.Fatalf("Parse within MaxBytes: %v", err)
	}
}

// A corrupt compressed range is found when the view is decoded: Inspect
// reports it as a read error and Render refuses the model; nothing panics.
func TestMeshoptCorruptStreamIsAReadError(t *testing.T) {
	b := meshoptCubeDocument(t)
	data := b.finish()
	vectors := loadMeshoptVectors(t)
	// Drop the positions stream's header byte so its tail no longer lines up.
	offset := bytes.Index(data, vectors["cube_positions"].Compressed)
	if offset < 0 {
		t.Fatal("compressed positions not found in the GLB")
	}
	data[offset] = 0x00
	model := mustParseModel(t, data, DefaultLimits())
	report := Inspect(model, DefaultInspectOptions())
	if !containsWarning(report, "read_error") || report.Topology.Watertight {
		t.Fatalf("report with a corrupt stream: warnings %v topology %+v", report.Warnings, report.Topology)
	}
	if _, err := Render(model, RenderRequest{Width: 16, Height: 16, Workers: 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Render with a corrupt stream: err = %v, want ErrInvalid", err)
	}
}

func TestMeshoptStructureIsValidated(t *testing.T) {
	cases := map[string]func(*testGLB){
		"fallback view without the extension": func(b *testGLB) {
			delete(b.doc["bufferViews"].([]any)[0].(map[string]any), "extensions")
		},
		"compressed data outside the BIN chunk": func(b *testGLB) {
			extension(b, 0)["byteOffset"] = 1 << 20
		},
		"attribute stride not a multiple of four": func(b *testGLB) {
			extension(b, 0)["byteStride"] = 6
		},
		"unknown mode": func(b *testGLB) {
			extension(b, 0)["mode"] = "MESHLETS"
		},
		"view longer than its decoded data": func(b *testGLB) {
			extension(b, 0)["count"] = 4
		},
		"filter on index data": func(b *testGLB) {
			extension(b, 1)["filter"] = "OCTAHEDRAL"
		},
		"second buffer that is not a fallback": func(b *testGLB) {
			delete(b.extraBuffers[0].(map[string]any), "extensions")
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			b := meshoptCubeDocument(t)
			mutate(b)
			data := b.finish()
			model, err := Parse(bytes.NewReader(data), int64(len(data)), DefaultLimits())
			if err == nil || model != nil {
				t.Fatalf("Parse accepted the document: err = %v", err)
			}
			if !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrUnsupported) {
				t.Fatalf("err = %v, want ErrInvalid or ErrUnsupported", err)
			}
		})
	}
}

func extension(b *testGLB, view int) map[string]any {
	return b.doc["bufferViews"].([]any)[view].(map[string]any)["extensions"].(map[string]any)["EXT_meshopt_compression"].(map[string]any)
}

// Draco and KHR_meshopt_compression are reported as a CompressionError that
// names the extension, even when another unsupported extension is required
// too, so a caller can explain that the file is compressed.
func TestParseRequiredCompressionIsACompressionError(t *testing.T) {
	for _, extensionName := range []string{"KHR_draco_mesh_compression", "KHR_meshopt_compression"} {
		builder := cubeDocument()
		builder.doc["extensionsRequired"] = []string{"VENDOR_unknown_extension", extensionName}
		data := builder.finish()
		_, err := Parse(bytes.NewReader(data), int64(len(data)), DefaultLimits())
		var compressed *CompressionError
		if !errors.As(err, &compressed) || compressed.Extension != extensionName {
			t.Fatalf("%s: err = %v, want a CompressionError naming it", extensionName, err)
		}
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("%s: CompressionError does not wrap ErrUnsupported", extensionName)
		}
	}
	builder := cubeDocument()
	builder.doc["extensionsRequired"] = []string{"KHR_texture_basisu"}
	data := builder.finish()
	_, err := Parse(bytes.NewReader(data), int64(len(data)), DefaultLimits())
	var compressed *CompressionError
	if errors.As(err, &compressed) || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("KHR_texture_basisu: err = %v, want a plain ErrUnsupported", err)
	}
}

// gltfpack quantizes by default, so meshopt files require
// KHR_mesh_quantization: integer positions are placed by the node scale.
func TestParseAcceptsMeshQuantization(t *testing.T) {
	b := newTestGLB()
	corners := cubePositions()
	quantized := make([]byte, 8*8) // SHORT VEC3 padded to an 8-byte stride
	for vertex := 0; vertex < 8; vertex++ {
		for component := 0; component < 3; component++ {
			value := int16(corners[vertex*3+component] * 2)
			binary.LittleEndian.PutUint16(quantized[vertex*8+component*2:], uint16(value))
		}
	}
	view := b.addBufferView(quantized, 8)
	position := b.addAccessor(view, 0, 5122, 8, "VEC3", false, []float64{-1, -1, -1}, []float64{1, 1, 1})
	indices := b.addIndices16(flattenFaces(cubeFaces()))
	mesh := b.addMesh([]any{map[string]any{
		"attributes": map[string]int{"POSITION": position}, "indices": indices,
	}})
	b.addScene([]int{b.addNode(map[string]any{"mesh": mesh, "scale": []float64{0.5, 0.5, 0.5}})})
	b.doc["extensionsUsed"] = []string{"KHR_mesh_quantization"}
	b.doc["extensionsRequired"] = []string{"KHR_mesh_quantization"}
	report := Inspect(mustParseModel(t, b.finish(), DefaultLimits()), DefaultInspectOptions())
	for axis := 0; axis < 3; axis++ {
		approx(t, report.BBox.Min[axis], -0.5, 1e-9, "quantized bbox min")
		approx(t, report.BBox.Max[axis], 0.5, 1e-9, "quantized bbox max")
	}
	if !report.Topology.Watertight {
		t.Fatalf("quantized cube topology = %+v", report.Topology)
	}
}

// KHR_texture_transform moves the sampled texture: an offset of half the
// texture height swaps the red top row and the blue bottom row.
func TestRenderAppliesTextureTransform(t *testing.T) {
	red := color.NRGBA{R: 255, A: 255}
	blue := color.NRGBA{B: 255, A: 255}
	b := texturedQuadDocument(t, [4]color.NRGBA{red, red, blue, blue}, [4]float64{1, 1, 1, 1})
	material := b.doc["materials"].([]any)[0].(map[string]any)
	info := material["pbrMetallicRoughness"].(map[string]any)["baseColorTexture"].(map[string]any)
	info["extensions"] = map[string]any{"KHR_texture_transform": map[string]any{"offset": []float64{0, 0.5}}}
	b.doc["extensionsUsed"] = []string{"KHR_texture_transform"}
	b.doc["extensionsRequired"] = []string{"KHR_texture_transform"}
	model := mustParseModel(t, b.finish(), DefaultLimits())
	if transform := model.Materials[0].BaseColorTexture.Transform; transform == nil || transform.Offset != [2]float64{0, 0.5} {
		t.Fatalf("parsed transform = %+v", transform)
	}
	img := renderOrFail(t, model, RenderRequest{Width: 64, Height: 64, View: ViewFront, Mode: ModeTextured, Workers: 1})
	top, bottom := img.NRGBAAt(32, 16), img.NRGBAAt(32, 48)
	if top.B < 150 || top.R > 100 {
		t.Fatalf("top pixel = %v, want the blue row after the offset", top)
	}
	if bottom.R < 150 || bottom.B > 100 {
		t.Fatalf("bottom pixel = %v, want the red row after the offset", bottom)
	}
}
