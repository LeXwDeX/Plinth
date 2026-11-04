package localparts

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

const (
	attributeTarget = 34962
	indexTarget     = 34963
)

// outputBuilder assembles one output GLB. Every output starts empty: accessor,
// material and texture indices are local to the file that contains them.
type outputBuilder struct {
	bin       []byte
	views     []bufferViewDef
	accessors []accessorDef
	meshes    []meshDef
	materials []materialDef
	textures  []textureDef
	images    []imageDef
	samplers  []json.RawMessage
	nodes     []nodeDef
	roots     []int
	limits    modelinput.Limits
	budget    *workBudget

	materialCache map[*sourceModel]map[int]int
	textureCache  map[*sourceModel]map[int]int
	imageCache    map[*sourceModel]map[int]int
	samplerCache  map[*sourceModel]map[int]int
}

func newOutputBuilder(limits modelinput.Limits, budget *workBudget) *outputBuilder {
	return &outputBuilder{limits: limits, budget: budget,
		materialCache: map[*sourceModel]map[int]int{},
		textureCache:  map[*sourceModel]map[int]int{},
		imageCache:    map[*sourceModel]map[int]int{},
		samplerCache:  map[*sourceModel]map[int]int{},
	}
}

func intPointer(value int) *int { return &value }

func align4(value int) int { return (value + 3) &^ 3 }

// appendView copies data into the BIN payload and returns its bufferView. The
// caller declares a stride only when the packed element size is not already a
// multiple of four.
func (b *outputBuilder) appendView(data []byte, stride, target *int) (int, error) {
	if len(data) == 0 {
		return 0, invalidModel("empty_view")
	}
	if err := b.budget.charge(int64(len(data))); err != nil {
		return 0, err
	}
	if len(b.bin)+len(data) > int(b.limits.MaxBytes) {
		return 0, limitError("bytes")
	}
	for len(b.bin)%4 != 0 {
		b.bin = append(b.bin, 0)
	}
	offset := len(b.bin)
	b.bin = append(b.bin, data...)
	view := bufferViewDef{Buffer: 0, ByteOffset: offset, ByteLength: len(data), ByteStride: stride, Target: target}
	b.views = append(b.views, view)
	return len(b.views) - 1, nil
}

func (b *outputBuilder) addNode(node nodeDef) (int, error) {
	if int64(len(b.nodes)) >= b.limits.MaxNodes {
		return 0, limitError("nodes")
	}
	b.nodes = append(b.nodes, node)
	return len(b.nodes) - 1, nil
}

func copyTransform(target *nodeDef, source nodeDef) {
	if len(source.Matrix) == 16 {
		target.Matrix = append([]float64(nil), source.Matrix...)
		return
	}
	if len(source.Translation) == 3 {
		target.Translation = append([]float64(nil), source.Translation...)
	}
	if len(source.Rotation) == 4 {
		target.Rotation = append([]float64(nil), source.Rotation...)
	}
	if len(source.Scale) == 3 {
		target.Scale = append([]float64(nil), source.Scale...)
	}
}

func groupByNode(pieces []*piece) ([][]*piece, []int) {
	index := map[int]int{}
	var groups [][]*piece
	var order []int
	for _, entry := range pieces {
		if group, ok := index[entry.node.index]; ok {
			groups[group] = append(groups[group], entry)
			continue
		}
		index[entry.node.index] = len(groups)
		groups = append(groups, []*piece{entry})
		order = append(order, entry.node.index)
	}
	return groups, order
}

// buildGroup writes one mesh and the source ancestor chain that positions it.
// Ancestor nodes carry their source name, extras and local transform; the leaf
// carries the mesh and, for a split part, the part name. Reproducing the chain
// (instead of composing a world matrix) keeps every accepted source matrix
// intact and makes the world-space result bit-identical to modelinspect's
// traversal.
func (b *outputBuilder) buildGroup(leafName string, pieces []*piece) (int, error) {
	if len(pieces) == 0 {
		return 0, invalidModel("empty_group")
	}
	meshIndex, err := b.mesh(pieces)
	if err != nil {
		return 0, err
	}
	leaf := pieces[0]
	if int64(len(leaf.node.chain))+1 > b.limits.MaxDepth {
		return 0, limitError("depth")
	}
	child := -1
	for i := len(leaf.node.chain) - 1; i >= 0; i-- {
		source := leaf.source.doc.Nodes[leaf.node.chain[i]]
		node := nodeDef{Name: source.Name, Extras: cloneRaw(source.Extras)}
		copyTransform(&node, source)
		if i == len(leaf.node.chain)-1 {
			node.Mesh = intPointer(meshIndex)
			if leafName != "" {
				node.Name = leafName
			}
		}
		if child >= 0 {
			node.Children = []int{child}
		}
		child, err = b.addNode(node)
		if err != nil {
			return 0, err
		}
	}
	return child, nil
}

// buildPart writes one split part: a named root whose children are one source
// ancestor chain per contributing node instance.
func (b *outputBuilder) buildPart(target *part) error {
	groups, _ := groupByNode(target.pieces)
	root := nodeDef{Name: target.name}
	for _, pieces := range groups {
		child, err := b.buildGroup(target.name, pieces)
		if err != nil {
			return err
		}
		root.Children = append(root.Children, child)
	}
	index, err := b.addNode(root)
	if err != nil {
		return err
	}
	b.roots = append(b.roots, index)
	return nil
}

func (b *outputBuilder) mesh(pieces []*piece) (int, error) {
	primitives := make([]primitiveDef, 0, len(pieces))
	for _, piece := range pieces {
		primitive, err := b.primitive(piece)
		if err != nil {
			return 0, err
		}
		primitives = append(primitives, primitive)
	}
	if len(primitives) == 0 {
		return 0, invalidModel("empty_mesh")
	}
	source := pieces[0].source
	if pieces[0].mesh < 0 || pieces[0].mesh >= len(source.doc.Meshes) {
		return 0, invalidModel("mesh")
	}
	mesh := source.doc.Meshes[pieces[0].mesh]
	b.meshes = append(b.meshes, meshDef{Name: mesh.Name, Extras: cloneRaw(mesh.Extras), Primitives: primitives})
	return len(b.meshes) - 1, nil
}

func (b *outputBuilder) primitive(piece *piece) (primitiveDef, error) {
	source := piece.source
	if piece.mesh < 0 || piece.mesh >= len(source.doc.Meshes) {
		return primitiveDef{}, invalidModel("mesh")
	}
	sourcePrimitive := source.doc.Meshes[piece.mesh].Primitives[piece.primitive]
	names := make([]string, 0, len(sourcePrimitive.Attributes))
	for name := range sourcePrimitive.Attributes {
		names = append(names, name)
	}
	sort.Strings(names)
	attributes := make(map[string]int, len(names))
	for _, name := range names {
		accessor, err := b.copyAttribute(piece, name, sourcePrimitive.Attributes[name])
		if err != nil {
			return primitiveDef{}, err
		}
		attributes[name] = accessor
	}
	if _, ok := attributes["POSITION"]; !ok {
		return primitiveDef{}, invalidModel("position")
	}
	indices, err := b.indices(piece)
	if err != nil {
		return primitiveDef{}, err
	}
	primitive := primitiveDef{Attributes: attributes, Indices: intPointer(indices)}
	if piece.material >= 0 {
		material, err := b.material(source, piece.material)
		if err != nil {
			return primitiveDef{}, err
		}
		primitive.Material = intPointer(material)
	}
	return primitive, nil
}

func (b *outputBuilder) copyAttribute(piece *piece, name string, accessorIndex int) (int, error) {
	source := piece.source
	if accessorIndex < 0 || accessorIndex >= len(source.doc.Accessors) {
		return 0, invalidModel("accessor")
	}
	accessor := source.doc.Accessors[accessorIndex]
	width := componentWidth(accessor.ComponentType)
	components := componentCount(accessor.Type)
	if width == 0 || components == 0 {
		return 0, invalidModel("accessor")
	}
	if name == "POSITION" && (accessor.ComponentType != 5126 || accessor.Type != "VEC3" || accessor.Normalized) {
		return 0, invalidModel("position")
	}
	count := piece.vertexCount
	elementSize := width * components
	stride := align4(elementSize)
	data := make([]byte, count*stride)
	if err := b.budget.charge(int64(count)); err != nil {
		return 0, err
	}
	for index := 0; index < count; index++ {
		vertex := index
		if piece.vertices != nil {
			vertex = piece.vertices[index]
		}
		offset, size, err := source.elementOffset(accessor, vertex)
		if err != nil {
			return 0, err
		}
		copy(data[index*stride:index*stride+size], source.bin[offset:offset+size])
	}
	var stridePointer *int
	if stride != elementSize {
		stridePointer = intPointer(stride)
	}
	target := attributeTarget
	view, err := b.appendView(data, stridePointer, intPointer(target))
	if err != nil {
		return 0, err
	}
	output := accessorDef{
		BufferView: view, ComponentType: accessor.ComponentType, Normalized: accessor.Normalized,
		Count: count, Type: accessor.Type,
	}
	if name == "POSITION" {
		var minimum, maximum [3]float64
		for index := 0; index < count; index++ {
			for axis := 0; axis < 3; axis++ {
				value := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[index*stride+axis*4:])))
				if index == 0 || value < minimum[axis] {
					minimum[axis] = value
				}
				if index == 0 || value > maximum[axis] {
					maximum[axis] = value
				}
			}
		}
		output.Min = []float64{minimum[0], minimum[1], minimum[2]}
		output.Max = []float64{maximum[0], maximum[1], maximum[2]}
	}
	b.accessors = append(b.accessors, output)
	return len(b.accessors) - 1, nil
}

func (b *outputBuilder) indices(piece *piece) (int, error) {
	if len(piece.indices) == 0 || len(piece.indices)%3 != 0 {
		return 0, invalidModel("triangles")
	}
	componentType := 5123
	width := 2
	if piece.vertexCount > 65535 {
		componentType = 5125
		width = 4
	}
	data := make([]byte, len(piece.indices)*width)
	for index, value := range piece.indices {
		if int(value) >= piece.vertexCount {
			return 0, invalidModel("indices")
		}
		if width == 2 {
			binary.LittleEndian.PutUint16(data[index*2:], uint16(value))
		} else {
			binary.LittleEndian.PutUint32(data[index*4:], value)
		}
	}
	target := indexTarget
	view, err := b.appendView(data, nil, intPointer(target))
	if err != nil {
		return 0, err
	}
	b.accessors = append(b.accessors, accessorDef{
		BufferView: view, ComponentType: componentType, Count: len(piece.indices), Type: "SCALAR",
	})
	return len(b.accessors) - 1, nil
}

func (b *outputBuilder) image(source *sourceModel, index int) (int, error) {
	if cached, ok := b.imageCache[source][index]; ok {
		return cached, nil
	}
	if index < 0 || index >= len(source.doc.Images) {
		return 0, invalidModel("image")
	}
	image := source.doc.Images[index]
	if image.BufferView < 0 || image.BufferView >= len(source.doc.BufferViews) {
		return 0, invalidModel("image")
	}
	view := source.doc.BufferViews[image.BufferView]
	if view.ByteOffset < 0 || view.ByteLength <= 0 || view.ByteOffset+view.ByteLength > len(source.bin) {
		return 0, invalidModel("image")
	}
	data := source.bin[view.ByteOffset : view.ByteOffset+view.ByteLength]
	outputView, err := b.appendView(data, nil, nil)
	if err != nil {
		return 0, err
	}
	if b.imageCache[source] == nil {
		b.imageCache[source] = map[int]int{}
	}
	output := imageDef{Name: image.Name, Extras: cloneRaw(image.Extras), BufferView: outputView, MimeType: image.MimeType}
	b.images = append(b.images, output)
	b.imageCache[source][index] = len(b.images) - 1
	return len(b.images) - 1, nil
}

func (b *outputBuilder) texture(source *sourceModel, index int) (int, error) {
	if cached, ok := b.textureCache[source][index]; ok {
		return cached, nil
	}
	if index < 0 || index >= len(source.doc.Textures) {
		return 0, invalidModel("texture")
	}
	texture := source.doc.Textures[index]
	image, err := b.image(source, texture.Source)
	if err != nil {
		return 0, err
	}
	var sampler *int
	if texture.Sampler != nil {
		samplerIndex, err := b.sampler(source, *texture.Sampler)
		if err != nil {
			return 0, err
		}
		sampler = intPointer(samplerIndex)
	}
	if b.textureCache[source] == nil {
		b.textureCache[source] = map[int]int{}
	}
	b.textures = append(b.textures, textureDef{Name: texture.Name, Extras: cloneRaw(texture.Extras), Source: image, Sampler: sampler})
	b.textureCache[source][index] = len(b.textures) - 1
	return len(b.textures) - 1, nil
}

func (b *outputBuilder) sampler(source *sourceModel, index int) (int, error) {
	if cached, ok := b.samplerCache[source][index]; ok {
		return cached, nil
	}
	if index < 0 || index >= len(source.doc.Samplers) {
		return 0, invalidModel("sampler")
	}
	if b.samplerCache[source] == nil {
		b.samplerCache[source] = map[int]int{}
	}
	b.samplers = append(b.samplers, cloneRaw(source.doc.Samplers[index]))
	b.samplerCache[source][index] = len(b.samplers) - 1
	return len(b.samplers) - 1, nil
}

func (b *outputBuilder) textureReference(source *sourceModel, reference *textureRef) (*textureRef, error) {
	if reference == nil {
		return nil, nil
	}
	index, err := b.texture(source, reference.Index)
	if err != nil {
		return nil, err
	}
	return &textureRef{Index: index, TexCoord: reference.TexCoord, Extras: cloneRaw(reference.Extras), Scale: reference.Scale, Strength: reference.Strength}, nil
}

func (b *outputBuilder) material(source *sourceModel, index int) (int, error) {
	if cached, ok := b.materialCache[source][index]; ok {
		return cached, nil
	}
	if index < 0 || index >= len(source.doc.Materials) {
		return 0, invalidModel("material")
	}
	sourceMaterial := source.doc.Materials[index]
	output := materialDef{
		Name: sourceMaterial.Name, Extras: cloneRaw(sourceMaterial.Extras),
		EmissiveFactor: append([]float64(nil), sourceMaterial.EmissiveFactor...),
		AlphaMode:      sourceMaterial.AlphaMode, AlphaCutoff: sourceMaterial.AlphaCutoff,
		DoubleSided: sourceMaterial.DoubleSided,
	}
	if sourceMaterial.PBR != nil {
		pbr := &pbrDef{
			BaseColorFactor: append([]float64(nil), sourceMaterial.PBR.BaseColorFactor...),
			MetallicFactor:  sourceMaterial.PBR.MetallicFactor, RoughnessFactor: sourceMaterial.PBR.RoughnessFactor,
		}
		var err error
		if pbr.BaseColorTexture, err = b.textureReference(source, sourceMaterial.PBR.BaseColorTexture); err != nil {
			return 0, err
		}
		if pbr.MetallicRoughnessTexture, err = b.textureReference(source, sourceMaterial.PBR.MetallicRoughnessTexture); err != nil {
			return 0, err
		}
		output.PBR = pbr
	}
	var err error
	if output.Normal, err = b.textureReference(source, sourceMaterial.Normal); err != nil {
		return 0, err
	}
	if output.Occlusion, err = b.textureReference(source, sourceMaterial.Occlusion); err != nil {
		return 0, err
	}
	if output.Emissive, err = b.textureReference(source, sourceMaterial.Emissive); err != nil {
		return 0, err
	}
	b.materials = append(b.materials, output)
	if b.materialCache[source] == nil {
		b.materialCache[source] = map[int]int{}
	}
	b.materialCache[source][index] = len(b.materials) - 1
	return len(b.materials) - 1, nil
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return bytes.Clone(raw)
}

// finish pads both chunks and writes the GLB container. The caller validates
// the result with modelinput before it is stored or discarded.
func (b *outputBuilder) finish(sceneName string) ([]byte, error) {
	if len(b.roots) == 0 || len(b.nodes) == 0 || len(b.meshes) == 0 {
		return nil, invalidModel("empty_output")
	}
	for len(b.bin)%4 != 0 {
		b.bin = append(b.bin, 0)
	}
	document := &glbDocument{
		Asset:       assetDef{Version: "2.0", Generator: "plinth-local-parts/1"},
		Scene:       intPointer(0),
		Scenes:      []sceneDef{{Name: cleanName(sceneName), Nodes: b.roots}},
		Nodes:       b.nodes,
		Meshes:      b.meshes,
		Materials:   b.materials,
		Textures:    b.textures,
		Images:      b.images,
		Samplers:    b.samplers,
		Accessors:   b.accessors,
		BufferViews: b.views,
		Buffers:     []bufferDef{{ByteLength: len(b.bin)}},
	}
	jsonBytes, err := json.Marshal(document)
	if err != nil {
		return nil, unavailableError("本地模型写出失败", err)
	}
	if int64(len(jsonBytes)) > b.limits.MaxJSONBytes {
		return nil, limitError("json_bytes")
	}
	for len(jsonBytes)%4 != 0 {
		jsonBytes = append(jsonBytes, ' ')
	}
	total := 12 + 8 + len(jsonBytes) + 8 + len(b.bin)
	if int64(total) > b.limits.MaxBytes {
		return nil, limitError("bytes")
	}
	output := make([]byte, total)
	binary.LittleEndian.PutUint32(output[0:4], 0x46546C67)
	binary.LittleEndian.PutUint32(output[4:8], 2)
	binary.LittleEndian.PutUint32(output[8:12], uint32(total))
	binary.LittleEndian.PutUint32(output[12:16], uint32(len(jsonBytes)))
	binary.LittleEndian.PutUint32(output[16:20], 0x4E4F534A)
	copy(output[20:20+len(jsonBytes)], jsonBytes)
	binStart := 20 + len(jsonBytes)
	binary.LittleEndian.PutUint32(output[binStart:binStart+4], uint32(len(b.bin)))
	binary.LittleEndian.PutUint32(output[binStart+4:binStart+8], 0x004E4942)
	copy(output[binStart+8:], b.bin)
	return output, nil
}

func sourceDisplayName(asset generation.InputAsset, index int, used map[string]bool) string {
	base := ""
	if asset.OriginalFilename != "" {
		base = filepath.Base(strings.ReplaceAll(asset.OriginalFilename, "\\", "/"))
		base = strings.TrimSuffix(base, filepath.Ext(base))
	}
	if base == "" && asset.Ordinal > 0 {
		base = "model_" + strconv.Itoa(asset.Ordinal)
	}
	if base == "" {
		base = "source_" + strconv.Itoa(index+1)
	}
	base = truncateName(cleanName(base), 120)
	if base == "" {
		base = "source_" + strconv.Itoa(index+1)
	}
	unique := base
	for suffix := 2; used[unique]; suffix++ {
		unique = base + "_" + strconv.Itoa(suffix)
	}
	used[unique] = true
	return unique
}

// buildMerged writes every source scene as one wrapper node whose children are
// the source's mesh-bearing node chains. Materials, textures and images from
// different sources never collide because each source has its own remap.
func buildMerged(sources []*sourceModel, name string, limits modelinput.Limits, budget *workBudget) ([]byte, error) {
	builder := newOutputBuilder(limits, budget)
	used := map[string]bool{}
	for index, source := range sources {
		pieces, err := buildPieces(source, budget, schema.ModelSplitModeNode)
		if err != nil {
			return nil, err
		}
		if len(pieces) == 0 {
			continue
		}
		wrapper := nodeDef{Name: sourceDisplayName(source.asset, index, used)}
		groups, _ := groupByNode(pieces)
		for _, group := range groups {
			child, err := builder.buildGroup("", group)
			if err != nil {
				return nil, err
			}
			wrapper.Children = append(wrapper.Children, child)
		}
		node, err := builder.addNode(wrapper)
		if err != nil {
			return nil, err
		}
		builder.roots = append(builder.roots, node)
	}
	if len(builder.roots) == 0 {
		return nil, unsupportedModel("来源模型不包含可合并的三角形几何")
	}
	sceneName := cleanName(name)
	if sceneName == "" {
		sceneName = "merged"
	}
	return builder.finish(sceneName)
}
