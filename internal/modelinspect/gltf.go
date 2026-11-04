package modelinspect

import "encoding/json"

// gltfDocument mirrors the JSON chunk. Only fields this package consumes are
// declared; unknown JSON members are ignored as glTF requires.
type gltfDocument struct {
	Asset              gltfAsset        `json:"asset"`
	Scene              *int             `json:"scene"`
	Scenes             []gltfScene      `json:"scenes"`
	Nodes              []gltfNode       `json:"nodes"`
	Meshes             []gltfMesh       `json:"meshes"`
	Materials          []gltfMaterial   `json:"materials"`
	Textures           []gltfTexture    `json:"textures"`
	Images             []gltfImage      `json:"images"`
	Samplers           []gltfSampler    `json:"samplers"`
	Buffers            []gltfBuffer     `json:"buffers"`
	BufferViews        []gltfBufferView `json:"bufferViews"`
	Accessors          []gltfAccessor   `json:"accessors"`
	Skins              []gltfSkin       `json:"skins"`
	Animations         []gltfAnimation  `json:"animations"`
	ExtensionsUsed     []string         `json:"extensionsUsed"`
	ExtensionsRequired []string         `json:"extensionsRequired"`
}

type gltfAsset struct {
	Version string `json:"version"`
}

type gltfScene struct {
	Name  string `json:"name"`
	Nodes []int  `json:"nodes"`
}

type gltfNode struct {
	Name        string    `json:"name"`
	Children    []int     `json:"children"`
	Mesh        *int      `json:"mesh"`
	Skin        *int      `json:"skin"`
	Matrix      []float64 `json:"matrix"`
	Translation []float64 `json:"translation"`
	Rotation    []float64 `json:"rotation"`
	Scale       []float64 `json:"scale"`
}

type gltfMesh struct {
	Name       string          `json:"name"`
	Primitives []gltfPrimitive `json:"primitives"`
}

type gltfPrimitive struct {
	Attributes map[string]int   `json:"attributes"`
	Indices    *int             `json:"indices"`
	Material   *int             `json:"material"`
	Mode       *int             `json:"mode"`
	Targets    []map[string]int `json:"targets"`
}

type gltfTextureInfo struct {
	Index    *int `json:"index"`
	TexCoord *int `json:"texCoord"`
}

type gltfNormalTexture struct {
	Index    *int     `json:"index"`
	TexCoord *int     `json:"texCoord"`
	Scale    *float64 `json:"scale"`
}

type gltfOcclusionTexture struct {
	Index    *int     `json:"index"`
	TexCoord *int     `json:"texCoord"`
	Strength *float64 `json:"strength"`
}

type gltfPBR struct {
	BaseColorFactor          []float64        `json:"baseColorFactor"`
	BaseColorTexture         *gltfTextureInfo `json:"baseColorTexture"`
	MetallicFactor           *float64         `json:"metallicFactor"`
	RoughnessFactor          *float64         `json:"roughnessFactor"`
	MetallicRoughnessTexture *gltfTextureInfo `json:"metallicRoughnessTexture"`
}

type gltfMaterial struct {
	Name             string                `json:"name"`
	PBR              *gltfPBR              `json:"pbrMetallicRoughness"`
	NormalTexture    *gltfNormalTexture    `json:"normalTexture"`
	OcclusionTexture *gltfOcclusionTexture `json:"occlusionTexture"`
	EmissiveTexture  *gltfTextureInfo      `json:"emissiveTexture"`
	EmissiveFactor   []float64             `json:"emissiveFactor"`
	AlphaMode        string                `json:"alphaMode"`
	DoubleSided      bool                  `json:"doubleSided"`
}

type gltfTexture struct {
	Name    string `json:"name"`
	Source  *int   `json:"source"`
	Sampler *int   `json:"sampler"`
}

type gltfImage struct {
	Name       string `json:"name"`
	URI        string `json:"uri"`
	MIMEType   string `json:"mimeType"`
	BufferView *int   `json:"bufferView"`
}

type gltfSampler struct {
	WrapS int `json:"wrapS"`
	WrapT int `json:"wrapT"`
}

type gltfBuffer struct {
	URI        string `json:"uri"`
	ByteLength int    `json:"byteLength"`
}

type gltfBufferView struct {
	Buffer     int `json:"buffer"`
	ByteOffset int `json:"byteOffset"`
	ByteLength int `json:"byteLength"`
	ByteStride int `json:"byteStride"`
	Target     int `json:"target"`
}

type gltfAccessor struct {
	BufferView    *int            `json:"bufferView"`
	ByteOffset    int             `json:"byteOffset"`
	ComponentType int             `json:"componentType"`
	Normalized    bool            `json:"normalized"`
	Count         int             `json:"count"`
	Type          string          `json:"type"`
	Min           []float64       `json:"min"`
	Max           []float64       `json:"max"`
	Sparse        json.RawMessage `json:"sparse"`
}

type gltfSkin struct {
	Name                string `json:"name"`
	Joints              []int  `json:"joints"`
	InverseBindMatrices *int   `json:"inverseBindMatrices"`
	Skeleton            *int   `json:"skeleton"`
}

type gltfAnimation struct {
	Name     string                 `json:"name"`
	Channels []gltfAnimationChannel `json:"channels"`
	Samplers []gltfAnimationSampler `json:"samplers"`
}

type gltfAnimationChannel struct {
	Sampler int                 `json:"sampler"`
	Target  gltfAnimationTarget `json:"target"`
}

type gltfAnimationTarget struct {
	Node *int   `json:"node"`
	Path string `json:"path"`
}

type gltfAnimationSampler struct {
	Input         int    `json:"input"`
	Output        int    `json:"output"`
	Interpolation string `json:"interpolation"`
}

// sampler is the wrap configuration this package needs. Minification filters
// are irrelevant because Render samples bilinearly without mipmaps.
type sampler struct {
	wrapS int
	wrapT int
}
