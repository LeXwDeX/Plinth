package modelinspect

import (
	"io"
	"sync"
	"sync/atomic"
)

// Model is a parsed GLB asset. Parse fills the exported metadata; accessor
// data stays in the caller's ReaderAt and is decoded on demand, so the
// ReaderAt must remain readable for as long as the Model is used.
//
// A Model is safe for concurrent use by Inspect and Render after Parse
// returns. Its exported slices must not be modified.
type Model struct {
	// FileBytes is the GLB container size in bytes.
	FileBytes int64
	// ExtensionsUsed is copied verbatim from the JSON chunk.
	ExtensionsUsed []string
	// ExtensionsRequired is copied verbatim from the JSON chunk.
	ExtensionsRequired []string
	// Scenes, Nodes, Meshes, Materials, Textures, Images, Skins and Animations
	// are the parsed JSON metadata. Indices in those values are stable
	// positions in the corresponding slice.
	Scenes     []Scene
	Nodes      []Node
	Meshes     []Mesh
	Materials  []Material
	Textures   []Texture
	Images     []Image
	Skins      []Skin
	Animations []Animation

	src      io.ReaderAt
	limits   Limits
	binStart int64
	binLen   int64

	bufferViews []bufferView
	accessors   []accessor
	samplers    []sampler
	parts       []*part
	// jointWorld holds the world matrix of every skin joint the active scene
	// reaches; skinned parts are posed from it.
	jointWorld map[int]mat4

	// metadataBytes estimates the parsed structure Parse keeps; decodedBytes
	// is the size of every cache that grows afterwards (decoded accessors,
	// meshopt views and textures). RetainedBytes reports their sum.
	metadataBytes int64
	decodedBytes  atomic.Int64

	cache        accessorCache
	textureCache textureCache
	skinCache    skinCache
}

// Scene is one glTF scene.
type Scene struct {
	Name  string `json:"name,omitempty"`
	Nodes []int  `json:"nodes,omitempty"`
}

// Node is one glTF node. Exactly one representation is valid: when HasMatrix
// is true, Matrix holds the column-major local transform; otherwise
// Translation, Rotation and Scale are combined in that order.
type Node struct {
	Name        string      `json:"name,omitempty"`
	Children    []int       `json:"children,omitempty"`
	Mesh        int         `json:"mesh"` // -1 when absent
	Skin        int         `json:"skin"` // -1 when absent
	Translation [3]float64  `json:"translation,omitempty"`
	Rotation    [4]float64  `json:"rotation,omitempty"`
	Scale       [3]float64  `json:"scale,omitempty"`
	Matrix      [16]float64 `json:"-"`
	HasMatrix   bool        `json:"-"`
}

// Mesh is one glTF mesh.
type Mesh struct {
	Name       string      `json:"name,omitempty"`
	Primitives []Primitive `json:"primitives"`
}

// Primitive is one drawable primitive. Attributes maps glTF attribute names to
// accessor indices. Material and Indices are -1 when absent. Triangles is the
// number of triangles the primitive mode declares for its element count; for
// strips and fans it includes stitching triangles, which Inspect excludes
// once it has read the indices.
type Primitive struct {
	Mode         int            `json:"mode"`
	Material     int            `json:"material"`
	Indices      int            `json:"indices"`
	Attributes   map[string]int `json:"attributes"`
	Triangles    int64          `json:"triangles"`
	MorphTargets int            `json:"morph_targets,omitempty"`

	hasTexCoords []int // sorted TEXCOORD_n set numbers
}

// TextureRef identifies a texture slot and the UV set it uses. TexCoord
// already reflects a KHR_texture_transform texCoord override. Transform is
// nil when the slot has no KHR_texture_transform.
type TextureRef struct {
	Texture   int
	TexCoord  int
	Transform *TextureTransform
}

// TextureTransform is a KHR_texture_transform: texture coordinates are
// scaled, rotated by Rotation radians and then offset, as the extension
// defines.
type TextureTransform struct {
	Offset   [2]float64
	Rotation float64
	Scale    [2]float64
}

// NormalTexture is the normalTexture material slot.
type NormalTexture struct {
	TextureRef
	Scale float64
}

// OcclusionTexture is the occlusionTexture material slot.
type OcclusionTexture struct {
	TextureRef
	Strength float64
}

// Material is the subset of a glTF PBR material needed for inspection and
// rendering. Defaults follow the glTF specification.
type Material struct {
	Name                     string
	BaseColorFactor          [4]float64
	MetallicFactor           float64
	RoughnessFactor          float64
	BaseColorTexture         *TextureRef
	MetallicRoughnessTexture *TextureRef
	NormalTexture            *NormalTexture
	OcclusionTexture         *OcclusionTexture
	EmissiveTexture          *TextureRef
	EmissiveFactor           [3]float64
	AlphaMode                string
	// AlphaCutoff is the MASK threshold; the glTF default is 0.5.
	AlphaCutoff float64
	DoubleSided bool
}

// Texture is one glTF texture. Sampler is -1 when the default sampler is used.
type Texture struct {
	Name    string `json:"name,omitempty"`
	Source  int    `json:"source"`
	Sampler int    `json:"sampler"`
}

// Image is one embedded image. Width and Height come from image.DecodeConfig
// during Parse; MIME is the declared mimeType.
type Image struct {
	Name       string `json:"name,omitempty"`
	MIME       string `json:"mime_type"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	BufferView int    `json:"buffer_view"`
	ByteLength int    `json:"byte_length"`
}

// Skin is one glTF skin. Joints lists the joint node indices; the inverse
// bind matrices are decoded on demand to pose skinned parts.
type Skin struct {
	Name   string `json:"name,omitempty"`
	Joints []int  `json:"joints"`

	inverseBind int // accessor index, -1 when every matrix is the identity
}

// Animation summarizes one glTF animation. Channel counts are grouped by the
// target path; Duration is the largest input accessor max, in seconds.
type Animation struct {
	Name         string  `json:"name,omitempty"`
	Channels     int     `json:"channels"`
	Translations int     `json:"translations"`
	Rotations    int     `json:"rotations"`
	Scales       int     `json:"scales"`
	Weights      int     `json:"weights"`
	Duration     float64 `json:"duration"`
}

// bufferView is a validated view. An uncompressed view addresses the
// embedded BIN chunk directly: offset is relative to the start of the
// ReaderAt. A view with meshopt set has no stored bytes of its own; its
// length bytes are decoded from the compressed copy.
type bufferView struct {
	offset  int64
	length  int64
	stride  int
	meshopt *meshoptSource
}

// accessor is a validated accessor. srcOffset is relative to the start of the
// ReaderAt; stride and components describe how one element is laid out. view
// and viewOffset locate the same bytes in their bufferView, which a
// compressed view needs because it has no position in the reader.
type accessor struct {
	srcOffset     int64
	view          int // -1 without a bufferView
	viewOffset    int64
	count         int
	components    int
	componentType int
	componentSize int
	elemSize      int
	normalized    bool
	stride        int
	min           []float64
	max           []float64
}

// part is one node x primitive instance in the active scene.
type part struct {
	id              string
	node            int
	mesh            int
	primitive       int
	path            string
	world           mat4
	vertexCount     int
	triangleCount   int64
	mode            int
	material        int
	position        int
	normal          int
	color           int
	colorComponents int
	index           int
	uv              map[int]int
	// skin is the node's skin, -1 when the part is not skinned. A skinned
	// part ignores world: glTF poses it from its joints instead.
	skin int
}

// accessorCache holds decoded accessors and, for EXT_meshopt_compression,
// decoded bufferViews. One mutex guards all three maps.
type accessorCache struct {
	mu      sync.Mutex
	f32     map[int][]float32
	u32     map[int][]uint32
	meshopt map[int][]byte
}

// skinCache holds each skin's joint matrices so every part of a skin poses
// from the same result.
type skinCache struct {
	mu       sync.Mutex
	matrices map[int][]mat4
}

type textureCache struct {
	mu sync.Mutex
	// images caches decoded textures by texture index, including a nil entry
	// for an image that exceeds MaxTexturePixels.
	images map[int]*decodedTexture
	// decoded caches the shared pixel buffer by image index so textures that
	// reference the same image decode it only once.
	decoded map[int]*decodedImage
	// pixels and bytes are the cumulative budgets already charged.
	pixels int64
	bytes  int64
}
