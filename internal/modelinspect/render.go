package modelinspect

import (
	"context"
	"errors"
	"image"
	"image/color"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
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
	// ModeTextured shades base color, COLOR_0, normal maps and, when the
	// material explicitly declares metallic-roughness factors or a texture, an
	// approximate metallic-roughness model lit by a headlight, one fixed main
	// light and an analytic environment.
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
	// Context, when set, stops rasterisation once it is done; Render then
	// returns its error.
	Context context.Context
}

// renderCoverageFactor bounds rasterisation work: the summed screen bounding
// boxes of all drawn triangles may cover the supersampled image at most this
// many times. Real models stay far below it; a mesh that repeats a
// full-screen triangle millions of times is rejected instead of running for
// hours.
const renderCoverageFactor = 256

// Render draws one view of m with a software rasteriser and returns an opaque
// NRGBA image. It uses a z-buffer, 2x supersampling and bilinear texture
// sampling with the sampler's wrap modes.
//
// In textured mode Render decodes base color, normal and, for materials that
// explicitly declare them, metallic-roughness textures. Each slot uses its own
// texCoord and KHR_texture_transform. The limits passed to Parse decide which
// textures are eligible: an image over MaxTexturePixels is replaced by material
// factors, and an optional metallic-roughness or normal texture that exhausts a
// cumulative budget is skipped rather than failing the render. Documentation
// and callers should treat the metallic-roughness path as an approximation: it
// has no image-based environment lighting, prefiltered environment map, BRDF
// lookup table or shadows.
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

	ctx := req.Context
	if ctx == nil {
		ctx = context.Background()
	}
	raster := renderer{
		camera:        camera,
		mode:          mode,
		width:         supersampledWidth,
		parts:         parts,
		background:    backgroundLinear,
		cullBackfaces: req.CullBackfaces,
		ctx:           ctx,
		coverageLeft:  int64(supersampledWidth) * int64(supersampledHeight) * renderCoverageFactor,
	}
	renderInBands(&raster, pixels, depth, supersampledHeight, workers)
	if err := raster.failure(); err != nil {
		return nil, err
	}

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
	uvs             []float32 // base-color UVs, two per vertex; may be nil
	colors          []float32 // linear, colorComponents per vertex; may be nil
	colorComponents int
	indices         []uint32        // nil for non-indexed primitives
	texture         *decodedTexture // base color; sampled as sRGB
	mrTexture       *decodedTexture // G is roughness, B is metallic; sampled linear
	mrUVs           []float32
	normalTexture   *decodedTexture // tangent-space normal map; sampled linear
	normalUVs       []float32
	normalScale     float64
	highlighted     bool
	// mirrored marks a negative-determinant world transform, which reverses
	// the winding of every triangle.
	mirrored      bool
	bounds        bounds
	triangleCount int
}

var fallbackMaterial = Material{
	BaseColorFactor: [4]float64{1, 1, 1, 1},
	MetallicFactor:  1,
	RoughnessFactor: 1,
	AlphaMode:       "OPAQUE",
	AlphaCutoff:     0.5,
}

func (m *Model) buildRenderParts(req RenderRequest, mode RenderMode, highlight map[string]bool) ([]*renderPart, int64, error) {
	parts := make([]*renderPart, 0, len(m.parts))
	totalTriangles := int64(0)
	for _, source := range m.parts {
		if req.Context != nil {
			if err := req.Context.Err(); err != nil {
				return nil, 0, err
			}
		}
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
		pose := m.pose(source)
		worldPositions := make([]float32, source.vertexCount*3)
		for vertex := 0; vertex < source.vertexCount; vertex++ {
			point := pose.point(vertex,
				float64(positions[vertex*3]),
				float64(positions[vertex*3+1]),
				float64(positions[vertex*3+2]),
			)
			worldPositions[vertex*3] = float32(point[0])
			worldPositions[vertex*3+1] = float32(point[1])
			worldPositions[vertex*3+2] = float32(point[2])
		}
		renderPart := &renderPart{
			source: source, material: material,
			positions: worldPositions, indices: indices,
			highlighted:   len(highlight) == 0 || highlight[source.id],
			mirrored:      pose.mirrored,
			triangleCount: source.partTriangles(indices),
		}
		renderPart.bounds = renderPart.referencedBounds()
		totalTriangles += int64(renderPart.triangleCount)

		normals, err := m.normals(source)
		if err != nil {
			return nil, 0, err
		}
		if normals != nil {
			renderPart.normals = make([]float32, len(normals))
			for i := 0; i < len(normals); i += 3 {
				normal := pose.normal(i/3, vec3{
					float64(normals[i]), float64(normals[i+1]), float64(normals[i+2]),
				})
				renderPart.normals[i] = float32(normal[0])
				renderPart.normals[i+1] = float32(normal[1])
				renderPart.normals[i+2] = float32(normal[2])
			}
		}
		if mode == ModeTextured {
			if material.BaseColorTexture != nil {
				texture, uvs, err := m.renderTextureSlot(source, material.BaseColorTexture, false)
				if err != nil {
					return nil, 0, err
				}
				renderPart.texture, renderPart.uvs = texture, uvs
			}
			if material.MetallicRoughnessTexture != nil {
				texture, uvs, err := m.renderTextureSlot(source, material.MetallicRoughnessTexture, true)
				if err != nil {
					return nil, 0, err
				}
				renderPart.mrTexture, renderPart.mrUVs = texture, uvs
			}
			if material.NormalTexture != nil && material.NormalTexture.Scale != 0 {
				// A zero scale maps every texel to the unperturbed normal, so
				// the texture is equivalent to no normal map and is not decoded.
				texture, uvs, err := m.renderTextureSlot(source, &material.NormalTexture.TextureRef, true)
				if err != nil {
					return nil, 0, err
				}
				renderPart.normalTexture, renderPart.normalUVs = texture, uvs
				renderPart.normalScale = material.NormalTexture.Scale
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

// renderTextureSlot decodes one textured material slot, applying that slot's
// own texCoord set and KHR_texture_transform. A missing TEXCOORD_n skips the
// slot. When optional is set, exhausting a cumulative texture budget degrades
// the slot to material factors instead of failing the render; base color keeps
// its historical error semantics.
func (m *Model) renderTextureSlot(source *part, ref *TextureRef, optional bool) (*decodedTexture, []float32, error) {
	uvs, err := m.texCoords(source, ref.TexCoord)
	if err != nil {
		return nil, nil, err
	}
	if uvs == nil {
		return nil, nil, nil
	}
	texture, err := m.decodeTexture(ref.Texture)
	if err != nil {
		if optional && errors.Is(err, ErrTooLarge) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	if texture == nil {
		return nil, nil, nil
	}
	return texture, transformTexCoords(uvs, ref.Transform), nil
}

// referencedBounds frames only the vertices the part's triangles use, so an
// unreferenced vertex far from the mesh cannot shrink the model in the view.
// Indices were validated against the position count by the caller.
func (p *renderPart) referencedBounds() bounds {
	var box bounds
	add := func(vertex uint32) {
		offset := int(vertex) * 3
		box.add(vec3{
			float64(p.positions[offset]),
			float64(p.positions[offset+1]),
			float64(p.positions[offset+2]),
		})
	}
	if p.indices == nil {
		for vertex := 0; vertex < usedElements(p.source.mode, p.source.vertexCount); vertex++ {
			add(uint32(vertex))
		}
		return box
	}
	for _, vertex := range p.indices[:usedElements(p.source.mode, len(p.indices))] {
		add(vertex)
	}
	return box
}

// transformTexCoords applies a KHR_texture_transform. The decoded accessor
// is shared through the cache, so a transform returns a new slice.
func transformTexCoords(uvs []float32, transform *TextureTransform) []float32 {
	if transform == nil {
		return uvs
	}
	out := make([]float32, len(uvs))
	for i := 0; i+1 < len(uvs); i += 2 {
		u, v := transform.apply(float64(uvs[i]), float64(uvs[i+1]))
		out[i], out[i+1] = float32(u), float32(v)
	}
	return out
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

	ctx          context.Context
	coverageLeft int64 // atomic; shared by all bands
	stopped      atomic.Bool
	stopOnce     sync.Once
	stopErr      error
}

// renderCheckInterval is how many triangles a band draws between checks of
// the context and the coverage budget.
const renderCheckInterval = 1024

func (r *renderer) stop(err error) {
	r.stopOnce.Do(func() { r.stopErr = err })
	r.stopped.Store(true)
}

func (r *renderer) failure() error {
	if !r.stopped.Load() {
		return nil
	}
	return r.stopErr
}

// halted reports whether drawing must stop: the render was already stopped or
// its context is done.
func (r *renderer) halted() bool {
	if r.stopped.Load() {
		return true
	}
	if err := r.ctx.Err(); err != nil {
		r.stop(err)
		return true
	}
	return false
}

// spend charges one triangle's screen bounding box against the coverage
// budget and reports whether drawing may continue.
func (r *renderer) spend(area int64) bool {
	if atomic.AddInt64(&r.coverageLeft, -area) < 0 {
		r.stop(toolargef("render coverage exceeds %d times the image", renderCoverageFactor))
		return false
	}
	return true
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

// Fixed main-light direction components, normalized once into
// mainLightDirection.
const mainLightX = 0.4499
const mainLightY = 0.9998
const mainLightZ = 0.5999

// mainLightDirection is the normalized fixed main light direction. Allocate
// it once because shading is per pixel.
var mainLightDirection = vec3{mainLightX, mainLightY, mainLightZ}.normalized()

func (r *renderer) drawPartBand(part *renderPart, pixels []float32, depth []float64, startY, endY int) {
	for triangle := 0; triangle < part.triangleCount; triangle++ {
		if triangle%renderCheckInterval == 0 && r.halted() {
			return
		}
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
		if part.mirrored {
			// A negative-determinant world matrix reverses the winding, so the
			// geometric normal computed from world positions points to the back
			// of the material's front face.
			faceNormal = faceNormal.scale(-1)
		}
		toEye := r.camera.forward.scale(-1)
		if !r.camera.orthographic {
			toEye = r.camera.eye.sub(p0).normalized()
		}
		if r.cullBackfaces && !part.material.DoubleSided {
			if faceNormal.length() == 0 || faceNormal.dot(toEye) <= 0 {
				continue
			}
		}
		// The normal map's tangent frame comes from the triangle's world-space
		// positions and its own UV set, so no TANGENT attribute is required.
		// A degenerate UV mapping gets an arbitrary frame; the bump direction
		// is then meaningless but the surface still renders.
		var tangent, bitangent vec3
		useNormalMap := part.normalTexture != nil
		if useNormalMap {
			uv0 := textureUV(part.normalUVs, a)
			uv1 := textureUV(part.normalUVs, b)
			uv2 := textureUV(part.normalUVs, c)
			var basisOK bool
			tangent, bitangent, basisOK = triangleTangentBasis(p0, p1, p2, uv0, uv1, uv2)
			if !basisOK {
				tangent = perpendicularTo(faceNormal)
				bitangent = faceNormal.cross(tangent)
			}
			// Orthogonalise once per triangle. Per-pixel normals are then
			// mapped with this frame; the interpolated vertex normal still
			// varies per pixel, so a smooth surface keeps its shading normal.
			tangent, bitangent = orthonormalTangentBasis(tangent, bitangent, faceNormal)
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
		if !r.spend(int64(maxX-minX+1) * int64(maxY-minY+1)) {
			return
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
				inverseDenominator := 1 / denominator
				weights := [3]float64{
					b0 * invZ0 * inverseDenominator,
					b1 * invZ1 * inverseDenominator,
					b2 * invZ2 * inverseDenominator,
				}
				uv, hasUV := interpolateUVs(part.uvs, weights, a, b, c)
				mrUV, hasMRUV := interpolateUVs(part.mrUVs, weights, a, b, c)
				normal := interpolateNormal(part, weights, a, b, c, !useNormalMap)
				if useNormalMap {
					normalUV, hasNormalUV := interpolateUVs(part.normalUVs, weights, a, b, c)
					if hasNormalUV {
						texel := part.normalTexture.sampleLinear(normalUV[0], normalUV[1])
						normal = applyNormalMap(texel, part.normalScale, normal, faceNormal, tangent, bitangent)
					} else {
						normal = normal.normalized()
					}
				}
				vertexColor := interpolateColor(part, weights, a, b, c)
				color, discard := r.shade(part, surface{
					normal: normal, faceNormal: faceNormal, toEye: toEye,
					uv: uv, hasUV: hasUV, mrUV: mrUV, hasMRUV: hasMRUV,
					vertexColor: vertexColor,
				})
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

// textureUV returns one vertex's texture coordinates from a decoded UV slice.
func textureUV(uvs []float32, vertex uint32) [2]float64 {
	offset := int(vertex) * 2
	return [2]float64{float64(uvs[offset]), float64(uvs[offset+1])}
}

func interpolateUVs(uvs []float32, weights [3]float64, a, b, c uint32) ([2]float64, bool) {
	if uvs == nil {
		return [2]float64{}, false
	}
	indices := [3]uint32{a, b, c}
	var uv [2]float64
	for k := 0; k < 3; k++ {
		offset := int(indices[k]) * 2
		uv[0] += weights[k] * float64(uvs[offset])
		uv[1] += weights[k] * float64(uvs[offset+1])
	}
	return uv, true
}

// interpolateNormal returns the perspective-correct normal. A normal-mapped
// surface skips the normalisation because applyNormalMap normalises the mapped
// result, saving one square root per pixel.
func interpolateNormal(part *renderPart, weights [3]float64, a, b, c uint32, normalize bool) vec3 {
	if part.normals == nil {
		return vec3{}
	}
	indices := [3]uint32{a, b, c}
	var normal vec3
	for k := 0; k < 3; k++ {
		offset := int(indices[k]) * 3
		normal[0] += weights[k] * float64(part.normals[offset])
		normal[1] += weights[k] * float64(part.normals[offset+1])
		normal[2] += weights[k] * float64(part.normals[offset+2])
	}
	if !normalize {
		return normal
	}
	return normal.normalized()
}

func interpolateColor(part *renderPart, weights [3]float64, a, b, c uint32) [4]float64 {
	if part.colors == nil {
		return [4]float64{1, 1, 1, 1}
	}
	color := [4]float64{0, 0, 0, 1}
	indices := [3]uint32{a, b, c}
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

// surface holds the interpolated per-pixel shading inputs. normal is already
// perturbed by the normal map when the part has one.
type surface struct {
	normal      vec3
	faceNormal  vec3
	toEye       vec3
	uv          [2]float64
	hasUV       bool
	mrUV        [2]float64
	hasMRUV     bool
	vertexColor [4]float64
}

// shade returns linear RGB. The normal passed in may be zero, in which case
// the triangle's geometric normal is used for lighting.
func (r *renderer) shade(part *renderPart, s surface) ([3]float64, bool) {
	material := part.material
	var result [3]float64
	alpha := 1.0
	switch r.mode {
	case ModeNormals:
		n := s.normal
		if n.length() == 0 {
			n = s.faceNormal
		}
		if n.length() == 0 {
			n = s.toEye
		}
		result = [3]float64{n[0]*0.5 + 0.5, n[1]*0.5 + 0.5, n[2]*0.5 + 0.5}
	case ModeTextured:
		result = [3]float64{
			material.BaseColorFactor[0], material.BaseColorFactor[1], material.BaseColorFactor[2],
		}
		alpha = material.BaseColorFactor[3]
		if part.texture != nil && s.hasUV {
			texel := part.texture.sampleSRGB(s.uv[0], s.uv[1])
			result[0] *= texel[0]
			result[1] *= texel[1]
			result[2] *= texel[2]
			alpha *= texel[3]
		}
		result[0] *= s.vertexColor[0]
		result[1] *= s.vertexColor[1]
		result[2] *= s.vertexColor[2]
		alpha *= s.vertexColor[3]
		if material.AlphaMode == "MASK" && alpha < material.AlphaCutoff {
			return [3]float64{}, true
		}
		if material.pbrDeclared {
			metallic := material.MetallicFactor
			roughness := material.RoughnessFactor
			if part.mrTexture != nil && s.hasMRUV {
				texel := part.mrTexture.sampleLinear(s.mrUV[0], s.mrUV[1])
				roughness *= texel[1]
				metallic *= texel[2]
			}
			result = pbrLighting(result, clampUnit(metallic), clampUnit(roughness),
				s.normal, s.faceNormal, s.toEye)
		} else {
			result = applyLighting(result, s.normal, s.faceNormal, s.toEye)
		}
	default: // clay and wireframe
		result = applyLighting([3]float64{0.5, 0.5, 0.5}, s.normal, s.faceNormal, s.toEye)
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
		result[i] = min(max(result[i], 0), 1)
	}
	return result, false
}

// triangleTangentBasis derives world-space tangent and bitangent vectors from
// a triangle's positions and its normal-map UVs. The vectors are not
// normalised: the caller orthogonalises them once against the triangle's
// geometric normal. A degenerate UV mapping returns ok=false.
func triangleTangentBasis(p0, p1, p2 vec3, uv0, uv1, uv2 [2]float64) (vec3, vec3, bool) {
	edge1 := p1.sub(p0)
	edge2 := p2.sub(p0)
	du1, dv1 := uv1[0]-uv0[0], uv1[1]-uv0[1]
	du2, dv2 := uv2[0]-uv0[0], uv2[1]-uv0[1]
	determinant := du1*dv2 - du2*dv1
	if math.Abs(determinant) < 1e-12 {
		return vec3{}, vec3{}, false
	}
	inverse := 1 / determinant
	tangent := edge1.scale(dv2).sub(edge2.scale(dv1)).scale(inverse)
	bitangent := edge2.scale(du1).sub(edge1.scale(du2)).scale(inverse)
	if !tangent.finite() || !bitangent.finite() ||
		tangent.length() == 0 || bitangent.length() == 0 {
		return vec3{}, vec3{}, false
	}
	return tangent, bitangent, true
}

// perpendicularTo returns a unit vector perpendicular to n.
func perpendicularTo(n vec3) vec3 {
	axis := vec3{0, 1, 0}
	if math.Abs(n[1]) > 0.9 {
		axis = vec3{1, 0, 0}
	}
	tangent := n.cross(axis).normalized()
	if tangent.length() == 0 {
		return vec3{1, 0, 0}
	}
	return tangent
}

// orthonormalTangentBasis projects a triangle's tangent frame onto the plane
// perpendicular to n and returns unit tangent and bitangent vectors. It runs
// once per triangle, not once per pixel.
func orthonormalTangentBasis(tangent, bitangent, n vec3) (vec3, vec3) {
	n = n.normalized()
	if n.length() == 0 {
		return vec3{1, 0, 0}, vec3{0, 1, 0}
	}
	t := tangent.sub(n.scale(n.dot(tangent)))
	if t.length() == 0 {
		t = perpendicularTo(n)
	}
	t = t.normalized()
	b := bitangent.sub(n.scale(n.dot(bitangent))).sub(t.scale(t.dot(bitangent)))
	if b.length() == 0 {
		b = n.cross(t)
	}
	return t, b.normalized()
}

// applyNormalMap maps a tangent-space normal sample to world space with the
// triangle's orthonormal frame. The frame is orthogonalised per triangle, so
// this costs one normalisation per pixel.
func applyNormalMap(texel [4]float64, scale float64, normal, faceNormal, tangent, bitangent vec3) vec3 {
	if scale == 0 {
		return normal
	}
	n := normal
	if n == (vec3{}) {
		n = faceNormal
	}
	if n == (vec3{}) {
		n = vec3{0, 0, 1}
	}
	tangentX := (texel[0]*2 - 1) * scale
	tangentY := (texel[1]*2 - 1) * scale
	tangentZ := texel[2]*2 - 1
	mapped := tangent.scale(tangentX).add(bitangent.scale(tangentY)).add(n.scale(tangentZ))
	length := mapped.length()
	if length == 0 {
		return n
	}
	return mapped.scale(1 / length)
}

func clampUnit(value float64) float64 {
	return min(max(value, 0), 1)
}

// pbrLighting approximates a metallic-roughness BRDF. It reuses the headlight
// and fixed main light of applyLighting, adds a GGX specular lobe and a cheap
// analytic hemisphere environment so metals are not black away from
// highlights. It is not image-based lighting: there is no environment map,
// prefiltered radiance or BRDF lookup table, and there are no shadows.
func pbrLighting(baseColor [3]float64, metallic, roughness float64, normal, faceNormal, toEye vec3) [3]float64 {
	if normal == (vec3{}) {
		normal = faceNormal
	}
	if normal == (vec3{}) {
		normal = toEye
	}
	if normal.dot(toEye) < 0 {
		normal = normal.scale(-1)
	}
	// Interpolated and normal-mapped normals arrive normalised, and the camera
	// directions are unit vectors, so the hot path avoids extra square roots.
	n := normal
	v := toEye
	if v == (vec3{}) {
		v = vec3{0, 0, 1}
	}
	ndv := max(n.dot(v), 1e-4)
	invFourNdv := 0.25 / ndv

	// Keep roughness above a tiny floor so the GGX denominator stays finite.
	roughness = min(max(roughness, 0.04), 1)
	alpha := roughness * roughness
	alpha2 := alpha * alpha

	nonMetal := 1 - metallic
	f0 := [3]float64{
		0.04 + (baseColor[0]-0.04)*metallic,
		0.04 + (baseColor[1]-0.04)*metallic,
		0.04 + (baseColor[2]-0.04)*metallic,
	}
	diffuse := [3]float64{
		baseColor[0] * nonMetal * (1 - f0[0]),
		baseColor[1] * nonMetal * (1 - f0[1]),
		baseColor[2] * nonMetal * (1 - f0[2]),
	}

	// The Smith k and the view denominator are shared by both lights.
	k := (roughness + 1) * (roughness + 1) / 8
	oneMinusK := 1 - k
	viewDenominator := ndv*oneMinusK + k
	alpha2MinusOne := alpha2 - 1

	var result [3]float64
	// Headlight: the light direction equals the view direction, so the half
	// vector equals both and the Fresnel term is exactly F0.
	distribution := ggxDistribution(ndv, alpha2, alpha2MinusOne)
	geometry := ndv * ndv / (viewDenominator * viewDenominator)
	specular := min(distribution*geometry*invFourNdv, 4) * 0.55
	diffuseScale := ndv * 0.55
	result[0] = diffuse[0]*diffuseScale + f0[0]*specular
	result[1] = diffuse[1]*diffuseScale + f0[1]*specular
	result[2] = diffuse[2]*diffuseScale + f0[2]*specular
	// Fixed main light, matching applyLighting's direction and weight.
	if ndl := max(n.dot(mainLightDirection), 0); ndl > 0 {
		half := mainLightDirection.add(v).normalized()
		ndh := max(n.dot(half), 0)
		vdh := max(v.dot(half), 0)
		distribution := ggxDistribution(ndh, alpha2, alpha2MinusOne)
		geometry := ndv * ndl / (viewDenominator * (ndl*oneMinusK + k))
		fresnel := fresnelSchlick(vdh, f0)
		// D*G*F/(4*NdotV*NdotL) times NdotL and the light intensity. The
		// specular cap keeps a near-mirror highlight from producing extreme
		// fireflies in this unfiltered approximation.
		specular := min(distribution*geometry*invFourNdv, 4) * 0.65
		diffuseScale := ndl * 0.65
		result[0] += diffuse[0]*diffuseScale + fresnel[0]*specular
		result[1] += diffuse[1]*diffuseScale + fresnel[1]*specular
		result[2] += diffuse[2]*diffuseScale + fresnel[2]*specular
	}

	// A dim sky above and ground below stand in for an environment probe. The
	// diffuse ambient matches the legacy 0.22 constant; the specular part
	// gives metals a coloured reflection that sharpens as roughness drops.
	const ambientStrength = 0.22
	horizon := 0.5 + 0.5*n[1]
	environment := [3]float64{
		0.10 + 0.30*horizon,
		0.09 + 0.36*horizon,
		0.08 + 0.47*horizon,
	}
	environmentSpecular := 0.30 + 0.45*(1-roughness)
	ambientFresnel := fresnelSchlick(ndv, f0)
	result[0] += diffuse[0]*ambientStrength + ambientFresnel[0]*environment[0]*environmentSpecular
	result[1] += diffuse[1]*ambientStrength + ambientFresnel[1]*environment[1]*environmentSpecular
	result[2] += diffuse[2]*ambientStrength + ambientFresnel[2]*environment[2]*environmentSpecular
	return result
}

func ggxDistribution(ndh, alpha2, alpha2MinusOne float64) float64 {
	denominator := ndh*ndh*alpha2MinusOne + 1
	return alpha2 / (math.Pi * denominator * denominator)
}

func fresnelSchlick(cosTheta float64, f0 [3]float64) [3]float64 {
	oneMinus := 1 - min(max(cosTheta, 0), 1)
	squared := oneMinus * oneMinus
	factor := squared * squared * oneMinus
	return [3]float64{
		f0[0] + (1-f0[0])*factor,
		f0[1] + (1-f0[1])*factor,
		f0[2] + (1-f0[2])*factor,
	}
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
	headlight := max(normal.dot(toEye), 0)
	mainLight := max(normal.dot(mainLightDirection), 0)
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
		if triangle%renderCheckInterval == 0 && r.halted() {
			return
		}
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
