package schema

// Split modes accepted by the local-model-parts protocol. The Offering's mode
// parameter declares the same closed list; the adapter rejects anything else.
const (
	ModelSplitModeNode               = "node"
	ModelSplitModePrimitive          = "primitive"
	ModelSplitModeMaterial           = "material"
	ModelSplitModeConnectedComponent = "connected_component"
)

// ModelPartsManifestVersion is the schema version of the parts_manifest output.
const ModelPartsManifestVersion = 1

// ModelPartsOptions is the frozen local-model-parts protocol declaration. As
// with local-model, Catalog expands the referenced profile into ProfileLimits
// during load; an authored Offering must not supply its own ProfileLimits.
// MaxConcurrent bounds splits and merges running at once per Offering so peak
// memory stays a declared multiple of one source.
type ModelPartsOptions struct {
	ProfileID       string            `json:"profile_id"`
	ProfileRevision string            `json:"profile_revision"`
	MaxWork         int64             `json:"max_work"`
	MaxConcurrent   int64             `json:"max_concurrent"`
	ProfileLimits   *ModelInputLimits `json:"profile_limits,omitempty"`
}

// ModelPartsManifest is the application/json output of a split Task. Parts are
// listed in output ordinal order (Ordinal starts at 1) and correspond one to
// one with the Task's model assets.
type ModelPartsManifest struct {
	Version int         `json:"version"`
	Mode    string      `json:"mode"`
	Parts   []ModelPart `json:"parts"`
}

// ModelPart describes one split result. Bounds are the world-space AABB of the
// part with the same float64 arithmetic the model inspector reports.
type ModelPart struct {
	Ordinal   int             `json:"ordinal"`
	Name      string          `json:"name"`
	Triangles int             `json:"triangles"`
	Bounds    ModelPartBounds `json:"bounds"`
	Source    ModelPartSource `json:"source"`
}

type ModelPartBounds struct {
	Min [3]float64 `json:"min"`
	Max [3]float64 `json:"max"`
}

// ModelPartSource records where a part came from in the source document.
// Nodes are source node names (a stable name_<index> placeholder when the node
// had none); Primitives is the list of copied primitives with their component
// index when the mode selected connected components.
type ModelPartSource struct {
	Nodes      []string                   `json:"nodes"`
	Primitives []ModelPartPrimitiveSource `json:"primitives"`
}

type ModelPartPrimitiveSource struct {
	Node      string `json:"node"`
	Primitive int    `json:"primitive"`
	Component *int   `json:"component,omitempty"`
}
