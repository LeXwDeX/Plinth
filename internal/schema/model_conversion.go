package schema

// ModelInputLimits is the Product Schema resource contract shared by uploaded
// static models and frozen local container conversions.
type ModelInputLimits struct {
	MaxBytes            int64 `json:"max_bytes"`
	MaxJSONBytes        int64 `json:"max_json_bytes"`
	MaxDepth            int64 `json:"max_depth"`
	MaxElements         int64 `json:"max_elements"`
	MaxVertices         int64 `json:"max_vertices"`
	MaxTriangles        int64 `json:"max_triangles"`
	MaxNodes            int64 `json:"max_nodes"`
	MaxWork             int64 `json:"max_work"`
	MaxImagePixels      int64 `json:"max_image_pixels"`
	MaxTotalImagePixels int64 `json:"max_total_image_pixels"`
}

const (
	ModelConversionGLBToGLTF = "glb-to-gltf"
	ModelConversionGLTFToGLB = "gltf-to-glb"
	// ModelConversionGLBToGLTFPackage converts a GLB that a successful platform
	// Task produced into a ZIP of model.gltf, model.bin and image files. Its
	// source is a Task model output only, under the generated model profile.
	ModelConversionGLBToGLTFPackage = "glb-to-gltf-package"
)

// ModelConversionOptions is the closed local-model protocol. Source and output
// byte limits remain on the Offering parameter and required output slot.
// Catalog expands the referenced profile into ProfileLimits before the Offering
// is frozen; authored declarations must not supply their own ProfileLimits.
type ModelConversionOptions struct {
	Direction       string `json:"direction"`
	ProfileID       string `json:"profile_id"`
	ProfileRevision string `json:"profile_revision"`
	MaxWork         int64  `json:"max_work"`
	// MaxConcurrent bounds package conversions (and their read-only checks)
	// running at once per Offering, so peak memory stays a declared multiple
	// of one source. Required for the package direction only.
	MaxConcurrent int64             `json:"max_concurrent,omitempty"`
	ProfileLimits *ModelInputLimits `json:"profile_limits,omitempty"`
}
