package localparts

import (
	"sort"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// piece is one source primitive instance, optionally narrowed to one welded
// connected component. vertices is nil for a whole primitive.
type piece struct {
	source      *sourceModel
	node        nodeInstance
	mesh        int
	primitive   int
	material    int
	component   int
	vertices    []int
	indices     []uint32
	triangles   int
	vertexCount int
	box         bounds
	seq         int
}

type part struct {
	name      string
	pieces    []*piece
	triangles int
	box       bounds
	order     int
}

func newPart(name string, pieces []*piece) *part {
	p := &part{name: name, pieces: append([]*piece(nil), pieces...)}
	if len(p.pieces) > 0 {
		p.order = p.pieces[0].seq
	}
	for _, piece := range p.pieces {
		p.triangles += piece.triangles
		p.box.union(piece.box)
	}
	return p
}

func (p *part) absorb(other *part) {
	if other == nil || other == p {
		return
	}
	hadPieces := len(p.pieces) > 0
	p.pieces = append(p.pieces, other.pieces...)
	p.triangles += other.triangles
	p.box.union(other.box)
	if !hadPieces || other.order < p.order {
		p.order = other.order
	}
}

func sortPieces(pieces []*piece) {
	sort.SliceStable(pieces, func(i, j int) bool { return pieces[i].seq < pieces[j].seq })
}

func validSplitMode(mode string) bool {
	switch mode {
	case schema.ModelSplitModeNode, schema.ModelSplitModePrimitive, schema.ModelSplitModeMaterial, schema.ModelSplitModeConnectedComponent:
		return true
	default:
		return false
	}
}

// buildPieces assigns every rendered primitive instance to one or more pieces.
// Whole-primitive modes keep the primitive's full vertex range, matching the
// source PartReport bounding box computed by modelinspect. Connected-component
// mode keeps only the vertices a component's triangles reference.
func buildPieces(m *sourceModel, budget *workBudget, mode string) ([]*piece, error) {
	instances, err := walkActiveScene(m.doc)
	if err != nil {
		return nil, err
	}
	var pieces []*piece
	for _, instance := range instances {
		node := m.doc.Nodes[instance.index]
		if node.Mesh == nil {
			continue
		}
		if *node.Mesh < 0 || *node.Mesh >= len(m.doc.Meshes) {
			return nil, invalidModel("mesh")
		}
		mesh := m.doc.Meshes[*node.Mesh]
		for primitiveIndex, primitive := range mesh.Primitives {
			if primitive.Mode != nil && *primitive.Mode != 4 {
				return nil, unsupportedModel("模型包含非三角形图元，暂不支持拆分或合并")
			}
			positionIndex, ok := primitive.Attributes["POSITION"]
			if !ok || positionIndex < 0 || positionIndex >= len(m.doc.Accessors) {
				return nil, invalidModel("position")
			}
			vertexCount := m.doc.Accessors[positionIndex].Count
			indices, err := m.triangleIndices(primitive)
			if err != nil {
				return nil, err
			}
			if len(indices) == 0 || len(indices)%3 != 0 {
				return nil, invalidModel("triangles")
			}
			material := -1
			if primitive.Material != nil {
				material = *primitive.Material
				if material < 0 || material >= len(m.doc.Materials) {
					return nil, invalidModel("material")
				}
			}
			if mode == schema.ModelSplitModeConnectedComponent {
				components, err := splitComponents(m, budget, instance, *node.Mesh, primitiveIndex, primitive, indices, positionIndex, vertexCount, material, len(pieces))
				if err != nil {
					return nil, err
				}
				pieces = append(pieces, components...)
			} else {
				box, err := primitiveBounds(m, budget, instance, positionIndex, vertexCount)
				if err != nil {
					return nil, err
				}
				pieces = append(pieces, &piece{
					source: m, node: instance, mesh: *node.Mesh, primitive: primitiveIndex,
					material: material, component: -1, indices: indices,
					triangles: len(indices) / 3, vertexCount: vertexCount, box: box, seq: len(pieces),
				})
			}
			if len(pieces) > maxPiecesPerDocument {
				return nil, limitError("pieces")
			}
		}
	}
	return pieces, nil
}

// primitiveBounds mirrors modelinspect: it transforms every vertex the
// primitive declares, including vertices its indices never reference.
func primitiveBounds(m *sourceModel, budget *workBudget, instance nodeInstance, positionIndex, vertexCount int) (bounds, error) {
	var box bounds
	if err := budget.charge(int64(vertexCount)); err != nil {
		return box, err
	}
	for vertex := 0; vertex < vertexCount; vertex++ {
		position, err := m.position(positionIndex, vertex)
		if err != nil {
			return box, err
		}
		world := instance.world.transformPoint(position[0], position[1], position[2])
		box.add(world)
	}
	return box, nil
}

// splitComponents welds vertices by their exact 12 POSITION bytes, groups
// triangles into connected components and emits each component with a
// compacted vertex array. Components are numbered by their smallest vertex
// index, so the result is deterministic for a given file.
func splitComponents(m *sourceModel, budget *workBudget, instance nodeInstance, meshIndex, primitiveIndex int, primitive primitiveDef, indices []uint32, positionIndex, vertexCount, material, seqStart int) ([]*piece, error) {
	if err := budget.charge(int64(vertexCount)); err != nil {
		return nil, err
	}
	parent := make([]int32, vertexCount)
	for i := range parent {
		parent[i] = -1
	}
	used := make([]bool, vertexCount)
	var find func(int32) int32
	find = func(value int32) int32 {
		root := value
		for parent[root] != root {
			root = parent[root]
		}
		for parent[value] != value {
			next := parent[value]
			parent[value] = root
			value = next
		}
		return root
	}
	ensure := func(value int32) int32 {
		if parent[value] < 0 {
			parent[value] = value
		}
		return find(value)
	}
	union := func(a, b int32) {
		ra, rb := ensure(a), ensure(b)
		if ra != rb {
			parent[rb] = ra
		}
	}
	for i := 0; i < len(indices); i += 3 {
		a, b, c := int32(indices[i]), int32(indices[i+1]), int32(indices[i+2])
		used[a], used[b], used[c] = true, true, true
		union(a, b)
		union(a, c)
	}
	// Exact-position welding joins index-split seams without merging spatially
	// close but distinct vertices.
	welded := make(map[[12]byte]int32, vertexCount/2+1)
	for vertex := 0; vertex < vertexCount; vertex++ {
		if !used[vertex] {
			continue
		}
		offset, size, err := m.elementOffset(m.doc.Accessors[positionIndex], vertex)
		if err != nil || size != 12 {
			return nil, invalidModel("position")
		}
		var key [12]byte
		copy(key[:], m.bin[offset:offset+12])
		if first, ok := welded[key]; ok {
			union(int32(vertex), first)
		} else {
			welded[key] = int32(vertex)
		}
	}

	componentOfRoot := make(map[int32]int, 16)
	componentOfVertex := make([]int32, vertexCount)
	for i := range componentOfVertex {
		componentOfVertex[i] = -1
	}
	var verticesByComponent [][]int
	for vertex := 0; vertex < vertexCount; vertex++ {
		if !used[vertex] {
			continue
		}
		root := find(int32(vertex))
		component, ok := componentOfRoot[root]
		if !ok {
			component = len(verticesByComponent)
			componentOfRoot[root] = component
			verticesByComponent = append(verticesByComponent, nil)
		}
		componentOfVertex[vertex] = int32(component)
		verticesByComponent[component] = append(verticesByComponent[component], vertex)
	}
	localIndex := make([]int32, vertexCount)
	for i := range localIndex {
		localIndex[i] = -1
	}
	indicesByComponent := make([][]uint32, len(verticesByComponent))
	for component, vertices := range verticesByComponent {
		if len(vertices) == 0 {
			return nil, invalidModel("component")
		}
		for local, vertex := range vertices {
			localIndex[vertex] = int32(local)
		}
		indicesByComponent[component] = make([]uint32, 0, 16)
	}
	for i := 0; i < len(indices); i += 3 {
		component := componentOfVertex[indices[i]]
		if component < 0 || componentOfVertex[indices[i+1]] != component || componentOfVertex[indices[i+2]] != component {
			return nil, invalidModel("component")
		}
		indicesByComponent[component] = append(indicesByComponent[component],
			uint32(localIndex[indices[i]]), uint32(localIndex[indices[i+1]]), uint32(localIndex[indices[i+2]]))
	}
	pieces := make([]*piece, 0, len(verticesByComponent))
	for component, vertices := range verticesByComponent {
		box := bounds{}
		for _, vertex := range vertices {
			position, err := m.position(positionIndex, vertex)
			if err != nil {
				return nil, err
			}
			box.add(instance.world.transformPoint(position[0], position[1], position[2]))
		}
		if len(indicesByComponent[component]) == 0 {
			return nil, invalidModel("component")
		}
		pieces = append(pieces, &piece{
			source: m, node: instance, mesh: meshIndex, primitive: primitiveIndex,
			material: material, component: component, vertices: vertices,
			indices: indicesByComponent[component], triangles: len(indicesByComponent[component]) / 3,
			vertexCount: len(vertices), box: box, seq: seqStart + component,
		})
	}
	return pieces, nil
}

func materialName(m *sourceModel, index int) string {
	if index < 0 {
		return "no_material"
	}
	if index < len(m.doc.Materials) {
		if name := cleanName(m.doc.Materials[index].Name); name != "" {
			return name
		}
	}
	return "material_" + strconv.Itoa(index)
}

// groupPieces applies the split mode's grouping rule. Every mode produces the
// same part shape; only the partition differs.
func groupPieces(m *sourceModel, pieces []*piece, mode string) []*part {
	var parts []*part
	switch mode {
	case schema.ModelSplitModeNode:
		index := map[int]*part{}
		for _, piece := range pieces {
			group, ok := index[piece.node.index]
			if !ok {
				group = newPart(cleanName(nodeSourceName(piece.node, m.doc)), nil)
				index[piece.node.index] = group
				parts = append(parts, group)
			}
			group.pieces = append(group.pieces, piece)
			group.triangles += piece.triangles
			group.box.union(piece.box)
		}
	case schema.ModelSplitModeMaterial:
		index := map[int]*part{}
		for _, piece := range pieces {
			group, ok := index[piece.material]
			if !ok {
				group = newPart(materialName(m, piece.material), nil)
				index[piece.material] = group
				parts = append(parts, group)
			}
			group.pieces = append(group.pieces, piece)
			group.triangles += piece.triangles
			group.box.union(piece.box)
		}
	case schema.ModelSplitModePrimitive:
		for _, entry := range pieces {
			name := cleanName(nodeSourceName(entry.node, m.doc)) + ".primitive_" + strconv.Itoa(entry.primitive)
			parts = append(parts, newPart(name, []*piece{entry}))
		}
	case schema.ModelSplitModeConnectedComponent:
		for _, entry := range pieces {
			name := cleanName(nodeSourceName(entry.node, m.doc)) + ".component_" + strconv.Itoa(entry.component+1)
			parts = append(parts, newPart(name, []*piece{entry}))
		}
	}
	for _, group := range parts {
		sortPieces(group.pieces)
		if len(group.pieces) > 0 {
			group.order = group.pieces[0].seq
		}
	}
	sort.SliceStable(parts, func(i, j int) bool { return parts[i].order < parts[j].order })
	return parts
}

// applyMinRatio merges parts whose triangle share is below the threshold into
// the part with the nearest bounding-box centre. At least the largest part
// always survives, so a model made only of small fragments is not collapsed
// into an arbitrary choice.
func applyMinRatio(parts []*part, ratio float64, total int) []*part {
	if ratio <= 0 || len(parts) <= 1 {
		return parts
	}
	threshold := ratio * float64(total)
	small := make([]bool, len(parts))
	survivors := make([]*part, 0, len(parts))
	for index, candidate := range parts {
		if float64(candidate.triangles) < threshold {
			small[index] = true
			continue
		}
		survivors = append(survivors, candidate)
	}
	if len(survivors) == 0 {
		largest := 0
		for index := 1; index < len(parts); index++ {
			if parts[index].triangles > parts[largest].triangles ||
				(parts[index].triangles == parts[largest].triangles && parts[index].order < parts[largest].order) {
				largest = index
			}
		}
		small[largest] = false
		survivors = append(survivors, parts[largest])
	}
	for index, candidate := range parts {
		if !small[index] {
			continue
		}
		centre := candidate.box.center()
		best := survivors[0]
		bestDistance := squaredDistance(centre, best.box.center())
		for _, survivor := range survivors[1:] {
			distance := squaredDistance(centre, survivor.box.center())
			if distance < bestDistance || (distance == bestDistance && survivor.order < best.order) {
				best, bestDistance = survivor, distance
			}
		}
		best.absorb(candidate)
	}
	result := parts[:0]
	for index, candidate := range parts {
		if !small[index] {
			result = append(result, candidate)
		}
	}
	return result
}

func squaredDistance(a, b vec3) float64 {
	delta := a.sub(b)
	return delta.dot(delta)
}

// applyMaxParts keeps the largest max-1 parts and folds the remainder into one
// part named "rest". A bound of one folds every part into "rest".
func applyMaxParts(parts []*part, maximum int) []*part {
	if maximum <= 0 || len(parts) <= maximum {
		return parts
	}
	if maximum == 1 {
		order := parts[0].order
		for _, candidate := range parts[1:] {
			if candidate.order < order {
				order = candidate.order
			}
		}
		merged := newPart("rest", nil)
		merged.order = order
		for _, candidate := range parts {
			merged.absorb(candidate)
		}
		return []*part{merged}
	}
	sorted := append([]*part(nil), parts...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].triangles != sorted[j].triangles {
			return sorted[i].triangles > sorted[j].triangles
		}
		return sorted[i].order < sorted[j].order
	})
	survivors := sorted[:maximum-1]
	rest := sorted[maximum-1:]
	merged := newPart("rest", nil)
	merged.order = rest[0].order
	for _, candidate := range rest {
		merged.absorb(candidate)
	}
	result := append(append([]*part(nil), survivors...), merged)
	sort.SliceStable(result, func(i, j int) bool { return result[i].order < result[j].order })
	return result
}

func truncateName(name string, maximum int) string {
	runes := []rune(name)
	if len(runes) <= maximum {
		return name
	}
	return string(runes[:maximum])
}

func dedupeNames(parts []*part) {
	used := map[string]bool{}
	for index, candidate := range parts {
		name := cleanName(candidate.name)
		if name == "" {
			name = "part_" + strconv.Itoa(index+1)
		}
		name = truncateName(name, 120)
		unique := name
		for suffix := 2; used[unique]; suffix++ {
			unique = name + "_" + strconv.Itoa(suffix)
		}
		used[unique] = true
		candidate.name = unique
	}
}

// splitModel runs the mode selection, ratio merge and part bound over one
// source. It never writes; the adapter builds and stores the returned parts.
func splitModel(m *sourceModel, mode string, minRatio float64, maxParts int) ([]*part, error) {
	if !validSplitMode(mode) || minRatio < 0 || minRatio > 0.2 || maxParts < 1 || maxParts > maxSplitParts {
		return nil, invalidModel("options")
	}
	pieces, err := buildPieces(m, m.budget, mode)
	if err != nil {
		return nil, err
	}
	if len(pieces) == 0 {
		return nil, unsupportedModel("模型不包含可拆分的三角形几何")
	}
	total := 0
	for _, piece := range pieces {
		total += piece.triangles
	}
	parts := groupPieces(m, pieces, mode)
	parts = applyMinRatio(parts, minRatio, total)
	parts = applyMaxParts(parts, maxParts)
	sort.SliceStable(parts, func(i, j int) bool { return parts[i].order < parts[j].order })
	dedupeNames(parts)
	return parts, nil
}

func buildManifest(parts []*part, mode string) (schema.ModelPartsManifest, error) {
	manifest := schema.ModelPartsManifest{Version: schema.ModelPartsManifestVersion, Mode: mode, Parts: make([]schema.ModelPart, 0, len(parts))}
	for index, candidate := range parts {
		if !candidate.box.valid {
			return manifest, invalidModel("bounds")
		}
		part := schema.ModelPart{
			Ordinal:   index + 1,
			Name:      candidate.name,
			Triangles: candidate.triangles,
			Bounds: schema.ModelPartBounds{
				Min: [3]float64{candidate.box.min[0], candidate.box.min[1], candidate.box.min[2]},
				Max: [3]float64{candidate.box.max[0], candidate.box.max[1], candidate.box.max[2]},
			},
			Source: schema.ModelPartSource{},
		}
		if mode == schema.ModelSplitModeNode {
			part.Source.Nodes = distinctNodePaths([]nodeInstance{candidate.pieces[0].node})
		} else {
			part.Source.Primitives = make([]schema.ModelPartPrimitiveSource, 0, len(candidate.pieces))
			for _, piece := range candidate.pieces {
				source := schema.ModelPartPrimitiveSource{Node: piece.node.path, Primitive: piece.primitive}
				if piece.component >= 0 {
					component := piece.component
					source.Component = &component
				}
				part.Source.Primitives = append(part.Source.Primitives, source)
			}
		}
		manifest.Parts = append(manifest.Parts, part)
	}
	return manifest, nil
}

// cleanName is used for user-visible part names and source node names.
func cleanName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	var builder strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f:
			continue
		case r == '/' || r == '\\':
			builder.WriteRune('_')
		default:
			builder.WriteRune(r)
		}
	}
	return strings.TrimSpace(builder.String())
}

func distinctNodePaths(instances []nodeInstance) []string {
	seen := map[string]bool{}
	var paths []string
	for _, instance := range instances {
		if !seen[instance.path] {
			seen[instance.path] = true
			paths = append(paths, instance.path)
		}
	}
	sort.Strings(paths)
	return paths
}
