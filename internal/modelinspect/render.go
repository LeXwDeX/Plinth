package modelinspect

import (
	"image"
	"image/color"
	"math"
	"runtime"
	"sync"
)

// MaxRenderDimension is the absolute ceiling Render enforces on Width and
// Height. It is a safety bound rather than a product default: callers choose
// their own, smaller sizes.
const MaxRenderDimension = 2048

// View names a predefined camera direction following the glTF convention:
// +Y is up, the asset faces +Z, and the asset's own left side is +X.
type View string

const (
	// ViewFront places the camera at +Z looking towards -Z.
	ViewFront View = "front"
	// ViewBack places the camera at -Z looking towards +Z.
	ViewBack View = "back"
	// ViewLeft places the camera at +X, showing the asset's own left side.
	ViewLeft View = "left"
	// ViewRight places the camera at -X.
	ViewRight View = "right"
	// ViewTop places the camera at +Y with -Z at the top of the image.
	ViewTop View = "top"
	// ViewThreeQuarter places the camera 45 degrees between front and left,
	// elevated by 25 degrees.
	ViewThreeQuarter View = "three_quarter"
	// ViewCustom uses RenderRequest.Azimuth and Elevation, in degrees.
	ViewCustom View = "custom"
)

// Projection selects the camera projection.
type Projection string

const (
	// ProjectionOrthographic is the default and is convenient for comparing
	// views.
	ProjectionOrthographic Projection = "orthographic"
	// ProjectionPerspective uses a 35 degree vertical field of view.
	ProjectionPerspective Projection = "perspective"
)

// RenderMode selects what Render draws.
type RenderMode string

const (
	// ModeTextured shades the base-color texture and COLOR_0, lit by a
	// headlight, one fixed main light and ambient light.
	ModeTextured RenderMode = "textured"
	// ModeClay draws uniform mid-grey geometry for shape inspection.
	ModeClay RenderMode = "clay"
	// ModeNormals visualizes world-space normals as RGB.
	ModeNormals RenderMode = "normals"
	// ModeWireframe draws clay shading with triangle edges. It is only
	// allowed when the triangle count is within WireframeMaxTriangles.
	ModeWireframe RenderMode = "wireframe"
)

// RenderRequest describes one view. Empty View, Projection and Mode select
// front, orthographic and textured respectively.
type RenderRequest struct {
	// View selects a predefined camera direction. ViewCustom uses Azimuth and
	// Elevation.
	View View
	// Azimuth is the custom horizontal angle in degrees: 0 is front, 90 is
	// left, 180 is back, 270 is right.
	Azimuth float64
	// Elevation is the custom vertical angle in degrees, within [-90, 90].
	Elevation  float64
	Projection Projection
	Mode       RenderMode

	// Width and Height are the output dimensions before 2x supersampling.
	// Both must be in [1, MaxRenderDimension].
	Width, Height int
	// Background is the output background colour in sRGB. Its zero value
	// selects a neutral light grey.
	Background color.NRGBA

	// Highlight lists part IDs (for example "n3.p0") that stay fully shaded.
	// All other parts are dimmed, unless Isolate is set, in which case only
	// the highlighted parts are drawn and framing uses their bounding box.
	Highlight []string
	Isolate   bool

	// Workers caps rasterisation goroutines. Zero uses runtime.NumCPU();
	// values above the CPU count are clamped.
	Workers int
	// WireframeMaxTriangles is the largest triangle count ModeWireframe may
	// draw. Zero therefore disallows wireframe; callers that want it must set
	// a positive budget.
	WireframeMaxTriangles int
	// CullBackfaces enables back-face culling for single-sided materials.
	// It is off by default so a model with inverted normals still has no holes.
	CullBackfaces bool
}

// Render draws one view of m with a software rasteriser and returns an opaque
// NRGBA image. It uses a z-buffer, 2x supersampling and bilinear texture
// sampling with the sampler's wrap modes.
//
// Render decodes only the base-color texture and only for textured mode; the
// limits passed to Parse decide which textures are eligible. Over-limit
// textures are replaced by material factors, which Inspect reports.
func Render(m *Model, req RenderRequest) (*image.NRGBA, error) {
	if m == nil {
		return nil, invalidf("nil model")
	}
	mode := req.Mode
	if mode == "" {
		mode = ModeTextured
	}
	switch mode {
	case ModeTextured, ModeClay, ModeNormals, ModeWireframe:
	default:
		return nil, invalidf("unknown render mode %q", req.Mode)
	}
	projection := req.Projection
	if projection == "" {
		projection = ProjectionOrthographic
	}
	switch projection {
	case ProjectionOrthographic, ProjectionPerspective:
	default:
		return nil, invalidf("unknown projection %q", req.Projection)
	}
	if req.Width <= 0 || req.Height <= 0 {
		return nil, invalidf("render dimensions must be positive")
	}
	if req.Width > MaxRenderDimension || req.Height > MaxRenderDimension {
		return nil, toolargef("render dimensions exceed %d", MaxRenderDimension)
	}
	if req.Width > math.MaxInt/req.Height {
		return nil, toolargef("render pixel count overflows")
	}
	if req.Workers < 0 {
		return nil, invalidf("Workers must not be negative")
	}
	if req.WireframeMaxTriangles < 0 {
		return nil, invalidf("WireframeMaxTriangles must not be negative")
	}
	azimuth, elevation, err := viewAngles(req)
	if err != nil {
		return nil, err
	}

	highlight, err := validateHighlight(m, req.Highlight)
	if err != nil {
		return nil, err
	}
	if req.Isolate && len(highlight) == 0 {
		return nil, invalidf("Isolate requires at least one highlighted part")
	}

	parts, totalTriangles, err := m.buildRenderParts(req, mode, highlight)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		return nil, invalidf("model has no renderable triangle geometry")
	}
	if mode == ModeWireframe && totalTriangles > int64(req.WireframeMaxTriangles) {
		return nil, toodensef("wireframe triangle count exceeds WireframeMaxTriangles")
	}
	var frame bounds
	for _, part := range parts {
		frame.union(part.bounds)
	}
	if !frame.valid {
		return nil, invalidf("model has no finite positions")
	}

	background := req.Background
	if background.A == 0 {
		background = color.NRGBA{R: 0xEC, G: 0xEC, B: 0xEC, A: 0xFF}
	}
	backgroundLinear := [3]float64{
		srgbToLinear[background.R],
		srgbToLinear[background.G],
		srgbToLinear[background.B],
	}

	supersampledWidth := req.Width * 2
	supersampledHeight := req.Height * 2
	camera := newCamera(azimuth, elevation, projection, frame, supersampledWidth, supersampledHeight)

	workers := req.Workers
	if workers == 0 {
		workers = runtime.NumCPU()
	}
	workers = min(workers, runtime.NumCPU())
	workers = max(workers, 1)

	pixels := make([]float32, supersampledWidth*supersampledHeight*3)
	depth := make([]float64, supersampledWidth*supersampledHeight)

	raster := renderer{
		camera:        camera,
		mode:          mode,
		width:         supersampledWidth,
		parts:         parts,
		background:    backgroundLinear,
		cullBackfaces: req.CullBackfaces,
	}
	renderInBands(&raster, pixels, depth, supersampledHeight, workers)

	return downsample(pixels, supersampledWidth, req.Width, req.Height), nil
}

func viewAngles(req RenderRequest) (float64, float64, error) {
	switch req.View {
	case "", ViewFront:
		return 0, 0, nil
	case ViewBack:
		return 180, 0, nil
	case ViewLeft:
		return 90, 0, nil
	case ViewRight:
		return -90, 0, nil
	case ViewTop:
		return 0, 90, nil
	case ViewThreeQuarter:
		return 45, 25, nil
	case ViewCustom:
		if !finite(req.Azimuth) || !finite(req.Elevation) {
			return 0, 0, invalidf("custom view angles must be finite")
		}
		if req.Elevation < -90 || req.Elevation > 90 {
			return 0, 0, invalidf("custom elevation must be within [-90, 90]")
		}
		azimuth := math.Mod(req.Azimuth, 360)
		if azimuth < 0 {
			azimuth += 360
		}
		return azimuth, req.Elevation, nil
	default:
		return 0, 0, invalidf("unknown view %q", req.View)
	}
}

func validateHighlight(m *Model, ids []string) (map[string]bool, error) {
	if len(ids) == 0 {
		return map[string]bool{}, nil
	}
	known := make(map[string]bool, len(m.parts))
	for _, p := range m.parts {
		known[p.id] = true
	}
	highlight := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !known[id] {
			return nil, invalidf("unknown part id %q", id)
		}
		highlight[id] = true
	}
	return highlight, nil
}

type renderPart struct {
	source          *part
	material        *Material
	positions       []float32 // world space, three per vertex
	normals         []float32 // world space, three per vertex; may be nil
	uvs             []float32 // two per vertex; may be nil
	colors          []float32 // linear, colorComponents per vertex; may be nil
	colorComponents int
	indices         []uint32 // nil for non-indexed primitives
	texture         *decodedTexture
	highlighted     bool
	bounds          bounds
	triangleCount   int
}

var fallbackMaterial = Material{
	BaseColorFactor: [4]float64{1, 1, 1, 1},
	MetallicFactor:  1,
	RoughnessFactor: 1,
	AlphaMode:       "OPAQUE",
}

func (m *Model) buildRenderParts(req RenderRequest, mode RenderMode, highlight map[string]bool) ([]*renderPart, int64, error) {
	parts := make([]*renderPart, 0, len(m.parts))
	totalTriangles := int64(0)
	for _, source := range m.parts {
		if source.triangleCount == 0 || source.mode < 4 || source.mode > 6 {
			continue
		}
		selected := !req.Isolate || highlight[source.id]
		if !selected {
			continue
		}
		positions, err := m.positions(source)
		if err != nil {
			return nil, 0, err
		}
		indices, ok := m.validatedIndices(source)
		if !ok {
			return nil, 0, invalidf("part %s has out-of-range indices", source.id)
		}
		material := &fallbackMaterial
		if source.material >= 0 {
			material = &m.Materials[source.material]
		}
		worldPositions := make([]float32, source.vertexCount*3)
		partBounds := bounds{}
		for vertex := 0; vertex < source.vertexCount; vertex++ {
			point := source.world.transformPoint(
				float64(positions[vertex*3]),
				float64(positions[vertex*3+1]),
				float64(positions[vertex*3+2]),
			)
			worldPositions[vertex*3] = float32(point[0])
			worldPositions[vertex*3+1] = float32(point[1])
			worldPositions[vertex*3+2] = float32(point[2])
			partBounds.add(point)
		}
		renderPart := &renderPart{
			source: source, material: material,
			positions: worldPositions, indices: indices,
			highlighted:   len(highlight) == 0 || highlight[source.id],
			bounds:        partBounds,
			triangleCount: source.partTriangles(indices),
		}
		totalTriangles += int64(renderPart.triangleCount)

		normals, err := m.normals(source)
		if err != nil {
			return nil, 0, err
		}
		if normals != nil {
			normalMatrix := source.world.normalMatrix()
			renderPart.normals = make([]float32, len(normals))
			for i := 0; i < len(normals); i += 3 {
				normal := normalMatrix.transform(vec3{
					float64(normals[i]), float64(normals[i+1]), float64(normals[i+2]),
				}).normalized()
				renderPart.normals[i] = float32(normal[0])
				renderPart.normals[i+1] = float32(normal[1])
				renderPart.normals[i+2] = float32(normal[2])
			}
		}
		if mode == ModeTextured {
			if material.BaseColorTexture != nil {
				uvs, err := m.texCoords(source, material.BaseColorTexture.TexCoord)
				if err != nil {
					return nil, 0, err
				}
				if uvs != nil {
					texture, err := m.decodeTexture(material.BaseColorTexture.Texture)
					if err != nil {
						return nil, 0, err
					}
					renderPart.uvs = uvs
					renderPart.texture = texture
				}
			}
			if source.color >= 0 {
				colors, err := m.colors(source)
				if err != nil {
					return nil, 0, err
				}
				renderPart.colors = colors
				renderPart.colorComponents = source.colorComponents
			}
		}
		parts = append(parts, renderPart)
	}
	return parts, totalTriangles, nil
}

// camera is a look-at camera with either an orthographic or a perspective
// projection. Depth comparisons store "larger is closer" for both projections.
type camera struct {
	width, height int
	forward       vec3
	right         vec3
	up            vec3
	center        vec3
	eye           vec3
	orthographic  bool
	scale         float64
	tanHalfX      float64
	tanHalfY      float64
	depthBias     float64
}

const viewMargin = 0.08

func newCamera(azimuth, elevation float64, projection Projection, frame bounds, width, height int) *camera {
	radiansAzimuth := azimuth * math.Pi / 180
	radiansElevation := elevation * math.Pi / 180
	toCamera := vec3{
		math.Sin(radiansAzimuth) * math.Cos(radiansElevation),
		math.Sin(radiansElevation),
		math.Cos(radiansAzimuth) * math.Cos(radiansElevation),
	}.normalized()
	forward := toCamera.scale(-1)
	if forward.length() == 0 {
		forward = vec3{0, 0, -1}
	}
	var right vec3
	if math.Abs(forward[1]) > 0.9999 {
		right = vec3{1, 0, 0}
	} else {
		right = forward.cross(vec3{0, 1, 0}).normalized()
	}
	up := right.cross(forward).normalized()
	center := frame.center()
	camera := &camera{
		width: width, height: height,
		forward: forward, right: right, up: up, center: center,
		orthographic: projection == ProjectionOrthographic,
	}
	size := frame.size()
	if camera.orthographic {
		halfWidth := 0.5 * (math.Abs(right[0])*size[0] + math.Abs(right[1])*size[1] + math.Abs(right[2])*size[2])
		halfHeight := 0.5 * (math.Abs(up[0])*size[0] + math.Abs(up[1])*size[1] + math.Abs(up[2])*size[2])
		available := 1 - 2*viewMargin
		scale := 1.0
		chosen := false
		if halfWidth > 0 {
			scale = float64(width) * available / (2 * halfWidth)
			chosen = true
		}
		if halfHeight > 0 {
			value := float64(height) * available / (2 * halfHeight)
			if !chosen || value < scale {
				scale = value
			}
		}
		camera.scale = scale
		camera.depthBias = math.Max(frame.diagonal()*1e-4, 1e-4)
	} else {
		radius := frame.diagonal() / 2
		if radius <= 0 {
			radius = 1e-6
		}
		const fovY = 35.0 * math.Pi / 180
		tanY := math.Tan(fovY / 2)
		aspect := float64(width) / float64(height)
		tanX := tanY * aspect
		halfY := math.Atan(tanY)
		halfX := math.Atan(tanX)
		distance := radius / math.Sin(math.Min(halfY, halfX))
		distance /= 1 - 2*viewMargin
		camera.eye = center.sub(forward.scale(distance))
		camera.tanHalfY = tanY
		camera.tanHalfX = tanX
		camera.depthBias = 1e-4
	}
	return camera
}

// project returns screen coordinates and a depth weight for which larger
// values are closer. ok is false for perspective points at or behind the
// camera.
func (c *camera) project(point vec3) (x, y, depth float64, ok bool) {
	if c.orthographic {
		relative := point.sub(c.center)
		x = float64(c.width)/2 + relative.dot(c.right)*c.scale
		y = float64(c.height)/2 - relative.dot(c.up)*c.scale
		depth = -relative.dot(c.forward)
		return x, y, depth, finite(x) && finite(y) && finite(depth)
	}
	relative := point.sub(c.eye)
	z := relative.dot(c.forward)
	if z <= 1e-9 || !finite(z) {
		return 0, 0, 0, false
	}
	x = float64(c.width)/2 + relative.dot(c.right)/(z*c.tanHalfX)*float64(c.width)/2
	y = float64(c.height)/2 - relative.dot(c.up)/(z*c.tanHalfY)*float64(c.height)/2
	depth = 1 / z
	return x, y, depth, finite(x) && finite(y) && finite(depth)
}

// inverseDepth is the weight used for perspective-correct attribute
// interpolation. Orthographic projection needs no correction.
func (c *camera) inverseDepth(depth float64) float64 {
	if c.orthographic {
		return 1
	}
	return depth
}

type renderer struct {
	camera        *camera
	mode          RenderMode
	width         int
	parts         []*renderPart
	background    [3]float64
	cullBackfaces bool
}

func renderInBands(renderer *renderer, pixels []float32, depth []float64, height, workers int) {
	bands := min(height, max(1, workers*4))
	bandHeight := (height + bands - 1) / bands
	actualBands := (height + bandHeight - 1) / bandHeight
	jobs := make(chan [2]int, actualBands)
	for start := 0; start < height; start += bandHeight {
		end := min(start+bandHeight, height)
		jobs <- [2]int{start, end}
	}
	close(jobs)
	var wait sync.WaitGroup
	wait.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer wait.Done()
			for job := range jobs {
				renderer.renderBand(pixels, depth, job[0], job[1])
			}
		}()
	}
	wait.Wait()
}

func (r *renderer) renderBand(pixels []float32, depth []float64, startY, endY int) {
	for y := startY; y < endY; y++ {
		row := y * r.width
		for x := 0; x < r.width; x++ {
			index := row + x
			depth[index] = math.Inf(-1)
			pixels[index*3] = float32(r.background[0])
			pixels[index*3+1] = float32(r.background[1])
			pixels[index*3+2] = float32(r.background[2])
		}
	}
	for _, part := range r.parts {
		r.drawPartBand(part, pixels, depth, startY, endY)
	}
	if r.mode == ModeWireframe {
		for _, part := range r.parts {
			r.drawWireBand(part, pixels, depth, startY, endY)
		}
	}
}

const mainLightX = 0.4499 // normalized (0.45, 1, 0.6)
const mainLightY = 0.9998
const mainLightZ = 0.5999

func (r *renderer) drawPartBand(part *renderPart, pixels []float32, depth []float64, startY, endY int) {
	for triangle := 0; triangle < part.triangleCount; triangle++ {
		a, b, c, ok := part.source.triangle(triangle, part.indices)
		if !ok {
			continue
		}
		ax, ay, az := part.vertex(a)
		bx, by, bz := part.vertex(b)
		cx, cy, cz := part.vertex(c)
		if !finite(ax) || !finite(ay) || !finite(az) ||
			!finite(bx) || !finite(by) || !finite(bz) ||
			!finite(cx) || !finite(cy) || !finite(cz) {
			continue
		}
		p0 := vec3{ax, ay, az}
		p1 := vec3{bx, by, bz}
		p2 := vec3{cx, cy, cz}
		x0, y0, z0, ok0 := r.camera.project(p0)
		x1, y1, z1, ok1 := r.camera.project(p1)
		x2, y2, z2, ok2 := r.camera.project(p2)
		if !ok0 || !ok1 || !ok2 {
			continue
		}
		faceNormal := p1.sub(p0).cross(p2.sub(p0)).normalized()
		toEye := r.camera.forward.scale(-1)
		if !r.camera.orthographic {
			toEye = r.camera.eye.sub(p0).normalized()
		}
		if r.cullBackfaces && !part.material.DoubleSided {
			if faceNormal.length() == 0 || faceNormal.dot(toEye) <= 0 {
				continue
			}
		}
		area := edgeFunction(x0, y0, x1, y1, x2, y2)
		if math.Abs(area) < 1e-12 {
			continue
		}
		minX := max(0, int(math.Floor(min3(x0, x1, x2))))
		maxX := min(r.width-1, int(math.Ceil(max3(x0, x1, x2))))
		minY := max(startY, int(math.Floor(min3(y0, y1, y2))))
		maxY := min(endY-1, int(math.Ceil(max3(y0, y1, y2))))
		if minX > maxX || minY > maxY {
			continue
		}
		invZ0 := r.camera.inverseDepth(z0)
		invZ1 := r.camera.inverseDepth(z1)
		invZ2 := r.camera.inverseDepth(z2)
		if !finite(invZ0) || !finite(invZ1) || !finite(invZ2) {
			continue
		}
		for y := minY; y <= maxY; y++ {
			py := float64(y) + 0.5
			row := y * r.width
			for x := minX; x <= maxX; x++ {
				px := float64(x) + 0.5
				b0 := edgeFunction(x1, y1, x2, y2, px, py) / area
				b1 := edgeFunction(x2, y2, x0, y0, px, py) / area
				b2 := 1 - b0 - b1
				if b0 < -1e-9 || b1 < -1e-9 || b2 < -1e-9 {
					continue
				}
				depthValue := b0*z0 + b1*z1 + b2*z2
				index := row + x
				if depthValue <= depth[index] {
					continue
				}
				denominator := b0*invZ0 + b1*invZ1 + b2*invZ2
				if denominator == 0 {
					continue
				}
				uv, hasUV := interpolateUV(part, b0, b1, b2, invZ0, invZ1, invZ2, denominator, a, b, c)
				normal := interpolateNormal(part, b0, b1, b2, invZ0, invZ1, invZ2, denominator, a, b, c)
				vertexColor := interpolateColor(part, b0, b1, b2, invZ0, invZ1, invZ2, denominator, a, b, c)
				color, discard := r.shade(part, normal, faceNormal, toEye, uv, hasUV, vertexColor)
				if discard {
					continue
				}
				depth[index] = depthValue
				pixels[index*3] = float32(color[0])
				pixels[index*3+1] = float32(color[1])
				pixels[index*3+2] = float32(color[2])
			}
		}
	}
}

func (p *renderPart) vertex(index uint32) (x, y, z float64) {
	offset := int(index) * 3
	return float64(p.positions[offset]),
		float64(p.positions[offset+1]),
		float64(p.positions[offset+2])
}

func interpolateUV(part *renderPart, b0, b1, b2, invZ0, invZ1, invZ2, denominator float64, a, b, c uint32) ([2]float64, bool) {
	if part.uvs == nil {
		return [2]float64{}, false
	}
	indices := [3]uint32{a, b, c}
	weights := [3]float64{
		b0 * invZ0 / denominator,
		b1 * invZ1 / denominator,
		b2 * invZ2 / denominator,
	}
	var uv [2]float64
	for k := 0; k < 3; k++ {
		offset := int(indices[k]) * 2
		uv[0] += weights[k] * float64(part.uvs[offset])
		uv[1] += weights[k] * float64(part.uvs[offset+1])
	}
	return uv, true
}

func interpolateNormal(part *renderPart, b0, b1, b2, invZ0, invZ1, invZ2, denominator float64, a, b, c uint32) vec3 {
	if part.normals == nil {
		return vec3{}
	}
	indices := [3]uint32{a, b, c}
	weights := [3]float64{
		b0 * invZ0 / denominator,
		b1 * invZ1 / denominator,
		b2 * invZ2 / denominator,
	}
	var normal vec3
	for k := 0; k < 3; k++ {
		offset := int(indices[k]) * 3
		normal[0] += weights[k] * float64(part.normals[offset])
		normal[1] += weights[k] * float64(part.normals[offset+1])
		normal[2] += weights[k] * float64(part.normals[offset+2])
	}
	return normal.normalized()
}

func interpolateColor(part *renderPart, b0, b1, b2, invZ0, invZ1, invZ2, denominator float64, a, b, c uint32) [4]float64 {
	if part.colors == nil {
		return [4]float64{1, 1, 1, 1}
	}
	color := [4]float64{0, 0, 0, 1}
	indices := [3]uint32{a, b, c}
	weights := [3]float64{
		b0 * invZ0 / denominator,
		b1 * invZ1 / denominator,
		b2 * invZ2 / denominator,
	}
	components := part.colorComponents
	for k := 0; k < 3; k++ {
		offset := int(indices[k]) * components
		color[0] += weights[k] * float64(part.colors[offset])
		color[1] += weights[k] * float64(part.colors[offset+1])
		color[2] += weights[k] * float64(part.colors[offset+2])
	}
	color[3] = 1
	if components == 4 {
		color[3] = 0
		for k := 0; k < 3; k++ {
			offset := int(indices[k])*components + 3
			color[3] += weights[k] * float64(part.colors[offset])
		}
	}
	return color
}

// shade returns linear RGB. The normal passed in may be zero, in which case
// the triangle's geometric normal is used for lighting.
func (r *renderer) shade(part *renderPart, normal, faceNormal, toEye vec3, uv [2]float64, hasUV bool, vertexColor [4]float64) ([3]float64, bool) {
	material := part.material
	var result [3]float64
	alpha := 1.0
	switch r.mode {
	case ModeNormals:
		n := normal
		if n.length() == 0 {
			n = faceNormal
		}
		if n.length() == 0 {
			n = toEye
		}
		result = [3]float64{n[0]*0.5 + 0.5, n[1]*0.5 + 0.5, n[2]*0.5 + 0.5}
	case ModeTextured:
		result = [3]float64{
			material.BaseColorFactor[0], material.BaseColorFactor[1], material.BaseColorFactor[2],
		}
		alpha = material.BaseColorFactor[3]
		if part.texture != nil && hasUV {
			texel := part.texture.sample(uv[0], uv[1])
			result[0] *= texel[0]
			result[1] *= texel[1]
			result[2] *= texel[2]
			alpha *= texel[3]
		}
		result[0] *= vertexColor[0]
		result[1] *= vertexColor[1]
		result[2] *= vertexColor[2]
		alpha *= vertexColor[3]
		if material.AlphaMode == "MASK" && alpha < 0.5 {
			return [3]float64{}, true
		}
		result = applyLighting(result, normal, faceNormal, toEye)
	default: // clay and wireframe
		result = applyLighting([3]float64{0.5, 0.5, 0.5}, normal, faceNormal, toEye)
	}
	if !part.highlighted {
		result[0] *= 0.35
		result[1] *= 0.35
		result[2] *= 0.35
	}
	for i := range result {
		if !finite(result[i]) {
			result[i] = 0
		}
		result[i] = math.Min(math.Max(result[i], 0), 1)
	}
	return result, false
}

func applyLighting(base [3]float64, normal, faceNormal, toEye vec3) [3]float64 {
	if normal.length() == 0 {
		normal = faceNormal
	}
	if normal.length() == 0 {
		normal = toEye
	}
	if normal.dot(toEye) < 0 {
		normal = normal.scale(-1)
	}
	headlight := math.Max(normal.dot(toEye.normalized()), 0)
	mainLight := math.Max(normal.dot(vec3{mainLightX, mainLightY, mainLightZ}.normalized()), 0)
	light := 0.22 + 0.55*headlight + 0.65*mainLight
	light = math.Min(light, 1.0)
	return [3]float64{base[0] * light, base[1] * light, base[2] * light}
}

func min3(a, b, c float64) float64 { return math.Min(a, math.Min(b, c)) }
func max3(a, b, c float64) float64 { return math.Max(a, math.Max(b, c)) }

func edgeFunction(ax, ay, bx, by, px, py float64) float64 {
	return (bx-ax)*(py-ay) - (by-ay)*(px-ax)
}

func (r *renderer) drawWireBand(part *renderPart, pixels []float32, depth []float64, startY, endY int) {
	for triangle := 0; triangle < part.triangleCount; triangle++ {
		a, b, c, ok := part.source.triangle(triangle, part.indices)
		if !ok {
			continue
		}
		points := [3]uint32{a, b, c}
		var screen [3][3]float64
		valid := true
		for k := 0; k < 3; k++ {
			x, y, z := part.vertex(points[k])
			if !finite(x) || !finite(y) || !finite(z) {
				valid = false
				break
			}
			sx, sy, depthValue, ok := r.camera.project(vec3{x, y, z})
			if !ok {
				valid = false
				break
			}
			screen[k] = [3]float64{sx, sy, depthValue}
		}
		if !valid {
			continue
		}
		for k := 0; k < 3; k++ {
			next := (k + 1) % 3
			drawLine(r, pixels, depth, startY, endY, screen[k], screen[next])
		}
	}
}

func drawLine(r *renderer, pixels []float32, depth []float64, startY, endY int, from, to [3]float64) {
	deltaX := to[0] - from[0]
	deltaY := to[1] - from[1]
	steps := int(math.Ceil(math.Max(math.Abs(deltaX), math.Abs(deltaY))))
	if steps <= 0 {
		return
	}
	bias := r.camera.depthBias
	for step := 0; step <= steps; step++ {
		t := float64(step) / float64(steps)
		x := int(math.Round(from[0] + deltaX*t))
		y := int(math.Round(from[1] + deltaY*t))
		if x < 0 || x >= r.width || y < startY || y >= endY {
			continue
		}
		depthValue := from[2] + (to[2]-from[2])*t + bias
		index := y*r.width + x
		if depthValue <= depth[index] {
			continue
		}
		depth[index] = depthValue
		pixels[index*3] = 0.02
		pixels[index*3+1] = 0.02
		pixels[index*3+2] = 0.02
	}
}

func downsample(pixels []float32, supersampledWidth, width, height int) *image.NRGBA {
	result := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			var red, green, blue float64
			for dy := 0; dy < 2; dy++ {
				row := (y*2 + dy) * supersampledWidth
				for dx := 0; dx < 2; dx++ {
					offset := (row + x*2 + dx) * 3
					red += float64(pixels[offset])
					green += float64(pixels[offset+1])
					blue += float64(pixels[offset+2])
				}
			}
			red /= 4
			green /= 4
			blue /= 4
			offset := result.PixOffset(x, y)
			result.Pix[offset] = linearToSRGBByte(red)
			result.Pix[offset+1] = linearToSRGBByte(green)
			result.Pix[offset+2] = linearToSRGBByte(blue)
			result.Pix[offset+3] = 0xFF
		}
	}
	return result
}
