package schema

// ModelInputProfileOptions binds a Provider model input to one frozen Product
// Schema profile. Catalog supplies ProfileLimits; authored overrides are rejected.
// MaxInstancedTriangles is an optional endpoint limit on all mesh node instances.
type ModelInputProfileOptions struct {
	Parameter             string            `json:"parameter"`
	ProfileID             string            `json:"profile_id"`
	ProfileRevision       string            `json:"profile_revision"`
	MaxInstancedTriangles int64             `json:"max_instanced_triangles,omitempty"`
	ProfileLimits         *ModelInputLimits `json:"profile_limits,omitempty"`
}
