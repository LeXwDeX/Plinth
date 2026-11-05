package modelinspect

import "reflect"

// RetainedBytes estimates the memory m holds: the parsed structure plus every
// cache Inspect and Render have filled so far (decoded accessors, which hold
// four bytes per component whatever the stored type, decoded meshopt views,
// skin matrices and decoded textures). It grows as views are rendered and is
// safe to call concurrently with Inspect and Render.
//
// The bytes behind the ReaderAt given to Parse are the caller's and are not
// included. Neither is the temporary memory a running Inspect or Render
// allocates and releases before returning.
func (m *Model) RetainedBytes() int64 {
	if m == nil {
		return 0
	}
	return m.metadataBytes + m.decodedBytes.Load()
}

// RetainedBytes estimates the memory r holds: its structs, slices and
// strings. Strings it shares with the Model, such as part paths, are counted
// again, which only makes the estimate larger.
func (r Report) RetainedBytes() int64 {
	size := int64(sizeOfReport)
	for _, part := range r.Parts {
		size += int64(sizeOfPartReport) + stringBytes(part.ID, part.Node, part.Mesh, part.MaterialName)
	}
	for _, material := range r.Materials {
		size += int64(sizeOfMaterialReport) + stringBytes(material.Name, material.AlphaMode)
		for _, slot := range material.Slots {
			size += int64(sizeOfSlotReport) + stringBytes(slot.Slot, slot.MIME) + 8*int64(len(slot.Factor))
		}
	}
	for _, primitive := range r.UV.Primitives {
		size += int64(sizeOfUVReport) + stringBytes(primitive.PartID) + 8*int64(len(primitive.Sets))
	}
	for _, skin := range r.Skins {
		size += int64(sizeOfSkinReport) + stringBytes(skin.Name)
	}
	for _, animation := range r.Animations {
		size += int64(sizeOfAnimationReport) + stringBytes(animation.Name)
	}
	for _, warning := range r.Warnings {
		size += int64(sizeOfString) + stringBytes(warning)
	}
	return size
}

// Struct sizes for the estimates. A Go map costs roughly mapEntryBytes per
// small entry beyond its keys and values.
var (
	sizeOfReport          = reflect.TypeFor[Report]().Size()
	sizeOfPartReport      = reflect.TypeFor[PartReport]().Size()
	sizeOfMaterialReport  = reflect.TypeFor[MaterialReport]().Size()
	sizeOfSlotReport      = reflect.TypeFor[MaterialSlotReport]().Size()
	sizeOfUVReport        = reflect.TypeFor[PrimitiveUVReport]().Size()
	sizeOfSkinReport      = reflect.TypeFor[SkinReport]().Size()
	sizeOfAnimationReport = reflect.TypeFor[AnimationReport]().Size()
	sizeOfString          = reflect.TypeFor[string]().Size()

	sizeOfModel      = reflect.TypeFor[Model]().Size()
	sizeOfNode       = reflect.TypeFor[Node]().Size()
	sizeOfMesh       = reflect.TypeFor[Mesh]().Size()
	sizeOfPrimitive  = reflect.TypeFor[Primitive]().Size()
	sizeOfMaterial   = reflect.TypeFor[Material]().Size()
	sizeOfTextureRef = reflect.TypeFor[NormalTexture]().Size()
	sizeOfTexture    = reflect.TypeFor[Texture]().Size()
	sizeOfImage      = reflect.TypeFor[Image]().Size()
	sizeOfSkin       = reflect.TypeFor[Skin]().Size()
	sizeOfAnimation  = reflect.TypeFor[Animation]().Size()
	sizeOfScene      = reflect.TypeFor[Scene]().Size()
	sizeOfView       = reflect.TypeFor[bufferView]().Size() + reflect.TypeFor[meshoptSource]().Size()
	sizeOfAccessor   = reflect.TypeFor[accessor]().Size()
	sizeOfSampler    = reflect.TypeFor[sampler]().Size()
	sizeOfPart       = reflect.TypeFor[part]().Size()
)

const mapEntryBytes = 48

func stringBytes(values ...string) int64 {
	total := int64(0)
	for _, value := range values {
		total += int64(len(value))
	}
	return total
}

// estimateMetadataBytes sizes the structure Parse keeps. It runs once, after
// the scene walk, and is an estimate: allocator rounding is ignored.
func (m *Model) estimateMetadataBytes() int64 {
	size := int64(sizeOfModel)
	for _, name := range m.ExtensionsUsed {
		size += int64(sizeOfString) + stringBytes(name)
	}
	for _, name := range m.ExtensionsRequired {
		size += int64(sizeOfString) + stringBytes(name)
	}
	for _, scene := range m.Scenes {
		size += int64(sizeOfScene) + stringBytes(scene.Name) + 8*int64(len(scene.Nodes))
	}
	for _, node := range m.Nodes {
		size += int64(sizeOfNode) + stringBytes(node.Name) + 8*int64(len(node.Children))
	}
	for _, mesh := range m.Meshes {
		size += int64(sizeOfMesh) + stringBytes(mesh.Name)
		for _, primitive := range mesh.Primitives {
			size += int64(sizeOfPrimitive) + 8*int64(len(primitive.hasTexCoords))
			for name := range primitive.Attributes {
				size += mapEntryBytes + stringBytes(name)
			}
		}
	}
	for _, material := range m.Materials {
		size += int64(sizeOfMaterial) + stringBytes(material.Name, material.AlphaMode) + 5*int64(sizeOfTextureRef)
	}
	for _, texture := range m.Textures {
		size += int64(sizeOfTexture) + stringBytes(texture.Name)
	}
	for _, image := range m.Images {
		size += int64(sizeOfImage) + stringBytes(image.Name, image.MIME)
	}
	for _, skin := range m.Skins {
		size += int64(sizeOfSkin) + stringBytes(skin.Name) + 8*int64(len(skin.Joints))
	}
	for _, animation := range m.Animations {
		size += int64(sizeOfAnimation) + stringBytes(animation.Name)
	}
	size += int64(len(m.bufferViews)) * int64(sizeOfView)
	for _, accessor := range m.accessors {
		size += int64(sizeOfAccessor) + 8*int64(len(accessor.min)+len(accessor.max))
	}
	size += int64(len(m.samplers)) * int64(sizeOfSampler)
	for _, p := range m.parts {
		// Parts of one node share their path string; counting it per part
		// keeps the estimate an upper bound.
		size += int64(sizeOfPart) + 8 + stringBytes(p.id, p.path) + mapEntryBytes*int64(1+len(p.uv))
	}
	size += int64(len(m.jointWorld)) * (mat4Bytes + mapEntryBytes)
	return size
}
