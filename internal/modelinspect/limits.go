package modelinspect

// Limits caps the resources Parse, Inspect and Render may use. Every field is
// supplied by the caller; this package defines no production defaults.
//
// A non-positive value disables that individual cap. Disabling a cap makes the
// caller responsible for the input, because a declared count can then be as
// large as the input file allows. MaxNodes and MaxDepth are checked while the
// node hierarchy is walked, MaxBytes before any data is read, and the triangle
// and vertex caps while primitives are indexed.
type Limits struct {
	// MaxBytes is the largest GLB file accepted by Parse.
	MaxBytes int64
	// MaxTriangles is the total number of triangles across all rendered parts.
	MaxTriangles int64
	// MaxVertices is the total number of POSITION elements across all rendered
	// parts, counting an instanced mesh once per node that uses it.
	MaxVertices int64
	// MaxTexturePixels is the largest image (width times height) that Render
	// will decode. Larger images are reported but replaced by the material
	// factor.
	MaxTexturePixels int64
	// MaxTotalTexturePixels caps the total pixel count of all distinct images
	// Render decodes in one Model. Zero disables that cumulative cap.
	MaxTotalTexturePixels int64
	// MaxTextureBytes caps the total decoded RGBA bytes (four per pixel) of all
	// distinct images Render decodes in one Model. Zero disables that
	// cumulative cap.
	MaxTextureBytes int64
	// MaxNodes is the largest node count accepted in the JSON document.
	MaxNodes int
	// MaxDepth is the deepest node path accepted while walking the scene.
	MaxDepth int
}

// Fixed ceilings that apply whatever Limits a caller passes. Zero-triangle
// primitives pass MaxTriangles and MaxVertices, so instancing them across many
// nodes could otherwise multiply into millions of parts, and long node names
// are copied into every descendant's path.
const (
	maxParts     = 1 << 17
	maxPathBytes = 16 << 10
)

// DefaultLimits returns limits suitable for tests and for the model sizes this
// package was designed for: roughly two million triangles and a 62-68 MiB GLB.
// Production callers should choose their own values.
func DefaultLimits() Limits {
	return Limits{
		MaxBytes:              1 << 30,
		MaxTriangles:          10_000_000,
		MaxVertices:           20_000_000,
		MaxTexturePixels:      16_777_216, // 4096 x 4096
		MaxTotalTexturePixels: 33_554_432, // two 4096 x 4096 images
		MaxTextureBytes:       134_217_728,
		MaxNodes:              65_536,
		MaxDepth:              256,
	}
}
