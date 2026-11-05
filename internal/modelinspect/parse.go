package modelinspect

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"
)

const (
	glbMagic       = 0x46546C67 // "glTF"
	glbVersion     = 2
	chunkJSON      = 0x4E4F534A
	chunkBIN       = 0x004E4942
	glbHeaderLen   = 12
	chunkHeaderLen = 8
)

// supportedExtensions lists the required extensions this package
// reproduces. Every other extension listed in extensionsRequired changes
// semantics this package cannot reproduce, so the safe answer is
// ErrUnsupported rather than a silently wrong report.
//
//   - KHR_mesh_quantization only widens the component types attributes may
//     use; every accessor is already decoded through decodeComponentF32.
//   - EXT_meshopt_compression views are decoded by meshopt.go.
//   - KHR_texture_transform is applied when Render samples a texture.
var supportedExtensions = map[string]bool{
	"KHR_mesh_quantization":   true,
	"EXT_meshopt_compression": true,
	"KHR_texture_transform":   true,
}

// compressionExtensions are required mesh compression extensions Parse
// cannot decode. They are reported as a CompressionError so a caller can
// explain that the file is compressed rather than malformed. Draco has no
// mature pure-Go decoder; KHR_meshopt_compression adds a vertex codec
// version and filter that meshopt.go does not implement.
var compressionExtensions = map[string]bool{
	"KHR_draco_mesh_compression": true,
	"KHR_meshopt_compression":    true,
}

// Parse reads and validates a GLB v2 container.
//
// The container and JSON structure are validated fully. Accessor payloads are
// only checked for declared bounds, then decoded lazily by Inspect and Render
// through r; keep r readable for the lifetime of the returned Model. External
// buffers and images are rejected with ErrUnsupported: this package never
// performs network or filesystem reads.
func Parse(r io.ReaderAt, size int64, limits Limits) (*Model, error) {
	if r == nil {
		return nil, invalidf("nil reader")
	}
	if size < glbHeaderLen {
		return nil, invalidf("file is shorter than the GLB header")
	}
	if size > math.MaxUint32 {
		return nil, invalidf("file is larger than the GLB length field can express")
	}
	if limits.MaxBytes > 0 && size > limits.MaxBytes {
		return nil, toolargef("file size exceeds MaxBytes")
	}

	header, err := readAt(r, 0, glbHeaderLen)
	if err != nil {
		return nil, err
	}
	if binary.LittleEndian.Uint32(header[0:4]) != glbMagic {
		return nil, invalidf("bad GLB magic")
	}
	if version := binary.LittleEndian.Uint32(header[4:8]); version != glbVersion {
		return nil, unsupportedf("GLB version %d", version)
	}
	if declared := int64(binary.LittleEndian.Uint32(header[8:12])); declared != size {
		return nil, invalidf("declared container length does not match file size")
	}

	jsonChunk, binStart, binLen, err := parseChunks(r, size)
	if err != nil {
		return nil, err
	}
	var doc gltfDocument
	if err := json.Unmarshal(jsonChunk, &doc); err != nil {
		return nil, invalidf("JSON chunk is not valid glTF JSON")
	}
	if err := checkRequiredExtensions(doc.ExtensionsRequired); err != nil {
		return nil, err
	}

	m := &Model{
		FileBytes:          size,
		ExtensionsUsed:     append([]string(nil), doc.ExtensionsUsed...),
		ExtensionsRequired: append([]string(nil), doc.ExtensionsRequired...),
		src:                r,
		limits:             limits,
		binStart:           binStart,
		binLen:             binLen,
		cache: accessorCache{
			f32:     make(map[int][]float32),
			u32:     make(map[int][]uint32),
			meshopt: make(map[int][]byte),
		},
		skinCache:  skinCache{matrices: make(map[int][]mat4)},
		jointWorld: make(map[int]mat4),
		textureCache: textureCache{
			images:  make(map[int]*decodedTexture),
			decoded: make(map[int]*decodedImage),
		},
	}
	if err := m.loadBuffersAndViews(doc.Buffers, doc.BufferViews); err != nil {
		return nil, err
	}
	if err := m.loadAccessors(doc.Accessors); err != nil {
		return nil, err
	}
	if err := m.loadImages(doc.Images); err != nil {
		return nil, err
	}
	if err := m.loadTextures(doc.Samplers, doc.Textures); err != nil {
		return nil, err
	}
	if err := m.loadMaterials(doc.Materials); err != nil {
		return nil, err
	}
	if err := m.loadMeshes(doc.Meshes); err != nil {
		return nil, err
	}
	if err := m.loadNodes(doc.Nodes, limits); err != nil {
		return nil, err
	}
	if err := m.loadSkins(doc.Skins); err != nil {
		return nil, err
	}
	if err := m.loadAnimations(doc.Animations); err != nil {
		return nil, err
	}
	if err := m.loadScenes(doc.Scenes, doc.Scene, limits); err != nil {
		return nil, err
	}
	m.metadataBytes = m.estimateMetadataBytes()
	return m, nil
}

// checkRequiredExtensions rejects required extensions this package does not
// reproduce. A compression extension is reported first, because "the file
// is compressed" is the actionable reason even when other extensions are
// missing too.
func checkRequiredExtensions(required []string) error {
	for _, name := range required {
		if compressionExtensions[name] {
			return &CompressionError{Extension: name}
		}
	}
	for _, name := range required {
		if supportedExtensions[name] {
			continue
		}
		return unsupportedf("required extension %q", name)
	}
	return nil
}

// parseChunks validates the chunk table and returns the JSON bytes plus the
// absolute BIN chunk location. Unknown chunks are skipped, as GLB v2 allows.
func parseChunks(r io.ReaderAt, size int64) ([]byte, int64, int64, error) {
	var (
		jsonChunk []byte
		binStart  int64 = -1
		binLen    int64
	)
	offset := int64(glbHeaderLen)
	index := 0
	for offset < size {
		if size-offset < chunkHeaderLen {
			return nil, 0, 0, invalidf("truncated chunk header")
		}
		header, err := readAt(r, offset, chunkHeaderLen)
		if err != nil {
			return nil, 0, 0, err
		}
		length := int64(binary.LittleEndian.Uint32(header[0:4]))
		kind := binary.LittleEndian.Uint32(header[4:8])
		if length%4 != 0 {
			return nil, 0, 0, invalidf("chunk %d length is not 4-byte aligned", index)
		}
		dataStart := offset + chunkHeaderLen
		if length > size-dataStart {
			return nil, 0, 0, invalidf("chunk %d extends past the container", index)
		}
		switch {
		case index == 0 && kind != chunkJSON:
			return nil, 0, 0, invalidf("first GLB chunk is not JSON")
		case kind == chunkJSON && index != 0:
			return nil, 0, 0, invalidf("duplicate JSON chunk at index %d", index)
		}
		switch kind {
		case chunkJSON:
			if length == 0 {
				return nil, 0, 0, invalidf("empty JSON chunk")
			}
			jsonChunk, err = readAt(r, dataStart, length)
			if err != nil {
				return nil, 0, 0, err
			}
		case chunkBIN:
			if index != 1 {
				return nil, 0, 0, invalidf("BIN chunk is not the second chunk")
			}
			binStart, binLen = dataStart, length
		}
		offset = dataStart + length
		index++
	}
	if offset != size {
		return nil, 0, 0, invalidf("chunk table does not end at the container size")
	}
	if jsonChunk == nil {
		return nil, 0, 0, invalidf("missing JSON chunk")
	}
	return jsonChunk, binStart, binLen, nil
}

func (m *Model) loadBuffersAndViews(buffers []gltfBuffer, views []gltfBufferView) error {
	if len(buffers) == 0 {
		if len(views) != 0 {
			return invalidf("bufferViews exist without a buffer")
		}
		return nil
	}
	first := buffers[0]
	if first.URI != "" {
		if strings.HasPrefix(first.URI, "data:") {
			// TODO(#103): accept data: URIs with a caller-supplied bounded
			// reader. Network and filesystem reads stay out of this package.
			return unsupportedf("external buffer: data URI is not supported")
		}
		return unsupportedf("external buffer URI is not supported")
	}
	if first.ByteLength < 0 {
		return invalidf("buffer 0 has a negative byteLength")
	}
	if int64(first.ByteLength) > m.binLen {
		return invalidf("buffer 0 byteLength exceeds the BIN chunk")
	}
	if m.binLen-int64(first.ByteLength) > 3 {
		return invalidf("BIN chunk has more than 3 padding bytes")
	}
	// GLB embeds only buffer 0. A later buffer is acceptable only as an
	// EXT_meshopt_compression fallback, whose bytes this package never reads:
	// every view on it is decoded from its compressed copy instead.
	lengths := make([]int64, len(buffers))
	lengths[0] = int64(first.ByteLength)
	for i := 1; i < len(buffers); i++ {
		buffer := buffers[i]
		if buffer.Extensions == nil || buffer.Extensions.Meshopt == nil || !buffer.Extensions.Meshopt.Fallback {
			if buffer.URI != "" {
				return unsupportedf("external buffer URI is not supported")
			}
			return invalidf("only buffer 0 may be embedded")
		}
		if buffer.ByteLength < 0 {
			return invalidf("buffer %d has a negative byteLength", i)
		}
		lengths[i] = int64(buffer.ByteLength)
	}

	m.bufferViews = make([]bufferView, len(views))
	decoded := int64(0)
	for i, view := range views {
		if view.Buffer < 0 || view.Buffer >= len(buffers) {
			return invalidf("bufferView %d references buffer %d", i, view.Buffer)
		}
		if view.ByteOffset < 0 || view.ByteLength < 0 || view.ByteStride < 0 {
			return invalidf("bufferView %d has a negative offset, length or stride", i)
		}
		if view.ByteStride != 0 && (view.ByteStride%4 != 0 || view.ByteStride > 252) {
			return invalidf("bufferView %d has an invalid byteStride", i)
		}
		end := int64(view.ByteOffset) + int64(view.ByteLength)
		if end < int64(view.ByteOffset) || end > lengths[view.Buffer] {
			return invalidf("bufferView %d is out of buffer bounds", i)
		}
		parsed := bufferView{length: int64(view.ByteLength), stride: view.ByteStride}
		if view.Buffer == 0 {
			// Stored bytes are read directly even when a compressed copy
			// exists; both must hold the same data.
			parsed.offset = m.binStart + int64(view.ByteOffset)
			m.bufferViews[i] = parsed
			continue
		}
		var extension *gltfMeshoptView
		if view.Extensions != nil {
			extension = view.Extensions.Meshopt
		}
		if extension == nil {
			return unsupportedf("bufferView %d is in a fallback buffer without EXT_meshopt_compression", i)
		}
		source, err := m.meshoptSourceFor(i, view, extension, lengths[0])
		if err != nil {
			return err
		}
		// The decoded views stay cached, so they are bounded like the file:
		// a compressed model may not unpack to more than an uncompressed
		// GLB the caller would accept.
		decoded += source.decodedBytes()
		if m.limits.MaxBytes > 0 && decoded > m.limits.MaxBytes {
			return toolargef("decoded EXT_meshopt_compression data exceeds MaxBytes")
		}
		parsed.meshopt = source
		m.bufferViews[i] = parsed
	}
	return nil
}

// meshoptSourceFor validates the EXT_meshopt_compression extension of view
// index. The compressed bytes must lie in buffer 0, the GLB BIN chunk.
func (m *Model) meshoptSourceFor(index int, view gltfBufferView, extension *gltfMeshoptView, binBufferLength int64) (*meshoptSource, error) {
	if extension.Buffer == nil || extension.ByteLength == nil || extension.ByteStride == nil || extension.Count == nil {
		return nil, invalidf("bufferView %d EXT_meshopt_compression is incomplete", index)
	}
	if *extension.Buffer != 0 {
		return nil, unsupportedf("bufferView %d compressed data is outside the GLB BIN chunk", index)
	}
	offset, length := int64(extension.ByteOffset), int64(*extension.ByteLength)
	if offset < 0 || length < 0 || offset+length > binBufferLength {
		return nil, invalidf("bufferView %d compressed data is out of buffer bounds", index)
	}
	mode, ok := parseMeshoptMode(extension.Mode)
	if !ok {
		return nil, invalidf("bufferView %d EXT_meshopt_compression mode %q", index, extension.Mode)
	}
	filter, ok := parseMeshoptFilter(extension.Filter)
	if !ok {
		return nil, invalidf("bufferView %d EXT_meshopt_compression filter %q", index, extension.Filter)
	}
	source := &meshoptSource{
		offset: m.binStart + offset, length: length,
		stride: *extension.ByteStride, count: *extension.Count,
		mode: mode, filter: filter,
	}
	if problem := source.problem(); problem != "" {
		return nil, invalidf("bufferView %d EXT_meshopt_compression: %s", index, problem)
	}
	if source.decodedBytes() < int64(view.ByteLength) {
		return nil, invalidf("bufferView %d is longer than its decoded data", index)
	}
	return source, nil
}

func (m *Model) loadAccessors(defs []gltfAccessor) error {
	m.accessors = make([]accessor, len(defs))
	for i, def := range defs {
		if def.Count < 0 {
			return invalidf("accessor %d has a negative count", i)
		}
		componentSize, ok := componentByteSize(def.ComponentType)
		if !ok {
			return invalidf("accessor %d uses componentType %d", i, def.ComponentType)
		}
		if def.ComponentType == 5126 && def.Normalized {
			return invalidf("accessor %d marks a float accessor as normalized", i)
		}
		components, _, ok := accessorComponents(def.Type)
		if !ok {
			return invalidf("accessor %d has type %q", i, def.Type)
		}
		if len(def.Sparse) != 0 && !bytes.Equal(bytes.TrimSpace(def.Sparse), []byte("null")) {
			return unsupportedf("sparse accessor %d", i)
		}
		elemSize, err := accessorElementSize(def.Type, componentSize)
		if err != nil {
			return invalidf("accessor %d: %s", i, err.Error())
		}
		if def.BufferView == nil {
			if def.Count != 0 {
				return unsupportedf("accessor %d without a bufferView", i)
			}
			m.accessors[i] = accessor{
				view:  -1,
				count: def.Count, components: components, elemSize: elemSize,
				componentType: def.ComponentType, componentSize: componentSize,
				normalized: def.Normalized, stride: elemSize,
				min: append([]float64(nil), def.Min...),
				max: append([]float64(nil), def.Max...),
			}
			continue
		}
		viewIndex := *def.BufferView
		if viewIndex < 0 || viewIndex >= len(m.bufferViews) {
			return invalidf("accessor %d references bufferView %d", i, viewIndex)
		}
		view := m.bufferViews[viewIndex]
		if def.ByteOffset < 0 {
			return invalidf("accessor %d has a negative byteOffset", i)
		}
		if componentSize > 0 && def.ByteOffset%componentSize != 0 {
			return invalidf("accessor %d byteOffset is not component-aligned", i)
		}
		stride := view.stride
		if stride == 0 {
			stride = elemSize
		}
		if stride < elemSize {
			return invalidf("accessor %d stride is smaller than its element", i)
		}
		if int64(def.ByteOffset) > view.length {
			return invalidf("accessor %d byteOffset is outside its bufferView", i)
		}
		if def.Count > 0 && def.Count > maxIntValue {
			return invalidf("accessor %d count is too large", i)
		}
		if def.Count > 0 {
			// Compare by division so an enormous count cannot overflow the
			// multiplication; stride is positive and at least elemSize.
			remaining := view.length - int64(def.ByteOffset)
			if remaining < int64(elemSize) ||
				int64(def.Count-1) > (remaining-int64(elemSize))/int64(stride) {
				return invalidf("accessor %d is out of bufferView bounds", i)
			}
		}
		m.accessors[i] = accessor{
			srcOffset:     view.offset + int64(def.ByteOffset),
			view:          viewIndex,
			viewOffset:    int64(def.ByteOffset),
			count:         def.Count,
			components:    components,
			componentType: def.ComponentType,
			componentSize: componentSize,
			elemSize:      elemSize,
			normalized:    def.Normalized,
			stride:        stride,
			min:           append([]float64(nil), def.Min...),
			max:           append([]float64(nil), def.Max...),
		}
	}
	return nil
}

const maxIntValue = int(^uint(0) >> 1)

func componentByteSize(componentType int) (int, bool) {
	switch componentType {
	case 5120, 5121:
		return 1, true
	case 5122, 5123:
		return 2, true
	case 5125, 5126:
		return 4, true
	default:
		return 0, false
	}
}

// accessorComponents returns the component count for a type and whether the
// type is a matrix. Matrices are accepted structurally but never decoded.
func accessorComponents(typeName string) (components int, matrix bool, ok bool) {
	switch typeName {
	case "SCALAR":
		return 1, false, true
	case "VEC2":
		return 2, false, true
	case "VEC3":
		return 3, false, true
	case "VEC4":
		return 4, false, true
	case "MAT2":
		return 4, true, true
	case "MAT3":
		return 9, true, true
	case "MAT4":
		return 16, true, true
	default:
		return 0, false, false
	}
}

// accessorElementSize includes the column padding glTF requires for MAT2 and
// MAT3 elements with byte or short components.
func accessorElementSize(typeName string, componentSize int) (int, error) {
	switch typeName {
	case "SCALAR":
		return componentSize, nil
	case "VEC2":
		return 2 * componentSize, nil
	case "VEC3":
		return 3 * componentSize, nil
	case "VEC4":
		return 4 * componentSize, nil
	case "MAT2", "MAT3", "MAT4":
		n := 2
		if typeName == "MAT3" {
			n = 3
		} else if typeName == "MAT4" {
			n = 4
		}
		column := n * componentSize
		if column%4 != 0 {
			column += 4 - column%4
		}
		return n * column, nil
	default:
		return 0, invalidf("unknown accessor type")
	}
}

func (m *Model) loadImages(defs []gltfImage) error {
	m.Images = make([]Image, len(defs))
	for i, def := range defs {
		if def.URI != "" {
			return unsupportedf("external image URI is not supported")
		}
		if def.BufferView == nil {
			return invalidf("image %d has no bufferView", i)
		}
		viewIndex := *def.BufferView
		if viewIndex < 0 || viewIndex >= len(m.bufferViews) {
			return invalidf("image %d references bufferView %d", i, viewIndex)
		}
		mime := strings.ToLower(strings.TrimSpace(def.MIMEType))
		var want string
		switch mime {
		case "image/png":
			want = "png"
		case "image/jpeg":
			want = "jpeg"
		case "image/webp":
			want = "webp"
		default:
			return unsupportedf("image %d mimeType %q", i, def.MIMEType)
		}
		view := m.bufferViews[viewIndex]
		if view.meshopt != nil {
			// EXT_meshopt_compression only defines geometry and animation
			// streams; an image has no compressed form to decode.
			return unsupportedf("image %d is stored in a meshopt-compressed bufferView", i)
		}
		config, format, err := image.DecodeConfig(io.NewSectionReader(m.src, view.offset, view.length))
		if err != nil {
			return invalidf("image %d cannot be decoded", i)
		}
		if format != want {
			return invalidf("image %d content is %s, not the declared %s", i, format, want)
		}
		if config.Width <= 0 || config.Height <= 0 {
			return invalidf("image %d has empty dimensions", i)
		}
		length := view.length
		if length > int64(maxIntValue) {
			return toolargef("image %d is too large", i)
		}
		m.Images[i] = Image{
			Name: def.Name, MIME: mime, Width: config.Width, Height: config.Height,
			BufferView: viewIndex, ByteLength: int(length),
		}
	}
	return nil
}

func (m *Model) loadTextures(samplerDefs []gltfSampler, defs []gltfTexture) error {
	m.samplers = make([]sampler, len(samplerDefs))
	for i, def := range samplerDefs {
		wrapS, ok := validWrap(def.WrapS)
		if !ok {
			return invalidf("sampler %d has wrapS %d", i, def.WrapS)
		}
		wrapT, ok := validWrap(def.WrapT)
		if !ok {
			return invalidf("sampler %d has wrapT %d", i, def.WrapT)
		}
		m.samplers[i] = sampler{wrapS: wrapS, wrapT: wrapT}
	}
	m.Textures = make([]Texture, len(defs))
	for i, def := range defs {
		if def.Source == nil {
			return invalidf("texture %d has no source", i)
		}
		if *def.Source < 0 || *def.Source >= len(m.Images) {
			return invalidf("texture %d references image %d", i, *def.Source)
		}
		samplerIndex := -1
		if def.Sampler != nil {
			samplerIndex = *def.Sampler
			if samplerIndex < 0 || samplerIndex >= len(m.samplers) {
				return invalidf("texture %d references sampler %d", i, samplerIndex)
			}
		}
		m.Textures[i] = Texture{Name: def.Name, Source: *def.Source, Sampler: samplerIndex}
	}
	return nil
}

func validWrap(value int) (int, bool) {
	switch value {
	case 0, 10497:
		return 10497, true
	case 33071:
		return 33071, true
	case 33648:
		return 33648, true
	default:
		return 0, false
	}
}

func (m *Model) loadMaterials(defs []gltfMaterial) error {
	m.Materials = make([]Material, len(defs))
	for i, def := range defs {
		material := Material{
			Name:            def.Name,
			BaseColorFactor: [4]float64{1, 1, 1, 1},
			MetallicFactor:  1,
			RoughnessFactor: 1,
			AlphaMode:       "OPAQUE",
			AlphaCutoff:     0.5,
			DoubleSided:     def.DoubleSided,
		}
		if def.PBR != nil {
			if def.PBR.BaseColorFactor != nil {
				factor, err := factor4(def.PBR.BaseColorFactor)
				if err != nil {
					return invalidf("material %d baseColorFactor", i)
				}
				material.BaseColorFactor = factor
			}
			if def.PBR.MetallicFactor != nil {
				material.MetallicFactor = *def.PBR.MetallicFactor
			}
			if def.PBR.RoughnessFactor != nil {
				material.RoughnessFactor = *def.PBR.RoughnessFactor
			}
			ref, err := m.textureRef(i, "baseColorTexture", def.PBR.BaseColorTexture)
			if err != nil {
				return err
			}
			material.BaseColorTexture = ref
			ref, err = m.textureRef(i, "metallicRoughnessTexture", def.PBR.MetallicRoughnessTexture)
			if err != nil {
				return err
			}
			material.MetallicRoughnessTexture = ref
		}
		if def.NormalTexture != nil {
			ref, err := m.textureRefIndex(i, "normalTexture", def.NormalTexture.Index, def.NormalTexture.TexCoord,
				def.NormalTexture.Extensions)
			if err != nil {
				return err
			}
			if ref == nil {
				return invalidf("material %d normalTexture has no index", i)
			}
			scale := 1.0
			if def.NormalTexture.Scale != nil {
				scale = *def.NormalTexture.Scale
			}
			material.NormalTexture = &NormalTexture{TextureRef: *ref, Scale: scale}
		}
		if def.OcclusionTexture != nil {
			ref, err := m.textureRefIndex(i, "occlusionTexture", def.OcclusionTexture.Index, def.OcclusionTexture.TexCoord,
				def.OcclusionTexture.Extensions)
			if err != nil {
				return err
			}
			if ref == nil {
				return invalidf("material %d occlusionTexture has no index", i)
			}
			strength := 1.0
			if def.OcclusionTexture.Strength != nil {
				strength = *def.OcclusionTexture.Strength
			}
			material.OcclusionTexture = &OcclusionTexture{TextureRef: *ref, Strength: strength}
		}
		ref, err := m.textureRef(i, "emissiveTexture", def.EmissiveTexture)
		if err != nil {
			return err
		}
		material.EmissiveTexture = ref
		if def.EmissiveFactor != nil {
			if len(def.EmissiveFactor) != 3 {
				return invalidf("material %d emissiveFactor", i)
			}
			copy(material.EmissiveFactor[:], def.EmissiveFactor)
		}
		if def.AlphaMode != "" {
			switch def.AlphaMode {
			case "OPAQUE", "MASK", "BLEND":
				material.AlphaMode = def.AlphaMode
			default:
				return invalidf("material %d alphaMode %q", i, def.AlphaMode)
			}
		}
		if def.AlphaCutoff != nil {
			if *def.AlphaCutoff < 0 || !finite(*def.AlphaCutoff) {
				return invalidf("material %d alphaCutoff", i)
			}
			material.AlphaCutoff = *def.AlphaCutoff
		}
		m.Materials[i] = material
	}
	return nil
}

func factor4(values []float64) ([4]float64, error) {
	if len(values) != 4 {
		return [4]float64{}, invalidf("expected four components")
	}
	var out [4]float64
	copy(out[:], values)
	return out, nil
}

func (m *Model) textureRef(material int, slot string, info *gltfTextureInfo) (*TextureRef, error) {
	if info == nil {
		return nil, nil
	}
	return m.textureRefIndex(material, slot, info.Index, info.TexCoord, info.Extensions)
}

func (m *Model) textureRefIndex(
	material int, slot string, indexPointer, texCoordPointer *int, extensions *gltfTextureInfoExtensions,
) (*TextureRef, error) {
	if indexPointer == nil {
		return nil, nil
	}
	index := *indexPointer
	if index < 0 || index >= len(m.Textures) {
		return nil, invalidf("material %d %s references texture %d", material, slot, index)
	}
	texCoord := 0
	if texCoordPointer != nil {
		texCoord = *texCoordPointer
	}
	ref := &TextureRef{Texture: index, TexCoord: texCoord}
	if extensions != nil && extensions.Transform != nil {
		transform, ok := textureTransform(extensions.Transform)
		if !ok {
			return nil, invalidf("material %d %s has an invalid KHR_texture_transform", material, slot)
		}
		ref.Transform = transform
		if extensions.Transform.TexCoord != nil {
			ref.TexCoord = *extensions.Transform.TexCoord
		}
	}
	if ref.TexCoord < 0 {
		return nil, invalidf("material %d %s has a negative texCoord", material, slot)
	}
	return ref, nil
}

// textureTransform applies the KHR_texture_transform defaults: no offset, no
// rotation and unit scale.
func textureTransform(def *gltfTextureTransform) (*TextureTransform, bool) {
	transform := &TextureTransform{Scale: [2]float64{1, 1}}
	if def.Offset != nil {
		if len(def.Offset) != 2 {
			return nil, false
		}
		copy(transform.Offset[:], def.Offset)
	}
	if def.Scale != nil {
		if len(def.Scale) != 2 {
			return nil, false
		}
		copy(transform.Scale[:], def.Scale)
	}
	if def.Rotation != nil {
		transform.Rotation = *def.Rotation
	}
	if !vectorFinite(transform.Offset[:]) || !vectorFinite(transform.Scale[:]) || !finite(transform.Rotation) {
		return nil, false
	}
	return transform, true
}

func (m *Model) loadMeshes(defs []gltfMesh) error {
	m.Meshes = make([]Mesh, len(defs))
	for i, def := range defs {
		if len(def.Primitives) == 0 {
			return invalidf("mesh %d has no primitives", i)
		}
		mesh := Mesh{Name: def.Name, Primitives: make([]Primitive, len(def.Primitives))}
		for p, primitive := range def.Primitives {
			mode := 4
			if primitive.Mode != nil {
				mode = *primitive.Mode
			}
			if mode < 0 || mode > 6 {
				return invalidf("mesh %d primitive %d has mode %d", i, p, mode)
			}
			position, ok := primitive.Attributes["POSITION"]
			if !ok {
				return invalidf("mesh %d primitive %d has no POSITION", i, p)
			}
			if err := m.checkAttribute(i, p, "POSITION", position); err != nil {
				return err
			}
			sets := make([]int, 0, len(primitive.Attributes))
			for name, accessorIndex := range primitive.Attributes {
				if accessorIndex < 0 || accessorIndex >= len(m.accessors) {
					return invalidf("mesh %d primitive %d attribute %q references accessor %d", i, p, name, accessorIndex)
				}
				if err := m.checkAttribute(i, p, name, accessorIndex); err != nil {
					return err
				}
				// Every vertex attribute must address the same vertices as
				// POSITION. Checking it here also bounds later accessor
				// allocations by MaxVertices instead of by the file size.
				if m.accessors[accessorIndex].count != m.accessors[position].count {
					return invalidf("mesh %d primitive %d attribute %q count does not match POSITION", i, p, name)
				}
				if strings.HasPrefix(name, "TEXCOORD_") {
					set, err := strconv.Atoi(strings.TrimPrefix(name, "TEXCOORD_"))
					if err != nil || set < 0 {
						return invalidf("mesh %d primitive %d has an invalid TEXCOORD name", i, p)
					}
					sets = append(sets, set)
				}
			}
			sort.Ints(sets)
			index := -1
			if primitive.Indices != nil {
				index = *primitive.Indices
				if index < 0 || index >= len(m.accessors) {
					return invalidf("mesh %d primitive %d references accessor %d", i, p, index)
				}
				def := m.accessors[index]
				if def.components != 1 || def.normalized ||
					(def.componentType != 5121 && def.componentType != 5123 && def.componentType != 5125) {
					return invalidf("mesh %d primitive %d has an invalid index accessor", i, p)
				}
			}
			materialIndex := -1
			if primitive.Material != nil {
				materialIndex = *primitive.Material
				if materialIndex < 0 || materialIndex >= len(m.Materials) {
					return invalidf("mesh %d primitive %d references material %d", i, p, materialIndex)
				}
			}
			count := m.accessors[position].count
			if index >= 0 {
				count = m.accessors[index].count
			}
			triangles, err := primitiveTriangles(mode, count)
			if err != nil {
				return invalidf("mesh %d primitive %d: %s", i, p, err.Error())
			}
			mesh.Primitives[p] = Primitive{
				Mode: mode, Material: materialIndex, Indices: index,
				Attributes:   cloneIntMap(primitive.Attributes),
				Triangles:    triangles,
				MorphTargets: len(primitive.Targets),
				hasTexCoords: sets,
			}
		}
		m.Meshes[i] = mesh
	}
	return nil
}

func cloneIntMap(source map[string]int) map[string]int {
	if source == nil {
		return nil
	}
	out := make(map[string]int, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func primitiveTriangles(mode int, count int) (int64, error) {
	switch mode {
	case 0, 1, 2, 3:
		return 0, nil
	case 4:
		if count%3 != 0 {
			return 0, invalidf("TRIANGLES index count is not a multiple of three")
		}
		return int64(count / 3), nil
	case 5, 6:
		if count < 3 {
			return 0, nil
		}
		return int64(count - 2), nil
	default:
		return 0, invalidf("unknown primitive mode")
	}
}

func (m *Model) checkAttribute(mesh, primitive int, name string, accessorIndex int) error {
	if accessorIndex < 0 || accessorIndex >= len(m.accessors) {
		return invalidf("mesh %d primitive %d attribute %q references accessor %d", mesh, primitive, name, accessorIndex)
	}
	accessor := m.accessors[accessorIndex]
	bad := func() error {
		return invalidf("mesh %d primitive %d attribute %q has the wrong layout", mesh, primitive, name)
	}
	switch {
	case name == "POSITION" || name == "NORMAL":
		if accessor.components != 3 {
			return bad()
		}
	case name == "COLOR_0":
		if accessor.components != 3 && accessor.components != 4 {
			return bad()
		}
	case strings.HasPrefix(name, "TEXCOORD_"):
		if accessor.components != 2 {
			return bad()
		}
	case name == "JOINTS_0":
		if accessor.components != 4 || accessor.normalized ||
			(accessor.componentType != 5121 && accessor.componentType != 5123) {
			return bad()
		}
	case name == "WEIGHTS_0":
		if accessor.components != 4 {
			return bad()
		}
	}
	return nil
}

func (m *Model) loadSkins(defs []gltfSkin) error {
	m.Skins = make([]Skin, len(defs))
	for i, def := range defs {
		for _, joint := range def.Joints {
			if joint < 0 || joint >= len(m.Nodes) {
				return invalidf("skin %d references joint node %d", i, joint)
			}
		}
		inverseBind := -1
		if def.InverseBindMatrices != nil {
			inverseBind = *def.InverseBindMatrices
			if inverseBind < 0 || inverseBind >= len(m.accessors) {
				return invalidf("skin %d references accessor %d", i, inverseBind)
			}
		}
		m.Skins[i] = Skin{Name: def.Name, Joints: append([]int(nil), def.Joints...), inverseBind: inverseBind}
	}
	for i := range m.Nodes {
		skin := m.Nodes[i].Skin
		if skin < -1 || skin >= len(m.Skins) {
			return invalidf("node %d references skin %d", i, skin)
		}
	}
	return nil
}

func (m *Model) loadAnimations(defs []gltfAnimation) error {
	m.Animations = make([]Animation, len(defs))
	for i, def := range defs {
		animation := Animation{Name: def.Name, Channels: len(def.Channels)}
		for _, sampler := range def.Samplers {
			if sampler.Input < 0 || sampler.Input >= len(m.accessors) ||
				sampler.Output < 0 || sampler.Output >= len(m.accessors) {
				return invalidf("animation %d sampler references an invalid accessor", i)
			}
			accessor := m.accessors[sampler.Input]
			if len(accessor.max) > 0 && finite(accessor.max[0]) {
				if accessor.max[0] > animation.Duration {
					animation.Duration = accessor.max[0]
				}
			}
		}
		for _, channel := range def.Channels {
			if channel.Sampler < 0 || channel.Sampler >= len(def.Samplers) {
				return invalidf("animation %d channel references sampler %d", i, channel.Sampler)
			}
			if channel.Target.Node != nil {
				node := *channel.Target.Node
				if node < 0 || node >= len(m.Nodes) {
					return invalidf("animation %d targets node %d", i, node)
				}
			}
			switch channel.Target.Path {
			case "translation":
				animation.Translations++
			case "rotation":
				animation.Rotations++
			case "scale":
				animation.Scales++
			case "weights":
				animation.Weights++
			default:
				return invalidf("animation %d has target path %q", i, channel.Target.Path)
			}
		}
		m.Animations[i] = animation
	}
	return nil
}

// loadNodes converts node definitions and validates the hierarchy. Skin
// indices are checked after skins are loaded, so a node may refer forward.
func (m *Model) loadNodes(nodeDefs []gltfNode, limits Limits) error {
	if limits.MaxNodes > 0 && len(nodeDefs) > limits.MaxNodes {
		return toolargef("node count exceeds MaxNodes")
	}
	m.Nodes = make([]Node, len(nodeDefs))
	parents := make([]int, len(nodeDefs))
	for index := range parents {
		parents[index] = -1
	}
	for i, def := range nodeDefs {
		node := Node{
			Name:     def.Name,
			Children: append([]int(nil), def.Children...),
			Mesh:     -1,
			Skin:     -1,
			Scale:    [3]float64{1, 1, 1},
			Rotation: [4]float64{0, 0, 0, 1},
		}
		if def.Mesh != nil {
			node.Mesh = *def.Mesh
			if node.Mesh < 0 || node.Mesh >= len(m.Meshes) {
				return invalidf("node %d references mesh %d", i, node.Mesh)
			}
		}
		if def.Skin != nil {
			node.Skin = *def.Skin
		}
		if def.Matrix != nil {
			if len(def.Matrix) != 16 {
				return invalidf("node %d matrix has %d values", i, len(def.Matrix))
			}
			copy(node.Matrix[:], def.Matrix)
			if !vectorFinite(node.Matrix[:]) {
				return invalidf("node %d matrix is not finite", i)
			}
			node.HasMatrix = true
			node.Translation = [3]float64{}
			node.Rotation = [4]float64{0, 0, 0, 1}
			node.Scale = [3]float64{1, 1, 1}
		} else {
			if err := copy3(&node.Translation, def.Translation, "node", i, "translation"); err != nil {
				return err
			}
			if err := copy4(&node.Rotation, def.Rotation, "node", i, "rotation"); err != nil {
				return err
			}
			if err := copy3(&node.Scale, def.Scale, "node", i, "scale"); err != nil {
				return err
			}
			if !vectorFinite(node.Translation[:]) || !vectorFinite(node.Rotation[:]) || !vectorFinite(node.Scale[:]) {
				return invalidf("node %d transform is not finite", i)
			}
		}
		for _, child := range node.Children {
			if child < 0 || child >= len(nodeDefs) {
				return invalidf("node %d references child %d", i, child)
			}
			if child == i {
				return invalidf("node %d is its own child", i)
			}
			if parents[child] != -1 {
				return invalidf("node %d has more than one parent", child)
			}
			parents[child] = i
		}
		m.Nodes[i] = node
	}
	return nil
}

// loadScenes selects the active scene and walks it once to build parts.
// Traversal is iterative so a hostile hierarchy cannot overflow the goroutine
// stack.
func (m *Model) loadScenes(sceneDefs []gltfScene, active *int, limits Limits) error {
	m.Scenes = make([]Scene, len(sceneDefs))
	for i, def := range sceneDefs {
		for _, root := range def.Nodes {
			if root < 0 || root >= len(m.Nodes) {
				return invalidf("scene %d references node %d", i, root)
			}
		}
		m.Scenes[i] = Scene{Name: def.Name, Nodes: append([]int(nil), def.Nodes...)}
	}

	parents := make([]int, len(m.Nodes))
	for i := range parents {
		parents[i] = -1
	}
	for i := range m.Nodes {
		for _, child := range m.Nodes[i].Children {
			if parents[child] != -1 {
				return invalidf("node %d has more than one parent", child)
			}
			parents[child] = i
		}
	}

	var roots []int
	switch {
	case len(m.Scenes) > 0:
		sceneIndex := 0
		if active != nil {
			sceneIndex = *active
		}
		if sceneIndex < 0 || sceneIndex >= len(m.Scenes) {
			return invalidf("scene index %d is out of range", sceneIndex)
		}
		roots = m.Scenes[sceneIndex].Nodes
	default:
		for i := range m.Nodes {
			if parents[i] == -1 {
				roots = append(roots, i)
			}
		}
		if len(m.Nodes) > 0 && len(roots) == 0 {
			return invalidf("node hierarchy has no root")
		}
	}
	for _, root := range roots {
		if parents[root] != -1 {
			return invalidf("scene root node %d is also a child", root)
		}
	}

	joints := make([]bool, len(m.Nodes))
	for _, skin := range m.Skins {
		for _, joint := range skin.Joints {
			joints[joint] = true
		}
	}

	type walkItem struct {
		node   int
		parent mat4
		path   string
		depth  int
	}
	state := make([]uint8, len(m.Nodes))
	totalTriangles := int64(0)
	totalVertices := int64(0)
	totalPathBytes := int64(0)
	stack := make([]walkItem, 0, len(roots))
	for i := len(roots) - 1; i >= 0; i-- {
		stack = append(stack, walkItem{node: roots[i], parent: identity4(), depth: 1})
	}
	for len(stack) > 0 {
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch state[item.node] {
		case 1:
			return invalidf("node hierarchy contains a cycle at node %d", item.node)
		case 2:
			return invalidf("node %d is reached more than once", item.node)
		}
		state[item.node] = 1
		if limits.MaxDepth > 0 && item.depth > limits.MaxDepth {
			return toolargef("node depth exceeds MaxDepth")
		}
		node := &m.Nodes[item.node]
		local := node.Matrix
		if !node.HasMatrix {
			local = trsMatrix(node.Translation, node.Rotation, node.Scale)
		}
		world := item.parent.mul(local)
		if !world.finite() {
			return invalidf("node %d world transform is not finite", item.node)
		}
		if joints[item.node] {
			m.jointWorld[item.node] = world
		}
		name := node.Name
		if name == "" {
			name = "node_" + strconv.Itoa(item.node)
		}
		// Both ceilings are checked before the path is built, so a rejected
		// hierarchy never allocates the string that exceeded them.
		pathBytes := len(name)
		if item.path != "" {
			pathBytes += len(item.path) + 1
		}
		if pathBytes > maxPathBytes {
			return toolargef("node path exceeds %d bytes", maxPathBytes)
		}
		totalPathBytes += int64(pathBytes)
		if totalPathBytes > maxTotalPathBytes {
			return toolargef("node paths exceed %d bytes in total", maxTotalPathBytes)
		}
		path := name
		if item.path != "" {
			path = item.path + "/" + name
		}
		if node.Mesh >= 0 {
			mesh := &m.Meshes[node.Mesh]
			for primitiveIndex := range mesh.Primitives {
				primitive := &mesh.Primitives[primitiveIndex]
				totalTriangles += primitive.Triangles
				totalVertices += int64(m.accessors[primitive.Attributes["POSITION"]].count)
				if limits.MaxTriangles > 0 && totalTriangles > limits.MaxTriangles {
					return toolargef("triangle count exceeds MaxTriangles")
				}
				if limits.MaxVertices > 0 && totalVertices > limits.MaxVertices {
					return toolargef("vertex count exceeds MaxVertices")
				}
				if len(m.parts) >= maxParts {
					return toolargef("part count exceeds %d", maxParts)
				}
				m.parts = append(m.parts, m.makePart(item.node, node.Mesh, primitiveIndex, path, world, *primitive, node.Skin))
			}
		}
		state[item.node] = 2
		for i := len(node.Children) - 1; i >= 0; i-- {
			stack = append(stack, walkItem{
				node: node.Children[i], parent: world, path: path, depth: item.depth + 1,
			})
		}
	}
	return nil
}

func (m *Model) makePart(node, mesh, primitive int, path string, world mat4, def Primitive, skin int) *part {
	position := def.Attributes["POSITION"]
	normal, hasNormal := def.Attributes["NORMAL"]
	if !hasNormal {
		normal = -1
	}
	color, hasColor := def.Attributes["COLOR_0"]
	if !hasColor {
		color = -1
	}
	uv := make(map[int]int)
	for _, set := range def.hasTexCoords {
		for name, accessorIndex := range def.Attributes {
			if name == "TEXCOORD_"+strconv.Itoa(set) {
				uv[set] = accessorIndex
			}
		}
	}
	colorComponents := 0
	if hasColor {
		colorComponents = m.accessors[color].components
	}
	return &part{
		id:              "n" + strconv.Itoa(node) + ".p" + strconv.Itoa(primitive),
		node:            node,
		mesh:            mesh,
		primitive:       primitive,
		path:            path,
		world:           world,
		vertexCount:     m.accessors[position].count,
		triangleCount:   def.Triangles,
		mode:            def.Mode,
		material:        def.Material,
		position:        position,
		normal:          normal,
		color:           color,
		colorComponents: colorComponents,
		index:           def.Indices,
		uv:              uv,
		skin:            skin,
	}
}

func copy3(target *[3]float64, source []float64, kind string, index int, field string) error {
	if source == nil {
		return nil
	}
	if len(source) != 3 {
		return invalidf("%s %d %s has %d values", kind, index, field, len(source))
	}
	copy(target[:], source)
	return nil
}

func copy4(target *[4]float64, source []float64, kind string, index int, field string) error {
	if source == nil {
		return nil
	}
	if len(source) != 4 {
		return invalidf("%s %d %s has %d values", kind, index, field, len(source))
	}
	copy(target[:], source)
	return nil
}

func vectorFinite(values []float64) bool {
	for _, value := range values {
		if !finite(value) {
			return false
		}
	}
	return true
}

func readAt(r io.ReaderAt, offset, length int64) ([]byte, error) {
	if offset < 0 || length < 0 || length > int64(maxIntValue) {
		return nil, invalidf("read range is outside the addressable model")
	}
	if length == 0 {
		return nil, nil
	}
	data := make([]byte, int(length))
	read := 0
	for read < len(data) {
		n, err := r.ReadAt(data[read:], offset+int64(read))
		read += n
		if err != nil {
			if err == io.EOF && read == len(data) {
				break
			}
			// The underlying error may include a filesystem path; only the
			// fact that the model could not be read is safe to expose.
			return nil, invalidf("model data cannot be read")
		}
		if n == 0 {
			return nil, invalidf("model data cannot be read")
		}
	}
	return data, nil
}
