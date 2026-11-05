package modelinspect

import (
	"context"
	"sort"
	"strconv"
)

// InspectOptions tunes topology analysis. The zero value welds exact
// positions and disables debris classification; callers that want the usual
// inspection behaviour should use DefaultInspectOptions.
type InspectOptions struct {
	// WeldEpsilonRatio is multiplied by the world-space bounding-box diagonal
	// to obtain the position-welding tolerance. A position is quantized to
	// multiples of that tolerance before hashing. Zero welds exact positions.
	WeldEpsilonRatio float64
	// DebrisRatio marks connected components whose triangle share is below
	// this fraction as debris. Zero disables debris classification.
	DebrisRatio float64
}

// DefaultInspectOptions is a reasonable starting point for Agent inspection:
// vertices within 1e-4 of the bbox diagonal weld together, and components
// below 1% of the triangles are debris.
func DefaultInspectOptions() InspectOptions {
	return InspectOptions{WeldEpsilonRatio: 1e-4, DebrisRatio: 0.01}
}

// Report is the JSON-ready result of Inspect. All lengths are in the glTF
// unit (meters) as stored in the file; no unit conversion is applied.
//
// Triangles counts drawable triangles: a strip or fan triangle that repeats
// an index (strip stitching or restart) is not a face and is excluded.
// Vertices counts stored POSITION elements, used or not. Geometry is placed
// as a viewer shows it before any animation: skinned parts are posed by
// their joints and ignore their own node transform.
type Report struct {
	Triangles       int64 `json:"triangles"`
	Vertices        int64 `json:"vertices"`
	Primitives      int   `json:"primitives"`
	Meshes          int   `json:"meshes"`
	Nodes           int   `json:"nodes"`
	MaterialCount   int   `json:"material_count"`
	TextureCount    int   `json:"texture_count"`
	FileBytes       int64 `json:"file_bytes"`
	PointPrimitives int   `json:"point_primitives"`
	LinePrimitives  int   `json:"line_primitives"`

	Parts      []PartReport      `json:"parts"`
	Materials  []MaterialReport  `json:"materials"`
	UV         UVReport          `json:"uv"`
	Skins      []SkinReport      `json:"skins"`
	Animations []AnimationReport `json:"animations"`
	BBox       BBoxReport        `json:"bbox"`
	Topology   TopologyReport    `json:"topology"`
	Warnings   []string          `json:"warnings"`
}

// BBox is an axis-aligned box in world space.
type BBox struct {
	Min  [3]float64 `json:"min"`
	Max  [3]float64 `json:"max"`
	Size [3]float64 `json:"size"`
}

// BBoxReport adds the bottom-center offset used to judge whether a model's
// pivot sits at the bottom centre. The offset is the world position of the
// bottom centre, so an offset of [0, 0, 0] means the pivot already is there.
//
// The box covers the vertices that triangle primitives reference. Point and
// line primitives (often helpers that Render does not draw) and vertices no
// element references are excluded, so a model without triangles has an
// empty box.
type BBoxReport struct {
	Min                [3]float64 `json:"min"`
	Max                [3]float64 `json:"max"`
	Size               [3]float64 `json:"size"`
	Unit               string     `json:"unit"`
	BottomCenterOffset [3]float64 `json:"bottom_center_offset"`
}

// PartReport describes one node x primitive instance. ID is stable for a
// given file (for example "n3.p0"), TriangleRatio is in [0, 1]. BBox covers
// the vertices the part's elements reference, points and lines included.
type PartReport struct {
	ID            string  `json:"id"`
	Node          string  `json:"node"`
	Mesh          string  `json:"mesh"`
	Material      int     `json:"material"`
	MaterialName  string  `json:"material_name"`
	Triangles     int64   `json:"triangles"`
	BBox          BBox    `json:"bbox"`
	TriangleRatio float64 `json:"triangle_ratio"`
}

// MaterialReport describes one material and all five texture slots. Missing
// slots are still listed with Present=false so consumers do not have to infer
// absence from field presence.
type MaterialReport struct {
	Index           int                  `json:"index"`
	Name            string               `json:"name"`
	AlphaMode       string               `json:"alpha_mode"`
	DoubleSided     bool                 `json:"double_sided"`
	BaseColorFactor [4]float64           `json:"base_color_factor"`
	MetallicFactor  float64              `json:"metallic_factor"`
	RoughnessFactor float64              `json:"roughness_factor"`
	EmissiveFactor  [3]float64           `json:"emissive_factor"`
	Slots           []MaterialSlotReport `json:"slots"`
}

// MaterialSlotReport is one texture slot. Factor holds the slot's scalar
// factors: base_color: RGBA; metallic_roughness: metallic, roughness; normal:
// scale; occlusion: strength; emissive: RGB.
type MaterialSlotReport struct {
	Slot      string    `json:"slot"`
	Present   bool      `json:"present"`
	Texture   int       `json:"texture"`
	Image     int       `json:"image"`
	MIME      string    `json:"mime,omitempty"`
	Width     int       `json:"width,omitempty"`
	Height    int       `json:"height,omitempty"`
	TexCoord  int       `json:"tex_coord"`
	Factor    []float64 `json:"factor,omitempty"`
	OverLimit bool      `json:"over_limit,omitempty"`
}

// UVReport lists the TEXCOORD sets of every part and whether every triangle
// primitive has at least one set.
type UVReport struct {
	Primitives []PrimitiveUVReport `json:"primitives"`
	AllHaveUV  bool                `json:"all_have_uv"`
}

// PrimitiveUVReport lists the sorted TEXCOORD_n set numbers of one part.
type PrimitiveUVReport struct {
	PartID string `json:"part_id"`
	Sets   []int  `json:"sets"`
}

// SkinReport summarizes one skin.
type SkinReport struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Joints int    `json:"joints"`
}

// AnimationReport summarizes one animation. The four channel counts add up to
// Channels; Duration is the largest sampler input accessor max in seconds.
type AnimationReport struct {
	Index        int     `json:"index"`
	Name         string  `json:"name"`
	Channels     int     `json:"channels"`
	Translations int     `json:"translations"`
	Rotations    int     `json:"rotations"`
	Scales       int     `json:"scales"`
	Weights      int     `json:"weights"`
	Duration     float64 `json:"duration"`
}

// TopologyReport is computed after welding positions. Watertight is true
// exactly when every triangle was analysed, at least one is not degenerate,
// and BoundaryEdges, NonManifoldEdges and InconsistentWindingEdges are all
// zero: the surface is closed, manifold and consistently oriented.
//
// InconsistentWindingEdges counts manifold edges whose two triangles
// traverse them in the same direction, i.e. adjacent faces facing opposite
// ways. Winding is judged in world space, with mirrored (negative
// determinant) parts flipped as glTF requires.
type TopologyReport struct {
	WeldEpsilon              float64 `json:"weld_epsilon"`
	ConnectedComponents      int     `json:"connected_components"`
	LargestComponentRatio    float64 `json:"largest_component_ratio"`
	DebrisComponents         int     `json:"debris_components"`
	DebrisTriangles          int64   `json:"debris_triangles"`
	BoundaryEdges            int64   `json:"boundary_edges"`
	NonManifoldEdges         int64   `json:"non_manifold_edges"`
	InconsistentWindingEdges int64   `json:"inconsistent_winding_edges"`
	DegenerateTriangles      int64   `json:"degenerate_triangles"`
	Watertight               bool    `json:"watertight"`
}

// Inspect summarizes m for an Agent. It is InspectContext without
// cancellation; a read failure is reported as the "read_error" warning
// because the signature has no error result.
func Inspect(m *Model, opts InspectOptions) Report {
	report, _ := InspectContext(context.Background(), m, opts)
	return report
}

// inspectCheckInterval is how many vertices or triangles Inspect processes
// between checks of its context.
const inspectCheckInterval = 1 << 16

// InspectContext summarizes m for an Agent. It reads accessor data lazily
// through the Model's reader; a read failure is reported as the
// "read_error" warning, not as an error. The only error is ctx.Err() when
// ctx is done before the report is complete, so the topology pass, which
// allocates in proportion to the model, can be cancelled.
//
// Ratios are clamped to non-negative values. The report is deterministic for
// a given model and options.
func InspectContext(ctx context.Context, m *Model, opts InspectOptions) (Report, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	report := Report{
		Parts:      []PartReport{},
		Materials:  []MaterialReport{},
		Skins:      []SkinReport{},
		Animations: []AnimationReport{},
		Warnings:   []string{},
		UV:         UVReport{Primitives: []PrimitiveUVReport{}},
	}
	if m == nil {
		report.Warnings = []string{"nil_model"}
		return report, nil
	}
	report.Meshes = len(m.Meshes)
	report.Nodes = len(m.Nodes)
	report.MaterialCount = len(m.Materials)
	report.TextureCount = len(m.Textures)
	report.FileBytes = m.FileBytes
	report.Primitives = len(m.parts)

	if opts.WeldEpsilonRatio < 0 {
		opts.WeldEpsilonRatio = 0
	}
	if opts.DebrisRatio < 0 {
		opts.DebrisRatio = 0
	}

	analyses := make([]*partAnalysis, len(m.parts))
	triangles := make([]int64, len(m.parts))
	partBoxes := make([]bounds, len(m.parts))
	readFailed := false
	var worldBox bounds
	invalidPositions := false
	missingNormals := false
	bindPose := false
	for index, p := range m.parts {
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}
		report.Vertices += int64(p.vertexCount)
		switch p.mode {
		case 0:
			report.PointPrimitives++
		case 1, 2, 3:
			report.LinePrimitives++
		}
		triangles[index] = p.triangleCount
		analysis, err := m.analyzePart(p)
		if err != nil {
			readFailed = true
			report.Triangles += triangles[index]
			continue
		}
		analyses[index] = analysis
		triangles[index] = analysis.triangles
		report.Triangles += analysis.triangles
		if analysis.triangles > 0 && p.normal < 0 {
			missingNormals = true
		}
		bindPose = bindPose || analysis.pose.bindPose
		surface := p.mode >= 4 && analysis.triangles > 0
		positions := analysis.positions
		for vertex := 0; vertex < p.vertexCount; vertex++ {
			if vertex%inspectCheckInterval == 0 && vertex > 0 {
				if err := ctx.Err(); err != nil {
					return Report{}, err
				}
			}
			if !analysis.uses(vertex) {
				continue
			}
			world := analysis.pose.point(vertex,
				float64(positions[vertex*3]),
				float64(positions[vertex*3+1]),
				float64(positions[vertex*3+2]),
			)
			if !world.finite() {
				invalidPositions = true
				continue
			}
			partBoxes[index].add(world)
			if surface {
				worldBox.add(world)
			}
		}
	}

	overLimit := false
	if m.limits.MaxTexturePixels > 0 {
		for _, image := range m.Images {
			if int64(image.Width)*int64(image.Height) > m.limits.MaxTexturePixels {
				overLimit = true
				break
			}
		}
	}

	report.BBox = makeBBoxReport(worldBox)
	report.Parts = makePartReports(m, partBoxes, triangles, report.Triangles)
	report.Materials = makeMaterialReports(m)
	report.UV = makeUVReport(m.parts, triangles)
	report.Skins = makeSkinReports(m)
	report.Animations = makeAnimationReports(m)
	topology, issues, err := m.computeTopology(ctx, analyses, worldBox, opts)
	if err != nil {
		return Report{}, err
	}
	report.Topology = topology

	invalidIndices := issues.invalidIndices
	if invalidPositions || issues.invalidPositions {
		report.Warnings = append(report.Warnings, "invalid_positions")
	}
	if invalidIndices {
		report.Warnings = append(report.Warnings, "invalid_indices")
	}
	if bindPose {
		report.Warnings = append(report.Warnings, "skin_bind_pose")
	}
	if !report.UV.AllHaveUV && report.Triangles > 0 {
		report.Warnings = append(report.Warnings, "missing_uv")
	}
	if missingNormals {
		report.Warnings = append(report.Warnings, "missing_normals")
	}
	if overLimit {
		report.Warnings = append(report.Warnings, "texture_over_limit")
	}
	if topology.DebrisComponents > 0 {
		report.Warnings = append(report.Warnings, "has_debris")
	}
	if !topology.Watertight {
		report.Warnings = append(report.Warnings, "not_watertight")
	}
	if report.PointPrimitives+report.LinePrimitives > 0 {
		report.Warnings = append(report.Warnings, "unsupported_primitive_mode")
	}
	if report.Triangles == 0 {
		report.Warnings = append(report.Warnings, "no_triangles")
	}
	if readFailed {
		report.Warnings = append(report.Warnings, "read_error")
	}
	return report, nil
}

func makeBBoxReport(worldBox bounds) BBoxReport {
	report := BBoxReport{Unit: "m"}
	if !worldBox.valid {
		return report
	}
	report.Min = [3]float64{worldBox.min[0], worldBox.min[1], worldBox.min[2]}
	report.Max = [3]float64{worldBox.max[0], worldBox.max[1], worldBox.max[2]}
	size := worldBox.size()
	report.Size = [3]float64{size[0], size[1], size[2]}
	report.BottomCenterOffset = [3]float64{
		(worldBox.min[0] + worldBox.max[0]) / 2,
		worldBox.min[1],
		(worldBox.min[2] + worldBox.max[2]) / 2,
	}
	return report
}

func makePartReports(m *Model, boxes []bounds, triangles []int64, totalTriangles int64) []PartReport {
	reports := make([]PartReport, 0, len(m.parts))
	for index, p := range m.parts {
		meshName := m.Meshes[p.mesh].Name
		if meshName == "" {
			meshName = "mesh_" + strconv.Itoa(p.mesh)
		}
		materialName := ""
		if p.material >= 0 {
			materialName = m.Materials[p.material].Name
		}
		ratio := 0.0
		if totalTriangles > 0 {
			ratio = float64(triangles[index]) / float64(totalTriangles)
		}
		reports = append(reports, PartReport{
			ID: p.id, Node: p.path, Mesh: meshName,
			Material: p.material, MaterialName: materialName,
			Triangles: triangles[index], BBox: makeBBox(boxes[index]),
			TriangleRatio: ratio,
		})
	}
	return reports
}

func makeBBox(b bounds) BBox {
	if !b.valid {
		return BBox{}
	}
	size := b.size()
	return BBox{
		Min:  [3]float64{b.min[0], b.min[1], b.min[2]},
		Max:  [3]float64{b.max[0], b.max[1], b.max[2]},
		Size: [3]float64{size[0], size[1], size[2]},
	}
}

func makeMaterialReports(m *Model) []MaterialReport {
	reports := make([]MaterialReport, 0, len(m.Materials))
	for index, material := range m.Materials {
		report := MaterialReport{
			Index: index, Name: material.Name, AlphaMode: material.AlphaMode,
			DoubleSided:     material.DoubleSided,
			BaseColorFactor: material.BaseColorFactor,
			MetallicFactor:  material.MetallicFactor,
			RoughnessFactor: material.RoughnessFactor,
			EmissiveFactor:  material.EmissiveFactor,
			Slots: []MaterialSlotReport{
				m.makeSlotReport(material.BaseColorTexture, "base_color",
					material.BaseColorFactor[:]),
				m.makeSlotReport(material.MetallicRoughnessTexture, "metallic_roughness",
					[]float64{material.MetallicFactor, material.RoughnessFactor}),
			},
		}
		if material.NormalTexture != nil {
			report.Slots = append(report.Slots, m.makeSlotReport(&material.NormalTexture.TextureRef, "normal",
				[]float64{material.NormalTexture.Scale}))
		} else {
			report.Slots = append(report.Slots, m.makeSlotReport(nil, "normal", nil))
		}
		if material.OcclusionTexture != nil {
			report.Slots = append(report.Slots, m.makeSlotReport(&material.OcclusionTexture.TextureRef, "occlusion",
				[]float64{material.OcclusionTexture.Strength}))
		} else {
			report.Slots = append(report.Slots, m.makeSlotReport(nil, "occlusion", nil))
		}
		report.Slots = append(report.Slots, m.makeSlotReport(material.EmissiveTexture, "emissive",
			material.EmissiveFactor[:]))
		reports = append(reports, report)
	}
	return reports
}

func (m *Model) makeSlotReport(ref *TextureRef, slot string, factor []float64) MaterialSlotReport {
	report := MaterialSlotReport{
		Slot: slot, Present: ref != nil, Texture: -1, Image: -1,
		Factor: append([]float64(nil), factor...),
	}
	if ref == nil {
		return report
	}
	report.Texture = ref.Texture
	report.TexCoord = ref.TexCoord
	texture := m.Textures[ref.Texture]
	report.Image = texture.Source
	image := m.Images[texture.Source]
	report.MIME = image.MIME
	report.Width = image.Width
	report.Height = image.Height
	if m.limits.MaxTexturePixels > 0 &&
		int64(image.Width)*int64(image.Height) > m.limits.MaxTexturePixels {
		report.OverLimit = true
	}
	return report
}

func makeUVReport(parts []*part, triangles []int64) UVReport {
	report := UVReport{Primitives: make([]PrimitiveUVReport, 0, len(parts)), AllHaveUV: true}
	for index, p := range parts {
		sets := make([]int, 0, len(p.uv))
		for set := range p.uv {
			sets = append(sets, set)
		}
		sort.Ints(sets)
		report.Primitives = append(report.Primitives, PrimitiveUVReport{PartID: p.id, Sets: sets})
		if triangles[index] > 0 && len(sets) == 0 {
			report.AllHaveUV = false
		}
	}
	return report
}

func makeSkinReports(m *Model) []SkinReport {
	reports := make([]SkinReport, 0, len(m.Skins))
	for index, skin := range m.Skins {
		reports = append(reports, SkinReport{Index: index, Name: skin.Name, Joints: len(skin.Joints)})
	}
	return reports
}

func makeAnimationReports(m *Model) []AnimationReport {
	reports := make([]AnimationReport, 0, len(m.Animations))
	for index, animation := range m.Animations {
		reports = append(reports, AnimationReport{
			Index: index, Name: animation.Name, Channels: animation.Channels,
			Translations: animation.Translations, Rotations: animation.Rotations,
			Scales: animation.Scales, Weights: animation.Weights,
			Duration: animation.Duration,
		})
	}
	return reports
}
