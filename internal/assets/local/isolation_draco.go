package local

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// These are parser resource ceilings, independent of Provider/product settings.
const maxDracoBytes int64 = 64 << 20
const maxDracoElements int64 = 10_000_000
const maxDracoAttributes = 16
const dracoTimeout = 60 * time.Second

//go:embed draco/inspect.wasm
var dracoModule []byte

var dracoEngine struct {
	once     sync.Once
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
	err      error
	slot     chan struct{}
}

type dracoAttribute struct {
	id, component, components, normalized, bounds uint32
	min, max                                      [4]float64
}
type dracoRange struct {
	indexBounds                                 uint32
	indexMin, indexMax                          [4]float64
	start, end, points, indices, work, scanWork int64
	indexType                                   uint32
	attributes                                  []dracoAttribute
}

func dracoError() error {
	return errors.New("Draco 压缩网格无效、不受支持或超出检查预算")
}

func dracoAccessor(document map[string]any, index any) (map[string]any, error) {
	n, ok := index.(json.Number)
	if !ok {
		return nil, dracoError()
	}
	i, err := n.Int64()
	accessors, arrErr := gltfArray(document, "accessors")
	if err != nil || arrErr != nil || i < 0 || i >= int64(len(accessors)) {
		return nil, dracoError()
	}
	a, ok := accessors[i].(map[string]any)
	if !ok || a["sparse"] != nil {
		return nil, dracoError()
	}
	return a, nil
}

func dracoBounds(a map[string]any, key string, count int, values *[4]float64) (bool, error) {
	raw, present := a[key]
	if !present {
		return false, nil
	}
	array, ok := raw.([]any)
	if !ok || len(array) != count {
		return false, dracoError()
	}
	for i, raw := range array {
		n, ok := raw.(json.Number)
		if !ok {
			return false, dracoError()
		}
		v, err := n.Float64()
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return false, dracoError()
		}
		values[i] = v
	}
	return true, nil
}

func dracoListed(document map[string]any, key string) bool {
	entries, err := gltfArray(document, key)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry == "KHR_draco_mesh_compression" {
			return true
		}
	}
	return false
}

// Plan only explicitly bound GLB buffer-zero ranges. Other glTF buffers retain
// the existing unknown-byte policy; no speculative compressed-span exemptions.
func glbDracoRanges(value any, length int64, images []gltfImageRange, fields []inspectionField) ([]dracoRange, error) {
	document, ok := value.(map[string]any)
	if !ok {
		return nil, dracoError()
	}
	meshes, err := gltfArray(document, "meshes")
	if err != nil {
		return nil, err
	}
	views, err := gltfArray(document, "bufferViews")
	if err != nil {
		return nil, err
	}
	var ranges []dracoRange
	for _, rawMesh := range meshes {
		mesh, ok := rawMesh.(map[string]any)
		if !ok {
			return nil, dracoError()
		}
		primitives, err := gltfArray(mesh, "primitives")
		if err != nil {
			return nil, err
		}
		for _, rawPrimitive := range primitives {
			primitive, ok := rawPrimitive.(map[string]any)
			if !ok {
				return nil, dracoError()
			}
			extensions, _ := primitive["extensions"].(map[string]any)
			raw, exists := extensions["KHR_draco_mesh_compression"]
			if !exists {
				continue
			}
			ext, ok := raw.(map[string]any)
			if !ok || !dracoListed(document, "extensionsUsed") {
				return nil, dracoError()
			}
			mode, err := gltfInteger(primitive, "mode", 4)
			if err != nil || mode != 4 {
				return nil, dracoError()
			}
			vi, err := gltfInteger(ext, "bufferView", -1)
			if err != nil || vi < 0 || vi >= int64(len(views)) {
				return nil, dracoError()
			}
			view, ok := views[vi].(map[string]any)
			if !ok {
				return nil, dracoError()
			}
			buffer, err := gltfInteger(view, "buffer", -1)
			if err != nil || buffer != 0 || view["byteStride"] != nil {
				return nil, dracoError()
			}
			buffers, err := gltfArray(document, "buffers")
			if err != nil || len(buffers) == 0 {
				return nil, dracoError()
			}
			b, ok := buffers[0].(map[string]any)
			if !ok || b["uri"] != nil {
				return nil, dracoError()
			}
			start, err := gltfInteger(view, "byteOffset", 0)
			if err != nil {
				return nil, err
			}
			size, err := gltfInteger(view, "byteLength", -1)
			if err != nil || size < 12 || size > maxDracoBytes || start > length || size > length-start {
				return nil, dracoError()
			}
			r := dracoRange{start: start, end: start + size}
			index, err := dracoAccessor(document, primitive["indices"])
			if err != nil {
				return nil, err
			}
			count, err := gltfInteger(index, "count", -1)
			if err != nil || count <= 0 || count%3 != 0 || count/3 > maxDracoElements || index["type"] != "SCALAR" {
				return nil, dracoError()
			}
			it, err := gltfInteger(index, "componentType", -1)
			if err != nil || (it != 5121 && it != 5123 && it != 5125) {
				return nil, dracoError()
			}
			if v, exists := index["normalized"]; exists && v != false {
				return nil, dracoError()
			}
			r.indices, r.indexType = count, uint32(it)
			if exists, err := dracoBounds(index, "min", 1, &r.indexMin); err != nil {
				return nil, err
			} else if exists {
				r.indexBounds |= 1
			}
			if exists, err := dracoBounds(index, "max", 1, &r.indexMax); err != nil {
				return nil, err
			} else if exists {
				r.indexBounds |= 2
			}
			needsRequired := index["bufferView"] == nil
			attrs, ok := ext["attributes"].(map[string]any)
			if !ok || len(attrs) == 0 || len(attrs) > maxDracoAttributes {
				return nil, dracoError()
			}
			declared, ok := primitive["attributes"].(map[string]any)
			if !ok {
				return nil, dracoError()
			}
			if attrs["POSITION"] == nil {
				return nil, dracoError()
			}
			var perPoint int64
			ids := map[uint32]bool{}
			for semantic, idValue := range attrs {
				id, err := gltfInteger(map[string]any{"id": idValue}, "id", -1)
				if err != nil || id < 0 || id > math.MaxUint32 || ids[uint32(id)] {
					return nil, dracoError()
				}
				ids[uint32(id)] = true
				a, err := dracoAccessor(document, declared[semantic])
				if err != nil {
					return nil, err
				}
				needsRequired = needsRequired || a["bufferView"] == nil
				count, err := gltfInteger(a, "count", -1)
				if err != nil || count <= 0 || count > maxDracoElements || (r.points != 0 && r.points != count) {
					return nil, dracoError()
				}
				r.points = count
				component, err := gltfInteger(a, "componentType", -1)
				if err != nil {
					return nil, err
				}
				width := int64(0)
				switch component {
				case 5120, 5121:
					width = 1
				case 5122, 5123:
					width = 2
				case 5125, 5126:
					width = 4
				default:
					return nil, dracoError()
				}
				components := uint32(0)
				switch a["type"] {
				case "SCALAR":
					components = 1
				case "VEC2":
					components = 2
				case "VEC3":
					components = 3
				case "VEC4":
					components = 4
				default:
					return nil, dracoError()
				}
				if semantic == "POSITION" && (component != 5126 || components != 3) {
					return nil, dracoError()
				}
				binding := dracoAttribute{id: uint32(id), component: uint32(component), components: components}
				if normalized, exists := a["normalized"]; exists {
					v, ok := normalized.(bool)
					if !ok {
						return nil, dracoError()
					}
					if v {
						binding.normalized = 1
						if component == 5125 || component == 5126 {
							return nil, dracoError()
						}
					}
				}
				if exists, err := dracoBounds(a, "min", int(components), &binding.min); err != nil {
					return nil, err
				} else if exists {
					binding.bounds |= 1
				}
				if exists, err := dracoBounds(a, "max", int(components), &binding.max); err != nil {
					return nil, err
				} else if exists {
					binding.bounds |= 2
				}
				if semantic == "POSITION" && binding.bounds != 3 {
					return nil, dracoError()
				}
				for i := uint32(0); i < components; i++ {
					if binding.bounds == 3 && binding.min[i] > binding.max[i] {
						return nil, dracoError()
					}
				}
				perPoint += width*int64(components) + 4
				r.attributes = append(r.attributes, binding)
			}
			for semantic, index := range declared {
				if _, compressed := attrs[semantic]; compressed {
					continue
				}
				a, err := dracoAccessor(document, index)
				if err != nil {
					return nil, err
				}
				count, err := gltfInteger(a, "count", -1)
				if err != nil || count != r.points {
					return nil, dracoError()
				}
			}
			if needsRequired && !dracoListed(document, "extensionsRequired") {
				return nil, dracoError()
			}
			sort.Slice(r.attributes, func(i, j int) bool { return r.attributes[i].id < r.attributes[j].id })
			// Reserve one value/index traversal plus the exact host scan stream,
			// including the scanner's overlap rereads. Decoding remains independently
			// bounded by the WASM memory ceiling and shared deadline.
			numericBytes := r.points*perPoint + r.indices*4
			r.scanWork = numericBytes + inspectionCeilChunks(numericBytes)*isolationOverlap
			r.work = (1 << 20) + 2*size + numericBytes + r.scanWork
			if r.work > maxInspectionWork {
				return nil, inspectionWorkError()
			}
			for _, image := range images {
				if r.start < image.end && r.end > image.start {
					return nil, dracoError()
				}
			}
			for _, field := range fields {
				if r.start < field.end() && r.end > field.start {
					return nil, dracoError()
				}
			}
			duplicate := false
			for _, other := range ranges {
				if r.start < other.end && r.end > other.start {
					if reflect.DeepEqual(r, other) {
						duplicate = true
						break
					}
					return nil, dracoError()
				}
			}
			if !duplicate {
				if len(ranges) >= 64 {
					return nil, dracoError()
				}
				ranges = append(ranges, r)
			}
		}
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
	return ranges, nil
}

func dracoRequest(data []byte, r dracoRange) []byte {
	var request bytes.Buffer
	put := func(v uint32) { _ = binary.Write(&request, binary.LittleEndian, v) }
	put(0x31435244)
	put(uint32(len(data)))
	put(uint32(r.points))
	put(uint32(r.indices / 3))
	put(uint32(len(r.attributes)))
	put(r.indexType)
	put(r.indexBounds)
	_ = binary.Write(&request, binary.LittleEndian, r.indexMin[0])
	_ = binary.Write(&request, binary.LittleEndian, r.indexMax[0])
	for _, a := range r.attributes {
		put(a.id)
		put(a.component)
		put(a.components)
		put(a.normalized)
		put(a.bounds)
		for i := uint32(0); i < a.components; i++ {
			_ = binary.Write(&request, binary.LittleEndian, a.min[i])
			_ = binary.Write(&request, binary.LittleEndian, a.max[i])
		}
	}
	// The frame and immutable input are streamed separately by the runner.
	return request.Bytes()
}

// The only custom WASM import receives a borrowed memory view and never keeps
// it. Reuse the same bounded scanner as ordinary typed numeric fields so percent,
// JSON, HTML entity and UTF-16 handling cannot drift between wire formats.
type dracoScanContextKey struct{}
type dracoNumericScan struct {
	scanner   addressScanner
	remaining int64
	scanned   int64
	failed    bool
}

func newDracoNumericScan(limit int64) *dracoNumericScan {
	return &dracoNumericScan{scanner: addressScanner{initial: absoluteAddress, continuation: absoluteAddress}, remaining: limit}
}

func (s *dracoNumericScan) consume(ctx context.Context, data []byte) bool {
	cost := int64(len(data) + len(s.scanner.previous))
	if s.failed || ctx.Err() != nil || len(data) == 0 || len(data) > isolationChunkBytes || cost > s.remaining {
		s.failed = true
		return false
	}
	s.remaining -= cost
	s.scanned += int64(len(data))
	// Each synchronous host call is bounded to 64 KiB plus existing carry. Check
	// cancellation both sides; the WASM deadline also covers these host scans.
	if s.scanner.contains(data) || ctx.Err() != nil {
		s.failed = true
		return false
	}
	return true
}

func instantiateDracoScanner(ctx context.Context, runtime wazero.Runtime) error {
	_, err := runtime.NewHostModuleBuilder("art_ai").NewFunctionBuilder().WithFunc(func(ctx context.Context, module api.Module, offset, length uint32) uint32 {
		scan, ok := ctx.Value(dracoScanContextKey{}).(*dracoNumericScan)
		if !ok || ctx.Err() != nil || length == 0 || length > isolationChunkBytes {
			return 0
		}
		data, ok := module.Memory().Read(offset, length)
		if !ok || !scan.consume(ctx, data) {
			return 0
		}
		return 1
	}).Export("scan_numeric").Instantiate(ctx)
	return err
}

func initializeDraco() {
	ctx := context.Background()
	dracoEngine.slot = make(chan struct{}, 1)
	dracoEngine.runtime = wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithMemoryLimitPages(16384).WithCloseOnContextDone(true))
	_, dracoEngine.err = wasi_snapshot_preview1.Instantiate(ctx, dracoEngine.runtime)
	if dracoEngine.err == nil {
		dracoEngine.err = instantiateDracoScanner(ctx, dracoEngine.runtime)
	}
	if dracoEngine.err == nil {
		dracoEngine.compiled, dracoEngine.err = dracoEngine.runtime.CompileModule(ctx, dracoModule)
	}
}

// A trusted compiled module is shared; every untrusted decode receives a new
// instance with no preopened paths, environment, clocks, randomness or sockets.
func validateDraco(data []byte, r dracoRange, work *inspectionWorkBudget) error {
	if work.dracoDeadline.IsZero() {
		work.dracoDeadline = time.Now().Add(dracoTimeout)
	}
	ctx, cancel := context.WithDeadline(context.Background(), work.dracoDeadline)
	defer cancel()
	return validateDracoContext(ctx, data, r)
}
func validateDracoContext(ctx context.Context, data []byte, r dracoRange) error {
	dracoEngine.once.Do(initializeDraco)
	if dracoEngine.err != nil {
		return dracoError()
	}
	select {
	case dracoEngine.slot <- struct{}{}:
		defer func() { <-dracoEngine.slot }()
	case <-ctx.Done():
		return dracoError()
	}
	// r.work was reserved by the shared asset budget before this call. It
	// includes decoded values, mappings, indices and traversal/carry headroom.
	scan := newDracoNumericScan(r.scanWork)
	ctx = context.WithValue(ctx, dracoScanContextKey{}, scan)
	var output dracoReceipt
	input := bytes.NewReader(data)
	module, err := dracoEngine.runtime.InstantiateModule(ctx, dracoEngine.compiled, wazero.NewModuleConfig().WithName("").WithStdin(io.MultiReader(bytes.NewReader(dracoRequest(data, r)), input)).WithStdout(&output))
	if module != nil {
		_ = module.Close(context.Background())
	}
	if err != nil || scan.failed || scan.scanned == 0 || ctx.Err() != nil || string(output.data[:output.n]) != "DRC1OK\r\n" {
		return dracoError()
	}
	return nil
}

type dracoReceipt struct {
	data [8]byte
	n    int
}

func (r *dracoReceipt) Write(p []byte) (int, error) {
	if len(p) > len(r.data)-r.n {
		return 0, dracoError()
	}
	copy(r.data[r.n:], p)
	r.n += len(p)
	return len(p), nil
}
