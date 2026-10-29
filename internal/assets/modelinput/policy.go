// Package modelinput validates the deliberately bounded uploaded static GLB
// profile. It is separate from the historical delivered-output contract.
package modelinput

const ProfileID = "static-glb-v1"
const ProfileRevision = "1"

// GeneratedProfileID is the conversion-input contract for a GLB that a
// successful platform Task already materialized (#264). It shares the static
// structure rules but is never an upload profile: its sources passed the
// delivered-asset isolation check when they were stored, and every conversion
// output is checked again by the asset store before it is committed. Rasters
// are therefore bounded by their headers here and never fully decoded.
const GeneratedProfileID = "generated-glb-v1"
const GeneratedProfileRevision = "1"

// MaxElements counts JSON values and object keys across every structure.
// MaxWork counts wire passes, those structural visits, unique accessor scalar
// decodes/normal checks, unique image bytes and both possible pixel decodes.
// Pixel ceilings apply before full decode; node/geometry totals apply to the
// complete document, while HasTextures is scoped to the active scene.
type Limits struct {
	MaxBytes, MaxJSONBytes, MaxDepth, MaxElements int64
	MaxVertices, MaxTriangles, MaxNodes, MaxWork  int64
	MaxImagePixels, MaxTotalImagePixels           int64
}
type Policy struct {
	ProfileID string
	Revision  string
	Limits    Limits
}
type Profile struct {
	ProfileID                        string
	Revision                         string
	SHA256                           string
	Size, Vertices, Triangles, Nodes int64
	// InstancedTriangles counts every node mesh instance and each unreferenced mesh once.
	// It is a conservative derived total; legacy MaxTriangles still bounds definitions.
	InstancedTriangles int64
	HasTextures        bool
	// HasSingleBaseColorTexture covers every mesh primitive, including unused meshes.
	HasSingleBaseColorTexture bool
}
type Kind string

const (
	Invalid       Kind = "invalid"
	Unsupported   Kind = "unsupported"
	Limit         Kind = "limit"
	InvalidPolicy Kind = "policy"
	IO            Kind = "io"
	Canceled      Kind = "canceled"
)

type Error struct {
	Kind   Kind
	Reason string
}

func (e *Error) Error() string               { return "model_input." + string(e.Kind) + "." + e.Reason }
func failure(kind Kind, reason string) error { return &Error{Kind: kind, Reason: reason} }

// Ceilings are the parser safety boundaries of a profile: the largest Limits a
// Policy may declare. They are the single source of those numbers; Validate
// rejects anything above them and a caller that only reads a delivered GLB
// takes them from GeneratedPolicy instead of copying them.
func Ceilings(profileID, revision string) (Limits, bool) {
	// Generated sources never decode pixels, so their raster ceilings bound
	// header dimensions only; uploads keep the full-decode ceilings.
	limits := Limits{
		MaxBytes: 256 << 20, MaxJSONBytes: 32 << 20, MaxDepth: 256, MaxElements: 1000000,
		MaxVertices: 16000000, MaxTriangles: 16000000, MaxNodes: 100000, MaxWork: 1 << 32,
		MaxImagePixels: 1 << 25, MaxTotalImagePixels: 1 << 27,
	}
	switch {
	case profileID == ProfileID && revision == ProfileRevision:
	case profileID == GeneratedProfileID && revision == GeneratedProfileRevision:
		limits.MaxImagePixels, limits.MaxTotalImagePixels = 1<<28, 1<<30
	default:
		return Limits{}, false
	}
	return limits, true
}

// GeneratedPolicy is the policy for reading a GLB that a successful platform
// Task or an owned upload already stored, at the profile's own ceilings. It is
// a parser safety bound, not a product limit: a product limit such as a
// triangle ceiling is compared with the measured Profile (see
// Profile.ExceedsInstancedTriangles).
func GeneratedPolicy() Policy {
	limits, _ := Ceilings(GeneratedProfileID, GeneratedProfileRevision)
	return Policy{ProfileID: GeneratedProfileID, Revision: GeneratedProfileRevision, Limits: limits}
}

// ExceedsInstancedTriangles reports whether the measured model has more
// instanced triangles than limit. A limit of zero or less means undeclared.
// The two Product Schema triangle ceilings share this comparison: the
// endpoint limit on a profiled inline model input
// (model_input_profile.max_instanced_triangles) and the limit on a step's
// source model (Offering.source_model_limit).
func (p Profile) ExceedsInstancedTriangles(limit int64) bool {
	return limit > 0 && p.InstancedTriangles > limit
}

// Limits are explicit Product Schema inputs. The ceilings above are parser
// safety boundaries; an out-of-range policy is rejected, never clamped.
func (p Policy) Validate() error {
	ceilings, known := Ceilings(p.ProfileID, p.Revision)
	if !known {
		return failure(InvalidPolicy, "unknown_profile")
	}
	limits := [][2]int64{
		{p.Limits.MaxBytes, ceilings.MaxBytes}, {p.Limits.MaxJSONBytes, ceilings.MaxJSONBytes},
		{p.Limits.MaxDepth, ceilings.MaxDepth}, {p.Limits.MaxElements, ceilings.MaxElements},
		{p.Limits.MaxVertices, ceilings.MaxVertices}, {p.Limits.MaxTriangles, ceilings.MaxTriangles},
		{p.Limits.MaxNodes, ceilings.MaxNodes}, {p.Limits.MaxWork, ceilings.MaxWork},
		{p.Limits.MaxImagePixels, ceilings.MaxImagePixels}, {p.Limits.MaxTotalImagePixels, ceilings.MaxTotalImagePixels},
	}
	for _, limit := range limits {
		if limit[0] <= 0 || limit[0] > limit[1] {
			return failure(InvalidPolicy, "invalid_limits")
		}
	}
	if p.Limits.MaxJSONBytes > p.Limits.MaxBytes || p.Limits.MaxTotalImagePixels < p.Limits.MaxImagePixels {
		return failure(InvalidPolicy, "invalid_image_limits")
	}
	return nil
}

// Uploadable reports whether the policy may validate a direct user upload.
// Only the static upload profile qualifies; the generated conversion profile
// is reserved for Task outputs and is rejected on every upload path.
func (p Policy) Uploadable() bool { return p.ProfileID == ProfileID }

// generated reports the Task-output conversion contract.
func (p Policy) generated() bool { return p.ProfileID == GeneratedProfileID }
